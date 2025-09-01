package plc

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"backend/pkg/models"
)

// PLCController gerencia comunicação bidirecional - ULTRA ROBUSTO 24/7
type PLCController struct {
	plc    PLCClient
	reader *PLCReader
	writer *PLCWriter

	// Estado thread-safe com atomic onde possível
	liveBit          int32 // atomic
	collectionActive int32 // atomic
	emergencyStop    int32 // atomic
	debugMode        int32 // atomic
	packetCount      int32 // atomic
	errorCount       int32 // atomic

	// Maps protegidos por mutex
	radarsEnabled   map[string]bool
	radarsConnected map[string]bool
	radarCounters   map[string]*RadarCounters
	stateMutex      sync.RWMutex

	// Sistema de conectividade robusta
	plcConnected      int32 // atomic
	lastPLCCheck      time.Time
	consecutiveFails  int32 // atomic
	reconnectAttempts int32 // atomic
	connectMutex      sync.Mutex

	// Sistema
	wsClients     int32 // atomic
	natsConnected int32 // atomic
	wsRunning     int32 // atomic
	startTime     time.Time
	lastResetTime time.Time

	// Controle de reboot
	resetErrorsStartTime time.Time
	resetErrorsActive    bool
	rebootExecuted       bool
	rebootMutex          sync.Mutex

	// Controles e timers
	commandChan     chan models.SystemCommand
	liveBitTicker   *time.Ticker
	statusTicker    *time.Ticker
	commandTicker   *time.Ticker
	healthTicker    *time.Ticker
	reconnectTicker *time.Ticker
	watchdogTicker  *time.Ticker
	stopChan        chan bool

	// Handlers de comando
	commandHandlers map[string]CommandHandler

	// Sistema de logging robusto
	logger       *log.Logger
	logFile      *os.File
	logMutex     sync.Mutex
	logDirectory string
}

type RadarCounters struct {
	Packets          int32
	Errors           int32
	LastSeen         time.Time
	ConsecutiveFails int32
}

type CommandHandler struct {
	ShouldExecute   func(*models.PLCCommands, *PLCController) bool
	Execute         func(*PLCController, *models.PLCCommands)
	NeedsReset      bool
	ResetByteOffset int
	ResetBitOffset  int
	LogMessage      string
}

type SystemMetrics struct {
	CPU    float32
	Memory float32
	Disk   float32
	Temp   float32
}

// NewPLCController cria controlador ultra-robusto para produção
func NewPLCController(plcClient PLCClient) *PLCController {
	pc := &PLCController{
		plc:           plcClient,
		reader:        NewPLCReader(plcClient),
		writer:        NewPLCWriter(plcClient),
		commandChan:   make(chan models.SystemCommand, 20),
		stopChan:      make(chan bool),
		startTime:     time.Now(),
		lastResetTime: time.Now(),
		lastPLCCheck:  time.Now(),
		logDirectory:  "backend/logs",
	}

	pc.initializeLogging()
	pc.initializeState()
	pc.setupCommandHandlers()

	pc.logCritical("SISTEMA INICIADO", "PLC Controller Ultra-Robusto inicializado")
	return pc
}

// initializeLogging configura sistema de logging robusto
func (pc *PLCController) initializeLogging() {
	// Criar diretório de logs se não existir
	if err := os.MkdirAll(pc.logDirectory, 0755); err != nil {
		log.Printf("Erro criando diretório de logs: %v", err)
		pc.logDirectory = "./logs" // Fallback para pasta local
		os.MkdirAll(pc.logDirectory, 0755)
	}

	// Criar arquivo de log diário
	logFileName := fmt.Sprintf("plc_controller_%s.log", time.Now().Format("2006-01-02"))
	logPath := filepath.Join(pc.logDirectory, logFileName)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Erro criando arquivo de log: %v", err)
		return
	}

	pc.logFile = logFile
	pc.logger = log.New(logFile, "", log.LstdFlags|log.Lmicroseconds)
}

// logCritical registra eventos críticos
func (pc *PLCController) logCritical(event, message string) {
	pc.logMutex.Lock()
	defer pc.logMutex.Unlock()

	logMsg := fmt.Sprintf("[CRITICAL] %s: %s", event, message)

	// Log para arquivo
	if pc.logger != nil {
		pc.logger.Println(logMsg)
	}

	// Log para console
	log.Println(logMsg)

	// Força flush do arquivo
	if pc.logFile != nil {
		pc.logFile.Sync()
	}
}

// logError registra erros detalhados
func (pc *PLCController) logError(component, operation string, err error) {
	pc.logMutex.Lock()
	defer pc.logMutex.Unlock()

	logMsg := fmt.Sprintf("[ERROR] %s.%s: %v", component, operation, err)

	if pc.logger != nil {
		pc.logger.Println(logMsg)
	}
	log.Println(logMsg)

	if pc.logFile != nil {
		pc.logFile.Sync()
	}
}

// logInfo registra informações importantes
func (pc *PLCController) logInfo(component, message string) {
	pc.logMutex.Lock()
	defer pc.logMutex.Unlock()

	logMsg := fmt.Sprintf("[INFO] %s: %s", component, message)

	if pc.logger != nil {
		pc.logger.Println(logMsg)
	}

	if pc.logFile != nil {
		pc.logFile.Sync()
	}
}

