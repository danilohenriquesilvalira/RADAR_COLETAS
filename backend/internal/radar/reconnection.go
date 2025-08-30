package radar

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// ReconnectionConfig configuração de reconexão
type ReconnectionConfig struct {
	MaxRetries     int
	RetryInterval  time.Duration
	ErrorThreshold int
	BackoffEnabled bool
}

// ReconnectionState estado da reconexão
type ReconnectionState struct {
	isReconnecting    bool
	connectionLost    bool
	consecutiveErrors int
	lastError         error
}

// ReconnectionManager gerenciador thread-safe de reconexão
type ReconnectionManager struct {
	radar  *SICKRadar
	config ReconnectionConfig
	state  ReconnectionState
	mutex  sync.RWMutex
}

// NewReconnectionManager cria gerenciador otimizado
func NewReconnectionManager(radar *SICKRadar) *ReconnectionManager {
	return &ReconnectionManager{
		radar: radar,
		config: ReconnectionConfig{
			MaxRetries:     10,
			RetryInterval:  3 * time.Second,
			ErrorThreshold: 5,
			BackoffEnabled: true,
		},
		state: ReconnectionState{
			isReconnecting:    false,
			connectionLost:    false,
			consecutiveErrors: 0,
		},
	}
}

// CheckConnectionHealth verifica saúde da conexão thread-safe
func (rm *ReconnectionManager) CheckConnectionHealth(err error) bool {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if err != nil {
		rm.state.consecutiveErrors++
		rm.state.lastError = err

		if rm.state.consecutiveErrors >= rm.config.ErrorThreshold {
			if !rm.state.connectionLost {
				rm.state.connectionLost = true
				rm.logConnectionLost()
			}
			return false
		}
	} else {
		if rm.state.consecutiveErrors > 0 {
			rm.logConnectionRestored()
		}
		rm.resetErrorState()
	}

	return true
}

// StartReconnection processo de reconexão com backoff
func (rm *ReconnectionManager) StartReconnection() error {
	if rm.isCurrentlyReconnecting() {
		return nil
	}

	rm.setReconnecting(true)
	defer rm.setReconnecting(false)

	rm.logReconnectionStart()

	for attempt := 1; attempt <= rm.config.MaxRetries; attempt++ {
		if rm.attemptReconnection(attempt) {
			rm.logReconnectionSuccess(attempt)
			rm.resetConnectionState()
			return nil
		}
	}

	return fmt.Errorf("falha ao reconectar após %d tentativas", rm.config.MaxRetries)
}

// attemptReconnection tenta reconexão individual
func (rm *ReconnectionManager) attemptReconnection(attempt int) bool {
	rm.logAttempt(attempt)

	// Limpar conexão atual
	rm.radar.Disconnect()

	// Calcular delay com backoff exponencial
	delay := rm.calculateDelay(attempt)
	time.Sleep(delay)

	// Tentar conectar
	if err := rm.radar.Connect(); err != nil {
		rm.logAttemptFailed(attempt, err)
		return false
	}

	// Tentar iniciar medição
	if err := rm.radar.StartMeasurement(); err != nil {
		rm.logMeasurementFailed(attempt, err)
		rm.radar.Disconnect()
		return false
	}

	return true
}

// calculateDelay calcula delay com backoff exponencial
func (rm *ReconnectionManager) calculateDelay(attempt int) time.Duration {
	if !rm.config.BackoffEnabled {
		return rm.config.RetryInterval
	}

	// Backoff exponencial: 3s, 6s, 12s, mas max 30s
	multiplier := time.Duration(1 << (attempt - 1))
	delay := rm.config.RetryInterval * multiplier

	maxDelay := 30 * time.Second
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

// ========== THREAD-SAFE GETTERS/SETTERS ==========

func (rm *ReconnectionManager) IsReconnecting() bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.state.isReconnecting
}

func (rm *ReconnectionManager) IsConnectionLost() bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.state.connectionLost
}

func (rm *ReconnectionManager) GetConsecutiveErrors() int {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.state.consecutiveErrors
}

func (rm *ReconnectionManager) GetLastError() error {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.state.lastError
}

func (rm *ReconnectionManager) ResetErrorCount() {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.resetErrorState()
}

// ========== MÉTODOS PRIVADOS THREAD-SAFE ==========

func (rm *ReconnectionManager) isCurrentlyReconnecting() bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.state.isReconnecting
}

func (rm *ReconnectionManager) setReconnecting(reconnecting bool) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.state.isReconnecting = reconnecting
}

func (rm *ReconnectionManager) resetErrorState() {
	rm.state.consecutiveErrors = 0
	rm.state.connectionLost = false
	rm.state.lastError = nil
}

func (rm *ReconnectionManager) resetConnectionState() {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.state.isReconnecting = false
	rm.state.connectionLost = false
	rm.state.consecutiveErrors = 0
}

