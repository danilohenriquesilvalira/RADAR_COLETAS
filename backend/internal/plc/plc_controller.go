package plc

import (
	"context"
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

	// Sistema de conectividade robusta com circuit breaker
	plcConnected          int32 // atomic
	lastPLCCheck          time.Time
	consecutiveFails      int32 // atomic
	reconnectAttempts     int32 // atomic
	connectMutex          sync.Mutex
	circuitBreakerOpen    int32 // atomic - 0=closed, 1=open
	circuitBreakerTimeout time.Time
	lastSuccessfulOp      time.Time
	operationTimeout      time.Duration

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
	lastLogTime  map[string]time.Time // Evita logs duplicados
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
		plc:              plcClient,
		reader:           NewPLCReader(plcClient),
		writer:           NewPLCWriter(plcClient),
		commandChan:      make(chan models.SystemCommand, 20),
		stopChan:         make(chan bool),
		startTime:        time.Now(),
		lastResetTime:    time.Now(),
		lastPLCCheck:     time.Now(),
		lastSuccessfulOp: time.Now(),
		logDirectory:     "backend/logs",
		operationTimeout: 5 * time.Second, // Timeout para operações PLC
		lastLogTime:      make(map[string]time.Time),
	}

	pc.initializeLogging()
	pc.initializeState()
	pc.setupCommandHandlers()

	pc.logCriticalOnce("STARTUP", "SISTEMA INICIADO", "PLC Controller Ultra-Robusto inicializado")
	return pc
}

// initializeLogging configura sistema de logging robusto
func (pc *PLCController) initializeLogging() {
	if err := os.MkdirAll(pc.logDirectory, 0755); err != nil {
		log.Printf("Erro criando diretório de logs: %v", err)
		pc.logDirectory = "./logs"
		os.MkdirAll(pc.logDirectory, 0755)
	}

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

// logCriticalOnce evita logs duplicados em curto período
func (pc *PLCController) logCriticalOnce(component, event, message string) {
	key := fmt.Sprintf("%s_%s", component, event)

	pc.logMutex.Lock()
	defer pc.logMutex.Unlock()

	// Evitar logs duplicados em menos de 5 segundos
	if lastTime, exists := pc.lastLogTime[key]; exists {
		if time.Since(lastTime) < 5*time.Second {
			return
		}
	}

	pc.lastLogTime[key] = time.Now()

	logMsg := fmt.Sprintf("[CRITICAL] %s: %s", event, message)

	if pc.logger != nil {
		pc.logger.Println(logMsg)
	}
	log.Println(logMsg)

	if pc.logFile != nil {
		pc.logFile.Sync()
	}
}

// logError registra erros sem spam
func (pc *PLCController) logError(component, operation string, err error) {
	key := fmt.Sprintf("%s_%s", component, operation)

	pc.logMutex.Lock()
	defer pc.logMutex.Unlock()

	// Rate limiting: máximo 1 log por segundo para mesmo erro
	if lastTime, exists := pc.lastLogTime[key]; exists {
		if time.Since(lastTime) < 1*time.Second {
			return
		}
	}

	pc.lastLogTime[key] = time.Now()

	logMsg := fmt.Sprintf("[ERROR] %s.%s: %v", component, operation, err)

	if pc.logger != nil {
		pc.logger.Println(logMsg)
	}

	if pc.logFile != nil {
		pc.logFile.Sync()
	}
}

// logInfo com rate limiting
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
	atomic.StoreInt32(&pc.collectionActive, 1)
	atomic.StoreInt32(&pc.debugMode, 0)
	atomic.StoreInt32(&pc.emergencyStop, 0)
	atomic.StoreInt32(&pc.wsRunning, 0)
	atomic.StoreInt32(&pc.natsConnected, 0)
	atomic.StoreInt32(&pc.plcConnected, 1) // Assumir conectado inicialmente
	atomic.StoreInt32(&pc.circuitBreakerOpen, 0)

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
				pc.logCriticalOnce("COMMAND", "START_COLLECTION", "Coleta INICIADA")
			},
			LogMessage: "Coleta INICIADA",
		},
		"StopCollection": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.StopCollection && pc.IsCollectionActive()
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.collectionActive, 0)
				pc.logCriticalOnce("COMMAND", "STOP_COLLECTION", "Coleta PARADA")
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
				pc.logCriticalOnce("COMMAND", "EMERGENCY", "Sistema em estado de emergência")
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
				pc.logInfo("COMMAND", "Restart Caldeira solicitado")
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
				pc.logInfo("COMMAND", "Restart Porta Jusante solicitado")
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
				pc.logInfo("COMMAND", "Restart Porta Montante solicitado")
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
	pc.logCriticalOnce("STARTUP", "INIT", "Iniciando sistema ultra-robusto para produção 24/7")

	// Intervalos otimizados para reduzir overhead
	pc.liveBitTicker = time.NewTicker(2 * time.Second)    // Reduzido de 1s
	pc.statusTicker = time.NewTicker(2 * time.Second)     // Reduzido de 1s
	pc.commandTicker = time.NewTicker(1 * time.Second)    // Reduzido de 500ms
	pc.healthTicker = time.NewTicker(10 * time.Second)    // Aumentado de 5s
	pc.reconnectTicker = time.NewTicker(30 * time.Second) // Aumentado de 10s
	pc.watchdogTicker = time.NewTicker(60 * time.Second)  // Aumentado de 30s

	// Iniciar goroutines com recovery
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.dailyResetScheduler()
	go pc.healthCheckLoop()
	go pc.reconnectionLoop()
	go pc.watchdogLoop()
	go pc.circuitBreakerMonitor() // NOVO: Circuit breaker

	pc.logCriticalOnce("STARTUP", "COMPLETE", "Todos os sistemas iniciados")
}

// Stop para controlador com cleanup completo
func (pc *PLCController) Stop() {
	pc.logCriticalOnce("SHUTDOWN", "INIT", "Iniciando parada do sistema...")

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

	if pc.logFile != nil {
		pc.logFile.Close()
	}

	pc.logCriticalOnce("SHUTDOWN", "COMPLETE", "Sistema parado completamente")
}

// ========== CIRCUIT BREAKER PATTERN ==========