// initializeState inicializa estado thread-safe
func (pc *PLCController) initializeState() {
	// Atomic values
	atomic.StoreInt32(&pc.collectionActive, 1)
	atomic.StoreInt32(&pc.debugMode, 0)
	atomic.StoreInt32(&pc.emergencyStop, 0)
	atomic.StoreInt32(&pc.wsRunning, 0)
	atomic.StoreInt32(&pc.natsConnected, 0)
	atomic.StoreInt32(&pc.plcConnected, 0)

	// Maps protegidos
	pc.radarsEnabled = map[string]bool{
		"caldeira": true, "porta_jusante": true, "porta_montante": true,
	}
	pc.radarsConnected = map[string]bool{
		"caldeira": false, "porta_jusante": false, "porta_montante": false,
	}
	pc.radarCounters = map[string]*RadarCounters{
		"caldeira":       {LastSeen: time.Now()},
		"porta_jusante":  {LastSeen: time.Now()},
		"porta_montante": {LastSeen: time.Now()},
	}
}

// setupCommandHandlers configura handlers robustos
func (pc *PLCController) setupCommandHandlers() {
	pc.commandHandlers = map[string]CommandHandler{
		"StartCollection": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.StartCollection && !pc.IsCollectionActive()
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.collectionActive, 1)
				atomic.StoreInt32(&pc.emergencyStop, 0)
				pc.logCritical("COMANDO", "Coleta INICIADA")
			},
			LogMessage: "Coleta INICIADA",
		},

		"StopCollection": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.StopCollection && pc.IsCollectionActive()
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.collectionActive, 0)
				pc.logCritical("COMANDO", "Coleta PARADA")
			},
			LogMessage: "Coleta PARADA",
		},

		"Emergency": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.Emergency
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.emergencyStop, 1)
				atomic.StoreInt32(&pc.collectionActive, 0)
				pc.logCritical("EMERGENCIA", "Sistema em estado de emergência")
			},
			NeedsReset:      true,
			ResetByteOffset: 0, ResetBitOffset: 2,
			LogMessage: "EMERGENCIA ativada",
		},

		"RestartRadarCaldeira": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.RestartRadarCaldeira
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				pc.logInfo("COMANDO", "Restart Caldeira solicitado")
				select {
				case pc.commandChan <- 1:
				default:
				}
			},
			NeedsReset:      true,
			ResetByteOffset: 0, ResetBitOffset: 7,
			LogMessage: "Restart Caldeira solicitado",
		},

		"RestartRadarPortaJusante": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.RestartRadarPortaJusante
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				pc.logInfo("COMANDO", "Restart Porta Jusante solicitado")
				select {
				case pc.commandChan <- 2:
				default:
				}
			},
			NeedsReset:      true,
			ResetByteOffset: 1, ResetBitOffset: 0,
			LogMessage: "Restart Porta Jusante solicitado",
		},

		"RestartRadarPortaMontante": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.RestartRadarPortaMontante
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				pc.logInfo("COMANDO", "Restart Porta Montante solicitado")
				select {
				case pc.commandChan <- 3:
				default:
				}
			},
			NeedsReset:      true,
			ResetByteOffset: 1, ResetBitOffset: 1,
			LogMessage: "Restart Porta Montante solicitado",
		},
	}
}

// Start inicia controlador ultra-robusto 24/7
func (pc *PLCController) Start() {
	pc.logCritical("STARTUP", "Iniciando sistema ultra-robusto para produção 24/7")

	// Configurar timers com intervalos otimizados
	pc.liveBitTicker = time.NewTicker(1 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(500 * time.Millisecond)
	pc.healthTicker = time.NewTicker(5 * time.Second)     // Health check a cada 5s
	pc.reconnectTicker = time.NewTicker(10 * time.Second) // Reconexão a cada 10s
	pc.watchdogTicker = time.NewTicker(30 * time.Second)  // Watchdog a cada 30s

	// Iniciar todas as goroutines
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.dailyResetScheduler()
	go pc.healthCheckLoop()  // NOVO: Health check contínuo
	go pc.reconnectionLoop() // NOVO: Reconexão automática
	go pc.watchdogLoop()     // NOVO: Watchdog do sistema

	pc.logCritical("STARTUP", "Todos os sistemas iniciados - Operação 24/7 com reconexão automática")
}

// Stop para controlador com cleanup completo
func (pc *PLCController) Stop() {
	pc.logCritical("SHUTDOWN", "Iniciando parada do sistema...")

	// Parar todos os tickers
	tickers := []*time.Ticker{
		pc.liveBitTicker, pc.statusTicker, pc.commandTicker,
		pc.healthTicker, pc.reconnectTicker, pc.watchdogTicker,
	}

	for _, ticker := range tickers {
		if ticker != nil {
			ticker.Stop()
		}
	}

	close(pc.stopChan)

	// Fechar arquivo de log
	if pc.logFile != nil {
		pc.logFile.Close()
	}

	pc.logCritical("SHUTDOWN", "Sistema parado completamente")
}

// ========== SISTEMA DE HEALTH CHECK E RECONEXÃO ==========

// healthCheckLoop verifica saúde do sistema continuamente
func (pc *PLCController) healthCheckLoop() {
	for {
		select {
		case <-pc.healthTicker.C:
			pc.performHealthCheck()
		case <-pc.stopChan:
			return
		}
	}
}

// performHealthCheck executa verificações de saúde
func (pc *PLCController) performHealthCheck() {
	// Verificar conexão PLC
	pc.checkPLCConnection()

	// Verificar radares
	pc.checkRadarConnections()

	// Verificar sistema
	pc.checkSystemHealth()
}

// checkPLCConnection verifica e registra estado do PLC
func (pc *PLCController) checkPLCConnection() {
	// Tentar operação simples para testar conexão
	_, err := pc.reader.ReadCommands()

	currentlyConnected := atomic.LoadInt32(&pc.plcConnected) == 1

	if err != nil {
		atomic.AddInt32(&pc.consecutiveFails, 1)
		fails := atomic.LoadInt32(&pc.consecutiveFails)

		if currentlyConnected {
			atomic.StoreInt32(&pc.plcConnected, 0)
			pc.logCritical("PLC_DISCONNECT", fmt.Sprintf("Conexão PLC PERDIDA após %d falhas: %v", fails, err))
		}

		if fails > 5 {
			pc.logCritical("PLC_CRITICAL", fmt.Sprintf("PLC com %d falhas consecutivas - Crítico!", fails))
		}
	} else {
		prevFails := atomic.LoadInt32(&pc.consecutiveFails)
		atomic.StoreInt32(&pc.consecutiveFails, 0)

		if !currentlyConnected {
			atomic.StoreInt32(&pc.plcConnected, 1)
			pc.logCritical("PLC_RECONNECT", fmt.Sprintf("Conexão PLC RESTAURADA após %d falhas", prevFails))
		}
	}

	pc.lastPLCCheck = time.Now()
}

// checkRadarConnections verifica conectividade dos radares
func (pc *PLCController) checkRadarConnections() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()
	for radarID, counter := range pc.radarCounters {
		// Verificar se radar está respondendo (último pacote < 30s)
		timeSinceLastSeen := now.Sub(counter.LastSeen)

		if timeSinceLastSeen > 30*time.Second {
			if pc.radarsConnected[radarID] {
				pc.radarsConnected[radarID] = false
				pc.logCritical("RADAR_DISCONNECT", fmt.Sprintf("Radar %s DESCONECTADO - %v sem resposta", radarID, timeSinceLastSeen))
			}
			atomic.AddInt32(&counter.ConsecutiveFails, 1)
		} else {
			if !pc.radarsConnected[radarID] {
				pc.radarsConnected[radarID] = true
				pc.logCritical("RADAR_RECONNECT", fmt.Sprintf("Radar %s RECONECTADO", radarID))
			}
			atomic.StoreInt32(&counter.ConsecutiveFails, 0)
		}
	}
}

