// ============================================================================
// ARQUIVO: backend/internal/plc/plc_manager.go - FINAL CORRIGIDO
// PROTEÇÕES: Timeout, Overflow, Fallback, Validação, Panic Recovery
// OTIMIZAÇÃO: Logging inteligente por estados de conexão - SEM ERRO FALSO
// ============================================================================
package plc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"backend/internal/logger"
	"backend/pkg/models"
)

// PLCClient interface
type PLCClient interface {
	AGWriteDB(dbNumber, byteOffset, size int, buffer []byte) error
	AGReadDB(dbNumber, byteOffset, size int, buffer []byte) error
}

// PLCManager - UNIFICADO E BLINDADO para 1 PLC + 3 Radares
type PLCManager struct {
	// Conexão PLC
	plcSiemens   *SiemensPLC
	client       PLCClient
	systemLogger *logger.SystemLogger

	// 🛡️ ESTADOS ATÔMICOS (THREAD-SAFE)
	connected         int32 // atomic boolean (0/1)
	liveBit           int32 // atomic boolean (0/1)
	collectionActive  int32 // atomic boolean (0/1)
	emergencyStop     int32 // atomic boolean (0/1)
	isShuttingDown    int32 // atomic boolean (0/1)
	consecutiveErrors int32 // atomic counter com overflow protection

	// 🛡️ ESTADOS DOS 3 RADARES (MUTEX PROTEGIDO)
	radarMutex                   sync.RWMutex
	radarCaldeiraEnabled         bool
	radarPortaJusanteEnabled     bool
	radarPortaMontanteEnabled    bool
	radarCaldeiraConnected       bool
	radarPortaJusanteConnected   bool
	radarPortaMontanteConnected  bool
	lastRadarCaldeiraUpdate      time.Time
	lastRadarPortaJusanteUpdate  time.Time
	lastRadarPortaMontanteUpdate time.Time
	radarTimeoutDuration         time.Duration

	// 🛡️ SISTEMA DE CONTROLE COM CLEANUP GARANTIDO
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	commandChan chan models.SystemCommand
	stopOnce    sync.Once

	// 🛡️ TICKERS COM FALLBACK E LEAK PROTECTION
	tickers struct {
		mutex        sync.Mutex
		liveBit      *time.Ticker
		status       *time.Ticker
		command      *time.Ticker
		radarMonitor *time.Ticker
		allStopped   bool
	}

	// 🛡️ REBOOT SYSTEM THREAD-SAFE
	rebootMutex          sync.Mutex
	rebootTimer          *time.Timer
	rebootTimerActive    bool
	lastResetErrorsState bool

	// 🛡️ SISTEMA DE LOGGING INTELIGENTE POR ESTADOS
	connectionLogging struct {
		mutex                  sync.RWMutex
		lastConnectionState    bool      // estado anterior da conexão
		disconnectionStartTime time.Time // início da desconexão atual
		reconnectionAttempts   int32     // atomic - tentativas de reconexão
		isInDisconnectedPeriod bool      // período de desconexão ativo
		lastErrorTime          time.Time // último erro para fallback
		fallbackThrottle       struct {
			lastLog     time.Time
			lastMessage string
			count       int32 // atomic para thread safety
		}
	}

	// 🛡️ ERROR HANDLING COM OVERFLOW PROTECTION (MANTIDO PARA COMPATIBILIDADE)
	maxErrors int32
}

const (
	REBOOT_TIMEOUT_SECONDS           = 10
	RADAR_TIMEOUT_TOLERANCE          = 45 * time.Second
	PLC_OPERATION_TIMEOUT            = 3 * time.Second  // 🛡️ TIMEOUT OBRIGATÓRIO
	PLC_RECONNECT_TIMEOUT            = 15 * time.Second // 🛡️ TIMEOUT RECONEXÃO
	MAX_ERROR_COUNT                  = 1000000          // 🛡️ OVERFLOW PROTECTION
	CONNECTION_STATE_LOG_TIMEOUT     = 15 * time.Minute // Fallback para logs críticos
	MAX_RECONNECTION_ATTEMPTS_REPORT = 10000            // Overflow protection para tentativas
)

// NewPLCManager cria gerenciador BLINDADO para 1 PLC + 3 Radares
func NewPLCManager(plcIP string) *PLCManager {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())

	pm := &PLCManager{
		plcSiemens:           NewSiemensPLC(plcIP),
		ctx:                  ctx,
		cancel:               cancel,
		commandChan:          make(chan models.SystemCommand, 20),
		radarTimeoutDuration: RADAR_TIMEOUT_TOLERANCE,
		maxErrors:            15,

		lastRadarCaldeiraUpdate:      now,
		lastRadarPortaJusanteUpdate:  now,
		lastRadarPortaMontanteUpdate: now,
	}

	// 🛡️ ESTADOS INICIAIS ATÔMICOS
	atomic.StoreInt32(&pm.collectionActive, 1) // true
	atomic.StoreInt32(&pm.consecutiveErrors, 0)

	// 🛡️ RADARES INICIALMENTE HABILITADOS
	pm.radarMutex.Lock()
	pm.radarCaldeiraEnabled = true
	pm.radarPortaJusanteEnabled = true
	pm.radarPortaMontanteEnabled = true
	pm.radarMutex.Unlock()

	// 🛡️ INICIALIZAÇÃO DO SISTEMA DE LOGGING INTELIGENTE
	pm.connectionLogging.mutex.Lock()
	pm.connectionLogging.lastConnectionState = false // assume desconectado inicialmente
	pm.connectionLogging.isInDisconnectedPeriod = false
	pm.connectionLogging.lastErrorTime = time.Time{}
	atomic.StoreInt32(&pm.connectionLogging.reconnectionAttempts, 0)
	atomic.StoreInt32(&pm.connectionLogging.fallbackThrottle.count, 0)
	pm.connectionLogging.mutex.Unlock()

	return pm
}

func (pm *PLCManager) SetSystemLogger(logger *logger.SystemLogger) {
	pm.systemLogger = logger
}

// 🛡️ CONNECT COM TIMEOUT E ERROR HANDLING - SEM LOG DE ERRO FALSO
func (pm *PLCManager) Connect() error {
	// Timeout na conexão
	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during connect: %v", r)
			}
		}()
		done <- pm.plcSiemens.Connect()
	}()

	select {
	case err := <-done:
		if err != nil {
			atomic.StoreInt32(&pm.connected, 0)
			pm.handleConnectionStateChangeSecure(false, err)
			return err
		}
		pm.client = pm.plcSiemens.Client
		atomic.StoreInt32(&pm.connected, 1)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)
		pm.handleConnectionStateChangeSecure(true, nil)
		return nil

	case <-time.After(10 * time.Second): // 🛡️ TIMEOUT 10s
		atomic.StoreInt32(&pm.connected, 0)
		err := fmt.Errorf("connection timeout (10s)")
		pm.handleConnectionStateChangeSecure(false, err)
		return err
	}
}

func (pm *PLCManager) IsPLCConnected() bool {
	return atomic.LoadInt32(&pm.connected) == 1
}

