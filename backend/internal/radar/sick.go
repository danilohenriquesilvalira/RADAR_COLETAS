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
	thresholdMudanca          float64
	ciclosMinimosEstabilidade int
	mutex                     sync.Mutex

	// 🔧 CORREÇÃO: Detecção de desconexão melhorada
	lastSuccessfulRead   time.Time
	consecutiveErrors    int
	maxConsecutiveErrors int

	// 🆕 TCP LEAK PREVENTION
	connectionID   string    // ID único para debug
	createdAt      time.Time // Timestamp de criação
	lastCleanup    time.Time // Último cleanup realizado
	forceReconnect bool      // Flag para forçar reconexão
}

// NewSICKRadar cria uma nova instância do radar
func NewSICKRadar(ip string, port int) *SICKRadar {
	if port == 0 {
		port = 2111 // Porta padrão
	}

	now := time.Now()
	connectionID := fmt.Sprintf("%s_%d_%d", ip, port, now.Unix())

	return &SICKRadar{
		IP:                        ip,
		Port:                      port,
		Connected:                 false,
		DebugMode:                 false,
		objetoPrincipalInfo:       nil,
		thresholdMudanca:          15.0, // 15% de diferença mínima para trocar
		ciclosMinimosEstabilidade: 3,    // Manter por pelo menos 3 ciclos

		// 🔧 CORREÇÃO: Timeouts menos agressivos
		lastSuccessfulRead:   now,
		consecutiveErrors:    0,
		maxConsecutiveErrors: 5, // ✅ AUMENTADO de 3 para 5

		// 🆕 TCP LEAK PREVENTION
		connectionID:   connectionID,
		createdAt:      now,
		lastCleanup:    now,
		forceReconnect: false,
	}
}

// 🔧 CORREÇÃO CRÍTICA: Connect com TCP leak prevention
func (r *SICKRadar) Connect() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// 🛡️ CLEANUP COMPLETO da conexão anterior
	r.forceCloseConnection()

	// 🆕 AGUARDAR CLEANUP COMPLETO
	time.Sleep(100 * time.Millisecond)

	// 🔧 CORREÇÃO: Timeout mais generoso
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 10*time.Second)
	if err != nil {
		fmt.Printf("❌ Erro ao conectar radar %s:%d: %v\n", r.IP, r.Port, err)
		r.Connected = false
		return err
	}

	// 🆕 CONFIGURAR TCP KEEPALIVE para detectar conexões mortas
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		tcpConn.SetLinger(0) // ✅ FORÇA CLOSE IMEDIATO
	}

	r.conn = conn
	r.Connected = true
	r.lastSuccessfulRead = time.Now()
	r.consecutiveErrors = 0
	r.lastCleanup = time.Now()
	r.forceReconnect = false

	fmt.Printf("✅ Conectado ao radar em %s:%d (ID: %s)\n", r.IP, r.Port, r.connectionID)
	return nil
}

// 🔧 CORREÇÃO CRÍTICA: Disconnect com cleanup completo
func (r *SICKRadar) Disconnect() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.forceCloseConnection()
	fmt.Printf("🔌 Desconectado do radar %s:%d (ID: %s)\n", r.IP, r.Port, r.connectionID)
}

// 🆕 forceCloseConnection - cleanup completo para evitar TCP leaks
func (r *SICKRadar) forceCloseConnection() {
	if r.conn != nil {
		// 🛡️ MÚLTIPLAS CAMADAS DE CLEANUP

		// 1. Set linger para 0 (force close)
		if tcpConn, ok := r.conn.(*net.TCPConn); ok {
			tcpConn.SetLinger(0)
		}

		// 2. Set deadline para forçar close
		r.conn.SetDeadline(time.Now())

		// 3. Close real
		r.conn.Close()

		// 4. Limpar referência
		r.conn = nil

		r.lastCleanup = time.Now()

		if r.DebugMode {
			fmt.Printf("🧹 TCP cleanup realizado para %s:%d\n", r.IP, r.Port)
		}
	}

	r.Connected = false
}