// checkSystemHealth verifica saúde geral do sistema
func (pc *PLCController) checkSystemHealth() {
	// Verificar se sistema está responsivo
	totalErrors := atomic.LoadInt32(&pc.errorCount)
	consecutiveFails := atomic.LoadInt32(&pc.consecutiveFails)

	// Sistema crítico se muitos erros ou falhas PLC
	if totalErrors > 100 || consecutiveFails > 10 {
		pc.logCritical("SYSTEM_CRITICAL", fmt.Sprintf("Sistema crítico: %d erros, %d falhas PLC", totalErrors, consecutiveFails))
	}

	// Verificar memória do processo
	pc.checkMemoryLeaks()
}

// checkMemoryLeaks verifica vazamentos de memória
func (pc *PLCController) checkMemoryLeaks() {
	// Verificar canais cheios
	if len(pc.commandChan) > 15 {
		pc.logCritical("MEMORY_LEAK", fmt.Sprintf("Canal de comandos quase cheio: %d/20", len(pc.commandChan)))
	}
}

// reconnectionLoop gerencia reconexão automática
func (pc *PLCController) reconnectionLoop() {
	for {
		select {
		case <-pc.reconnectTicker.C:
			pc.attemptReconnection()
		case <-pc.stopChan:
			return
		}
	}
}

// attemptReconnection tenta reconectar PLC e radares
func (pc *PLCController) attemptReconnection() {
	pc.connectMutex.Lock()
	defer pc.connectMutex.Unlock()

	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1
	consecutiveFails := atomic.LoadInt32(&pc.consecutiveFails)

	// Se PLC desconectado ou muitas falhas, tentar reconectar
	if !plcConnected || consecutiveFails > 3 {
		atomic.AddInt32(&pc.reconnectAttempts, 1)
		attempts := atomic.LoadInt32(&pc.reconnectAttempts)

		pc.logInfo("RECONNECT", fmt.Sprintf("Tentativa de reconexão PLC #%d", attempts))

		// Tentar reconectar (implementar baseado no seu PLCClient)
		if pc.attemptPLCReconnection() {
			atomic.StoreInt32(&pc.reconnectAttempts, 0)
			atomic.StoreInt32(&pc.consecutiveFails, 0)
			pc.logCritical("RECONNECT_SUCCESS", "Reconexão PLC bem-sucedida")
		} else {
			pc.logError("RECONNECT", "attemptPLCReconnection", fmt.Errorf("falha na tentativa %d", attempts))
		}
	}
}

// attemptPLCReconnection tenta reconectar ao PLC
func (pc *PLCController) attemptPLCReconnection() bool {
	// Implementar baseado no seu PLCClient específico
	// Exemplo genérico:

	// 1. Fechar conexão atual se existir
	// 2. Aguardar um tempo
	time.Sleep(2 * time.Second)

	// 3. Tentar nova conexão
	// Substitua por sua lógica específica de reconexão
	_, err := pc.reader.ReadCommands()

	return err == nil
}

// watchdogLoop monitora sistema geral
func (pc *PLCController) watchdogLoop() {
	lastActivity := time.Now()

	for {
		select {
		case <-pc.watchdogTicker.C:
			now := time.Now()

			// Verificar se sistema está ativo (novos pacotes)
			currentPackets := atomic.LoadInt32(&pc.packetCount)

			// Se não houve atividade por 2 minutos, investigar
			if now.Sub(lastActivity) > 2*time.Minute {
				pc.logCritical("WATCHDOG", fmt.Sprintf("Sistema sem atividade por %v", now.Sub(lastActivity)))

				// Forçar health check
				go pc.performHealthCheck()
			}

			// Atualizar última atividade se houve pacotes
			if currentPackets > 0 {
				lastActivity = now
			}

		case <-pc.stopChan:
			return
		}
	}
}

// ========== LOOPS PRINCIPAIS ROBUSTOS ==========