// 🛡️ START COM PROTEÇÃO COMPLETA
func (pm *PLCManager) StartWithContext(parentCtx context.Context) {
	if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
		fmt.Println("⚠️  PLC Manager já em shutdown")
		return
	}

	pm.ctx, pm.cancel = context.WithCancel(parentCtx)

	// 🛡️ TENTAR CONECTAR COM TIMEOUT - SEM LOG DE ERRO FALSO
	if err := pm.Connect(); err != nil {
		// Apenas console - logging inteligente cuida dos logs
		fmt.Printf("⚠️  PLC inicial não conectou: %v\n", err)
	} else {
		// ✅ SUCESSO NO CONSOLE - SEM LOG DE ERRO
		fmt.Println("✅ PLC conectado inicialmente")
	}

	// 🛡️ INICIALIZAR TICKERS COM PROTEÇÃO
	if err := pm.initTickersSecure(); err != nil {
		fmt.Printf("❌ Falha ao inicializar tickers: %v\n", err)
		return
	}

	// 🛡️ INICIAR GOROUTINES BLINDADAS
	pm.wg.Add(5)
	go pm.liveBitLoopSecure()
	go pm.statusWriteLoopSecure()
	go pm.commandReadLoopSecure()
	go pm.commandProcessorSecure()
	go pm.radarMonitorLoopSecure()

	fmt.Println("✅ PLC Manager: Sistema BLINDADO iniciado (1 PLC + 3 Radares)")
}

func (pm *PLCManager) Start() {
	pm.StartWithContext(context.Background())
}

// 🛡️ STOP COM CLEANUP GARANTIDO
func (pm *PLCManager) Stop() {
	pm.stopOnce.Do(func() {
		fmt.Println("🛑 PLC Manager: Parando sistema BLINDADO...")

		atomic.StoreInt32(&pm.isShuttingDown, 1)
		pm.stopTickersSecure()
		pm.cancelRebootTimerSecure()

		if pm.cancel != nil {
			pm.cancel()
		}

		pm.closeCommandChanSecure()

		// 🛡️ AGUARDAR GOROUTINES COM TIMEOUT
		done := make(chan struct{})
		go func() {
			pm.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			fmt.Println("✅ PLC Manager: Todas goroutines finalizadas")
		case <-time.After(10 * time.Second):
			fmt.Println("⚠️  PLC Manager: Timeout - forçando parada")
		}

		// 🛡️ CLEANUP FINAL
		if pm.plcSiemens != nil {
			pm.plcSiemens.Disconnect()
		}
		atomic.StoreInt32(&pm.isShuttingDown, 0)
		fmt.Println("✅ PLC Manager: Sistema BLINDADO parado")
	})
}

// ============================================================================
// 🛡️ SISTEMA DE LOGGING INTELIGENTE POR ESTADOS DE CONEXÃO - SEM ERRO FALSO
// ============================================================================

// handleConnectionStateChangeSecure - LÓGICA INTELIGENTE DE LOGGING CORRIGIDA
func (pm *PLCManager) handleConnectionStateChangeSecure(connected bool, err error) {
	pm.connectionLogging.mutex.Lock()
	defer pm.connectionLogging.mutex.Unlock()

	now := time.Now()
	previousState := pm.connectionLogging.lastConnectionState

	// 🛡️ PRIMEIRA CONEXÃO (STARTUP) - SEM LOG DE ERRO
	if connected && !previousState && !pm.connectionLogging.isInDisconnectedPeriod {
		// ✅ PRIMEIRA CONEXÃO BEM-SUCEDIDA - APENAS CONSOLE
		// NÃO LOGAR COMO ERRO - é normal na inicialização
		pm.connectionLogging.lastConnectionState = connected
		return
	}

	// 🛡️ MUDANÇA DE ESTADO: DESCONECTADO → CONECTADO (RECONEXÃO)
	if connected && !previousState && pm.connectionLogging.isInDisconnectedPeriod {
		// Calculando duração da desconexão
		disconnectionDuration := now.Sub(pm.connectionLogging.disconnectionStartTime)
		attempts := atomic.LoadInt32(&pm.connectionLogging.reconnectionAttempts)

		// 🛡️ OVERFLOW PROTECTION
		if attempts > MAX_RECONNECTION_ATTEMPTS_REPORT {
			attempts = MAX_RECONNECTION_ATTEMPTS_REPORT
		}

		// LOG INFORMATIVO DE RECONEXÃO
		message := fmt.Sprintf("PLC reconectado após %v - %d tentativas",
			pm.formatDurationSecure(disconnectionDuration), attempts)
		fmt.Printf("✅ %s\n", message)

		if pm.systemLogger != nil {
			pm.systemLogger.LogConfigurationChange("PLC_MANAGER", message)
		}

		// Reset do período de desconexão
		pm.connectionLogging.isInDisconnectedPeriod = false
		atomic.StoreInt32(&pm.connectionLogging.reconnectionAttempts, 0)
	}

	// 🛡️ MUDANÇA DE ESTADO: CONECTADO → DESCONECTADO
	if !connected && previousState {
		// INÍCIO DO PERÍODO DE DESCONEXÃO - LOG ÚNICO
		message := "PLC desconectado - tentando reconectar"
		if err != nil {
			message = fmt.Sprintf("PLC desconectado (%v) - tentando reconectar", err)
		}

		fmt.Printf("⚠️  %s\n", message)
		if pm.systemLogger != nil {
			pm.systemLogger.LogConfigurationChange("PLC_MANAGER", message)
		}

		pm.connectionLogging.isInDisconnectedPeriod = true
		pm.connectionLogging.disconnectionStartTime = now
		atomic.StoreInt32(&pm.connectionLogging.reconnectionAttempts, 0)
	}

	// 🛡️ PERÍODO DE DESCONEXÃO CONTÍNUA - SILÊNCIO
	if !connected && !previousState && pm.connectionLogging.isInDisconnectedPeriod {
		// SILÊNCIO TOTAL - apenas contabilizar tentativas se for erro de reconexão
		if err != nil && pm.isConnectionError(err) {
			atomic.AddInt32(&pm.connectionLogging.reconnectionAttempts, 1)
		}

		// 🛡️ FALLBACK DE SEGURANÇA: Log crítico após muito tempo
		if now.Sub(pm.connectionLogging.disconnectionStartTime) > CONNECTION_STATE_LOG_TIMEOUT {
			pm.logCriticalDisconnectionFallbackSecure(now)
		}
		return // SILÊNCIO - não loga durante período contínuo
	}

	// Atualizar estado
	pm.connectionLogging.lastConnectionState = connected
	pm.connectionLogging.lastErrorTime = now
}

// logCriticalDisconnectionFallbackSecure - FALLBACK para desconexões muito longas
func (pm *PLCManager) logCriticalDisconnectionFallbackSecure(now time.Time) {
	// Evitar spam do próprio fallback
	if now.Sub(pm.connectionLogging.fallbackThrottle.lastLog) < 30*time.Minute {
		return
	}

	disconnectionDuration := now.Sub(pm.connectionLogging.disconnectionStartTime)
	attempts := atomic.LoadInt32(&pm.connectionLogging.reconnectionAttempts)

	message := fmt.Sprintf("CRÍTICO: PLC desconectado há %v (%d tentativas) - verificar infraestrutura",
		pm.formatDurationSecure(disconnectionDuration), attempts)

	fmt.Printf("🚨 %s\n", message)
	if pm.systemLogger != nil {
		pm.systemLogger.LogCriticalError("PLC_MANAGER", "CRITICAL_DISCONNECTION_PROLONGED",
			errors.New(message))
	}

	pm.connectionLogging.fallbackThrottle.lastLog = now
}

// formatDurationSecure - Formatação legível de duração com proteção
func (pm *PLCManager) formatDurationSecure(d time.Duration) string {
	if d < 0 {
		return "0s"
	}

	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	days := int(d.Hours() / 24)
	hours := d.Hours() - float64(days*24)
	return fmt.Sprintf("%dd %.1fh", days, hours)
}

// ============================================================================
// 🛡️ ERROR HANDLING OTIMIZADO COM ESTADOS DE CONEXÃO
// ============================================================================

