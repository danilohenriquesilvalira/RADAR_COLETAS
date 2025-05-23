package plc

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// SystemController gerencia comandos de sistema como restart
type SystemController struct {
	restartChan  chan bool
	shutdownChan chan bool
	restartNATS  chan bool
	restartWS    chan bool
	mutex        sync.RWMutex
}

// NewSystemController cria um novo controlador de sistema
func NewSystemController() *SystemController {
	return &SystemController{
		restartChan:  make(chan bool, 1),
		shutdownChan: make(chan bool, 1),
		restartNATS:  make(chan bool, 1),
		restartWS:    make(chan bool, 1),
	}
}

// Start inicia o controlador de sistema
func (sc *SystemController) Start() {
	go sc.systemCommandProcessor()
}

// systemCommandProcessor processa comandos de sistema
func (sc *SystemController) systemCommandProcessor() {
	for {
		select {
		case <-sc.restartChan:
			log.Println("🔄 SYSTEM: Comando de RESTART recebido do PLC")
			sc.executeSystemRestart()

		case <-sc.shutdownChan:
			log.Println("🛑 SYSTEM: Comando de SHUTDOWN recebido")
			sc.executeSystemShutdown()

		case <-sc.restartNATS:
			log.Println("🔄 SYSTEM: Restart NATS solicitado")
			// Implementar restart específico do NATS

		case <-sc.restartWS:
			log.Println("🔄 SYSTEM: Restart WebSocket solicitado")
			// Implementar restart específico do WebSocket
		}
	}
}

// RequestSystemRestart solicita restart do sistema
func (sc *SystemController) RequestSystemRestart() {
	select {
	case sc.restartChan <- true:
		log.Println("📤 SYSTEM: Solicitação de restart enviada")
	default:
		log.Println("⚠️ SYSTEM: Restart já em andamento")
	}
}

// RequestNATSRestart solicita restart do NATS
func (sc *SystemController) RequestNATSRestart() {
	select {
	case sc.restartNATS <- true:
		log.Println("📤 SYSTEM: Solicitação de restart NATS enviada")
	default:
		log.Println("⚠️ SYSTEM: Restart NATS já em andamento")
	}
}

// RequestWebSocketRestart solicita restart do WebSocket
func (sc *SystemController) RequestWebSocketRestart() {
	select {
	case sc.restartWS <- true:
		log.Println("📤 SYSTEM: Solicitação de restart WebSocket enviada")
	default:
		log.Println("⚠️ SYSTEM: Restart WebSocket já em andamento")
	}
}

// executeSystemRestart executa restart do sistema
func (sc *SystemController) executeSystemRestart() {
	log.Println("🔄 SYSTEM: Executando restart do sistema...")

	// Dar tempo para logs serem escritos
	time.Sleep(1 * time.Second)

	// Método 1: Tentar restart via comando do sistema
	err := sc.restartViaSystemCommand()
	if err != nil {
		log.Printf("❌ SYSTEM: Falha no restart via comando: %v", err)

		// Método 2: Tentar restart via exit code especial
		sc.restartViaExitCode()
	}
}

// restartViaSystemCommand tenta restart via comando do sistema operacional
func (sc *SystemController) restartViaSystemCommand() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// Windows: usar shutdown command
		cmd = exec.Command("shutdown", "/r", "/t", "5", "/c", "Radar System Restart")

	case "linux":
		// Linux: usar systemctl ou reboot
		cmd = exec.Command("sudo", "reboot")

	default:
		return fmt.Errorf("sistema operacional não suportado para restart: %s", runtime.GOOS)
	}

	log.Printf("🔄 SYSTEM: Executando comando: %s", cmd.String())

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("erro ao executar comando de restart: %v", err)
	}

	return nil
}

// restartViaExitCode sai do programa com código especial para restart
func (sc *SystemController) restartViaExitCode() {
	log.Println("🔄 SYSTEM: Saindo com código de restart...")

	// Usar exit code especial que pode ser detectado por um script supervisor
	// Por exemplo, um batch file ou systemd service que monitora este código
	os.Exit(99) // Código especial para restart
}

// executeSystemShutdown executa shutdown do sistema
func (sc *SystemController) executeSystemShutdown() {
	log.Println("🛑 SYSTEM: Executando shutdown graceful...")

	// Cleanup e saída normal
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}

// RestartApplication reinicia apenas a aplicação (não o sistema)
func (sc *SystemController) RestartApplication() {
	log.Println("🔄 APPLICATION: Reiniciando aplicação...")

	// Método para reiniciar apenas a aplicação Go
	// Isso é útil para recarregar configurações sem reiniciar o sistema todo

	// Obter caminho do executável atual
	executable, err := os.Executable()
	if err != nil {
		log.Printf("❌ APPLICATION: Erro ao obter executável: %v", err)
		return
	}

	// Iniciar nova instância
	cmd := exec.Command(executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Start()
	if err != nil {
		log.Printf("❌ APPLICATION: Erro ao iniciar nova instância: %v", err)
		return
	}

	log.Printf("✅ APPLICATION: Nova instância iniciada com PID: %d", cmd.Process.Pid)

	// Sair da instância atual
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// HealthChecker verifica saúde do sistema
type HealthChecker struct {
	lastRadarData   time.Time
	lastPLCResponse time.Time
	systemStartTime time.Time
	mutex           sync.RWMutex
}

// NewHealthChecker cria um novo verificador de saúde
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		systemStartTime: time.Now(),
	}
}

// UpdateRadarData atualiza timestamp dos dados do radar
func (hc *HealthChecker) UpdateRadarData() {
	hc.mutex.Lock()
	hc.lastRadarData = time.Now()
	hc.mutex.Unlock()
}

// UpdatePLCResponse atualiza timestamp da resposta PLC
func (hc *HealthChecker) UpdatePLCResponse() {
	hc.mutex.Lock()
	hc.lastPLCResponse = time.Now()
	hc.mutex.Unlock()
}

// GetSystemHealth retorna status de saúde do sistema
func (hc *HealthChecker) GetSystemHealth() *SystemHealth {
	hc.mutex.RLock()
	defer hc.mutex.RUnlock()

	now := time.Now()

	health := &SystemHealth{
		SystemUptime:       now.Sub(hc.systemStartTime),
		LastRadarDataAge:   now.Sub(hc.lastRadarData),
		LastPLCResponseAge: now.Sub(hc.lastPLCResponse),
		RadarDataHealthy:   now.Sub(hc.lastRadarData) < 10*time.Second,
		PLCResponseHealthy: now.Sub(hc.lastPLCResponse) < 30*time.Second,
	}

	health.OverallHealthy = health.RadarDataHealthy && health.PLCResponseHealthy

	return health
}

// SystemHealth representa a saúde do sistema
type SystemHealth struct {
	SystemUptime       time.Duration `json:"systemUptime"`
	LastRadarDataAge   time.Duration `json:"lastRadarDataAge"`
	LastPLCResponseAge time.Duration `json:"lastPLCResponseAge"`
	RadarDataHealthy   bool          `json:"radarDataHealthy"`
	PLCResponseHealthy bool          `json:"plcResponseHealthy"`
	OverallHealthy     bool          `json:"overallHealthy"`
}