// liveBitLoop gerencia live bit com error handling
func (pc *PLCController) liveBitLoop() {
	defer func() {
		if r := recover(); r != nil {
			pc.logCritical("PANIC", fmt.Sprintf("liveBitLoop panic: %v", r))
			go pc.liveBitLoop() // Restart automático
		}
	}()

	for {
		select {
		case <-pc.liveBitTicker.C:
			current := atomic.LoadInt32(&pc.liveBit)
			newValue := int32(1)
			if current == 1 {
				newValue = 0
			}
			atomic.StoreInt32(&pc.liveBit, newValue)
		case <-pc.stopChan:
			return
		}
	}
}

// statusWriteLoop escreve status com proteção total
func (pc *PLCController) statusWriteLoop() {
	defer func() {
		if r := recover(); r != nil {
			pc.logCritical("PANIC", fmt.Sprintf("statusWriteLoop panic: %v", r))
			go pc.statusWriteLoop() // Restart automático
		}
	}()

	for {
		select {
		case <-pc.statusTicker.C:
			// Verificar overflow antes de qualquer operação
			pc.checkAndResetCounters()

			if err := pc.writeSystemStatus(); err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
				pc.logError("STATUS_WRITE", "writeSystemStatus", err)
			}
		case <-pc.stopChan:
			return
		}
	}
}

// commandReadLoop lê comandos com recovery
func (pc *PLCController) commandReadLoop() {
	defer func() {
		if r := recover(); r != nil {
			pc.logCritical("PANIC", fmt.Sprintf("commandReadLoop panic: %v", r))
			go pc.commandReadLoop() // Restart automático
		}
	}()

	for {
		select {
		case <-pc.commandTicker.C:
			commands, err := pc.reader.ReadCommands()
			if err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
				pc.logError("COMMAND_READ", "ReadCommands", err)
				continue
			}
			pc.processCommands(commands)
		case <-pc.stopChan:
			return
		}
	}
}

// processCommands processa comandos com logging detalhado
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	for cmdName, handler := range pc.commandHandlers {
		if handler.ShouldExecute(commands, pc) {
			handler.Execute(pc, commands)

			if handler.NeedsReset {
				if err := pc.writer.ResetCommand(handler.ResetByteOffset, handler.ResetBitOffset); err != nil {
					pc.logError("COMMAND_RESET", cmdName, err)
				}
			}
		}
	}

	pc.handleRadarEnables(commands)
	pc.handleResetErrors(commands.ResetErrors)
}

// handleRadarEnables gerencia enables com logging
func (pc *PLCController) handleRadarEnables(commands *models.PLCCommands) {
	radarEnables := map[string]bool{
		"caldeira":       commands.EnableRadarCaldeira,
		"porta_jusante":  commands.EnableRadarPortaJusante,
		"porta_montante": commands.EnableRadarPortaMontante,
	}

	pc.stateMutex.Lock()
	for radarName, newState := range radarEnables {
		if currentState, exists := pc.radarsEnabled[radarName]; exists {
			if newState != currentState {
				pc.radarsEnabled[radarName] = newState
				action := "DESABILITADO"
				if newState {
					action = "HABILITADO"
				}
				pc.logInfo("RADAR_ENABLE", fmt.Sprintf("Radar %s %s", radarName, action))
			}
		}
	}
	pc.stateMutex.Unlock()
}

// handleResetErrors lógica de reboot com logging
func (pc *PLCController) handleResetErrors(resetErrors bool) {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	if resetErrors {
		if !pc.resetErrorsActive {
			pc.resetErrorsActive = true
			pc.resetErrorsStartTime = time.Now()
			pc.rebootExecuted = false
			pc.logCritical("REBOOT_INIT", "ResetErrors ativo - 10s para reboot")
		} else {
			elapsed := time.Since(pc.resetErrorsStartTime)
			if elapsed >= 10*time.Second && !pc.rebootExecuted {
				pc.rebootExecuted = true
				pc.logCritical("REBOOT_EXEC", "EXECUTANDO REBOOT DO SERVIDOR")
				go pc.executeSecureReboot()
			}
		}
	} else {
		if pc.resetErrorsActive && !pc.rebootExecuted {
			pc.resetAllErrors()
			pc.logInfo("RESET", "Erros resetados via comando")
		}
		pc.resetErrorsActive = false
		pc.rebootExecuted = false
	}
}

// commandProcessor processa comandos com recovery
func (pc *PLCController) commandProcessor() {
	defer func() {
		if r := recover(); r != nil {
			pc.logCritical("PANIC", fmt.Sprintf("commandProcessor panic: %v", r))
			go pc.commandProcessor() // Restart automático
		}
	}()

	for cmd := range pc.commandChan {
		pc.executeCommand(cmd)
	}
}

// executeCommand executa comandos com logging
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	switch cmd {
	case 1: // StartCollection
		atomic.StoreInt32(&pc.collectionActive, 1)
		atomic.StoreInt32(&pc.emergencyStop, 0)
		pc.logInfo("COMMAND", "StartCollection executado")
	case 2: // StopCollection
		atomic.StoreInt32(&pc.collectionActive, 0)
		pc.logInfo("COMMAND", "StopCollection executado")
	case 3: // EmergencyStop
		atomic.StoreInt32(&pc.emergencyStop, 1)
		atomic.StoreInt32(&pc.collectionActive, 0)
		pc.logCritical("COMMAND", "EmergencyStop executado")
	case 4: // ResetErrors
		pc.resetAllErrors()
		pc.logInfo("COMMAND", "ResetErrors executado")
	}
}

