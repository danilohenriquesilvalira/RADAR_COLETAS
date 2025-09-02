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

	// 🆕 RESOURCE LEAK PREVENTION
	connectionID   string
	createdAt      time.Time
	lastCleanup    time.Time
	reconnectCount int
}

// NewSiemensPLC cria uma nova instância de conexão com o PLC
func NewSiemensPLC(ip string) *SiemensPLC {
	now := time.Now()
	connectionID := fmt.Sprintf("plc_%s_%d", ip, now.Unix())

	return &SiemensPLC{
		IP:             ip,
		Rack:           0, // Valores padrão para S7-1200/1500
		Slot:           1,
		Connected:      false,
		connectionID:   connectionID,
		createdAt:      now,
		lastCleanup:    now,
		reconnectCount: 0,
	}
}

// 🔧 CORREÇÃO CRÍTICA: Connect com resource leak prevention
func (p *SiemensPLC) Connect() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// 🛡️ CLEANUP COMPLETO de recursos anteriores
	p.forceCleanupResources()

	// 🆕 AGUARDAR CLEANUP COMPLETO
	time.Sleep(200 * time.Millisecond)

	// Criar novo handler para conexão TCP
	p.Handler = gos7.NewTCPClientHandler(p.IP, p.Rack, p.Slot)
	p.Handler.Timeout = 30 * time.Second // ✅ Timeout generoso
	p.Handler.IdleTimeout = 0            // ✅ Sem idle timeout

	// Conectar ao PLC
	err := p.Handler.Connect()
	if err != nil {
		// 🛡️ CLEANUP em caso de erro
		p.forceCleanupResources()
		p.Connected = false
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	// Criar cliente S7
	p.Client = gos7.NewClient(p.Handler)
	p.Connected = true
	p.reconnectCount++
	p.lastCleanup = time.Now()

	fmt.Printf("✅ Conectado ao PLC Siemens em %s (Rack: %d, Slot: %d, ID: %s, Reconexão: %d)\n",
		p.IP, p.Rack, p.Slot, p.connectionID, p.reconnectCount)
	return nil
}

// 🔧 CORREÇÃO CRÍTICA: Disconnect com cleanup completo
func (p *SiemensPLC) Disconnect() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.forceCleanupResources()
	fmt.Printf("🔌 Desconectado do PLC Siemens %s (ID: %s)\n", p.IP, p.connectionID)
}

// 🆕 forceCleanupResources - cleanup completo para evitar resource leaks
func (p *SiemensPLC) forceCleanupResources() {
	// 1. Fechar Handler se existir
	if p.Handler != nil {
		p.Handler.Close()
		p.Handler = nil // ✅ CRÍTICO: Limpar referência
	}

	// 2. Limpar Client
	p.Client = nil // ✅ CRÍTICO: Limpar referência

	// 3. Marcar como desconectado
	p.Connected = false

	// 4. Atualizar timestamp de cleanup
	p.lastCleanup = time.Now()

	fmt.Printf("🧹 PLC cleanup realizado para %s\n", p.IP)
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
	return p.Connected && p.Handler != nil && p.Client != nil
}

// 🆕 GetResourceStats retorna estatísticas de recursos
func (p *SiemensPLC) GetResourceStats() map[string]interface{} {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return map[string]interface{}{
		"connection_id":   p.connectionID,
		"created_at":      p.createdAt.Format("2006-01-02 15:04:05"),
		"last_cleanup":    p.lastCleanup.Format("2006-01-02 15:04:05"),
		"reconnect_count": p.reconnectCount,
		"connected":       p.Connected,
		"has_handler":     p.Handler != nil,
		"has_client":      p.Client != nil,
	}
}

// 🆕 ForceReconnect força reconexão
func (p *SiemensPLC) ForceReconnect() error {
	fmt.Printf("🔄 Forçando reconexão PLC %s...\n", p.IP)
	p.Disconnect()
	time.Sleep(1 * time.Second)
	return p.Connect()
}
