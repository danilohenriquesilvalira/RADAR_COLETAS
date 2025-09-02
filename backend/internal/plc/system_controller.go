package plc

import (
	"context"
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

	// 🔧 CORREÇÃO: Context para controle de goroutines
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// 🆕 PROCESS MANAGEMENT
	activeProcesses []*exec.Cmd
	processMutex    sync.Mutex

	mutex sync.RWMutex
}

// NewSystemController cria um novo controlador de sistema
func NewSystemController() *SystemController {
	ctx, cancel := context.WithCancel(context.Background())

	return &SystemController{
		restartChan:     make(chan bool, 1),
		shutdownChan:    make(chan bool, 1),
		ctx:             ctx,
		cancel:          cancel,
		activeProcesses: make([]*exec.Cmd, 0),
	}
}

// 🔧 CORREÇÃO: Start com WaitGroup
func (sc *SystemController) Start() {
	log.Println("🚀 SystemController: Iniciando controlador de sistema...")

	sc.wg.Add(1)
	go sc.systemCommandProcessor()

	log.Println("✅ SystemController: Controlador iniciado")
}

// 🔧 CORREÇÃO: Stop para cleanup gracioso
func (sc *SystemController) Stop() {
	log.Println("🛑 SystemController: Parando controlador...")

	// Cancelar context
	sc.cancel()

	// Fechar channels COM PROTEÇÃO
	select {
	case <-sc.restartChan:
		// Channel já vazio
	default:
		// Channel tem dados
	}
	close(sc.restartChan)

	select {
	case <-sc.shutdownChan:
		// Channel já vazio
	default:
		// Channel tem dados
	}
	close(sc.shutdownChan)

	// 🆕 CLEANUP DE PROCESSOS ATIVOS
	sc.cleanupActiveProcesses()

	// Aguardar goroutine terminar
	done := make(chan struct{})
	go func() {
		sc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("✅ SystemController: Goroutine terminada com sucesso")
	case <-time.After(5 * time.Second):
		log.Println("⚠️ SystemController: Timeout ao aguardar goroutine")
	}

	log.Println("✅ SystemController: Controlador parado")
}

// 🔧 CORREÇÃO: systemCommandProcessor com context control
func (sc *SystemController) systemCommandProcessor() {
	defer sc.wg.Done() // ✅ CRÍTICO: WaitGroup done

	log.Println("⚡ SystemCommand processor iniciado")
	defer log.Println("⚡ SystemCommand processor finalizado")

	for {
		select {
		case <-sc.restartChan:
			log.Println("🔄 SYSTEM: Comando de RESTART recebido do PLC")
			sc.executeSystemRestart()

		case <-sc.shutdownChan:
			log.Println("🛑 SYSTEM: Comando de SHUTDOWN recebido")
			sc.executeSystemShutdown()

		case <-sc.ctx.Done(): // ✅ CORREÇÃO CRÍTICA: Context control
			log.Println("   ⚡ SystemCommand recebeu sinal de parada")
			return
		}
	}
}

// RequestSystemRestart solicita restart do sistema COM PROTEÇÃO
func (sc *SystemController) RequestSystemRestart() {
	select {
	case sc.restartChan <- true:
		log.Println("📤 SYSTEM: Solicitação de restart enviada")
	case <-sc.ctx.Done():
		log.Println("⚠️ SYSTEM: Sistema parando - restart cancelado")
	case <-time.After(1 * time.Second):
		log.Println("⚠️ SYSTEM: Restart já em andamento - timeout")
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
		cmd = exec.Command("shutdown", "/r", "/t", "5", "/c", "Radar System Restart")
	case "linux":
		cmd = exec.Command("sudo", "reboot")
	default:
		return fmt.Errorf("sistema operacional não suportado para restart: %s", runtime.GOOS)
	}

	log.Printf("🔄 SYSTEM: Executando comando: %s", cmd.String())

	// 🆕 ADICIONAR À LISTA DE PROCESSOS ATIVOS
	sc.processMutex.Lock()
	sc.activeProcesses = append(sc.activeProcesses, cmd)
	sc.processMutex.Unlock()

	err := cmd.Start()
	if err != nil {
		// 🆕 REMOVER DA LISTA EM CASO DE ERRO
		sc.removeFromActiveProcesses(cmd)
		return fmt.Errorf("erro ao executar comando de restart: %v", err)
	}

	return nil
}

// restartViaExitCode sai do programa com código especial para restart
func (sc *SystemController) restartViaExitCode() {
	log.Println("🔄 SYSTEM: Saindo com código de restart...")

	// Cleanup antes de sair
	sc.cleanupActiveProcesses()

	// Exit code especial para restart
	os.Exit(99)
}

// executeSystemShutdown executa shutdown do sistema
func (sc *SystemController) executeSystemShutdown() {
	log.Println("🛑 SYSTEM: Executando shutdown graceful...")

	// 🆕 CLEANUP COMPLETO
	sc.cleanupActiveProcesses()

	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}