// circuitBreakerMonitor monitora e gerencia circuit breaker
func (pc *PLCController) circuitBreakerMonitor() {
	for {
		select {
		case <-time.Tick(15 * time.Second):
			pc.evaluateCircuitBreaker()
		case <-pc.stopChan:
			return
		}
	}
}

// evaluateCircuitBreaker com detecção mais agressiva
func (pc *PLCController) evaluateCircuitBreaker() {
	consecutiveFails := atomic.LoadInt32(&pc.consecutiveFails)
	timeSinceSuccess := time.Since(pc.lastSuccessfulOp)

	isOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1

	// ABRIR circuit breaker mais agressivamente
	if !isOpen && (consecutiveFails >= 5 || timeSinceSuccess > 1*time.Minute) {
		atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
		pc.circuitBreakerTimeout = time.Now().Add(2 * time.Minute)
		pc.logCriticalOnce("CIRCUIT_BREAKER", "OPEN", fmt.Sprintf("CB ABERTO: %d falhas, %v sem sucesso", consecutiveFails, timeSinceSuccess))

		// Reset para evitar overflow
		atomic.StoreInt32(&pc.consecutiveFails, 0)
		atomic.StoreInt32(&pc.errorCount, 0)
		return
	}

	// Tentar fechar apenas após timeout e teste bem-sucedido
	if isOpen && time.Now().After(pc.circuitBreakerTimeout) {
		// Teste COMPLETO: leitura E escrita
		if pc.testPLCConnectionComplete() {
			atomic.StoreInt32(&pc.circuitBreakerOpen, 0)
			atomic.StoreInt32(&pc.consecutiveFails, 0)
			atomic.StoreInt32(&pc.plcConnected, 1)
			pc.lastSuccessfulOp = time.Now()
			pc.logCriticalOnce("CIRCUIT_BREAKER", "CLOSED", "CB FECHADO - Leitura E escrita OK")
		} else {
			// Estender timeout com backoff
			pc.circuitBreakerTimeout = time.Now().Add(3 * time.Minute)
			pc.logInfo("CIRCUIT_BREAKER", "Timeout estendido - ainda com problemas")
		}
	}
}

// testPLCConnectionComplete testa leitura E escrita antes de considerar "OK"
func (pc *PLCController) testPLCConnectionComplete() bool {
	// Verificar se cliente PLC existe
	if pc.plc == nil || pc.reader == nil || pc.writer == nil {
		pc.logError("PLC_TEST_COMPLETE", "testConnection", fmt.Errorf("cliente, reader ou writer é null"))
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	resultChan := make(chan bool, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- false
			}
		}()

		// TESTE 1: Leitura
		_, err1 := pc.reader.ReadCommands()
		if err1 != nil {
			if strings.Contains(strings.ToLower(err1.Error()), "null") {
				pc.logError("PLC_TEST_COMPLETE", "readTest", err1)
				resultChan <- false
				return
			}
		}

		// TESTE 2: Escrita simples (LiveBit)
		testStatus := &models.PLCSystemStatus{
			LiveBit:          atomic.LoadInt32(&pc.liveBit) == 1,
			CollectionActive: atomic.LoadInt32(&pc.collectionActive) == 1,
			SystemHealthy:    true,
			EmergencyActive:  atomic.LoadInt32(&pc.emergencyStop) == 1,
		}

		err2 := pc.writer.WriteSystemStatus(testStatus)
		if err2 != nil {
			if strings.Contains(strings.ToLower(err2.Error()), "null") {
				pc.logError("PLC_TEST_COMPLETE", "writeTest", err2)
				resultChan <- false
				return
			}
		}

		// Ambos OK = conexão completa funcional
		resultChan <- err1 == nil && err2 == nil
	}()

	select {
	case result := <-resultChan:
		return result
	case <-ctx.Done():
		return false // Timeout
	}
}

// IsSystemOperational verifica se sistema pode operar (usado externamente)
func (pc *PLCController) IsSystemOperational() bool {
	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1
	circuitBreakerOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1

	return plcConnected && !circuitBreakerOpen
}

// ========== SISTEMA DE HEALTH CHECK E RECONEXÃO ==========

// healthCheckLoop verifica saúde do sistema continuamente
func (pc *PLCController) healthCheckLoop() {
	defer pc.recoverLoop("healthCheckLoop")

	for {
		select {
		case <-pc.healthTicker.C:
			pc.performHealthCheck()
		case <-pc.stopChan:
			return
		}
	}
}

// performHealthCheck executa verificações com circuit breaker
func (pc *PLCController) performHealthCheck() {
	// Não fazer health check se circuit breaker estiver aberto
	if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
		return
	}

	pc.checkPLCConnectionSafe()
	pc.checkRadarConnections()
	pc.checkSystemHealth()
	pc.cleanupCounters() // NOVO: Cleanup automático
}

// checkPLCConnectionSafe verificação segura com proteção null
func (pc *PLCController) checkPLCConnectionSafe() {
	// Verificar se cliente e reader existem
	if pc.plc == nil || pc.reader == nil {
		atomic.StoreInt32(&pc.plcConnected, 0)
		atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
		pc.circuitBreakerTimeout = time.Now().Add(2 * time.Minute)
		pc.logCriticalOnce("PLC", "NULL_CLIENT", "Cliente PLC ou reader é null")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	connected := make(chan bool, 1)
	errorChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				connected <- false
				errorChan <- fmt.Errorf("panic na verificação: %v", r)
			}
		}()

		_, err := pc.reader.ReadCommands()

		// Detectar especificamente conexão null
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "null") {
			connected <- false
			errorChan <- err
			return
		}

		connected <- err == nil
		if err != nil {
			errorChan <- err
		}
	}()

	var isConnected bool
	var connectionError error

	select {
	case isConnected = <-connected:
		select {
		case connectionError = <-errorChan:
		default:
		}
	case <-ctx.Done():
		isConnected = false
		connectionError = fmt.Errorf("timeout na verificação PLC")
	}

	currentlyConnected := atomic.LoadInt32(&pc.plcConnected) == 1

	if !isConnected {
		atomic.AddInt32(&pc.consecutiveFails, 1)
		fails := atomic.LoadInt32(&pc.consecutiveFails)

		// Tratar conexão null especificamente
		if connectionError != nil && strings.Contains(strings.ToLower(connectionError.Error()), "null") {
			if fails == 1 {
				pc.logCriticalOnce("PLC", "NULL_CONNECTION", "ERRO CRÍTICO: Conexão PLC é NULL - Inicializando recovery")
				// Ativar circuit breaker imediatamente para conexão null
				atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
				pc.circuitBreakerTimeout = time.Now().Add(30 * time.Second)
			}
		}

		if currentlyConnected && fails == 1 {
			atomic.StoreInt32(&pc.plcConnected, 0)
			pc.logCriticalOnce("PLC", "DISCONNECT", "Conexão PLC PERDIDA")
		}

		// Log crítico apenas a cada 10 falhas para evitar spam
		if fails%10 == 0 {
			pc.logCriticalOnce("PLC", "CRITICAL", fmt.Sprintf("PLC: %d falhas consecutivas", fails))
		}
	} else {
		prevFails := atomic.LoadInt32(&pc.consecutiveFails)
		atomic.StoreInt32(&pc.consecutiveFails, 0)
		pc.lastSuccessfulOp = time.Now()

		if !currentlyConnected {
			atomic.StoreInt32(&pc.plcConnected, 1)
			pc.logCriticalOnce("PLC", "RECONNECT", fmt.Sprintf("Conexão PLC RESTAURADA após %d falhas", prevFails))
		}
	}

	pc.lastPLCCheck = time.Now()
}

