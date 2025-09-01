package radar

import (
	"fmt"
	"net"
	"sync"
	"time"

	"backend/pkg/models"
)

// SICKRadar representa a conexão e funcionalidade do radar
type SICKRadar struct {
	IP        string
	Port      int
	conn      net.Conn
	Connected bool
	DebugMode bool
	// Campos para estabilização do objeto principal
	objetoPrincipalInfo       *models.ObjetoPrincipalInfo
	thresholdMudanca          float64 // Margem mínima para trocar objeto principal (%)
	ciclosMinimosEstabilidade int     // Número mínimo de ciclos para manter objeto
	mutex                     sync.Mutex

	// DETECÇÃO DE DESCONEXÃO
	lastSuccessfulRead   time.Time
	consecutiveErrors    int
	maxConsecutiveErrors int
}

// NewSICKRadar cria uma nova instância do radar
func NewSICKRadar(ip string, port int) *SICKRadar {
	if port == 0 {
		port = 2111 // Porta padrão
	}
	return &SICKRadar{
		IP:                        ip,
		Port:                      port,
		Connected:                 false,
		DebugMode:                 false,
		objetoPrincipalInfo:       nil,
		thresholdMudanca:          15.0, // 15% de diferença mínima para trocar
		ciclosMinimosEstabilidade: 3,    // Manter por pelo menos 3 ciclos

		// DETECÇÃO DE DESCONEXÃO
		lastSuccessfulRead:   time.Now(),
		consecutiveErrors:    0,
		maxConsecutiveErrors: 3, // Após 3 erros consecutivos, marcar como desconectado
	}
}

// Connect estabelece conexão TCP com o radar
func (r *SICKRadar) Connect() error {
	var err error
	r.conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 5*time.Second)
	if err != nil {
		fmt.Printf("❌ Erro ao conectar radar %s:%d: %v\n", r.IP, r.Port, err)
		r.Connected = false
		return err
	}

	// RESETAR contadores ao conectar
	r.Connected = true
	r.lastSuccessfulRead = time.Now()
	r.consecutiveErrors = 0

	fmt.Printf("✅ Conectado ao radar em %s:%d\n", r.IP, r.Port)
	return nil
}

// Disconnect fecha a conexão com o radar
func (r *SICKRadar) Disconnect() {
	if r.conn != nil {
		r.conn.Close()
		r.Connected = false
		fmt.Printf("🔌 Desconectado do radar %s:%d\n", r.IP, r.Port)
	}
}

// IsConnected verifica se está conectado (com validação real)
func (r *SICKRadar) IsConnected() bool {
	if !r.Connected || r.conn == nil {
		return false
	}

	// Se muitos erros consecutivos, considerar desconectado
	if r.consecutiveErrors >= r.maxConsecutiveErrors {
		r.Connected = false
		return false
	}

	// Se não leu dados há muito tempo, considerar desconectado
	timeSinceLastRead := time.Since(r.lastSuccessfulRead)
	if timeSinceLastRead > 15*time.Second {
		fmt.Printf("⚠️ Radar %s:%d sem dados há %.1fs - considerando DESCONECTADO\n",
			r.IP, r.Port, timeSinceLastRead.Seconds())
		r.Connected = false
		return false
	}

	return true
}

// SetConnected define status de conexão (para recovery)
func (r *SICKRadar) SetConnected(connected bool) {
	r.Connected = connected
	if connected {
		r.consecutiveErrors = 0
		r.lastSuccessfulRead = time.Now()
	}
}

// SendCommand envia um comando para o radar e retorna a resposta
func (r *SICKRadar) SendCommand(command string) ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// Formato CoLa A: <STX>comando<ETX>
	telegram := append([]byte{0x02}, []byte(command)...)
	telegram = append(telegram, 0x03)

	if r.DebugMode {
		fmt.Printf("Enviando comando: %s\n", command)
	}

	// Definir timeout para operações de rede
	err := r.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	_, err = r.conn.Write(telegram)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Ler resposta
	buffer := make([]byte, 4096)
	n, err := r.conn.Read(buffer)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	// SUCESSO - resetar contadores
	r.markSuccessfulOperation()
	return buffer[:n], nil
}