// 🔧 CORREÇÃO: RestartApplication sem processos órfãos
func (sc *SystemController) RestartApplication() {
	log.Println("🔄 APPLICATION: Reiniciando aplicação...")

	executable, err := os.Executable()
	if err != nil {
		log.Printf("❌ APPLICATION: Erro ao obter executável: %v", err)
		return
	}

	// 🔧 CORREÇÃO: Configurar comando com context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 🆕 ADICIONAR À LISTA DE PROCESSOS ATIVOS
	sc.processMutex.Lock()
	sc.activeProcesses = append(sc.activeProcesses, cmd)
	sc.processMutex.Unlock()

	err = cmd.Start()
	if err != nil {
		log.Printf("❌ APPLICATION: Erro ao iniciar nova instância: %v", err)
		sc.removeFromActiveProcesses(cmd)
		return
	}

	log.Printf("✅ APPLICATION: Nova instância iniciada com PID: %d", cmd.Process.Pid)

	// 🔧 CORREÇÃO: Aguardar processo de forma assíncrona
	go func() {
		// Aguardar processo filho
		err := cmd.Wait()
		if err != nil {
			log.Printf("⚠️ APPLICATION: Processo filho terminou com erro: %v", err)
		}

		// Remover da lista
		sc.removeFromActiveProcesses(cmd)

		// Sair após delay
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// 🆕 cleanupActiveProcesses limpa todos os processos ativos
func (sc *SystemController) cleanupActiveProcesses() {
	sc.processMutex.Lock()
	defer sc.processMutex.Unlock()

	for _, cmd := range sc.activeProcesses {
		if cmd.Process != nil {
			log.Printf("🧹 Terminando processo PID: %d", cmd.Process.Pid)

			// Tentar terminar graciosamente
			cmd.Process.Signal(os.Interrupt)

			// Aguardar um pouco
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			select {
			case <-done:
				// Processo terminou
			case <-time.After(3 * time.Second):
				// Force kill após timeout
				log.Printf("🔨 Force kill processo PID: %d", cmd.Process.Pid)
				cmd.Process.Kill()
			}
		}
	}

	// Limpar lista
	sc.activeProcesses = sc.activeProcesses[:0]
	log.Println("✅ Cleanup de processos concluído")
}

// 🆕 removeFromActiveProcesses remove processo da lista
func (sc *SystemController) removeFromActiveProcesses(targetCmd *exec.Cmd) {
	sc.processMutex.Lock()
	defer sc.processMutex.Unlock()

	for i, cmd := range sc.activeProcesses {
		if cmd == targetCmd {
			// Remover da lista
			sc.activeProcesses = append(sc.activeProcesses[:i], sc.activeProcesses[i+1:]...)
			break
		}
	}
}

// 🔧 CORREÇÃO: HealthChecker thread-safe
type HealthChecker struct {
	lastRadarData   time.Time
	lastPLCResponse time.Time
	systemStartTime time.Time

	// 🆕 ATOMIC OPERATIONS
	mutex sync.RWMutex

	// 🆕 STATISTICS
	radarDataCount   int64
	plcResponseCount int64
}

// NewHealthChecker cria um novo verificador de saúde
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		systemStartTime: time.Now(),
		lastRadarData:   time.Now(),
		lastPLCResponse: time.Now(),
	}
}

// 🔧 CORREÇÃO: UpdateRadarData thread-safe
func (hc *HealthChecker) UpdateRadarData() {
	hc.mutex.Lock()
	hc.lastRadarData = time.Now()
	hc.radarDataCount++
	hc.mutex.Unlock()
}

// 🔧 CORREÇÃO: UpdatePLCResponse thread-safe
func (hc *HealthChecker) UpdatePLCResponse() {
	hc.mutex.Lock()
	hc.lastPLCResponse = time.Now()
	hc.plcResponseCount++
	hc.mutex.Unlock()
}

// 🔧 CORREÇÃO: GetSystemHealth completamente thread-safe
func (hc *HealthChecker) GetSystemHealth() *SystemHealth {
	hc.mutex.RLock()

	// 🆕 COPIAR VALORES PARA EVITAR RACE CONDITIONS
	now := time.Now()
	lastRadarData := hc.lastRadarData
	lastPLCResponse := hc.lastPLCResponse
	systemStartTime := hc.systemStartTime
	radarDataCount := hc.radarDataCount
	plcResponseCount := hc.plcResponseCount

	hc.mutex.RUnlock()

	health := &SystemHealth{
		SystemUptime:       now.Sub(systemStartTime),
		LastRadarDataAge:   now.Sub(lastRadarData),
		LastPLCResponseAge: now.Sub(lastPLCResponse),
		RadarDataHealthy:   now.Sub(lastRadarData) < 10*time.Second,
		PLCResponseHealthy: now.Sub(lastPLCResponse) < 30*time.Second,

		// 🆕 ESTATÍSTICAS ADICIONAIS
		RadarDataCount:   radarDataCount,
		PLCResponseCount: plcResponseCount,
	}

	health.OverallHealthy = health.RadarDataHealthy && health.PLCResponseHealthy

	return health
}

// 🔧 CORREÇÃO: SystemHealth expandido
type SystemHealth struct {
	SystemUptime       time.Duration `json:"systemUptime"`
	LastRadarDataAge   time.Duration `json:"lastRadarDataAge"`
	LastPLCResponseAge time.Duration `json:"lastPLCResponseAge"`
	RadarDataHealthy   bool          `json:"radarDataHealthy"`
	PLCResponseHealthy bool          `json:"plcResponseHealthy"`
	OverallHealthy     bool          `json:"overallHealthy"`

	// 🆕 ESTATÍSTICAS
	RadarDataCount   int64 `json:"radarDataCount"`
	PLCResponseCount int64 `json:"plcResponseCount"`
}

// 🆕 GetProcessStats retorna estatísticas de processos
func (sc *SystemController) GetProcessStats() map[string]interface{} {
	sc.processMutex.Lock()
	defer sc.processMutex.Unlock()

	return map[string]interface{}{
		"active_processes": len(sc.activeProcesses),
		"goroutines":       runtime.NumGoroutine(),
		"os":               runtime.GOOS,
		"arch":             runtime.GOARCH,
	}
}