// checkRadarConnections - SÓ verifica radares HABILITADOS
func (pc *PLCController) checkRadarConnections() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()
	for radarID, counter := range pc.radarCounters {
		// SE RADAR DESABILITADO, NÃO FAZER NADA
		enabled := pc.radarsEnabled[radarID]
		if !enabled {
			// Marcar como desconectado se estava conectado
			if pc.radarsConnected[radarID] {
				pc.radarsConnected[radarID] = false
			}
			continue // PULAR radar desabilitado
		}

		// Só verificar radares HABILITADOS
		timeSinceLastSeen := now.Sub(counter.LastSeen)

		if timeSinceLastSeen > 60*time.Second {
			if pc.radarsConnected[radarID] {
				pc.radarsConnected[radarID] = false
				pc.logCriticalOnce("RADAR", "DISCONNECT", fmt.Sprintf("Radar %s DESCONECTADO", radarID))
			}
			atomic.AddInt32(&counter.ConsecutiveFails, 1)
		} else {
			if !pc.radarsConnected[radarID] {
				pc.radarsConnected[radarID] = true
				pc.logCriticalOnce("RADAR", "RECONNECT", fmt.Sprintf("Radar %s RECONECTADO", radarID))
			}
			atomic.StoreInt32(&counter.ConsecutiveFails, 0)
		}
	}
}

// cleanupCounters limpa contadores automaticamente
func (pc *PLCController) cleanupCounters() {
	errorCount := atomic.LoadInt32(&pc.errorCount)
	packetCount := atomic.LoadInt32(&pc.packetCount)

	// Auto-reset se contadores muito altos (prevenir overflow)
	if errorCount > 100000 || packetCount > 1000000 {
		pc.logCriticalOnce("CLEANUP", "AUTO_RESET", fmt.Sprintf("Auto-reset: %d erros, %d pacotes", errorCount, packetCount))
		pc.resetAllErrors()
	}
}

// checkSystemHealth verifica saúde com limites inteligentes
func (pc *PLCController) checkSystemHealth() {
	totalErrors := atomic.LoadInt32(&pc.errorCount)
	consecutiveFails := atomic.LoadInt32(&pc.consecutiveFails)

	// Sistema crítico apenas com limites mais altos
	if totalErrors > 1000 && consecutiveFails > 50 {
		pc.logCriticalOnce("SYSTEM", "CRITICAL", fmt.Sprintf("Sistema crítico: %d erros, %d falhas PLC", totalErrors, consecutiveFails))
	}
}

// reconnectionLoop com backoff exponencial
func (pc *PLCController) reconnectionLoop() {
	defer pc.recoverLoop("reconnectionLoop")

	for {
		select {
		case <-pc.reconnectTicker.C:
			pc.attemptReconnectionSafe()
		case <-pc.stopChan:
			return
		}
	}
}

// attemptReconnectionSafe com circuit breaker e backoff
func (pc *PLCController) attemptReconnectionSafe() {
	// Não tentar reconectar se circuit breaker estiver aberto
	if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
		return
	}

	pc.connectMutex.Lock()
	defer pc.connectMutex.Unlock()

	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1
	consecutiveFails := atomic.LoadInt32(&pc.consecutiveFails)

	// Só tentar reconectar se realmente necessário
	if !plcConnected && consecutiveFails >= 5 {
		atomic.AddInt32(&pc.reconnectAttempts, 1)
		attempts := atomic.LoadInt32(&pc.reconnectAttempts)

		// Backoff exponencial
		backoffDuration := time.Duration(min(attempts*2, 30)) * time.Second

		pc.logInfo("RECONNECT", fmt.Sprintf("Tentativa #%d (aguardando %v)", attempts, backoffDuration))

		// Aguardar backoff
		time.Sleep(backoffDuration)

		if pc.attemptPLCReconnection() {
			atomic.StoreInt32(&pc.reconnectAttempts, 0)
			atomic.StoreInt32(&pc.consecutiveFails, 0)
			atomic.StoreInt32(&pc.plcConnected, 1)
			pc.lastSuccessfulOp = time.Now()
			pc.logCriticalOnce("RECONNECT", "SUCCESS", "Reconexão PLC bem-sucedida")
		} else {
			pc.logError("RECONNECT", "attemptPLCReconnection", fmt.Errorf("falha na tentativa %d", attempts))

			// Se muitas tentativas, abrir circuit breaker
			if attempts >= 5 {
				atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
				pc.circuitBreakerTimeout = time.Now().Add(2 * time.Minute)
				pc.logCriticalOnce("RECONNECT", "CIRCUIT_BREAK", "Muitas falhas - ativando circuit breaker")
			}
		}
	}
}