// StartMeasurement inicia a medição do radar
func (r *SICKRadar) StartMeasurement() error {
	fmt.Printf("🎯 Iniciando medição do radar %s:%d...\n", r.IP, r.Port)
	_, err := r.SendCommand("sEN LMDradardata 1")
	if err != nil {
		return fmt.Errorf("erro ao iniciar medição: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

// ReadData lê dados do radar COM DETECÇÃO DE DESCONEXÃO
func (r *SICKRadar) ReadData() ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// Definir timeout para cada leitura - MAIS AGRESSIVO
	err := r.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	buffer := make([]byte, 8192)
	n, err := r.conn.Read(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// Timeout normal, mas contar como erro se muito frequente
			r.consecutiveErrors++
			if r.consecutiveErrors >= r.maxConsecutiveErrors {
				fmt.Printf("❌ Radar %s:%d - muitos timeouts consecutivos (%d) - DESCONECTANDO\n",
					r.IP, r.Port, r.consecutiveErrors)
				r.Connected = false
			}
			return nil, nil
		}

		// Erro real de conexão
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro de leitura: %v", err)
	}

	if n > 0 {
		// SUCESSO - resetar contadores
		r.markSuccessfulOperation()
		return buffer[:n], nil
	}

	return nil, nil
}

// markConnectionError marca erro de conexão
func (r *SICKRadar) markConnectionError(err error) {
	r.consecutiveErrors++

	if r.isConnectionError(err) {
		fmt.Printf("🔌 Radar %s:%d - Erro de conexão detectado: %v (erro %d/%d)\n",
			r.IP, r.Port, err, r.consecutiveErrors, r.maxConsecutiveErrors)

		if r.consecutiveErrors >= r.maxConsecutiveErrors {
			fmt.Printf("❌ Radar %s:%d - DESCONEXÃO DETECTADA após %d erros consecutivos\n",
				r.IP, r.Port, r.consecutiveErrors)
			r.Connected = false
		}
	}
}

// markSuccessfulOperation marca operação bem-sucedida
func (r *SICKRadar) markSuccessfulOperation() {
	r.consecutiveErrors = 0
	r.lastSuccessfulRead = time.Now()
}

// isConnectionError verifica se é erro de conexão
func (r *SICKRadar) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Verificar se é erro de rede
	if netErr, ok := err.(net.Error); ok {
		if !netErr.Temporary() {
			return true
		}
	}

	// Verificar strings de erro comuns
	errStr := err.Error()
	connectionErrors := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"network unreachable",
		"no route to host",
		"connection timed out",
		"forcibly closed",
	}

	for _, connErr := range connectionErrors {
		if contains(errStr, connErr) {
			return true
		}
	}

	return false
}

// contains verifica se string contém substring (helper function)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			indexOfSubstring(s, substr) >= 0))
}

// indexOfSubstring encontra substring (helper function)
func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// SetupContinuousReading configura socket para leitura contínua
func (r *SICKRadar) SetupContinuousReading() error {
	if r.conn != nil {
		err := r.conn.SetReadDeadline(time.Time{}) // Sem deadline
		if err != nil {
			return fmt.Errorf("erro ao remover deadline: %v", err)
		}
	}
	return nil
}

// SetDebugMode ativa/desativa modo debug
func (r *SICKRadar) SetDebugMode(enabled bool) {
	r.DebugMode = enabled
}

// GetDebugMode retorna estado do modo debug
func (r *SICKRadar) GetDebugMode() bool {
	return r.DebugMode
}

// GetConnectionStats retorna estatísticas de conexão
func (r *SICKRadar) GetConnectionStats() (int, time.Time) {
	return r.consecutiveErrors, r.lastSuccessfulRead
}