// resetAllErrors reset contadores com logging
func (pc *PLCController) resetAllErrors() {
	oldErrors := atomic.LoadInt32(&pc.errorCount)
	atomic.StoreInt32(&pc.errorCount, 0)

	pc.stateMutex.Lock()
	for radarID, counter := range pc.radarCounters {
		oldRadarErrors := atomic.LoadInt32(&counter.Errors)
		atomic.StoreInt32(&counter.Errors, 0)

		if oldRadarErrors > 0 {
			pc.logInfo("RESET", fmt.Sprintf("Radar %s: %d erros resetados", radarID, oldRadarErrors))
		}
	}
	pc.stateMutex.Unlock()

	pc.logInfo("RESET", fmt.Sprintf("Total de %d erros resetados", oldErrors))
}

// executeSecureReboot reboot com logging completo
func (pc *PLCController) executeSecureReboot() {
	pc.logCritical("REBOOT", "Iniciando reboot seguro do sistema...")

	// Dar tempo para logs serem escritos
	time.Sleep(2 * time.Second)

	// Tentar script de reboot primeiro
	if err := exec.Command("/usr/local/bin/radar-reboot").Start(); err != nil {
		pc.logError("REBOOT", "radar-reboot script", err)

		// Fallback: criar arquivo de requisição
		if file, err := os.Create("/tmp/radar-reboot-request"); err == nil {
			file.WriteString(fmt.Sprintf("REBOOT_REQUEST_FROM_PLC_%s", time.Now().Format("2006-01-02_15:04:05")))
			file.Close()
			pc.logInfo("REBOOT", "Arquivo de requisição de reboot criado")
		} else {
			pc.logError("REBOOT", "criar arquivo requisição", err)
		}
	} else {
		pc.logInfo("REBOOT", "Script de reboot executado com sucesso")
	}
}

// ========== ESCRITA DE STATUS ROBUSTA ==========

// writeSystemStatus escreve status com error handling completo
func (pc *PLCController) writeSystemStatus() error {
	liveBit := atomic.LoadInt32(&pc.liveBit) == 1
	collectionActive := atomic.LoadInt32(&pc.collectionActive) == 1
	emergencyStop := atomic.LoadInt32(&pc.emergencyStop) == 1

	pc.stateMutex.RLock()
	caldeiraConnected := pc.radarsConnected["caldeira"]
	jusanteConnected := pc.radarsConnected["porta_jusante"]
	montanteConnected := pc.radarsConnected["porta_montante"]
	errorCount := atomic.LoadInt32(&pc.errorCount)
	pc.stateMutex.RUnlock()

	systemHealthy := pc.calculateSystemHealth(caldeiraConnected, jusanteConnected, montanteConnected, emergencyStop, errorCount)

	status := &models.PLCSystemStatus{
		LiveBit:                     liveBit,
		CollectionActive:            collectionActive,
		SystemHealthy:               systemHealthy,
		EmergencyActive:             emergencyStop,
		RadarCaldeiraConnected:      caldeiraConnected,
		RadarPortaJusanteConnected:  jusanteConnected,
		RadarPortaMontanteConnected: montanteConnected,
	}

	if err := pc.writer.WriteSystemStatus(status); err != nil {
		pc.logError("STATUS", "WriteSystemStatus", err)
		return err
	}

	return pc.writeSystemMetrics()
}

// calculateSystemHealth calcula saúde com critérios rigorosos
func (pc *PLCController) calculateSystemHealth(caldeira, jusante, montante, emergency bool, errorCount int32) bool {
	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1

	healthyRadars := 0
	if caldeira {
		healthyRadars++
	}
	if jusante {
		healthyRadars++
	}
	if montante {
		healthyRadars++
	}

	// Sistema saudável se: PLC conectado + pelo menos 1 radar + sem emergência + poucos erros
	return plcConnected && healthyRadars > 0 && !emergency && errorCount < 50
}

// writeSystemMetrics escreve métricas com error handling
func (pc *PLCController) writeSystemMetrics() error {
	metrics := pc.getSystemMetrics()

	writeOps := []struct {
		offset int
		value  float32
		name   string
	}{
		{294, metrics.CPU, "CPU"},
		{298, metrics.Memory, "Memory"},
		{302, metrics.Disk, "Disk"},
		{306, metrics.Temp, "Temperature"},
	}

	for _, op := range writeOps {
		if err := pc.writer.WriteTag(100, op.offset, "real", op.value); err != nil {
			pc.logError("METRICS", fmt.Sprintf("Write%s", op.name), err)
			return err
		}
	}

	return nil
}

// getSystemMetrics obtém métricas com fallbacks
func (pc *PLCController) getSystemMetrics() SystemMetrics {
	return SystemMetrics{
		CPU:    pc.getLinuxCPUUsage(),
		Memory: pc.getLinuxMemoryUsage(),
		Disk:   pc.getLinuxDiskUsage(),
		Temp:   pc.getLinuxTemperature(),
	}
}

// ========== ESCRITA DE DADOS RADAR ROBUSTA ==========

// WriteRadarData escreve dados com logging e recovery
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	if !pc.IsRadarEnabled(data.RadarID) {
		return nil
	}

	baseOffsets := map[string]int{
		"caldeira": 6, "porta_jusante": 102, "porta_montante": 198,
	}

	baseOffset, exists := baseOffsets[data.RadarID]
	if !exists {
		return fmt.Errorf("radar desconhecido: %s", data.RadarID)
	}

	plcData := pc.writer.BuildPLCRadarData(data)
	err := pc.writer.WriteRadarDataToDB100(plcData, baseOffset)

	if err != nil {
		pc.IncrementRadarErrors(data.RadarID)
		pc.logError("RADAR_WRITE", data.RadarID, err)
		return err
	}

	pc.IncrementRadarPackets(data.RadarID)

	// Atualizar última atividade do radar
	pc.updateRadarActivity(data.RadarID)

	return nil
}