// attemptPLCReconnection com verificação e inicialização null-safe
func (pc *PLCController) attemptPLCReconnection() bool {
	// CRÍTICO: Verificar e re-inicializar cliente PLC se null
	if pc.plc == nil {
		pc.logCriticalOnce("RECONNECT", "NULL_PLC", "Cliente PLC é null - tentando re-inicializar")
		// Aqui você deve implementar re-inicialização do seu PLCClient específico
		// Exemplo: pc.plc = NewPLCClient("192.168.1.33", 102)
		return false // Retorna false até que cliente seja re-inicializado
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	success := make(chan bool, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				success <- false
			}
		}()

		// Aguardar antes de tentar
		time.Sleep(3 * time.Second)

		// Tentar operação de teste com proteção null
		if pc.reader == nil {
			success <- false
			return
		}

		_, err := pc.reader.ReadCommands()

		// Verificar especificamente por conexão null
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "null") ||
				strings.Contains(err.Error(), "192.168.1.33:102 is null") {
				pc.logError("RECONNECT", "nullConnectionDetected", err)
				success <- false
				return
			}
		}

		success <- err == nil
	}()

	select {
	case result := <-success:
		return result
	case <-ctx.Done():
		return false // Timeout
	}
}

// watchdogLoop monitora sistema geral
func (pc *PLCController) watchdogLoop() {
	defer pc.recoverLoop("watchdogLoop")

	lastPacketCount := atomic.LoadInt32(&pc.packetCount)

	for {
		select {
		case <-pc.watchdogTicker.C:
			currentPackets := atomic.LoadInt32(&pc.packetCount)

			// Verificar se há atividade
			if currentPackets == lastPacketCount {
				timeSinceSuccess := time.Since(pc.lastSuccessfulOp)
				if timeSinceSuccess > 5*time.Minute {
					pc.logCriticalOnce("WATCHDOG", "NO_ACTIVITY", fmt.Sprintf("Sistema sem atividade por %v", timeSinceSuccess))
					// Forçar reset se necessário
					if timeSinceSuccess > 10*time.Minute {
						go pc.executeDailyReset()
					}
				}
			} else {
				lastPacketCount = currentPackets
				pc.lastSuccessfulOp = time.Now()
			}

		case <-pc.stopChan:
			return
		}
	}
}

// recoverLoop função genérica de recovery
func (pc *PLCController) recoverLoop(loopName string) {
	if r := recover(); r != nil {
		pc.logCriticalOnce("PANIC", "RECOVERY", fmt.Sprintf("%s panic: %v", loopName, r))

		// Aguardar antes de restart para evitar loops rápidos
		time.Sleep(5 * time.Second)

		// Restart específico baseado no loop
		switch loopName {
		case "healthCheckLoop":
			go pc.healthCheckLoop()
		case "reconnectionLoop":
			go pc.reconnectionLoop()
		case "watchdogLoop":
			go pc.watchdogLoop()
		case "liveBitLoop":
			go pc.liveBitLoop()
		case "statusWriteLoop":
			go pc.statusWriteLoop()
		case "commandReadLoop":
			go pc.commandReadLoop()
		case "commandProcessor":
			go pc.commandProcessor()
		case "dailyResetScheduler":
			go pc.dailyResetScheduler()
		}
	}
}

// ========== LOOPS PRINCIPAIS CORRIGIDOS ==========

// liveBitLoop com recovery e validação
func (pc *PLCController) liveBitLoop() {
	defer pc.recoverLoop("liveBitLoop")

	for {
		select {
		case <-pc.liveBitTicker.C:
			// Só operar se circuit breaker fechado
			if atomic.LoadInt32(&pc.circuitBreakerOpen) == 0 {
				current := atomic.LoadInt32(&pc.liveBit)
				newValue := int32(1)
				if current == 1 {
					newValue = 0
				}
				atomic.StoreInt32(&pc.liveBit, newValue)
			}
		case <-pc.stopChan:
			return
		}
	}
}

// statusWriteLoop com proteção total e circuit breaker
func (pc *PLCController) statusWriteLoop() {
	defer pc.recoverLoop("statusWriteLoop")

	for {
		select {
		case <-pc.statusTicker.C:
			// Não escrever se circuit breaker aberto
			if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
				continue
			}

			pc.checkAndResetCounters()

			if err := pc.writeSystemStatusSafe(); err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
				pc.logError("STATUS_WRITE", "writeSystemStatus", err)
			} else {
				pc.lastSuccessfulOp = time.Now()
			}
		case <-pc.stopChan:
			return
		}
	}
}

// writeSystemStatusSafe com timeout e validação null
func (pc *PLCController) writeSystemStatusSafe() error {
	// Verificar se writer existe
	if pc.writer == nil {
		return fmt.Errorf("CRÍTICO: writer PLC é null")
	}

	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				result <- fmt.Errorf("panic na escrita: %v", r)
			}
		}()

		err := pc.writeSystemStatus()

		// Detectar conexão null na escrita
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "null") {
			atomic.StoreInt32(&pc.plcConnected, 0)
			atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
			pc.circuitBreakerTimeout = time.Now().Add(1 * time.Minute)
		}

		result <- err
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout na escrita de status")
	}
}

// commandReadLoop com circuit breaker
func (pc *PLCController) commandReadLoop() {
	defer pc.recoverLoop("commandReadLoop")

	for {
		select {
		case <-pc.commandTicker.C:
			// Não ler comandos se circuit breaker aberto
			if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
				continue
			}

			commands, err := pc.readCommandsSafe()
			if err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
				pc.logError("COMMAND_READ", "ReadCommands", err)
				continue
			}

			pc.lastSuccessfulOp = time.Now()
			pc.processCommands(commands)
		case <-pc.stopChan:
			return
		}
	}
}

// readCommandsSafe com timeout e proteção null
func (pc *PLCController) readCommandsSafe() (*models.PLCCommands, error) {
	// Verificação crítica: cliente e reader devem existir
	if pc.plc == nil {
		return nil, fmt.Errorf("CRÍTICO: cliente PLC é null")
	}

	if pc.reader == nil {
		return nil, fmt.Errorf("CRÍTICO: reader PLC é null")
	}

	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	type result struct {
		commands *models.PLCCommands
		err      error
	}

	resultChan := make(chan result, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultChan <- result{nil, fmt.Errorf("panic na leitura: %v", r)}
			}
		}()

		cmd, err := pc.reader.ReadCommands()

		// Detectar e tratar conexão null especificamente
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "null") {
			// Marcar como desconectado imediatamente
			atomic.StoreInt32(&pc.plcConnected, 0)
			atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
			resultChan <- result{nil, fmt.Errorf("CONEXÃO NULL DETECTADA: %v", err)}
			return
		}

		resultChan <- result{cmd, err}
	}()

	select {
	case res := <-resultChan:
		return res.commands, res.err
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout na leitura de comandos")
	}
}

