package plc

import (
	"fmt"
	"sync"
	"time"

	"backend/pkg/models"

	"github.com/robinson/gos7"
)

// SiemensPLC representa a conexão com o PLC Siemens
type SiemensPLC struct {
	IP        string
	Rack      int
	Slot      int
	Connected bool
	Client    gos7.Client
	Handler   *gos7.TCPClientHandler
	mutex     sync.Mutex
}

// NewSiemensPLC cria uma nova instância de conexão com o PLC
func NewSiemensPLC(ip string) *SiemensPLC {
	return &SiemensPLC{
		IP:        ip,
		Rack:      0, // Valores padrão para S7-1200/1500
		Slot:      1,
		Connected: false,
	}
}

// Connect estabelece conexão com o PLC
func (p *SiemensPLC) Connect() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Criar novo handler para conexão TCP
	p.Handler = gos7.NewTCPClientHandler(p.IP, p.Rack, p.Slot)
	p.Handler.Timeout = 5 * time.Second
	p.Handler.IdleTimeout = 10 * time.Second

	// Conectar ao PLC
	err := p.Handler.Connect()
	if err != nil {
		p.Connected = false
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	// Criar cliente S7
	p.Client = gos7.NewClient(p.Handler)
	p.Connected = true

	fmt.Printf("Conectado ao PLC Siemens em %s (Rack: %d, Slot: %d)\n", p.IP, p.Rack, p.Slot)
	return nil
}

// Disconnect fecha a conexão com o PLC
func (p *SiemensPLC) Disconnect() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.Connected && p.Handler != nil {
		p.Handler.Close()
		p.Connected = false
		fmt.Println("Desconectado do PLC Siemens")
	}
}

// GetConnectionStatus retorna o status atual da conexão
func (p *SiemensPLC) GetConnectionStatus() *models.PLCStatus {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	status := &models.PLCStatus{
		Connected: p.Connected,
	}

	if !p.Connected {
		status.Error = "PLC não conectado"
	}

	return status
}

// IsConnected verifica se está conectado
func (p *SiemensPLC) IsConnected() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.Connected
}