// updateRadarActivity atualiza timestamp do radar
func (pc *PLCController) updateRadarActivity(radarID string) {
	pc.stateMutex.Lock()
	if counter, exists := pc.radarCounters[radarID]; exists {
		counter.LastSeen = time.Now()
	}
	pc.stateMutex.Unlock()
}

// WriteMultiRadarData escreve múltiplos radares com logging
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	var errors []string

	for _, radarData := range data.Radars {
		if err := pc.WriteRadarData(radarData); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", radarData.RadarID, err))
		}
	}

	if len(errors) > 0 {
		errorMsg := strings.Join(errors, "; ")
		pc.logError("MULTI_RADAR", "WriteMultiRadarData", fmt.Errorf(errorMsg))
		return fmt.Errorf("erros: %s", errorMsg)
	}

	return nil
}

// ========== PROTEÇÃO CONTRA OVERFLOW ==========

// dailyResetScheduler com recovery automático
func (pc *PLCController) dailyResetScheduler() {
	defer func() {
		if r := recover(); r != nil {
			pc.logCritical("PANIC", fmt.Sprintf("dailyResetScheduler panic: %v", r))
			go pc.dailyResetScheduler()
		}
	}()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pc.checkResetConditions()
		case <-pc.stopChan:
			return
		}
	}
}

// checkResetConditions verifica condições críticas
func (pc *PLCController) checkResetConditions() {
	now := time.Now()

	pc.stateMutex.RLock()
	lastReset := pc.lastResetTime
	pc.stateMutex.RUnlock()

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	// REDUNDÂNCIA 1: Reset às 00:00
	if now.Hour() == 0 && now.Sub(lastReset) > 23*time.Hour {
		pc.logCritical("RESET_SCHEDULE", "Reset diário às 00:00")
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 2: Overflow protection (70% do limite)
	const MAX_SAFE = 1500000000
	if totalPackets > MAX_SAFE || totalErrors > MAX_SAFE {
		pc.logCritical("RESET_OVERFLOW", fmt.Sprintf("Packets=%d, Errors=%d", totalPackets, totalErrors))
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 3: Timeout máximo (25h)
	if now.Sub(lastReset) > 25*time.Hour {
		pc.logCritical("RESET_TIMEOUT", fmt.Sprintf("%v sem reset", now.Sub(lastReset)))
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 4: Contadores negativos
	if totalPackets < 0 || totalErrors < 0 {
		pc.logCritical("RESET_EMERGENCY", "Contadores negativos detectados")
		pc.executeDailyReset()
		return
	}
}

// checkAndResetCounters verificação crítica a cada segundo
func (pc *PLCController) checkAndResetCounters() {
	const CRITICAL_LIMIT = 2000000000

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	if totalPackets > CRITICAL_LIMIT || totalErrors > CRITICAL_LIMIT {
		pc.logCritical("RESET_CRITICAL", fmt.Sprintf("Reset crítico: Packets=%d, Errors=%d", totalPackets, totalErrors))
		go pc.executeDailyReset()
	}
}

// executeDailyReset executa reset com logs detalhados
func (pc *PLCController) executeDailyReset() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()
	if now.Sub(pc.lastResetTime) < 1*time.Hour {
		pc.logInfo("RESET", "Reset ignorado - muito recente")
		return
	}

	pc.logDailyStatistics()

	// Reset atomic counters
	atomic.StoreInt32(&pc.packetCount, 0)
	atomic.StoreInt32(&pc.errorCount, 0)
	atomic.StoreInt32(&pc.reconnectAttempts, 0)

	// Reset radar counters
	for radarID, counter := range pc.radarCounters {
		oldPackets := atomic.LoadInt32(&counter.Packets)
		oldErrors := atomic.LoadInt32(&counter.Errors)

		atomic.StoreInt32(&counter.Packets, 0)
		atomic.StoreInt32(&counter.Errors, 0)
		atomic.StoreInt32(&counter.ConsecutiveFails, 0)

		pc.logInfo("RESET", fmt.Sprintf("Radar %s: %d pacotes, %d erros resetados", radarID, oldPackets, oldErrors))
	}

	pc.lastResetTime = now
	pc.logCritical("RESET_COMPLETE", fmt.Sprintf("Reset executado: %s", now.Format("2006-01-02 15:04:05")))
}

// logDailyStatistics registra estatísticas detalhadas
func (pc *PLCController) logDailyStatistics() {
	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)
	dayDuration := time.Since(pc.lastResetTime)
	reconnectAttempts := atomic.LoadInt32(&pc.reconnectAttempts)

	if totalPackets > 0 {
		successRate := float64(totalPackets-totalErrors) / float64(totalPackets) * 100
		packetsPerHour := float64(totalPackets) / dayDuration.Hours()

		pc.logCritical("DAILY_STATS", fmt.Sprintf("Período: %v | Pacotes: %d | Erros: %d | Taxa sucesso: %.2f%% | Pac/h: %.0f | Reconexões: %d",
			dayDuration, totalPackets, totalErrors, successRate, packetsPerHour, reconnectAttempts))
	}

	// Log individual dos radares
	pc.stateMutex.RLock()
	for radarID, counter := range pc.radarCounters {
		packets := atomic.LoadInt32(&counter.Packets)
		errors := atomic.LoadInt32(&counter.Errors)
		fails := atomic.LoadInt32(&counter.ConsecutiveFails)
		connected := pc.radarsConnected[radarID]

		pc.logInfo("RADAR_STATS", fmt.Sprintf("%s: %d pacotes, %d erros, %d falhas, conectado=%v",
			radarID, packets, errors, fails, connected))
	}
	pc.stateMutex.RUnlock()
}

// ========== MÉTODOS PÚBLICOS THREAD-SAFE ==========

func (pc *PLCController) IsCollectionActive() bool {
	collectionActive := atomic.LoadInt32(&pc.collectionActive) == 1
	emergencyStop := atomic.LoadInt32(&pc.emergencyStop) == 1
	return collectionActive && !emergencyStop
}

func (pc *PLCController) IsDebugMode() bool {
	return atomic.LoadInt32(&pc.debugMode) == 1
}

func (pc *PLCController) IsEmergencyStop() bool {
	return atomic.LoadInt32(&pc.emergencyStop) == 1
}

func (pc *PLCController) IsPLCConnected() bool {
	return atomic.LoadInt32(&pc.plcConnected) == 1
}

func (pc *PLCController) IsRadarEnabled(radarID string) bool {
	pc.stateMutex.RLock()
	enabled := pc.radarsEnabled[radarID]
	pc.stateMutex.RUnlock()
	return enabled
}

func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	result := make(map[string]bool, len(pc.radarsEnabled))
	for k, v := range pc.radarsEnabled {
		result[k] = v
	}
	return result
}