func (pm *PLCManager) markErrorSecure(err error) {
	// 🛡️ OVERFLOW PROTECTION - resetar a cada 1 milhão
	current := atomic.LoadInt32(&pm.consecutiveErrors)
	if current >= MAX_ERROR_COUNT {
		fmt.Printf("⚠️ Error count overflow protection: resetando de %d para 0\n", current)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)
		current = 0
	}

	errors := atomic.AddInt32(&pm.consecutiveErrors, 1)

	if pm.isConnectionError(err) && errors >= pm.maxErrors {
		atomic.StoreInt32(&pm.connected, 0)
		// 🛡️ USAR SISTEMA INTELIGENTE DE LOGGING
		pm.handleConnectionStateChangeSecure(false, err)
	} else if !pm.isConnectionError(err) {
		// Erros não relacionados à conexão - log normal (throttled para compatibilidade)
		pm.logErrorThrottledSecure("PLC_MANAGER", err)
	}
}

func (pm *PLCManager) markSuccess() {
	// Reset erros consecutivos em sucesso
	atomic.StoreInt32(&pm.consecutiveErrors, 0)

	// Se estava desconectado e agora teve sucesso, marca como conectado
	wasConnected := atomic.LoadInt32(&pm.connected) == 1
	if !wasConnected {
		atomic.StoreInt32(&pm.connected, 1)
		pm.handleConnectionStateChangeSecure(true, nil)
	}
}

func (pm *PLCManager) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"i/o timeout", "connection reset", "broken pipe",
		"connection refused", "invalid pdu", "invalid buffer",
		"network unreachable", "connection timed out",
	}
	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}
	return false
}

// 🛡️ LOG THROTTLING MANTIDO PARA ERROS NÃO-CONEXÃO
func (pm *PLCManager) logErrorThrottledSecure(component string, err error) {
	pm.connectionLogging.mutex.Lock()
	defer pm.connectionLogging.mutex.Unlock()

	now := time.Now()
	message := err.Error()

	if pm.connectionLogging.fallbackThrottle.lastMessage == message &&
		now.Sub(pm.connectionLogging.fallbackThrottle.lastLog) < 5*time.Minute {

		current := atomic.LoadInt32(&pm.connectionLogging.fallbackThrottle.count)
		if current >= MAX_ERROR_COUNT {
			fmt.Printf("⚠️ Fallback log count overflow protection: resetando após %d repetições\n", current)
			atomic.StoreInt32(&pm.connectionLogging.fallbackThrottle.count, 0)
			pm.connectionLogging.fallbackThrottle.lastLog = time.Time{}
			return
		}
		atomic.AddInt32(&pm.connectionLogging.fallbackThrottle.count, 1)
		return
	}

	count := atomic.LoadInt32(&pm.connectionLogging.fallbackThrottle.count)
	if count > 0 {
		if pm.systemLogger != nil {
			pm.systemLogger.LogCriticalError(component, "THROTTLED_ERROR",
				fmt.Errorf("%v (repeated %d times)", err, count))
		}
		fmt.Printf("❌ %s: %s (repetido %d vezes)\n", component, err, count)
		atomic.StoreInt32(&pm.connectionLogging.fallbackThrottle.count, 0)
	} else {
		if pm.systemLogger != nil {
			pm.systemLogger.LogCriticalError(component, "ERROR", err)
		}
		fmt.Printf("❌ %s: %v\n", component, err)
	}

	pm.connectionLogging.fallbackThrottle.lastLog = now
	pm.connectionLogging.fallbackThrottle.lastMessage = message
}

// ============================================================================
// 🛡️ TICKERS COM FALLBACK E LEAK PROTECTION
// ============================================================================

func (pm *PLCManager) initTickersSecure() error {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	if pm.tickers.allStopped {
		return fmt.Errorf("tickers already stopped")
	}

	// 🛡️ CRIAR TICKERS COM VERIFICAÇÃO
	pm.tickers.liveBit = time.NewTicker(3 * time.Second)
	pm.tickers.status = time.NewTicker(1 * time.Second)
	pm.tickers.command = time.NewTicker(2 * time.Second)
	pm.tickers.radarMonitor = time.NewTicker(8 * time.Second)

	if pm.tickers.liveBit == nil || pm.tickers.status == nil ||
		pm.tickers.command == nil || pm.tickers.radarMonitor == nil {
		return fmt.Errorf("failed to create tickers")
	}

	return nil
}

func (pm *PLCManager) stopTickersSecure() {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	if pm.tickers.allStopped {
		return
	}

	// 🛡️ PARAR TODOS COM VERIFICAÇÃO
	if pm.tickers.liveBit != nil {
		pm.tickers.liveBit.Stop()
		pm.tickers.liveBit = nil
	}
	if pm.tickers.status != nil {
		pm.tickers.status.Stop()
		pm.tickers.status = nil
	}
	if pm.tickers.command != nil {
		pm.tickers.command.Stop()
		pm.tickers.command = nil
	}
	if pm.tickers.radarMonitor != nil {
		pm.tickers.radarMonitor.Stop()
		pm.tickers.radarMonitor = nil
	}

	pm.tickers.allStopped = true
	fmt.Println("✅ Todos os tickers parados com segurança")
}

// 🛡️ GET TICKER COM FALLBACK GARANTIDO
func (pm *PLCManager) getTickerSecure(name string) <-chan time.Time {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	if pm.tickers.allStopped {
		// 🛡️ FALLBACK: criar ticker temporário
		fallbackTicker := time.NewTicker(time.Hour)
		go func() {
			time.Sleep(100 * time.Millisecond)
			fallbackTicker.Stop()
		}()
		return fallbackTicker.C
	}

	var ticker *time.Ticker
	switch name {
	case "liveBit":
		ticker = pm.tickers.liveBit
	case "status":
		ticker = pm.tickers.status
	case "command":
		ticker = pm.tickers.command
	case "radarMonitor":
		ticker = pm.tickers.radarMonitor
	}

	// 🛡️ VERIFICAÇÃO DE SEGURANÇA + FALLBACK
	if ticker == nil {
		fmt.Printf("⚠️ Ticker %s é nil, criando fallback\n", name)
		var interval time.Duration
		switch name {
		case "liveBit":
			interval = 3 * time.Second
		case "status":
			interval = 1 * time.Second
		case "command":
			interval = 2 * time.Second
		case "radarMonitor":
			interval = 8 * time.Second
		default:
			interval = 5 * time.Second
		}
		ticker = time.NewTicker(interval)
	}

	return ticker.C
}

func (pm *PLCManager) closeCommandChanSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️  Channel já fechado: %v\n", r)
		}
	}()

	// 🛡️ DRENAR CHANNEL ANTES DE FECHAR
	drainTimer := time.NewTimer(1 * time.Second)
	defer drainTimer.Stop()

DrainLoop:
	for {
		select {
		case <-pm.commandChan:
			// Drenar comandos pendentes
		case <-drainTimer.C:
			break DrainLoop
		default:
			break DrainLoop
		}
	}

	close(pm.commandChan)
}

// ============================================================================
// 🛡️ PLC OPERATIONS COM TIMEOUT OBRIGATÓRIO E VALIDAÇÃO
// ============================================================================

