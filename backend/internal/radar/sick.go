package radar

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"backend/pkg/models"
)

// SICKRadar representa a conexão e funcionalidade do radar - COMPATÍVEL COM PROCESSOR.GO
type SICKRadar struct {
	IP        string
	Port      int
	conn      net.Conn
	Connected bool
	DebugMode bool

	// Campos para estabilização do objeto principal (mantidos para processor.go)
	objetoPrincipalInfo       *models.ObjetoPrincipalInfo
	thresholdMudanca          float64 // Margem mínima para trocar objeto principal (%)
	ciclosMinimosEstabilidade int     // Número mínimo de ciclos para manter objeto

	// Mutex principal (mantido para compatibilidade com processor.go)
	mutex sync.Mutex

	// Atomic para operações rápidas (adicional para thread safety)
	atomicConnected int32 // 0=false, 1=true
}

// NewSICKRadar cria uma nova instância do radar - MANTÉM EXATO
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
	}
}

// Connect estabelece conexão TCP - THREAD-SAFE sem quebrar interface
func (r *SICKRadar) Connect() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Verificar se já conectado
	if r.Connected && r.conn != nil {
		return nil
	}

	// Tentar conectar
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 5*time.Second)
	if err != nil {
		fmt.Printf("Erro ao conectar: %v\n", err)
		r.Connected = false
		atomic.StoreInt32(&r.atomicConnected, 0)
		return err
	}

	r.conn = conn
	r.Connected = true
	atomic.StoreInt32(&r.atomicConnected, 1)
	fmt.Printf("Conectado ao radar em %s:%d\n", r.IP, r.Port)
	return nil
}

// Disconnect fecha a conexão - THREAD-SAFE
func (r *SICKRadar) Disconnect() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}

	if r.Connected {
		r.Connected = false
		atomic.StoreInt32(&r.atomicConnected, 0)
		fmt.Println("Desconectado do radar")
	}
}

// IsConnected verifica se está conectado - OTIMIZADO
func (r *SICKRadar) IsConnected() bool {
	// Usar atomic para operação ultra-rápida
	return atomic.LoadInt32(&r.atomicConnected) == 1
}

// SetConnected define status de conexão - THREAD-SAFE
func (r *SICKRadar) SetConnected(connected bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.Connected = connected
	if connected {
		atomic.StoreInt32(&r.atomicConnected, 1)
	} else {
		atomic.StoreInt32(&r.atomicConnected, 0)
	}
}

// SendCommand SEM DEADLOCK - crítico para produção
func (r *SICKRadar) SendCommand(command string) ([]byte, error) {
	// Validações rápidas sem lock
	if !r.IsConnected() {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	if command == "" {
		return nil, fmt.Errorf("comando não pode ser vazio")
	}

	// Lock curto só para pegar referências
	r.mutex.Lock()
	conn := r.conn
	debugMode := r.DebugMode
	r.mutex.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("conexão não disponível")
	}

	// Executar operação de rede SEM LOCK
	return r.executeNetworkCommandSafe(conn, command, debugMode)
}

// executeNetworkCommandSafe execução isolada sem locks
func (r *SICKRadar) executeNetworkCommandSafe(conn net.Conn, command string, debugMode bool) ([]byte, error) {
	// Formato CoLa A: <STX>comando<ETX>
	telegram := make([]byte, 0, len(command)+2)
	telegram = append(telegram, 0x02)               // STX
	telegram = append(telegram, []byte(command)...) // Comando
	telegram = append(telegram, 0x03)               // ETX

	if debugMode {
		fmt.Printf("Enviando comando: %s\n", command)
	}

	// Definir timeout para operações de rede
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	// Enviar comando
	if _, err := conn.Write(telegram); err != nil {
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Ler resposta
	buffer := make([]byte, 4096)
	n, err := conn.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	return buffer[:n], nil
}

// StartMeasurement inicia a medição - MANTÉM EXATO
func (r *SICKRadar) StartMeasurement() error {
	fmt.Println("Iniciando medição...")
	_, err := r.SendCommand("sEN LMDradardata 1")
	if err != nil {
		return fmt.Errorf("erro ao iniciar medição: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

// ReadData SEM DEADLOCK - crítico
func (r *SICKRadar) ReadData() ([]byte, error) {
	if !r.IsConnected() {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// Lock curto só para pegar conexão
	r.mutex.Lock()
	conn := r.conn
	r.mutex.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("conexão não disponível")
	}

	// Operação de rede SEM LOCK
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	buffer := make([]byte, 8192)
	n, err := conn.Read(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, nil // Timeout normal
		}
		return nil, fmt.Errorf("erro de leitura: %v", err)
	}

	if n > 0 {
		return buffer[:n], nil
	}
	return nil, nil
}

// SetupContinuousReading configura leitura contínua - THREAD-SAFE
func (r *SICKRadar) SetupContinuousReading() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.conn == nil {
		return fmt.Errorf("não conectado")
	}

	if err := r.conn.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("erro ao remover deadline: %v", err)
	}

	return nil
}

// SetDebugMode ativa/desativa modo debug - THREAD-SAFE
func (r *SICKRadar) SetDebugMode(enabled bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.DebugMode = enabled
}

// GetDebugMode retorna estado do modo debug - THREAD-SAFE
func (r *SICKRadar) GetDebugMode() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.DebugMode
}

// GetAddress retorna endereço de conexão - MANTÉM EXATO
func (r *SICKRadar) GetAddress() string {
	return fmt.Sprintf("%s:%d", r.IP, r.Port)
}

// GetConnectionInfo retorna informações de conexão - THREAD-SAFE
func (r *SICKRadar) GetConnectionInfo() map[string]interface{} {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return map[string]interface{}{
		"ip":        r.IP,
		"port":      r.Port,
		"connected": r.Connected,
		"address":   fmt.Sprintf("%s:%d", r.IP, r.Port),
	}
}

// ValidateConnection verifica se conexão é válida - THREAD-SAFE
func (r *SICKRadar) ValidateConnection() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if !r.Connected {
		return fmt.Errorf("radar não conectado")
	}

	if r.conn == nil {
		return fmt.Errorf("conexão de rede não disponível")
	}

	return nil
}

// SetStabilityParams configura parâmetros - THREAD-SAFE
func (r *SICKRadar) SetStabilityParams(threshold float64, cycles int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if threshold > 0 {
		r.thresholdMudanca = threshold
	}

	if cycles > 0 {
		r.ciclosMinimosEstabilidade = cycles
	}
}

// GetStabilityParams retorna parâmetros - THREAD-SAFE
func (r *SICKRadar) GetStabilityParams() (float64, int) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.thresholdMudanca, r.ciclosMinimosEstabilidade
}

// buildTelegram constrói telegrama CoLa A - método auxiliar
func (r *SICKRadar) buildTelegram(command string) []byte {
	telegram := make([]byte, 0, len(command)+2)
	telegram = append(telegram, 0x02)               // STX
	telegram = append(telegram, []byte(command)...) // Comando
	telegram = append(telegram, 0x03)               // ETX
	return telegram
}