// processCommands com validação e error handling
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	if commands == nil {
		return
	}

	for cmdName, handler := range pc.commandHandlers {
		if handler.ShouldExecute != nil && handler.ShouldExecute(commands, pc) {
			if handler.Execute != nil {
				handler.Execute(pc, commands)
			}

			if handler.NeedsReset {
				if err := pc.resetCommandSafe(handler.ResetByteOffset, handler.ResetBitOffset); err != nil {
					pc.logError("COMMAND_RESET", cmdName, err)
				}
			}
		}
	}

	pc.handleRadarEnables(commands)
	pc.handleResetErrors(commands.ResetErrors)
}

// resetCommandSafe com timeout
func (pc *PLCController) resetCommandSafe(byteOffset, bitOffset int) error {
	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		result <- pc.writer.ResetCommand(byteOffset, bitOffset)
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout no reset de comando")
	}
}

// commandProcessor com recovery
func (pc *PLCController) commandProcessor() {
	defer pc.recoverLoop("commandProcessor")

	for cmd := range pc.commandChan {
		pc.executeCommandSafe(cmd)
	}
}

// executeCommandSafe execução segura de comandos
func (pc *PLCController) executeCommandSafe(cmd models.SystemCommand) {
	switch cmd {
	case 1: // StartCollection
		if !pc.IsCollectionActive() {
			atomic.StoreInt32(&pc.collectionActive, 1)
			atomic.StoreInt32(&pc.emergencyStop, 0)
			pc.logInfo("COMMAND", "StartCollection executado")
		}
	case 2: // StopCollection
		if pc.IsCollectionActive() {
			atomic.StoreInt32(&pc.collectionActive, 0)
			pc.logInfo("COMMAND", "StopCollection executado")
		}
	case 3: // EmergencyStop
		atomic.StoreInt32(&pc.emergencyStop, 1)
		atomic.StoreInt32(&pc.collectionActive, 0)
		pc.logCriticalOnce("COMMAND", "EMERGENCY", "EmergencyStop executado")
	case 4: // ResetErrors
		pc.resetAllErrors()
		pc.logInfo("COMMAND", "ResetErrors executado")
	case 10: // System restart
		pc.executeSystemRestart()
	}
}

// ========== ESCRITA DE DADOS RADAR CORRIGIDA ==========

// WriteRadarData com proteção total contra conexão null
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	// Verificações críticas antes de qualquer operação
	if pc.plc == nil {
		return fmt.Errorf("CRÍTICO: cliente PLC é null")
	}

	if pc.writer == nil {
		return fmt.Errorf("CRÍTICO: writer PLC é null")
	}

	// Verificar circuit breaker
	if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
		return fmt.Errorf("circuit breaker aberto - operação bloqueada")
	}

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

	// Escrever com timeout e proteção null
	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				result <- fmt.Errorf("panic na escrita radar: %v", r)
			}
		}()

		plcData := pc.writer.BuildPLCRadarData(data)
		err := pc.writer.WriteRadarDataToDB100(plcData, baseOffset)

		// Detectar e tratar conexão null
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "null") {
			atomic.StoreInt32(&pc.plcConnected, 0)
			atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
			pc.circuitBreakerTimeout = time.Now().Add(30 * time.Second)
			result <- fmt.Errorf("CONEXÃO NULL: %v", err)
			return
		}

		result <- err
	}()

	var err error
	select {
	case err = <-result:
	case <-ctx.Done():
		err = fmt.Errorf("timeout na escrita do radar %s", data.RadarID)
	}

	if err != nil {
		pc.IncrementRadarErrors(data.RadarID)
		pc.logError("RADAR_WRITE", data.RadarID, err)
		return err
	}

	pc.IncrementRadarPackets(data.RadarID)
	pc.updateRadarActivity(data.RadarID)
	pc.lastSuccessfulOp = time.Now()

	return nil
}

// writeSystemStatus corrigido com timeout
func (pc *PLCController) writeSystemStatus() error {
	liveBit := atomic.LoadInt32(&pc.liveBit) == 1
	collectionActive := atomic.LoadInt32(&pc.collectionActive) == 1
	emergencyStop := atomic.LoadInt32(&pc.emergencyStop) == 1

	pc.stateMutex.RLock()
	caldeiraConnected := pc.radarsConnected["caldeira"]
	jusanteConnected := pc.radarsConnected["porta_jusante"]
	montanteConnected := pc.radarsConnected["porta_montante"]
	pc.stateMutex.RUnlock()

	errorCount := atomic.LoadInt32(&pc.errorCount)
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

	return pc.writeSystemMetricsSafe()
}

// writeSystemMetricsSafe com timeout
func (pc *PLCController) writeSystemMetricsSafe() error {
	ctx, cancel := context.WithTimeout(context.Background(), pc.operationTimeout)
	defer cancel()

	result := make(chan error, 1)

	go func() {
		result <- pc.writeSystemMetrics()
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout na escrita de métricas")
	}
}

// ========== RESET E RECOVERY INTELIGENTE ==========

// resetAllErrors com cleanup completo
func (pc *PLCController) resetAllErrors() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	oldErrors := atomic.LoadInt32(&pc.errorCount)
	oldPackets := atomic.LoadInt32(&pc.packetCount)

	// Reset contadores principais
	atomic.StoreInt32(&pc.errorCount, 0)
	atomic.StoreInt32(&pc.packetCount, 0)
	atomic.StoreInt32(&pc.consecutiveFails, 0)
	atomic.StoreInt32(&pc.reconnectAttempts, 0)

	// Reset circuit breaker se necessário
	if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
		atomic.StoreInt32(&pc.circuitBreakerOpen, 0)
		pc.logInfo("RESET", "Circuit breaker resetado")
	}

	// Reset radar counters
	for _, counter := range pc.radarCounters {
		atomic.StoreInt32(&counter.Packets, 0)
		atomic.StoreInt32(&counter.Errors, 0)
		atomic.StoreInt32(&counter.ConsecutiveFails, 0)
		counter.LastSeen = time.Now()
	}

	pc.lastResetTime = time.Now()
	pc.lastSuccessfulOp = time.Now()

	pc.logCriticalOnce("RESET", "COMPLETE", fmt.Sprintf("Reset completo: %d erros, %d pacotes resetados", oldErrors, oldPackets))
}