// 🛡️ READ TAG COM TIMEOUT E VALIDAÇÃO
func (pm *PLCManager) readTagSecure(dbNumber, byteOffset int, dataType string, bitOffset ...int) (interface{}, error) {
	if !pm.IsPLCConnected() || pm.client == nil {
		return nil, fmt.Errorf("PLC not connected")
	}

	// 🛡️ VALIDAÇÃO DE PARÂMETROS
	if dbNumber < 0 || dbNumber > 65535 {
		return nil, fmt.Errorf("invalid dbNumber %d (must be 0-65535)", dbNumber)
	}
	if byteOffset < 0 || byteOffset > 65535 {
		return nil, fmt.Errorf("invalid byteOffset %d (must be 0-65535)", byteOffset)
	}

	var size int
	switch dataType {
	case "real", "dint", "dword":
		size = 4
	case "int", "word":
		size = 2
	case "bool", "byte":
		size = 1
	default:
		return nil, fmt.Errorf("unsupported type: %s", dataType)
	}

	// 🛡️ OPERAÇÃO COM TIMEOUT OBRIGATÓRIO
	done := make(chan struct{})
	var buffer []byte
	var readErr error

	go func() {
		defer func() {
			if r := recover(); r != nil {
				readErr = fmt.Errorf("panic during read: %v", r)
			}
			close(done)
		}()
		buffer = make([]byte, size)
		readErr = pm.client.AGReadDB(dbNumber, byteOffset, size, buffer)
	}()

	select {
	case <-done:
		if readErr != nil {
			pm.markErrorSecure(readErr)
			return nil, readErr
		}
		pm.markSuccess()

	case <-time.After(PLC_OPERATION_TIMEOUT): // 🛡️ TIMEOUT 3s
		err := fmt.Errorf("PLC read timeout (%v)", PLC_OPERATION_TIMEOUT)
		pm.markErrorSecure(err)
		return nil, err
	}

	// 🛡️ CONVERTER DADOS COM VALIDAÇÃO
	switch dataType {
	case "real":
		if len(buffer) < 4 {
			return nil, fmt.Errorf("insufficient buffer for real")
		}
		result := math.Float32frombits(binary.BigEndian.Uint32(buffer))
		if math.IsNaN(float64(result)) || math.IsInf(float64(result), 0) {
			return nil, fmt.Errorf("invalid real value: NaN or Inf")
		}
		return result, nil

	case "dint":
		if len(buffer) < 4 {
			return nil, fmt.Errorf("insufficient buffer for dint")
		}
		return int32(binary.BigEndian.Uint32(buffer)), nil

	case "dword":
		if len(buffer) < 4 {
			return nil, fmt.Errorf("insufficient buffer for dword")
		}
		return binary.BigEndian.Uint32(buffer), nil

	case "int":
		if len(buffer) < 2 {
			return nil, fmt.Errorf("insufficient buffer for int")
		}
		return int16(binary.BigEndian.Uint16(buffer)), nil

	case "word":
		if len(buffer) < 2 {
			return nil, fmt.Errorf("insufficient buffer for word")
		}
		return binary.BigEndian.Uint16(buffer), nil

	case "byte":
		if len(buffer) < 1 {
			return nil, fmt.Errorf("insufficient buffer for byte")
		}
		return buffer[0], nil

	case "bool":
		if len(buffer) < 1 {
			return nil, fmt.Errorf("insufficient buffer for bool")
		}
		bit := 0
		if len(bitOffset) > 0 {
			if bitOffset[0] < 0 || bitOffset[0] > 7 {
				return nil, fmt.Errorf("invalid bit offset %d (must be 0-7)", bitOffset[0])
			}
			bit = bitOffset[0]
		}
		return ((buffer[0] >> uint(bit)) & 0x01) == 1, nil
	}

	return nil, fmt.Errorf("conversion failed for type %s", dataType)
}

// 🛡️ WRITE TAG COM TIMEOUT, VALIDAÇÃO E RANGE CHECK
func (pm *PLCManager) writeTagSecure(dbNumber, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	if !pm.IsPLCConnected() || pm.client == nil {
		return fmt.Errorf("PLC not connected")
	}

	// 🛡️ VALIDAÇÃO DE PARÂMETROS
	if dbNumber < 0 || dbNumber > 65535 {
		return fmt.Errorf("invalid dbNumber %d (must be 0-65535)", dbNumber)
	}
	if byteOffset < 0 || byteOffset > 65535 {
		return fmt.Errorf("invalid byteOffset %d (must be 0-65535)", byteOffset)
	}
	if value == nil {
		return fmt.Errorf("value cannot be nil")
	}

	// 🛡️ VALIDAÇÃO DE ENTRADA COMPLETA
	if err := pm.validateWriteValue(value, dataType); err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}

	var buffer []byte

	// 🛡️ CONVERSÃO COM RANGE CHECK
	switch dataType {
	case "real":
		buffer = make([]byte, 4)
		val := float32(0)
		switch v := value.(type) {
		case float32:
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("invalid float32 value: NaN or Inf")
			}
			val = v
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("invalid float64 value: NaN or Inf")
			}
			if v > math.MaxFloat32 || v < -math.MaxFloat32 {
				return fmt.Errorf("float64 value %.6f out of float32 range", v)
			}
			val = float32(v)
		case int:
			if v < -16777216 || v > 16777216 { // float32 safe integer range
				return fmt.Errorf("int value %d may lose precision in float32", v)
			}
			val = float32(v)
		default:
			return fmt.Errorf("unsupported value type for real: %T", value)
		}
		binary.BigEndian.PutUint32(buffer, math.Float32bits(val))

	case "dint":
		buffer = make([]byte, 4)
		val := int32(0)
		switch v := value.(type) {
		case int32:
			val = v
		case int:
			if v < math.MinInt32 || v > math.MaxInt32 {
				return fmt.Errorf("int value %d out of int32 range", v)
			}
			val = int32(v)
		case float64:
			if v < math.MinInt32 || v > math.MaxInt32 {
				return fmt.Errorf("float64 value %.0f out of int32 range", v)
			}
			val = int32(v)
		default:
			return fmt.Errorf("unsupported value type for dint: %T", value)
		}
		binary.BigEndian.PutUint32(buffer, uint32(val))

	case "int":
		buffer = make([]byte, 2)
		val := int16(0)
		switch v := value.(type) {
		case int16:
			val = v
		case int:
			if v < math.MinInt16 || v > math.MaxInt16 {
				return fmt.Errorf("int value %d out of int16 range", v)
			}
			val = int16(v)
		default:
			return fmt.Errorf("unsupported value type for int: %T", value)
		}
		binary.BigEndian.PutUint16(buffer, uint16(val))

	case "byte":
		buffer = make([]byte, 1)
		val := uint8(0)
		switch v := value.(type) {
		case uint8:
			val = v
		case int:
			if v < 0 || v > 255 {
				return fmt.Errorf("int value %d out of byte range (0-255)", v)
			}
			val = uint8(v)
		default:
			return fmt.Errorf("unsupported value type for byte: %T", value)
		}
		buffer[0] = val

	case "bool":
		buffer = make([]byte, 1)
		val := false
		switch v := value.(type) {
		case bool:
			val = v
		case int:
			val = v != 0
		default:
			return fmt.Errorf("unsupported value type for bool: %T", value)
		}

		bit := 0
		if len(bitOffset) > 0 {
			if bitOffset[0] < 0 || bitOffset[0] > 7 {
				return fmt.Errorf("invalid bit offset %d (must be 0-7)", bitOffset[0])
			}
			bit = bitOffset[0]
		}

		if val {
			buffer[0] = 1 << uint(bit)
		} else {
			buffer[0] = 0
		}

	default:
		return fmt.Errorf("unsupported type: %s", dataType)
	}

	// 🛡️ WRITE COM TIMEOUT OBRIGATÓRIO
	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during write: %v", r)
			}
		}()
		done <- pm.client.AGWriteDB(dbNumber, byteOffset, len(buffer), buffer)
	}()

	select {
	case err := <-done:
		if err != nil {
			pm.markErrorSecure(err)
			return err
		}
		pm.markSuccess()
		return nil

	case <-time.After(PLC_OPERATION_TIMEOUT): // 🛡️ TIMEOUT 3s
		err := fmt.Errorf("PLC write timeout (%v)", PLC_OPERATION_TIMEOUT)
		pm.markErrorSecure(err)
		return err
	}
}