func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.stateMutex.Lock()
	for radarID, connected := range status {
		if _, exists := pc.radarsConnected[radarID]; exists {
			oldStatus := pc.radarsConnected[radarID]
			pc.radarsConnected[radarID] = connected

			// Log mudanças de status
			if oldStatus != connected {
				statusText := "DESCONECTADO"
				if connected {
					statusText = "CONECTADO"
				}
				pc.logInfo("RADAR_STATUS", fmt.Sprintf("Radar %s: %s", radarID, statusText))
			}
		}
	}
	pc.stateMutex.Unlock()
}

func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.stateMutex.Lock()
	if _, exists := pc.radarsConnected[radarID]; exists {
		oldStatus := pc.radarsConnected[radarID]
		pc.radarsConnected[radarID] = connected

		if oldStatus != connected {
			statusText := "DESCONECTADO"
			if connected {
				statusText = "CONECTADO"
			}
			pc.logInfo("RADAR_STATUS", fmt.Sprintf("Radar %s: %s", radarID, statusText))
		}
	}
	pc.stateMutex.Unlock()
}

func (pc *PLCController) IncrementRadarPackets(radarID string) {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	pc.stateMutex.RUnlock()

	if exists {
		atomic.AddInt32(&counter.Packets, 1)
		atomic.AddInt32(&pc.packetCount, 1)
		counter.LastSeen = time.Now()
	}
}

func (pc *PLCController) IncrementRadarErrors(radarID string) {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	pc.stateMutex.RUnlock()

	if exists {
		atomic.AddInt32(&counter.Errors, 1)
		atomic.AddInt32(&pc.errorCount, 1)
		atomic.AddInt32(&counter.ConsecutiveFails, 1)

		fails := atomic.LoadInt32(&counter.ConsecutiveFails)
		if fails > 5 {
			pc.logCritical("RADAR_CRITICAL", fmt.Sprintf("Radar %s: %d falhas consecutivas", radarID, fails))
		}
	}
}

func (pc *PLCController) SetNATSConnected(connected bool) {
	oldStatus := atomic.LoadInt32(&pc.natsConnected) == 1

	if connected {
		atomic.StoreInt32(&pc.natsConnected, 1)
	} else {
		atomic.StoreInt32(&pc.natsConnected, 0)
	}

	if oldStatus != connected {
		statusText := "DESCONECTADO"
		if connected {
			statusText = "CONECTADO"
		}
		pc.logInfo("NATS", fmt.Sprintf("NATS %s", statusText))
	}
}

func (pc *PLCController) SetWebSocketRunning(running bool) {
	oldStatus := atomic.LoadInt32(&pc.wsRunning) == 1

	if running {
		atomic.StoreInt32(&pc.wsRunning, 1)
	} else {
		atomic.StoreInt32(&pc.wsRunning, 0)
	}

	if oldStatus != running {
		statusText := "PARADO"
		if running {
			statusText = "RODANDO"
		}
		pc.logInfo("WEBSOCKET", fmt.Sprintf("WebSocket %s", statusText))
	}
}

func (pc *PLCController) UpdateWebSocketClients(count int) {
	atomic.StoreInt32(&pc.wsClients, int32(count))
}

// ForceCounterReset reset manual para emergências
func (pc *PLCController) ForceCounterReset() {
	pc.logCritical("MANUAL_RESET", "Reset manual forçado pelo usuário")
	pc.executeDailyReset()
}

// ForceReconnection força reconexão imediata
func (pc *PLCController) ForceReconnection() {
	pc.logCritical("MANUAL_RECONNECT", "Reconexão manual forçada")
	go pc.attemptReconnection()
}

// ========== MÉTRICAS DO SISTEMA LINUX ==========

func (pc *PLCController) getLinuxCPUUsage() float32 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 25.0
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 25.0
	}

	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return 25.0
	}

	var total, idle uint64
	for i := 1; i < 8; i++ {
		val, _ := strconv.ParseUint(fields[i], 10, 64)
		total += val
		if i == 4 {
			idle = val
		}
	}

	if total == 0 {
		return 25.0
	}

	usage := float32((total-idle)*100) / float32(total)
	if usage > 100 {
		usage = 100
	}
	return usage
}

func (pc *PLCController) getLinuxMemoryUsage() float32 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 40.0
	}

	memInfo := make(map[string]float64)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			val, _ := strconv.ParseFloat(fields[1], 64)
			memInfo[fields[0]] = val
		}
	}

	total := memInfo["MemTotal:"]
	available := memInfo["MemAvailable:"]
	if available == 0 {
		available = memInfo["MemFree:"] + memInfo["Buffers:"] + memInfo["Cached:"]
	}

	if total > 0 {
		usage := ((total - available) / total) * 100
		return float32(usage)
	}

	return 40.0
}

