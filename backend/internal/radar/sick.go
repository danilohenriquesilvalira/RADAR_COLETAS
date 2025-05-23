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
	}
}

// Connect estabelece conexão TCP com o radar
func (r *SICKRadar) Connect() error {
	var err error
	r.conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 5*time.Second)
	if err != nil {
		fmt.Printf("Erro ao conectar: %v\n", err)
		r.Connected = false
		return err
	}

	r.Connected = true
	fmt.Printf("Conectado ao radar em %s:%d\n", r.IP, r.Port)
	return nil
}

// Disconnect fecha a conexão com o radar
func (r *SICKRadar) Disconnect() {
	if r.conn != nil {
		r.conn.Close()
		r.Connected = false
		fmt.Println("Desconectado do radar")
	}
}

// IsConnected verifica se está conectado
func (r *SICKRadar) IsConnected() bool {
	return r.Connected
}

// SetConnected define status de conexão (para recovery)
func (r *SICKRadar) SetConnected(connected bool) {
	r.Connected = connected
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
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	_, err = r.conn.Write(telegram)
	if err != nil {
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Ler resposta
	buffer := make([]byte, 4096)
	n, err := r.conn.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	return buffer[:n], nil
}

// StartMeasurement inicia a medição do radar
func (r *SICKRadar) StartMeasurement() error {
	fmt.Println("Iniciando medição...")
	_, err := r.SendCommand("sEN LMDradardata 1")
	if err != nil {
		return fmt.Errorf("erro ao iniciar medição: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

// ReadData lê dados do radar
func (r *SICKRadar) ReadData() ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// Definir timeout para cada leitura
	err := r.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if err != nil {
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	buffer := make([]byte, 8192)
	n, err := r.conn.Read(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// Timeout normal, retornar nil sem erro
			return nil, nil
		}
		return nil, fmt.Errorf("erro de leitura: %v", err)
	}

	if n > 0 {
		return buffer[:n], nil
	}
	return nil, nil
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