// 🛡️ VALIDAÇÃO COMPLETA DE VALORES
func (pm *PLCManager) validateWriteValue(value interface{}, dataType string) error {
	if value == nil {
		return fmt.Errorf("value cannot be nil")
	}

	switch dataType {
	case "real":
		switch v := value.(type) {
		case float32:
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("float32 cannot be NaN or Inf")
			}
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("float64 cannot be NaN or Inf")
			}
			if v > math.MaxFloat32 || v < -math.MaxFloat32 {
				return fmt.Errorf("float64 value out of float32 range")
			}
		case int:
			if v < -16777216 || v > 16777216 {
				return fmt.Errorf("int value may lose precision in float32")
			}
		default:
			return fmt.Errorf("unsupported type for real: %T", value)
		}
	case "dint":
		if v, ok := value.(int); ok {
			if v < math.MinInt32 || v > math.MaxInt32 {
				return fmt.Errorf("int %d out of int32 range", v)
			}
		}
	case "int":
		if v, ok := value.(int); ok {
			if v < math.MinInt16 || v > math.MaxInt16 {
				return fmt.Errorf("int %d out of int16 range", v)
			}
		}
	case "byte":
		if v, ok := value.(int); ok {
			if v < 0 || v > 255 {
				return fmt.Errorf("int %d out of byte range (0-255)", v)
			}
		}
	}

	return nil
}

// ============================================================================
// 🛡️ RECONNECTION COM TIMEOUT OBRIGATÓRIO E LOGGING INTELIGENTE
// ============================================================================

func (pm *PLCManager) tryReconnectSecure() bool {
	if pm.plcSiemens == nil {
		return false
	}

	// 🛡️ RECONEXÃO COM TIMEOUT OBRIGATÓRIO
	done := make(chan bool, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("🚨 Panic na reconexão: %v\n", r)
				done <- false
			}
		}()

		pm.plcSiemens.Disconnect()
		time.Sleep(2 * time.Second)

		if err := pm.plcSiemens.Connect(); err != nil {
			// 🛡️ USAR SISTEMA INTELIGENTE - não loga aqui, só reporta erro
			pm.handleConnectionStateChangeSecure(false, err)
			done <- false
			return
		}

		pm.client = pm.plcSiemens.Client
		atomic.StoreInt32(&pm.connected, 1)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)

		// 🛡️ USAR SISTEMA INTELIGENTE - loga sucesso de reconexão
		pm.handleConnectionStateChangeSecure(true, nil)
		done <- true
	}()

	// 🛡️ TIMEOUT OBRIGATÓRIO 15s
	select {
	case success := <-done:
		return success
	case <-time.After(PLC_RECONNECT_TIMEOUT):
		err := fmt.Errorf("reconnection timeout (%v)", PLC_RECONNECT_TIMEOUT)
		pm.handleConnectionStateChangeSecure(false, err)
		return false
	}
}

// ============================================================================
// 🛡️ GOROUTINES BLINDADAS COM FALLBACK E TIMEOUT
// ============================================================================

func (pm *PLCManager) liveBitLoopSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 LiveBit panic: %v\n", r)
			if pm.systemLogger != nil {
				pm.systemLogger.LogCriticalError("PLC_MANAGER", "LIVEBIT_PANIC", fmt.Errorf("%v", r))
			}
		}
		pm.wg.Done()
	}()

	// 🛡️ FALLBACK: Se ticker falhar, usar time.Sleep
	tickerFailed := false

	for {
		select {
		case <-pm.getTickerSecure("liveBit"):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}
			current := atomic.LoadInt32(&pm.liveBit)
			atomic.StoreInt32(&pm.liveBit, 1-current)
			tickerFailed = false

		case <-pm.ctx.Done():
			return

		case <-time.After(5 * time.Second): // 🛡️ FALLBACK TIMEOUT
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}

			if tickerFailed {
				// Usar fallback direto
				current := atomic.LoadInt32(&pm.liveBit)
				atomic.StoreInt32(&pm.liveBit, 1-current)
				time.Sleep(3 * time.Second)
			} else {
				// Primeira falha do ticker
				tickerFailed = true
				fmt.Println("⚠️ LiveBit ticker falhou - usando fallback")
			}
		}
	}
}

func (pm *PLCManager) statusWriteLoopSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 StatusWrite panic: %v\n", r)
			if pm.systemLogger != nil {
				pm.systemLogger.LogCriticalError("PLC_MANAGER", "STATUS_WRITE_PANIC", fmt.Errorf("%v", r))
			}
		}
		pm.wg.Done()
	}()

	reconnectAttempts := 0
	lastReconnectTime := time.Time{}

	for {
		select {
		case <-pm.getTickerSecure("status"):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}

			connected := pm.IsPLCConnected()

			// 🛡️ RECONEXÃO COM TIMEOUT E LIMITE
			if !connected && time.Since(lastReconnectTime) > 3*time.Second {
				reconnectAttempts++
				lastReconnectTime = time.Now()

				if reconnectAttempts > 20 { // Máximo 20 tentativas
					time.Sleep(30 * time.Second)
					reconnectAttempts = 0
					continue
				}

				if pm.tryReconnectSecure() {
					reconnectAttempts = 0
					connected = true
				}
			}

			// Escrever status se conectado
			if connected && !pm.shouldSkipOperation() {
				pm.writeSystemStatusSecure()
			}

		case <-pm.ctx.Done():
			return
		}
	}
}

func (pm *PLCManager) commandReadLoopSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 CommandRead panic: %v\n", r)
			if pm.systemLogger != nil {
				pm.systemLogger.LogCriticalError("PLC_MANAGER", "COMMAND_READ_PANIC", fmt.Errorf("%v", r))
			}
		}
		pm.wg.Done()
	}()

	for {
		select {
		case <-pm.getTickerSecure("command"):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}

			if !pm.IsPLCConnected() || pm.shouldSkipOperation() {
				continue
			}

			// 🛡️ LEITURA COM TIMEOUT
			commands, err := pm.readCommandsSecure()
			if err != nil {
				continue
			}

			pm.processCommandsSecure(commands)

		case <-pm.ctx.Done():
			return
		}
	}
}

func (pm *PLCManager) commandProcessorSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 CommandProcessor panic: %v\n", r)
			if pm.systemLogger != nil {
				pm.systemLogger.LogCriticalError("PLC_MANAGER", "COMMAND_PROCESSOR_PANIC", fmt.Errorf("%v", r))
			}
		}
		pm.wg.Done()
	}()

	for {
		select {
		case cmd, ok := <-pm.commandChan:
			if !ok {
				return
			}
			if atomic.LoadInt32(&pm.isShuttingDown) == 0 {
				pm.executeCommandSecure(cmd)
			}
		case <-pm.ctx.Done():
			return
		case <-time.After(30 * time.Second): // 🛡️ TIMEOUT PARA EVITAR TRAVAMENTO
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}
			// Continue loop - é normal ficar sem comandos
		}
	}
}

func (pm *PLCManager) radarMonitorLoopSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 RadarMonitor panic: %v\n", r)
			if pm.systemLogger != nil {
				pm.systemLogger.LogCriticalError("PLC_MANAGER", "RADAR_MONITOR_PANIC", fmt.Errorf("%v", r))
			}
		}
		pm.wg.Done()
	}()

	for {
		select {
		case <-pm.getTickerSecure("radarMonitor"):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}
			pm.checkRadarTimeoutsSecure()

		case <-pm.ctx.Done():
			return
		}
	}
}