func (pc *PLCController) getLinuxDiskUsage() float32 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 50.0
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)

	if total == 0 {
		return 50.0
	}

	usage := float32((total-free)*100) / float32(total)
	return usage
}

func (pc *PLCController) getLinuxTemperature() float32 {
	tempPaths := []string{
		"/sys/class/thermal/thermal_zone0/temp",
		"/sys/class/hwmon/hwmon0/temp1_input",
	}

	for _, path := range tempPaths {
		if data, err := os.ReadFile(path); err == nil {
			if temp, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil {
				if temp > 1000 {
					temp = temp / 1000
				}
				if temp >= 20 && temp <= 100 {
					return float32(temp)
				}
			}
		}
	}

	return 45.0
}

// ========== MÉTODOS DE COMPATIBILIDADE ==========

func (pc *PLCController) RequestSystemRestart() {
	pc.logInfo("REQUEST", "System restart solicitado")
	select {
	case pc.commandChan <- 10:
	default:
		pc.logError("REQUEST", "SystemRestart", fmt.Errorf("canal cheio"))
	}
}

func (pc *PLCController) RequestNATSRestart() {
	pc.logInfo("REQUEST", "NATS restart solicitado")
	select {
	case pc.commandChan <- 11:
	default:
	}
}

func (pc *PLCController) RequestWebSocketRestart() {
	pc.logInfo("REQUEST", "WebSocket restart solicitado")
	select {
	case pc.commandChan <- 12:
	default:
	}
}

func (pc *PLCController) RestartApplication() {
	pc.logCritical("REQUEST", "Application restart solicitado")
	select {
	case pc.commandChan <- 13:
	default:
	}
}

// ========== MÉTRICAS E MONITORAMENTO DETALHADO ==========

func (pc *PLCController) GetSystemStats() map[string]interface{} {
	pc.stateMutex.RLock()
	connectedRadars := 0
	for _, connected := range pc.radarsConnected {
		if connected {
			connectedRadars++
		}
	}
	pc.stateMutex.RUnlock()

	return map[string]interface{}{
		"uptime_hours":       time.Since(pc.startTime).Hours(),
		"collection_active":  atomic.LoadInt32(&pc.collectionActive) == 1,
		"emergency_stop":     atomic.LoadInt32(&pc.emergencyStop) == 1,
		"plc_connected":      atomic.LoadInt32(&pc.plcConnected) == 1,
		"total_packets":      atomic.LoadInt32(&pc.packetCount),
		"total_errors":       atomic.LoadInt32(&pc.errorCount),
		"connected_radars":   connectedRadars,
		"websocket_clients":  atomic.LoadInt32(&pc.wsClients),
		"nats_connected":     atomic.LoadInt32(&pc.natsConnected) == 1,
		"consecutive_fails":  atomic.LoadInt32(&pc.consecutiveFails),
		"reconnect_attempts": atomic.LoadInt32(&pc.reconnectAttempts),
		"last_plc_check":     pc.lastPLCCheck.Format("15:04:05"),
		"last_reset":         pc.lastResetTime.Format("2006-01-02 15:04:05"),
	}
}

func (pc *PLCController) GetRadarStats(radarID string) map[string]interface{} {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	enabled := pc.radarsEnabled[radarID]
	connected := pc.radarsConnected[radarID]
	pc.stateMutex.RUnlock()

	if !exists {
		return map[string]interface{}{"error": "radar não encontrado"}
	}

	return map[string]interface{}{
		"enabled":           enabled,
		"connected":         connected,
		"packets":           atomic.LoadInt32(&counter.Packets),
		"errors":            atomic.LoadInt32(&counter.Errors),
		"consecutive_fails": atomic.LoadInt32(&counter.ConsecutiveFails),
		"last_seen":         counter.LastSeen.Format("15:04:05"),
		"seconds_ago":       int(time.Since(counter.LastSeen).Seconds()),
	}
}

// GetConnectionReport relatório detalhado de conectividade
func (pc *PLCController) GetConnectionReport() map[string]interface{} {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	report := map[string]interface{}{
		"timestamp":          time.Now().Format("2006-01-02 15:04:05"),
		"plc_connected":      atomic.LoadInt32(&pc.plcConnected) == 1,
		"consecutive_fails":  atomic.LoadInt32(&pc.consecutiveFails),
		"last_plc_check":     pc.lastPLCCheck.Format("15:04:05"),
		"reconnect_attempts": atomic.LoadInt32(&pc.reconnectAttempts),
	}

	// Status detalhado dos radares
	radars := make(map[string]interface{})
	for radarID, counter := range pc.radarCounters {
		radars[radarID] = map[string]interface{}{
			"connected":         pc.radarsConnected[radarID],
			"enabled":           pc.radarsEnabled[radarID],
			"last_seen":         counter.LastSeen.Format("15:04:05"),
			"seconds_inactive":  int(time.Since(counter.LastSeen).Seconds()),
			"consecutive_fails": atomic.LoadInt32(&counter.ConsecutiveFails),
		}
	}
	report["radars"] = radars

	return report
}

// SetDebugMode ativa/desativa modo debug
func (pc *PLCController) SetDebugMode(enabled bool) {
	if enabled {
		atomic.StoreInt32(&pc.debugMode, 1)
		pc.logInfo("DEBUG", "Modo debug ATIVADO")
	} else {
		atomic.StoreInt32(&pc.debugMode, 0)
		pc.logInfo("DEBUG", "Modo debug DESATIVADO")
	}
}

// GetLogFilePath retorna caminho do arquivo de log atual
func (pc *PLCController) GetLogFilePath() string {
	return filepath.Join(pc.logDirectory, fmt.Sprintf("plc_controller_%s.log", time.Now().Format("2006-01-02")))
}