// executeDailyReset com proteção contra execuções múltiplas
func (pc *PLCController) executeDailyReset() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()
	if now.Sub(pc.lastResetTime) < 30*time.Minute {
		pc.logInfo("RESET", "Reset ignorado - muito recente")
		return
	}

	pc.logDailyStatistics()
	pc.resetAllErrors()
}

// ========== MÉTODOS AUXILIARES MELHORADOS ==========

// updateRadarActivity thread-safe
func (pc *PLCController) updateRadarActivity(radarID string) {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	if counter, exists := pc.radarCounters[radarID]; exists {
		counter.LastSeen = time.Now()
		// Reset falhas consecutivas em caso de sucesso
		atomic.StoreInt32(&counter.ConsecutiveFails, 0)
	}
}

// IncrementRadarErrors com limite e circuit breaker automático
func (pc *PLCController) IncrementRadarErrors(radarID string) {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	pc.stateMutex.RUnlock()

	if !exists {
		return
	}

	// Limitar incremento para evitar overflow
	currentErrors := atomic.LoadInt32(&counter.Errors)
	if currentErrors < 10000 { // Limite mais baixo
		atomic.AddInt32(&counter.Errors, 1)
		atomic.AddInt32(&pc.errorCount, 1)
	}

	atomic.AddInt32(&counter.ConsecutiveFails, 1)
	fails := atomic.LoadInt32(&counter.ConsecutiveFails)

	// Se radar tem muitos erros, ativar circuit breaker
	if fails >= 10 {
		atomic.StoreInt32(&pc.circuitBreakerOpen, 1)
		pc.circuitBreakerTimeout = time.Now().Add(1 * time.Minute)

		// Log apenas múltiplos de 20 para evitar spam
		if fails%20 == 0 {
			pc.logCriticalOnce("RADAR", "CRITICAL", fmt.Sprintf("Radar %s: %d falhas - circuit breaker ativado", radarID, fails))
		}
	}
}

// calculateSystemHealth melhorado
func (pc *PLCController) calculateSystemHealth(caldeira, jusante, montante, emergency bool, errorCount int32) bool {
	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1
	circuitBreakerOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1

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

	// Sistema saudável: PLC conectado + circuit breaker fechado + pelo menos 1 radar + sem emergência + poucos erros
	return plcConnected && !circuitBreakerOpen && healthyRadars > 0 && !emergency && errorCount < 500
}

// executeSystemRestart restart inteligente
func (pc *PLCController) executeSystemRestart() {
	pc.logCriticalOnce("RESTART", "INIT", "Sistema restart solicitado")

	// Reset completo
	pc.resetAllErrors()

	// Fechar circuit breaker
	atomic.StoreInt32(&pc.circuitBreakerOpen, 0)

	// Marcar como conectado para tentar operações
	atomic.StoreInt32(&pc.plcConnected, 1)

	pc.logCriticalOnce("RESTART", "COMPLETE", "Sistema restart concluído")
}

// ========== MÉTODOS PÚBLICOS CORRIGIDOS ==========

func (pc *PLCController) IsCollectionActive() bool {
	collectionActive := atomic.LoadInt32(&pc.collectionActive) == 1
	emergencyStop := atomic.LoadInt32(&pc.emergencyStop) == 1
	circuitBreakerOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1
	return collectionActive && !emergencyStop && !circuitBreakerOpen
}

func (pc *PLCController) IsPLCConnected() bool {
	plcConnected := atomic.LoadInt32(&pc.plcConnected) == 1
	circuitBreakerOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1
	return plcConnected && !circuitBreakerOpen
}

// ForceRecovery recovery manual para emergências
func (pc *PLCController) ForceRecovery() {
	pc.logCriticalOnce("MANUAL", "FORCE_RECOVERY", "Recovery manual forçado")

	// Reset completo
	atomic.StoreInt32(&pc.circuitBreakerOpen, 0)
	atomic.StoreInt32(&pc.consecutiveFails, 0)
	atomic.StoreInt32(&pc.reconnectAttempts, 0)
	atomic.StoreInt32(&pc.plcConnected, 1)
	pc.lastSuccessfulOp = time.Now()

	pc.resetAllErrors()
}

// InitializePLCConnection inicializa conexão de forma segura
func (pc *PLCController) InitializePLCConnection() error {
	pc.connectMutex.Lock()
	defer pc.connectMutex.Unlock()

	// Se cliente é null, tentar re-inicializar
	if pc.plc == nil {
		pc.logCriticalOnce("INIT", "NULL_CLIENT", "Re-inicializando cliente PLC null")
		// IMPLEMENTAR: Aqui você deve adicionar sua lógica específica para criar novo PLCClient
		// Exemplo: pc.plc = NewYourPLCClient("192.168.1.33", 102)
		return fmt.Errorf("cliente PLC precisa ser re-inicializado manualmente")
	}

	// Re-criar reader e writer se necessário
	if pc.reader == nil {
		pc.reader = NewPLCReader(pc.plc)
		pc.logInfo("INIT", "Reader PLC re-criado")
	}

	if pc.writer == nil {
		pc.writer = NewPLCWriter(pc.plc)
		pc.logInfo("INIT", "Writer PLC re-criado")
	}

	// Testar conexão COMPLETA (leitura E escrita)
	if pc.testPLCConnectionComplete() {
		atomic.StoreInt32(&pc.plcConnected, 1)
		atomic.StoreInt32(&pc.circuitBreakerOpen, 0)
		atomic.StoreInt32(&pc.consecutiveFails, 0)
		pc.lastSuccessfulOp = time.Now()
		pc.logCriticalOnce("INIT", "SUCCESS", "Inicialização PLC bem-sucedida")
		return nil
	}

	return fmt.Errorf("falha na inicialização da conexão PLC")
}

