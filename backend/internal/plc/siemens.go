package plc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"backend/pkg/models"

	"github.com/robinson/gos7"
)

// PLCInterface define o contrato para conexões PLC
type PLCInterface interface {
	Connect(ctx context.Context) error
	Disconnect() error
	GetConnectionStatus() *models.PLCStatus
	IsConnected() bool
	Ping(ctx context.Context) error
}

// PLCConfig contém as configurações do PLC
type PLCConfig struct {
	IP            string
	Rack          int
	Slot          int
	Timeout       time.Duration
	IdleTimeout   time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
}

// DefaultConfig retorna configuração padrão para S7-1200/1500
func DefaultConfig(ip string) *PLCConfig {
	return &PLCConfig{
		IP:            ip,
		Rack:          0,
		Slot:          1,
		Timeout:       5 * time.Second,
		IdleTimeout:   10 * time.Second,
		RetryAttempts: 3,
		RetryDelay:    time.Second,
	}
}

// SiemensPLC representa a conexão com o PLC Siemens
type SiemensPLC struct {
	config    *PLCConfig
	connected bool
	client    gos7.Client
	handler   *gos7.TCPClientHandler
	mutex     sync.RWMutex
	logger    *slog.Logger
	lastError error
}

// NewSiemensPLC cria uma nova instância de conexão com o PLC
func NewSiemensPLC(config *PLCConfig, logger *slog.Logger) (*SiemensPLC, error) {
	if config == nil {
		return nil, fmt.Errorf("configuração não pode ser nil")
	}

	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("configuração inválida: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &SiemensPLC{
		config:    config,
		connected: false,
		logger:    logger.With("component", "siemens_plc", "ip", config.IP),
	}, nil
}

// validateConfig valida a configuração do PLC
func validateConfig(config *PLCConfig) error {
	if config.IP == "" {
		return fmt.Errorf("IP não pode estar vazio")
	}

	if config.Rack < 0 || config.Rack > 7 {
		return fmt.Errorf("rack deve estar entre 0 e 7")
	}

	if config.Slot < 0 || config.Slot > 31 {
		return fmt.Errorf("slot deve estar entre 0 e 31")
	}

	if config.Timeout <= 0 {
		return fmt.Errorf("timeout deve ser maior que zero")
	}

	if config.RetryAttempts < 0 {
		return fmt.Errorf("tentativas de retry não podem ser negativas")
	}

	return nil
}

// Connect estabelece conexão com o PLC com retry automático
func (p *SiemensPLC) Connect(ctx context.Context) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.connected {
		return nil // Já conectado
	}

	var lastErr error

	for attempt := 0; attempt <= p.config.RetryAttempts; attempt++ {
		if attempt > 0 {
			p.logger.Warn("Tentativa de reconexão",
				"attempt", attempt,
				"max_attempts", p.config.RetryAttempts,
			)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(p.config.RetryDelay):
				// Continue para próxima tentativa
			}
		}

		if err := p.attemptConnection(ctx); err != nil {
			lastErr = err
			p.logger.Error("Falha na conexão",
				"attempt", attempt+1,
				"error", err,
			)
			continue
		}

		// Sucesso na conexão
		p.connected = true
		p.lastError = nil

		p.logger.Info("Conectado com sucesso ao PLC",
			"rack", p.config.Rack,
			"slot", p.config.Slot,
			"attempts", attempt+1,
		)

		return nil
	}

	p.lastError = lastErr
	return fmt.Errorf("falha ao conectar após %d tentativas: %w",
		p.config.RetryAttempts+1, lastErr)
}

// attemptConnection executa uma única tentativa de conexão
func (p *SiemensPLC) attemptConnection(ctx context.Context) error {
	// Limpar conexão anterior se existir
	if p.handler != nil {
		p.handler.Close()
	}

	// Criar novo handler
	p.handler = gos7.NewTCPClientHandler(p.config.IP, p.config.Rack, p.config.Slot)
	p.handler.Timeout = p.config.Timeout
	p.handler.IdleTimeout = p.config.IdleTimeout

	// Canal para controlar timeout
	done := make(chan error, 1)

	go func() {
		done <- p.handler.Connect()
	}()

	// Aguardar conexão ou cancelamento
	select {
	case <-ctx.Done():
		if p.handler != nil {
			p.handler.Close()
		}
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return err
		}
	}

	// Criar cliente S7
	p.client = gos7.NewClient(p.handler)

	return nil
}