// ============================================================================
// 🛡️ OPERAÇÕES DE COMANDO E STATUS COM TIMEOUT
// ============================================================================

func (pm *PLCManager) readCommandsSecure() (*models.PLCCommands, error) {
	commands := &models.PLCCommands{}

	// 🛡️ LEITURA COM TIMEOUT INDIVIDUAL
	if val, err := pm.readTagSecure(100, 0, "bool", 0); err == nil {
		commands.StartCollection = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read StartCollection: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 1); err == nil {
		commands.StopCollection = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read StopCollection: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 2); err == nil {
		commands.Emergency = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read Emergency: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 3); err == nil {
		commands.ResetErrors = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read ResetErrors: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 4); err == nil {
		commands.EnableRadarCaldeira = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read EnableRadarCaldeira: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 5); err == nil {
		commands.EnableRadarPortaJusante = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read EnableRadarPortaJusante: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 6); err == nil {
		commands.EnableRadarPortaMontante = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read EnableRadarPortaMontante: %w", err)
	}

	if val, err := pm.readTagSecure(100, 0, "bool", 7); err == nil {
		commands.RestartRadarCaldeira = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read RestartRadarCaldeira: %w", err)
	}

	if val, err := pm.readTagSecure(100, 1, "bool", 0); err == nil {
		commands.RestartRadarPortaJusante = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read RestartRadarPortaJusante: %w", err)
	}

	if val, err := pm.readTagSecure(100, 1, "bool", 1); err == nil {
		commands.RestartRadarPortaMontante = val.(bool)
	} else {
		return nil, fmt.Errorf("failed to read RestartRadarPortaMontante: %w", err)
	}

	return commands, nil
}

func (pm *PLCManager) writeSystemStatusSecure() {
	var statusByte uint8 = 0

	if atomic.LoadInt32(&pm.liveBit) == 1 {
		statusByte |= (1 << 0)
	}
	if atomic.LoadInt32(&pm.collectionActive) == 1 {
		statusByte |= (1 << 1)
	}
	if atomic.LoadInt32(&pm.emergencyStop) == 0 {
		statusByte |= (1 << 2)
	}
	if atomic.LoadInt32(&pm.emergencyStop) == 1 {
		statusByte |= (1 << 3)
	}

	// 🛡️ RADAR STATUS COM LOCK MÍNIMO
	pm.radarMutex.RLock()
	caldeiraStatus := pm.radarCaldeiraConnected && pm.radarCaldeiraEnabled
	jusanteStatus := pm.radarPortaJusanteConnected && pm.radarPortaJusanteEnabled
	montanteStatus := pm.radarPortaMontanteConnected && pm.radarPortaMontanteEnabled
	pm.radarMutex.RUnlock()

	if caldeiraStatus {
		statusByte |= (1 << 4)
	}
	if jusanteStatus {
		statusByte |= (1 << 5)
	}
	if montanteStatus {
		statusByte |= (1 << 6)
	}

	// 🛡️ WRITE COM TIMEOUT
	pm.writeTagSecure(100, 4, "byte", statusByte)
}

// WriteMultiRadarData com timeout para 3 radares
func (pm *PLCManager) WriteMultiRadarData(data models.MultiRadarData) error {
	if !pm.IsPLCConnected() || pm.shouldSkipOperation() {
		return nil
	}

	// 🛡️ TIMEOUT GERAL DA OPERAÇÃO
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic writing radar data: %v", r)
			}
		}()

		for _, radarData := range data.Radars {
			if !pm.IsRadarEnabled(radarData.RadarID) {
				continue
			}

			pm.updateRadarStatusSecure(radarData.RadarID, radarData.Connected)

			var baseOffset int
			switch radarData.RadarID {
			case "caldeira":
				baseOffset = 6
			case "porta_jusante":
				baseOffset = 102
			case "porta_montante":
				baseOffset = 198
			default:
				continue
			}

			if err := pm.writeRadarDataSecure(radarData, baseOffset); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout writing radar data (10s)")
	}
}

func (pm *PLCManager) writeRadarDataSecure(data models.RadarData, baseOffset int) error {
	// 🛡️ VALIDAÇÃO DE OFFSET
	if baseOffset < 0 || baseOffset > 65500 {
		return fmt.Errorf("invalid baseOffset %d", baseOffset)
	}

	// ObjectDetected
	if err := pm.writeTagSecure(100, baseOffset+0, "bool", data.MainObject != nil, 0); err != nil {
		return fmt.Errorf("failed to write ObjectDetected: %w", err)
	}

	// Amplitude
	amplitude := float32(0)
	if data.MainObject != nil {
		amplitude = float32(data.MainObject.Amplitude)
	}
	if err := pm.writeTagSecure(100, baseOffset+2, "real", amplitude); err != nil {
		return fmt.Errorf("failed to write Amplitude: %w", err)
	}

	// Distance
	distance := float32(0)
	if data.MainObject != nil && data.MainObject.Distancia != nil {
		distance = float32(*data.MainObject.Distancia)
	}
	if err := pm.writeTagSecure(100, baseOffset+6, "real", distance); err != nil {
		return fmt.Errorf("failed to write Distance: %w", err)
	}

	// Velocity
	velocity := float32(0)
	if data.MainObject != nil && data.MainObject.Velocidade != nil {
		velocity = float32(*data.MainObject.Velocidade)
	}
	if err := pm.writeTagSecure(100, baseOffset+10, "real", velocity); err != nil {
		return fmt.Errorf("failed to write Velocity: %w", err)
	}

	// ObjectsCount
	count := int16(len(data.Amplitudes))
	if count > 1000 { // 🛡️ LIMITE DE SEGURANÇA
		count = 1000
	}
	if err := pm.writeTagSecure(100, baseOffset+14, "int", count); err != nil {
		return fmt.Errorf("failed to write ObjectsCount: %w", err)
	}

	// Positions Array (limitado a 10)
	for i := 0; i < 10; i++ {
		offset := baseOffset + 16 + (i * 4)
		val := float32(0)
		if i < len(data.Positions) {
			val = float32(data.Positions[i])
		}
		if err := pm.writeTagSecure(100, offset, "real", val); err != nil {
			return fmt.Errorf("failed to write Position[%d]: %w", i, err)
		}
	}

	// Velocities Array (limitado a 10)
	for i := 0; i < 10; i++ {
		offset := baseOffset + 56 + (i * 4)
		val := float32(0)
		if i < len(data.Velocities) {
			val = float32(data.Velocities[i])
		}
		if err := pm.writeTagSecure(100, offset, "real", val); err != nil {
			return fmt.Errorf("failed to write Velocity[%d]: %w", i, err)
		}
	}

	return nil
}

// ============================================================================
// 🛡️ PROCESS COMMANDS E REBOOT SYSTEM
// ============================================================================