// ForceInitialization força re-inicialização completa
func (pc *PLCController) ForceInitialization() error {
	pc.logCriticalOnce("FORCE", "INIT", "Forçando re-inicialização completa do sistema PLC")

	// Reset total
	atomic.StoreInt32(&pc.circuitBreakerOpen, 0)
	atomic.StoreInt32(&pc.consecutiveFails, 0)
	atomic.StoreInt32(&pc.reconnectAttempts, 0)

	// Tentar inicialização
	if err := pc.InitializePLCConnection(); err != nil {
		pc.logError("FORCE_INIT", "InitializePLCConnection", err)
		return err
	}

	// Reset contadores após inicialização bem-sucedida
	pc.resetAllErrors()

	return nil
}

// ========== UTILITÁRIOS AUXILIARES ==========

// min função utilitária
func min(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// handleRadarEnables gerencia enables
func (pc *PLCController) handleRadarEnables(commands *models.PLCCommands) {
	radarEnables := map[string]bool{
		"caldeira":       commands.EnableRadarCaldeira,
		"porta_jusante":  commands.EnableRadarPortaJusante,
		"porta_montante": commands.EnableRadarPortaMontante,
	}

	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

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
}

// handleResetErrors com proteção
func (pc *PLCController) handleResetErrors(resetErrors bool) {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	if resetErrors {
		if !pc.resetErrorsActive {
			pc.resetErrorsActive = true
			pc.resetErrorsStartTime = time.Now()
			pc.rebootExecuted = false
			pc.logCriticalOnce("REBOOT", "INIT", "ResetErrors ativo - 10s para reboot")
		} else {
			elapsed := time.Since(pc.resetErrorsStartTime)
			if elapsed >= 10*time.Second && !pc.rebootExecuted {
				pc.rebootExecuted = true
				pc.logCriticalOnce("REBOOT", "EXEC", "EXECUTANDO REBOOT DO SERVIDOR")
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

// checkAndResetCounters com proteção inteligente
func (pc *PLCController) checkAndResetCounters() {
	const CRITICAL_LIMIT = 1000000 // Reduzido para ser mais conservador

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	if totalPackets > CRITICAL_LIMIT || totalErrors > CRITICAL_LIMIT {
		pc.logCriticalOnce("RESET", "AUTO_CRITICAL", fmt.Sprintf("Auto-reset crítico: Packets=%d, Errors=%d", totalPackets, totalErrors))
		go pc.executeDailyReset()
	}
}

// dailyResetScheduler com recovery
func (pc *PLCController) dailyResetScheduler() {
	defer pc.recoverLoop("dailyResetScheduler")

	ticker := time.NewTicker(2 * time.Hour) // Aumentado para reduzir overhead
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

// checkResetConditions com validações melhoradas
func (pc *PLCController) checkResetConditions() {
	now := time.Now()

	pc.stateMutex.RLock()
	lastReset := pc.lastResetTime
	pc.stateMutex.RUnlock()

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	// Reset diário às 00:00
	if now.Hour() == 0 && now.Sub(lastReset) > 23*time.Hour {
		pc.logCriticalOnce("RESET", "SCHEDULE", "Reset diário às 00:00")
		pc.executeDailyReset()
		return
	}

	// Overflow protection mais conservador
	const MAX_SAFE = 800000
	if totalPackets > MAX_SAFE || totalErrors > MAX_SAFE {
		pc.logCriticalOnce("RESET", "OVERFLOW", fmt.Sprintf("Overflow: Packets=%d, Errors=%d", totalPackets, totalErrors))
		pc.executeDailyReset()
		return
	}

	// Timeout máximo (24h)
	if now.Sub(lastReset) > 24*time.Hour {
		pc.logCriticalOnce("RESET", "TIMEOUT", fmt.Sprintf("%v sem reset", now.Sub(lastReset)))
		pc.executeDailyReset()
		return
	}

	// Contadores negativos (overflow)
	if totalPackets < 0 || totalErrors < 0 {
		pc.logCriticalOnce("RESET", "EMERGENCY", "Overflow detectado - reset emergencial")
		pc.executeDailyReset()
		return
	}
}

// logDailyStatistics com informações úteis
func (pc *PLCController) logDailyStatistics() {
	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)
	dayDuration := time.Since(pc.lastResetTime)
	reconnectAttempts := atomic.LoadInt32(&pc.reconnectAttempts)
	circuitBreakerOpen := atomic.LoadInt32(&pc.circuitBreakerOpen) == 1

	if totalPackets > 0 {
		successRate := float64(totalPackets-totalErrors) / float64(totalPackets) * 100
		packetsPerHour := float64(totalPackets) / dayDuration.Hours()

		pc.logCriticalOnce("STATS", "DAILY", fmt.Sprintf("Período: %v | Pacotes: %d | Erros: %d | Taxa: %.2f%% | Pac/h: %.0f | Reconexões: %d | CB: %v",
			dayDuration, totalPackets, totalErrors, successRate, packetsPerHour, reconnectAttempts, circuitBreakerOpen))
	}

	// Radar stats
	pc.stateMutex.RLock()
	for radarID, counter := range pc.radarCounters {
		packets := atomic.LoadInt32(&counter.Packets)
		errors := atomic.LoadInt32(&counter.Errors)
		connected := pc.radarsConnected[radarID]

		if packets > 0 || errors > 0 {
			pc.logInfo("RADAR_STATS", fmt.Sprintf("%s: %d pacotes, %d erros, conectado=%v", radarID, packets, errors, connected))
		}
	}
	pc.stateMutex.RUnlock()
}

// executeSecureReboot com timeout
func (pc *PLCController) executeSecureReboot() {
	pc.logCriticalOnce("REBOOT", "START", "Iniciando reboot seguro...")

	// Salvar logs antes do reboot
	if pc.logFile != nil {
		pc.logFile.Sync()
	}

	time.Sleep(2 * time.Second)

	// Tentar script de reboot com timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/local/bin/radar-reboot")
	if err := cmd.Start(); err != nil {
		pc.logError("REBOOT", "radar-reboot script", err)

		// Fallback
		if file, err := os.Create("/tmp/radar-reboot-request"); err == nil {
			file.WriteString(fmt.Sprintf("REBOOT_REQUEST_%s", time.Now().Format("2006-01-02_15:04:05")))
			file.Close()
			pc.logInfo("REBOOT", "Arquivo de requisição criado")
		}
	} else {
		pc.logInfo("REBOOT", "Script executado")
	}
}

// writeSystemMetrics com error handling
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
			return err // Retornar primeiro erro encontrado
		}
	}

	return nil
}

// getSystemMetrics com validação
func (pc *PLCController) getSystemMetrics() SystemMetrics {
	return SystemMetrics{
		CPU:    pc.getLinuxCPUUsage(),
		Memory: pc.getLinuxMemoryUsage(),
		Disk:   pc.getLinuxDiskUsage(),
		Temp:   pc.getLinuxTemperature(),
	}
}

// ========== MÉTODOS DE MONITORAMENTO ==========

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
		"uptime_hours":         time.Since(pc.startTime).Hours(),
		"collection_active":    atomic.LoadInt32(&pc.collectionActive) == 1,
		"emergency_stop":       atomic.LoadInt32(&pc.emergencyStop) == 1,
		"plc_connected":        atomic.LoadInt32(&pc.plcConnected) == 1,
		"circuit_breaker_open": atomic.LoadInt32(&pc.circuitBreakerOpen) == 1,
		"total_packets":        atomic.LoadInt32(&pc.packetCount),
		"total_errors":         atomic.LoadInt32(&pc.errorCount),
		"connected_radars":     connectedRadars,
		"websocket_clients":    atomic.LoadInt32(&pc.wsClients),
		"nats_connected":       atomic.LoadInt32(&pc.natsConnected) == 1,
		"consecutive_fails":    atomic.LoadInt32(&pc.consecutiveFails),
		"reconnect_attempts":   atomic.LoadInt32(&pc.reconnectAttempts),
		"last_plc_check":       pc.lastPLCCheck.Format("15:04:05"),
		"last_reset":           pc.lastResetTime.Format("2006-01-02 15:04:05"),
		"last_successful_op":   pc.lastSuccessfulOp.Format("15:04:05"),
	}
}