// 🔧 CORREÇÃO: IsConnected com validação TCP real
func (r *SICKRadar) IsConnected() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if !r.Connected || r.conn == nil {
		return false
	}

	// 🆕 FORÇAR RECONEXÃO se solicitado
	if r.forceReconnect {
		r.Connected = false
		return false
	}

	// Se muitos erros consecutivos, considerar desconectado
	if r.consecutiveErrors >= r.maxConsecutiveErrors {
		fmt.Printf("❌ Radar %s:%d - muitos erros consecutivos (%d) - DESCONECTANDO\n",
			r.IP, r.Port, r.consecutiveErrors)
		r.Connected = false
		return false
	}

	// 🔧 CORREÇÃO: Timeout menos agressivo
	timeSinceLastRead := time.Since(r.lastSuccessfulRead)
	if timeSinceLastRead > 30*time.Second { // ✅ AUMENTADO de 15s para 30s
		fmt.Printf("⚠️ Radar %s:%d sem dados há %.1fs - considerando DESCONECTADO\n",
			r.IP, r.Port, timeSinceLastRead.Seconds())
		r.Connected = false
		return false
	}

	return true
}

// SetConnected define status de conexão (para recovery)
func (r *SICKRadar) SetConnected(connected bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.Connected = connected
	if connected {
		r.consecutiveErrors = 0
		r.lastSuccessfulRead = time.Now()
		r.forceReconnect = false
	} else {
		r.forceReconnect = true
	}
}

// 🔧 CORREÇÃO: SendCommand com timeout management adequado
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

	// 🔧 CORREÇÃO: Timeout menos agressivo
	err := r.conn.SetDeadline(time.Now().Add(10 * time.Second)) // ✅ AUMENTADO de 5s para 10s
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	_, err = r.conn.Write(telegram)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}

	// 🔧 CORREÇÃO: Sleep menor
	time.Sleep(100 * time.Millisecond) // ✅ REDUZIDO de 500ms para 100ms

	// Ler resposta
	buffer := make([]byte, 4096)
	n, err := r.conn.Read(buffer)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	// ✅ RESETAR DEADLINE após sucesso
	r.conn.SetDeadline(time.Time{})
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
	time.Sleep(100 * time.Millisecond) // ✅ REDUZIDO de 500ms
	return nil
}

// 🔧 CORREÇÃO CRÍTICA: ReadData com timeout management
func (r *SICKRadar) ReadData() ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// 🔧 CORREÇÃO: Timeout menos agressivo
	err := r.conn.SetReadDeadline(time.Now().Add(1 * time.Second)) // ✅ AUMENTADO de 200ms para 1s
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	buffer := make([]byte, 8192)
	n, err := r.conn.Read(buffer)

	// 🆕 SEMPRE RESETAR DEADLINE após read
	r.conn.SetReadDeadline(time.Time{})

	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// 🔧 CORREÇÃO: Timeout normal, contar apenas se muito frequente
			r.consecutiveErrors++
			if r.consecutiveErrors >= r.maxConsecutiveErrors {
				fmt.Printf("❌ Radar %s:%d - muitos timeouts consecutivos (%d) - DESCONECTANDO\n",
					r.IP, r.Port, r.consecutiveErrors)
				r.Connected = false
			}
			return nil, nil // ✅ NÃO é erro fatal
		}

		// Erro real de conexão
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro de leitura: %v", err)
	}

	if n > 0 {
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
			r.forceReconnect = true // ✅ MARCAR PARA RECONEXÃO
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
		"use of closed network connection",
	}

	for _, connErr := range connectionErrors {
		if contains(errStr, connErr) {
			return true
		}
	}

	return false
}

// 🆕 ForceReconnect força reconexão na próxima verificação
func (r *SICKRadar) ForceReconnect() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.forceReconnect = true
	r.Connected = false
	fmt.Printf("🔄 Reconexão forçada para radar %s:%d\n", r.IP, r.Port)
}

// 🆕 GetTCPStats retorna estatísticas TCP
func (r *SICKRadar) GetTCPStats() map[string]interface{} {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return map[string]interface{}{
		"connection_id":      r.connectionID,
		"created_at":         r.createdAt.Format("2006-01-02 15:04:05"),
		"last_cleanup":       r.lastCleanup.Format("2006-01-02 15:04:05"),
		"consecutive_errors": r.consecutiveErrors,
		"last_successful":    r.lastSuccessfulRead.Format("2006-01-02 15:04:05"),
		"force_reconnect":    r.forceReconnect,
		"connected":          r.Connected,
	}
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
		// ✅ RESETAR DEADLINE adequadamente
		err := r.conn.SetReadDeadline(time.Time{})
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