func (pm *PLCManager) processCommandsSecure(commands *models.PLCCommands) {
	if commands == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 Panic processando comandos: %v\n", r)
		}
	}()

	// 🛡️ REBOOT LOGIC COM TIMEOUT
	pm.rebootMutex.Lock()
	lastState := pm.lastResetErrorsState
	pm.rebootMutex.Unlock()

	if commands.ResetErrors != lastState {
		if commands.ResetErrors {
			fmt.Println("🔄 DB100.0.3 ATIVADO - Timer de reboot iniciado")
			pm.startRebootTimerSecure()
		} else {
			fmt.Println("⏹️  DB100.0.3 DESATIVADO - Timer cancelado")
			pm.cancelRebootTimerSecure()
		}
		pm.rebootMutex.Lock()
		pm.lastResetErrorsState = commands.ResetErrors
		pm.rebootMutex.Unlock()
	}

	// Process commands with timeout protection
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	done := make(chan struct{}, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("🚨 Panic nos comandos: %v\n", r)
			}
			done <- struct{}{}
		}()

		if commands.StartCollection && !pm.IsCollectionActive() {
			select {
			case pm.commandChan <- models.CmdStartCollection:
				pm.writeTagSecure(100, 0, "bool", false, 0)
			default:
				fmt.Println("⚠️ Command channel full - skipping StartCollection")
			}
		}
		if commands.StopCollection && pm.IsCollectionActive() {
			select {
			case pm.commandChan <- models.CmdStopCollection:
				pm.writeTagSecure(100, 0, "bool", false, 1)
			default:
				fmt.Println("⚠️ Command channel full - skipping StopCollection")
			}
		}
		if commands.Emergency {
			select {
			case pm.commandChan <- models.CmdEmergencyStop:
				pm.writeTagSecure(100, 0, "bool", false, 2)
			default:
				fmt.Println("⚠️ Command channel full - skipping Emergency")
			}
		}

		// 🛡️ RADAR COMMANDS COM VERIFICAÇÃO
		if commands.EnableRadarCaldeira != pm.IsRadarEnabled("caldeira") {
			if commands.EnableRadarCaldeira {
				select {
				case pm.commandChan <- models.CmdEnableRadarCaldeira:
				default:
				}
			} else {
				select {
				case pm.commandChan <- models.CmdDisableRadarCaldeira:
				default:
				}
			}
		}
		if commands.EnableRadarPortaJusante != pm.IsRadarEnabled("porta_jusante") {
			if commands.EnableRadarPortaJusante {
				select {
				case pm.commandChan <- models.CmdEnableRadarPortaJusante:
				default:
				}
			} else {
				select {
				case pm.commandChan <- models.CmdDisableRadarPortaJusante:
				default:
				}
			}
		}
		if commands.EnableRadarPortaMontante != pm.IsRadarEnabled("porta_montante") {
			if commands.EnableRadarPortaMontante {
				select {
				case pm.commandChan <- models.CmdEnableRadarPortaMontante:
				default:
				}
			} else {
				select {
				case pm.commandChan <- models.CmdDisableRadarPortaMontante:
				default:
				}
			}
		}
	}()

	// 🛡️ TIMEOUT PROTECTION
	select {
	case <-done:
		// Commands processed successfully
	case <-timeout.C:
		fmt.Println("⚠️ Timeout processando comandos (5s)")
	}
}

func (pm *PLCManager) executeCommandSecure(cmd models.SystemCommand) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 Panic executando comando: %v\n", r)
		}
	}()

	switch cmd {
	case models.CmdStartCollection:
		atomic.StoreInt32(&pm.collectionActive, 1)
		atomic.StoreInt32(&pm.emergencyStop, 0)
	case models.CmdStopCollection:
		atomic.StoreInt32(&pm.collectionActive, 0)
	case models.CmdEmergencyStop:
		atomic.StoreInt32(&pm.emergencyStop, 1)
		atomic.StoreInt32(&pm.collectionActive, 0)
		if pm.systemLogger != nil {
			pm.systemLogger.LogCriticalError("PLC_MANAGER", "EMERGENCY_STOP",
				fmt.Errorf("emergency stop activated"))
		}
	case models.CmdResetErrors:
		pm.sendCleanRadarDataSecure()
	case models.CmdEnableRadarCaldeira:
		pm.radarMutex.Lock()
		pm.radarCaldeiraEnabled = true
		pm.radarMutex.Unlock()
	case models.CmdDisableRadarCaldeira:
		pm.radarMutex.Lock()
		pm.radarCaldeiraEnabled = false
		pm.radarCaldeiraConnected = false
		pm.radarMutex.Unlock()
	case models.CmdEnableRadarPortaJusante:
		pm.radarMutex.Lock()
		pm.radarPortaJusanteEnabled = true
		pm.radarMutex.Unlock()
	case models.CmdDisableRadarPortaJusante:
		pm.radarMutex.Lock()
		pm.radarPortaJusanteEnabled = false
		pm.radarPortaJusanteConnected = false
		pm.radarMutex.Unlock()
	case models.CmdEnableRadarPortaMontante:
		pm.radarMutex.Lock()
		pm.radarPortaMontanteEnabled = true
		pm.radarMutex.Unlock()
	case models.CmdDisableRadarPortaMontante:
		pm.radarMutex.Lock()
		pm.radarPortaMontanteEnabled = false
		pm.radarPortaMontanteConnected = false
		pm.radarMutex.Unlock()
	}
}

func (pm *PLCManager) sendCleanRadarDataSecure() {
	// 🛡️ CLEAN DATA PARA 3 RADARES COM TIMEOUT
	offsets := []int{6, 102, 198} // caldeira, porta_jusante, porta_montante

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, offset := range offsets {
		select {
		case <-ctx.Done():
			fmt.Println("⚠️ Timeout limpando dados dos radares")
			return
		default:
			pm.writeCleanRadarDataSecure(offset)
		}
	}
}

func (pm *PLCManager) writeCleanRadarDataSecure(baseOffset int) {
	// 🛡️ WRITE ZEROS COM TIMEOUT INDIVIDUAL
	pm.writeTagSecure(100, baseOffset+0, "bool", false, 0)
	pm.writeTagSecure(100, baseOffset+2, "real", float32(0.0))
	pm.writeTagSecure(100, baseOffset+6, "real", float32(0.0))
	pm.writeTagSecure(100, baseOffset+10, "real", float32(0.0))
	pm.writeTagSecure(100, baseOffset+14, "int", int16(0))

	for i := 0; i < 10; i++ {
		pm.writeTagSecure(100, baseOffset+16+(i*4), "real", float32(0.0)) // positions
		pm.writeTagSecure(100, baseOffset+56+(i*4), "real", float32(0.0)) // velocities
	}
}

// ============================================================================
// 🛡️ REBOOT TIMER COM TIMEOUT E PROTEÇÃO
// ============================================================================

func (pm *PLCManager) startRebootTimerSecure() {
	pm.rebootMutex.Lock()
	defer pm.rebootMutex.Unlock()

	if pm.rebootTimerActive {
		return
	}

	pm.rebootTimerActive = true
	pm.rebootTimer = time.AfterFunc(REBOOT_TIMEOUT_SECONDS*time.Second, pm.executeRebootSecure)

	if pm.systemLogger != nil {
		pm.systemLogger.LogCriticalError("PLC_MANAGER", "REBOOT_TIMER_STARTED",
			fmt.Errorf("system reboot scheduled in %d seconds", REBOOT_TIMEOUT_SECONDS))
	}
}

func (pm *PLCManager) cancelRebootTimerSecure() {
	pm.rebootMutex.Lock()
	defer pm.rebootMutex.Unlock()

	if pm.rebootTimerActive && pm.rebootTimer != nil {
		pm.rebootTimer.Stop()
		pm.rebootTimerActive = false
		fmt.Println("✅ Reboot timer cancelado")
	}
}

