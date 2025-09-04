// ============================================================================
// ARQUIVO 3: backend/internal/plc/siemens.go (MANTIDO IGUAL)
// ============================================================================
package plc

import (
	"fmt"
	"sync"
	"time"

	"backend/pkg/models"

	"github.com/robinson/gos7"
)

// SiemensPLC - SIMPLES, só conexão TCP
type SiemensPLC struct {
	IP        string
	Rack      int
	Slot      int
	Connected bool
	Client    gos7.Client
	Handler   *gos7.TCPClientHandler
	mutex     sync.Mutex
}

func NewSiemensPLC(ip string) *SiemensPLC {
	return &SiemensPLC{
		IP:   ip,
		Rack: 0,
		Slot: 1,
	}
}

func (p *SiemensPLC) Connect() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.forceCleanupResources()
	time.Sleep(200 * time.Millisecond)

	p.Handler = gos7.NewTCPClientHandler(p.IP, p.Rack, p.Slot)
	p.Handler.Timeout = 30 * time.Second
	p.Handler.IdleTimeout = 0

	if err := p.Handler.Connect(); err != nil {
		p.forceCleanupResources()
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	p.Client = gos7.NewClient(p.Handler)
	p.Connected = true

	return nil
}

func (p *SiemensPLC) Disconnect() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.forceCleanupResources()
}

func (p *SiemensPLC) forceCleanupResources() {
	if p.Handler != nil {
		p.Handler.Close()
		p.Handler = nil
	}
	p.Client = nil
	p.Connected = false
}

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

func (p *SiemensPLC) IsConnected() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.Connected && p.Handler != nil && p.Client != nil
}

func (p *SiemensPLC) ForceReconnect() error {
	p.Disconnect()
	time.Sleep(1 * time.Second)
	return p.Connect()
}