// Disconnect fecha a conexão com o PLC
func (p *SiemensPLC) Disconnect() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.connected {
		return nil // Já desconectado
	}

	var err error
	if p.handler != nil {
		err = p.handler.Close()
		p.handler = nil
	}

	p.connected = false
	p.client = nil

	if err != nil {
		p.logger.Error("Erro ao desconectar", "error", err)
		return fmt.Errorf("erro ao desconectar: %w", err)
	}

	p.logger.Info("Desconectado do PLC")
	return nil
}

// Ping verifica se a conexão está ativa
func (p *SiemensPLC) Ping(ctx context.Context) error {
	p.mutex.RLock()
	connected := p.connected
	client := p.client
	p.mutex.RUnlock()

	if !connected {
		return fmt.Errorf("PLC não conectado")
	}

	// Tentar uma operação simples para verificar conectividade
	done := make(chan error, 1)

	go func() {
		// Operação de ping usando leitura de CPU info
		_, err := client.GetCPUInfo()
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			p.logger.Warn("Ping falhou", "error", err)

			// Marcar como desconectado se ping falhar
			p.mutex.Lock()
			p.connected = false
			p.lastError = err
			p.mutex.Unlock()

			return fmt.Errorf("ping falhou: %w", err)
		}
	}

	return nil
}

// GetConnectionStatus retorna o status detalhado da conexão
func (p *SiemensPLC) GetConnectionStatus() *models.PLCStatus {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	status := &models.PLCStatus{
		Connected: p.connected,
	}

	if !p.connected {
		if p.lastError != nil {
			status.Error = p.lastError.Error()
		} else {
			status.Error = "PLC não conectado"
		}
	}

	return status
}

// IsConnected verifica se está conectado (thread-safe)
func (p *SiemensPLC) IsConnected() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.connected
}

// GetClient retorna o cliente S7 (use com cuidado - verificar conexão antes)
func (p *SiemensPLC) GetClient() (gos7.Client, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	if !p.connected {
		return nil, fmt.Errorf("PLC não conectado")
	}

	return p.client, nil
}

// ConnectWithRetry conecta com retry automático em background
func (p *SiemensPLC) ConnectWithRetry(ctx context.Context) <-chan error {
	result := make(chan error, 1)

	go func() {
		defer close(result)

		err := p.Connect(ctx)
		result <- err
	}()

	return result
}

// HealthCheck executa verificação completa de saúde da conexão
func (p *SiemensPLC) HealthCheck(ctx context.Context) error {
	if !p.IsConnected() {
		return fmt.Errorf("PLC não conectado")
	}

	// Verificar com ping
	if err := p.Ping(ctx); err != nil {
		return fmt.Errorf("falha no health check: %w", err)
	}

	p.logger.Debug("Health check passou")
	return nil
}

// SetTimeout atualiza os timeouts da conexão
func (p *SiemensPLC) SetTimeout(timeout, idleTimeout time.Duration) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if timeout <= 0 || idleTimeout <= 0 {
		return fmt.Errorf("timeouts devem ser maiores que zero")
	}

	p.config.Timeout = timeout
	p.config.IdleTimeout = idleTimeout

	// Aplicar aos handlers existentes
	if p.handler != nil {
		p.handler.Timeout = timeout
		p.handler.IdleTimeout = idleTimeout
	}

	return nil
}

// GetConfig retorna uma cópia da configuração atual
func (p *SiemensPLC) GetConfig() PLCConfig {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return *p.config
}

// Close implementa io.Closer para cleanup adequado
func (p *SiemensPLC) Close() error {
	return p.Disconnect()
}