func (pm *PLCManager) executeRebootSecure() {
	pm.rebootMutex.Lock()
	defer pm.rebootMutex.Unlock()

	fmt.Println("🚨 EXECUTANDO REBOOT SEGURO DO SERVIDOR")
	log.Printf("SERVER_REBOOT: Full server reboot triggered by PLC (SECURE)")

	if pm.systemLogger != nil {
		pm.systemLogger.LogCriticalError("PLC_MANAGER", "SYSTEM_REBOOT_EXECUTED",
			fmt.Errorf("full server reboot executed via reset errors"))
	}

	// 🛡️ RESET PLC BIT COM TIMEOUT
	pm.writeTagSecure(100, 0, "bool", false, 3)
	time.Sleep(2 * time.Second)

	// 🛡️ TENTAR DIFERENTES COMANDOS DE REBOOT
	commands := [][]string{
		{"/bin/systemctl", "reboot"},
		{"/sbin/reboot"},
		{"/bin/sh", "-c", "sudo reboot"},
	}

	for i, cmd := range commands {
		// 🛡️ TIMEOUT NO COMANDO DE REBOOT
		done := make(chan error, 1)
		go func() {
			done <- exec.Command(cmd[0], cmd[1:]...).Run()
		}()

		select {
		case err := <-done:
			if err == nil {
				fmt.Printf("✅ Reboot executado: %v\n", cmd)
				return
			}
			fmt.Printf("❌ Tentativa %d falhou: %v\n", i+1, err)
		case <-time.After(10 * time.Second):
			fmt.Printf("⚠️ Timeout na tentativa %d de reboot\n", i+1)
		}
	}

	fmt.Println("❌ ERRO: Todas as tentativas de reboot falharam")
	pm.rebootTimerActive = false

	if pm.systemLogger != nil {
		pm.systemLogger.LogCriticalError("PLC_MANAGER", "REBOOT_FAILED",
			fmt.Errorf("all reboot attempts failed"))
	}
}

// ============================================================================
// 🛡️ RADAR MANAGEMENT PARA 3 RADARES
// ============================================================================

func (pm *PLCManager) checkRadarTimeoutsSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 Panic no radar timeout check: %v\n", r)
		}
	}()

	pm.radarMutex.Lock()
	defer pm.radarMutex.Unlock()

	now := time.Now()

	// 🛡️ CHECK RADAR CALDEIRA
	if pm.radarCaldeiraEnabled && pm.radarCaldeiraConnected {
		if now.Sub(pm.lastRadarCaldeiraUpdate) > pm.radarTimeoutDuration {
			pm.radarCaldeiraConnected = false
			fmt.Println("⚠️ Radar CALDEIRA timeout - desconectado")
			if pm.systemLogger != nil {
				pm.systemLogger.LogRadarDisconnected("caldeira", "Radar Caldeira")
			}
		}
	}

	// 🛡️ CHECK RADAR PORTA JUSANTE
	if pm.radarPortaJusanteEnabled && pm.radarPortaJusanteConnected {
		if now.Sub(pm.lastRadarPortaJusanteUpdate) > pm.radarTimeoutDuration {
			pm.radarPortaJusanteConnected = false
			fmt.Println("⚠️ Radar PORTA JUSANTE timeout - desconectado")
			if pm.systemLogger != nil {
				pm.systemLogger.LogRadarDisconnected("porta_jusante", "Radar Porta Jusante")
			}
		}
	}

	// 🛡️ CHECK RADAR PORTA MONTANTE
	if pm.radarPortaMontanteEnabled && pm.radarPortaMontanteConnected {
		if now.Sub(pm.lastRadarPortaMontanteUpdate) > pm.radarTimeoutDuration {
			pm.radarPortaMontanteConnected = false
			fmt.Println("⚠️ Radar PORTA MONTANTE timeout - desconectado")
			if pm.systemLogger != nil {
				pm.systemLogger.LogRadarDisconnected("porta_montante", "Radar Porta Montante")
			}
		}
	}
}

func (pm *PLCManager) updateRadarStatusSecure(radarID string, connected bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("🚨 Panic atualizando radar status: %v\n", r)
		}
	}()

	// 🛡️ VALIDAÇÃO DE RADAR ID
	validRadarIDs := []string{"caldeira", "porta_jusante", "porta_montante"}
	valid := false
	for _, validID := range validRadarIDs {
		if radarID == validID {
			valid = true
			break
		}
	}
	if !valid {
		fmt.Printf("⚠️ Radar ID inválido: %s\n", radarID)
		return
	}

	pm.radarMutex.Lock()
	defer pm.radarMutex.Unlock()

	now := time.Now()
	switch radarID {
	case "caldeira":
		if pm.radarCaldeiraEnabled && connected {
			pm.radarCaldeiraConnected = true
			pm.lastRadarCaldeiraUpdate = now
		}
	case "porta_jusante":
		if pm.radarPortaJusanteEnabled && connected {
			pm.radarPortaJusanteConnected = true
			pm.lastRadarPortaJusanteUpdate = now
		}
	case "porta_montante":
		if pm.radarPortaMontanteEnabled && connected {
			pm.radarPortaMontanteConnected = true
			pm.lastRadarPortaMontanteUpdate = now
		}
	}
}

func (pm *PLCManager) shouldSkipOperation() bool {
	return atomic.LoadInt32(&pm.emergencyStop) == 1
}

// ============================================================================
// 🛡️ INTERFACE PÚBLICA THREAD-SAFE (MANTIDA IGUAL PARA COMPATIBILIDADE)
// ============================================================================

func (pm *PLCManager) IsCollectionActive() bool {
	return atomic.LoadInt32(&pm.collectionActive) == 1 && atomic.LoadInt32(&pm.emergencyStop) == 0
}

func (pm *PLCManager) IsEmergencyStop() bool {
	return atomic.LoadInt32(&pm.emergencyStop) == 1
}

func (pm *PLCManager) IsRadarEnabled(radarID string) bool {
	pm.radarMutex.RLock()
	defer pm.radarMutex.RUnlock()

	switch radarID {
	case "caldeira":
		return pm.radarCaldeiraEnabled
	case "porta_jusante":
		return pm.radarPortaJusanteEnabled
	case "porta_montante":
		return pm.radarPortaMontanteEnabled
	}
	return false
}

func (pm *PLCManager) GetRadarsEnabled() map[string]bool {
	pm.radarMutex.RLock()
	defer pm.radarMutex.RUnlock()

	return map[string]bool{
		"caldeira":       pm.radarCaldeiraEnabled,
		"porta_jusante":  pm.radarPortaJusanteEnabled,
		"porta_montante": pm.radarPortaMontanteEnabled,
	}
}

func (pm *PLCManager) SetRadarsConnected(status map[string]bool) {
	for radarID, connected := range status {
		pm.updateRadarStatusSecure(radarID, connected)
	}
}

func (pm *PLCManager) IsRadarTimingOut(radarID string) (bool, time.Duration) {
	pm.radarMutex.RLock()
	defer pm.radarMutex.RUnlock()

	now := time.Now()
	var lastUpdate time.Time
	var enabled, connected bool

	switch radarID {
	case "caldeira":
		enabled, connected = pm.radarCaldeiraEnabled, pm.radarCaldeiraConnected
		lastUpdate = pm.lastRadarCaldeiraUpdate
	case "porta_jusante":
		enabled, connected = pm.radarPortaJusanteEnabled, pm.radarPortaJusanteConnected
		lastUpdate = pm.lastRadarPortaJusanteUpdate
	case "porta_montante":
		enabled, connected = pm.radarPortaMontanteEnabled, pm.radarPortaMontanteConnected
		lastUpdate = pm.lastRadarPortaMontanteUpdate
	}

	if !enabled || !connected {
		return false, 0
	}

	timeSinceUpdate := now.Sub(lastUpdate)
	warningThreshold := pm.radarTimeoutDuration * 80 / 100
	return timeSinceUpdate > warningThreshold, timeSinceUpdate
}

// COMPATIBILIDADE COM PLCCONTROLLER
func (pm *PLCManager) SetSiemensPLC(siemens *SiemensPLC) {
	// Compatibilidade - sistema interno gerencia automaticamente
}
