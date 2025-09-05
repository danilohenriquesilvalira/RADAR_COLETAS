// ============================================================================
// ARQUIVO: backend/internal/plc/siemens.go - CORREÇÃO RACE CONDITION
// CORREÇÃO: Thread-safe completo para SiemensPLC
// ============================================================================
package plc

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"backend/pkg/models"

	"github.com/robinson/gos7"
)

// SiemensPLC - THREAD-SAFE COMPLETO
type SiemensPLC struct {
	IP        string
	Rack      int
	Slot      int
	connected int32 // CORREÇÃO: usar atomic ao invés de bool
	Client    gos7.Client
	Handler   *gos7.TCPClientHandler
	mutex     sync.RWMutex // CORREÇÃO: RWMutex para melhor performance
	opMutex   sync.Mutex   // NOVO: mutex separado para operações críticas
}

func NewSiemensPLC(ip string) *SiemensPLC {
	return &SiemensPLC{
		IP:        ip,
		Rack:      0,
		Slot:      1,
		connected: 0, // false
	}
}

func (p *SiemensPLC) Connect() error {
	// CORREÇÃO: Usar mutex separado para operações críticas
	p.opMutex.Lock()
	defer p.opMutex.Unlock()

	// Forçar cleanup antes de reconectar
	p.forceCleanupResourcesUnsafe()
	time.Sleep(200 * time.Millisecond)

	// Criar nova conexão
	handler := gos7.NewTCPClientHandler(p.IP, p.Rack, p.Slot)
	handler.Timeout = 30 * time.Second
	handler.IdleTimeout = 0

	if err := handler.Connect(); err != nil {
		// Limpar resources em caso de erro
		if handler != nil {
			handler.Close()
		}
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	client := gos7.NewClient(handler)

	// CORREÇÃO: Atualizar campos atomicamente
	p.mutex.Lock()
	p.Handler = handler
	p.Client = client
	p.mutex.Unlock()

	atomic.StoreInt32(&p.connected, 1) // true

	return nil
}

func (p *SiemensPLC) Disconnect() {
	// CORREÇÃO: Usar mutex separado para operações críticas
	p.opMutex.Lock()
	defer p.opMutex.Unlock()

	p.forceCleanupResourcesUnsafe()
}

// CORREÇÃO: Método thread-safe público
func (p *SiemensPLC) ForceCleanupResources() {
	p.opMutex.Lock()
	defer p.opMutex.Unlock()
	p.forceCleanupResourcesUnsafe()
}

// CORREÇÃO: Método unsafe interno (deve ser chamado com opMutex locked)
func (p *SiemensPLC) forceCleanupResourcesUnsafe() {
	// Primeiro atualizar estado atomico
	atomic.StoreInt32(&p.connected, 0) // false

	// Depois limpar resources com mutex
	p.mutex.Lock()
	var handler *gos7.TCPClientHandler
	if p.Handler != nil {
		handler = p.Handler
		p.Handler = nil
	}
	p.Client = nil
	p.mutex.Unlock()

	// Fechar handler fora do mutex para evitar deadlock
	if handler != nil {
		handler.Close()
	}
}

func (p *SiemensPLC) GetConnectionStatus() *models.PLCStatus {
	connected := atomic.LoadInt32(&p.connected) == 1

	status := &models.PLCStatus{
		Connected: connected,
	}

	if !connected {
		status.Error = "PLC não conectado"
	}

	return status
}

func (p *SiemensPLC) IsConnected() bool {
	if atomic.LoadInt32(&p.connected) == 0 {
		return false
	}

	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.Handler != nil && p.Client != nil
}

func (p *SiemensPLC) ForceReconnect() error {
	p.Disconnect()
	time.Sleep(1 * time.Second)
	return p.Connect()
}

// NOVO: Método thread-safe para obter client
func (p *SiemensPLC) GetClient() gos7.Client {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.Client
}

// NOVO: Método thread-safe para verificar se client está disponível
func (p *SiemensPLC) HasValidClient() bool {
	if atomic.LoadInt32(&p.connected) == 0 {
		return false
	}

	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.Client != nil && p.Handler != nil
}