// ========== IMPLEMENTAÇÕES DE COMPATIBILIDADE ==========

func (pc *PLCController) IsDebugMode() bool {
	return atomic.LoadInt32(&pc.debugMode) == 1
}

func (pc *PLCController) IsEmergencyStop() bool {
	return atomic.LoadInt32(&pc.emergencyStop) == 1
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
	defer pc.stateMutex.Unlock()

	for radarID, connected := range status {
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
	}
}

func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

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
}

func (pc *PLCController) IncrementRadarPackets(radarID string) {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	pc.stateMutex.RUnlock()

	if exists {
		// Limitar para evitar overflow
		currentPackets := atomic.LoadInt32(&counter.Packets)
		if currentPackets < 1000000 {
			atomic.AddInt32(&counter.Packets, 1)
			atomic.AddInt32(&pc.packetCount, 1)
		}
		counter.LastSeen = time.Now()
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

// WriteMultiRadarData com verificação TOTAL coordenada
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	// PRIMEIRA VERIFICAÇÃO: Circuit breaker
	if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
		return nil // NÃO tentar escrever nada se circuit breaker aberto
	}

	// SEGUNDA VERIFICAÇÃO: PLC conectado
	if atomic.LoadInt32(&pc.plcConnected) == 0 {
		return nil // NÃO escrever se PLC desconectado
	}

	var successCount int
	var errorCount int

	for _, radarData := range data.Radars {
		// Verificar enable individual + circuit breaker novamente
		if !pc.IsRadarEnabled(radarData.RadarID) {
			continue // Pular radar desabilitado
		}

		if atomic.LoadInt32(&pc.circuitBreakerOpen) == 1 {
			break // Parar se circuit breaker ativou durante o loop
		}

		if err := pc.WriteRadarData(radarData); err != nil {
			errorCount++
			// Se muitos erros, parar o loop
			if errorCount > 2 {
				pc.logCriticalOnce("MULTI_RADAR", "TOO_MANY_ERRORS", "Muitos erros - parando escrita")
				break
			}
		} else {
			successCount++
		}
	}

	// Log apenas erros - não logar sucessos normais
	if errorCount > 0 {
		pc.logInfo("MULTI_RADAR", fmt.Sprintf("Erros: %d de %d radares", errorCount, len(data.Radars)))
	}

	return nil // Sempre retornar nil para evitar propagação de erros
}

// ========== MÉTRICAS DO SISTEMA ==========

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

// ========== MÉTODOS DE CONTROLE ==========

func (pc *PLCController) ForceCounterReset() {
	pc.logCriticalOnce("MANUAL", "RESET", "Reset manual forçado")
	pc.executeDailyReset()
}

func (pc *PLCController) ForceReconnection() {
	pc.logCriticalOnce("MANUAL", "RECONNECT", "Reconexão manual forçada")

	// Fechar circuit breaker para permitir tentativa
	atomic.StoreInt32(&pc.circuitBreakerOpen, 0)

	go pc.attemptReconnectionSafe()
}

func (pc *PLCController) SetDebugMode(enabled bool) {
	if enabled {
		atomic.StoreInt32(&pc.debugMode, 1)
		pc.logInfo("DEBUG", "Modo debug ATIVADO")
	} else {
		atomic.StoreInt32(&pc.debugMode, 0)
		pc.logInfo("DEBUG", "Modo debug DESATIVADO")
	}
}

func (pc *PLCController) GetLogFilePath() string {
	return filepath.Join(pc.logDirectory, fmt.Sprintf("plc_controller_%s.log", time.Now().Format("2006-01-02")))
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
	pc.logCriticalOnce("REQUEST", "APP_RESTART", "Application restart solicitado")
	select {
	case pc.commandChan <- 13:
	default:
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

func (pc *PLCController) GetConnectionReport() map[string]interface{} {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	report := map[string]interface{}{
		"timestamp":            time.Now().Format("2006-01-02 15:04:05"),
		"plc_connected":        atomic.LoadInt32(&pc.plcConnected) == 1,
		"circuit_breaker_open": atomic.LoadInt32(&pc.circuitBreakerOpen) == 1,
		"consecutive_fails":    atomic.LoadInt32(&pc.consecutiveFails),
		"last_plc_check":       pc.lastPLCCheck.Format("15:04:05"),
		"last_successful_op":   pc.lastSuccessfulOp.Format("15:04:05"),
		"reconnect_attempts":   atomic.LoadInt32(&pc.reconnectAttempts),
	}

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