// ========== LOGGING CENTRALIZADO ==========

func (rm *ReconnectionManager) logConnectionLost() {
	log.Printf("🔴 RADAR: Conexão perdida após %d erros consecutivos", rm.state.consecutiveErrors)
	log.Printf("🔴 RADAR: Último erro: %v", rm.state.lastError)
}

func (rm *ReconnectionManager) logConnectionRestored() {
	log.Printf("✅ RADAR: Conexão estável - resetando contador de erros")
}

func (rm *ReconnectionManager) logReconnectionStart() {
	log.Printf("🔄 RADAR: Iniciando reconexão automática...")
}

func (rm *ReconnectionManager) logAttempt(attempt int) {
	log.Printf("🔄 RADAR: Tentativa de reconexão %d/%d", attempt, rm.config.MaxRetries)
}

func (rm *ReconnectionManager) logAttemptFailed(attempt int, err error) {
	log.Printf("❌ RADAR: Tentativa %d falhou: %v", attempt, err)
}

func (rm *ReconnectionManager) logMeasurementFailed(attempt int, err error) {
	log.Printf("❌ RADAR: Falha ao reiniciar medição na tentativa %d: %v", attempt, err)
}

func (rm *ReconnectionManager) logReconnectionSuccess(attempt int) {
	log.Printf("✅ RADAR: Reconectado com sucesso na tentativa %d", attempt)
}

// ========== CONFIGURAÇÃO E CONTROLE ==========

// SetConfig atualiza configuração de reconexão
func (rm *ReconnectionManager) SetConfig(config ReconnectionConfig) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()
	rm.config = config
}

// GetConfig retorna configuração atual
func (rm *ReconnectionManager) GetConfig() ReconnectionConfig {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.config
}

// GetHealthStatus retorna status de saúde completo
func (rm *ReconnectionManager) GetHealthStatus() map[string]interface{} {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	return map[string]interface{}{
		"is_reconnecting":    rm.state.isReconnecting,
		"connection_lost":    rm.state.connectionLost,
		"consecutive_errors": rm.state.consecutiveErrors,
		"error_threshold":    rm.config.ErrorThreshold,
		"max_retries":        rm.config.MaxRetries,
		"retry_interval_ms":  rm.config.RetryInterval.Milliseconds(),
		"last_error":         rm.formatLastError(),
		"radar_connected":    rm.radar.IsConnected(),
	}
}

// formatLastError formata último erro para display
func (rm *ReconnectionManager) formatLastError() string {
	if rm.state.lastError != nil {
		return rm.state.lastError.Error()
	}
	return "nenhum"
}

// ========== CIRCUIT BREAKER PATTERN ==========

// IsCircuitOpen verifica se circuit breaker está aberto
func (rm *ReconnectionManager) IsCircuitOpen() bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	// Circuit aberto se muitos erros E reconexão falhando
	return rm.state.consecutiveErrors >= rm.config.ErrorThreshold &&
		rm.state.connectionLost &&
		rm.state.isReconnecting
}

// CanAttemptConnection verifica se pode tentar conectar
func (rm *ReconnectionManager) CanAttemptConnection() bool {
	return !rm.IsCircuitOpen() && !rm.IsReconnecting()
}

// ========== METRICS E MONITORAMENTO ==========

// GetReconnectionMetrics retorna métricas de reconexão
func (rm *ReconnectionManager) GetReconnectionMetrics() ReconnectionMetrics {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	return ReconnectionMetrics{
		TotalErrors:      rm.state.consecutiveErrors,
		ErrorThreshold:   rm.config.ErrorThreshold,
		IsHealthy:        rm.state.consecutiveErrors < rm.config.ErrorThreshold,
		ConnectionStatus: rm.getConnectionStatusString(),
		LastErrorMessage: rm.formatLastError(),
		MaxRetries:       rm.config.MaxRetries,
		RetryIntervalMS:  int(rm.config.RetryInterval.Milliseconds()),
	}
}

// ReconnectionMetrics métricas de reconexão
type ReconnectionMetrics struct {
	TotalErrors      int    `json:"total_errors"`
	ErrorThreshold   int    `json:"error_threshold"`
	IsHealthy        bool   `json:"is_healthy"`
	ConnectionStatus string `json:"connection_status"`
	LastErrorMessage string `json:"last_error_message"`
	MaxRetries       int    `json:"max_retries"`
	RetryIntervalMS  int    `json:"retry_interval_ms"`
}

// getConnectionStatusString retorna status em string
func (rm *ReconnectionManager) getConnectionStatusString() string {
	switch {
	case rm.state.isReconnecting:
		return "reconectando"
	case rm.state.connectionLost:
		return "conexão perdida"
	case rm.radar.IsConnected():
		return "conectado"
	default:
		return "desconectado"
	}
}
