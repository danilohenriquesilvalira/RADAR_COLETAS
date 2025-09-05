// ============================================================================
// ARQUIVO: backend/internal/plc/plc_manager.go - FINAL CORRIGIDO DEFINITIVO
// CORREÇÕES: Race conditions, Memory leaks, Client thread-safe
// PROTEÇÕES: Timeout, Overflow, Fallback, Validação, Panic Recovery
// OTIMIZAÇÃO: Logging inteligente por estados de conexão
// CORREÇÃO RACE CONDITION: SiemensPLC thread-safe completo
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
	// Conexão PLC - THREAD-SAFE CORRIGIDO
	plcSiemens   *SiemensPLC
	client       PLCClient
	clientMutex  sync.RWMutex // NOVO: Protege pm.client
	systemLogger *logger.SystemLogger

	// ESTADOS ATÔMICOS (THREAD-SAFE)
	connected         int32 // atomic boolean (0/1)
	liveBit           int32 // atomic boolean (0/1)
	collectionActive  int32 // atomic boolean (0/1)
	emergencyStop     int32 // atomic boolean (0/1)
	isShuttingDown    int32 // atomic boolean (0/1)
	consecutiveErrors int32 // atomic counter com overflow protection

	// ESTADOS DOS 3 RADARES (MUTEX PROTEGIDO)
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
	reconnecting                 int32 // CORRIGIDO: atomic flag para evitar reconexões simultâneas

	// SISTEMA DE CONTROLE COM CLEANUP GARANTIDO
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	commandChan chan models.SystemCommand
	stopOnce    sync.Once

	// TICKERS COM FALLBACK E LEAK PROTECTION CORRIGIDO
	tickers struct {
		mutex        sync.Mutex
		liveBit      *time.Ticker
		status       *time.Ticker
		command      *time.Ticker
		radarMonitor *time.Ticker
		fallbacks    []*time.Ticker // Track fallback tickers para cleanup
		allStopped   bool
	}

	// REBOOT SYSTEM THREAD-SAFE
	rebootMutex          sync.Mutex
	rebootTimer          *time.Timer
	rebootTimerActive    bool
	lastResetErrorsState bool

	// SISTEMA DE LOGGING INTELIGENTE POR ESTADOS
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

	// ERROR HANDLING COM OVERFLOW PROTECTION
	maxErrors int32
}

const (
	REBOOT_TIMEOUT_SECONDS           = 10
	RADAR_TIMEOUT_TOLERANCE          = 45 * time.Second
	PLC_OPERATION_TIMEOUT            = 5 * time.Second
	PLC_RECONNECT_TIMEOUT            = 20 * time.Second
	MAX_ERROR_COUNT                  = 1000000
	CONNECTION_STATE_LOG_TIMEOUT     = 15 * time.Minute
	MAX_RECONNECTION_ATTEMPTS_REPORT = 10000
	MAX_FALLBACK_TICKERS             = 10 // NOVO: Limite para fallback tickers
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

	// ESTADOS INICIAIS ATÔMICOS
	atomic.StoreInt32(&pm.collectionActive, 1) // true
	atomic.StoreInt32(&pm.consecutiveErrors, 0)

	// RADARES INICIALMENTE HABILITADOS
	pm.radarMutex.Lock()
	pm.radarCaldeiraEnabled = true
	pm.radarPortaJusanteEnabled = true
	pm.radarPortaMontanteEnabled = true
	pm.radarMutex.Unlock()

	// INICIALIZAÇÃO DO SISTEMA DE LOGGING INTELIGENTE
	pm.connectionLogging.mutex.Lock()
	pm.connectionLogging.lastConnectionState = false
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

// ============================================================================
// CORREÇÃO 1: CLIENT THREAD-SAFE
// ============================================================================

// setClient - Método seguro para definir client
func (pm *PLCManager) setClient(client PLCClient) {
	pm.clientMutex.Lock()
	pm.client = client
	pm.clientMutex.Unlock()
}

// getClient - Método seguro para obter client
func (pm *PLCManager) getClient() PLCClient {
	pm.clientMutex.RLock()
	defer pm.clientMutex.RUnlock()
	return pm.client
}

// ============================================================================
// CONNECT COM TIMEOUT E ERROR HANDLING - CORRIGIDO THREAD-SAFE
// ============================================================================

func (pm *PLCManager) Connect() error {
	// NOVA PROTEÇÃO: Evitar múltiplas conexões simultâneas
	if !atomic.CompareAndSwapInt32(&pm.reconnecting, 0, 1) {
		return fmt.Errorf("connection already in progress")
	}
	defer atomic.StoreInt32(&pm.reconnecting, 0)

	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during connect: %v", r)
			}
		}()

		if err := pm.plcSiemens.Connect(); err != nil {
			done <- err
			return
		}

		// CORREÇÃO: Usar novos métodos thread-safe
		if !pm.plcSiemens.HasValidClient() {
			done <- fmt.Errorf("client validation failed after connect")
			return
		}

		// OPERAÇÃO ATÔMICA CORRIGIDA - THREAD-SAFE
		pm.setClient(pm.plcSiemens.GetClient()) // CORRIGIDO: Thread-safe
		atomic.StoreInt32(&pm.connected, 1)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)

		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			atomic.StoreInt32(&pm.connected, 0)
			pm.handleConnectionStateChangeSecure(false, err)
			return err
		}
		pm.handleConnectionStateChangeSecure(true, nil)
		return nil

	case <-time.After(PLC_RECONNECT_TIMEOUT):
		atomic.StoreInt32(&pm.connected, 0)
		err := fmt.Errorf("connection timeout (%v)", PLC_RECONNECT_TIMEOUT)
		pm.handleConnectionStateChangeSecure(false, err)
		return err
	}
}

func (pm *PLCManager) IsPLCConnected() bool {
	return atomic.LoadInt32(&pm.connected) == 1
}

// START COM PROTEÇÃO COMPLETA
func (pm *PLCManager) StartWithContext(parentCtx context.Context) {
	if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
		fmt.Println("PLC Manager já em shutdown")
		return
	}

	pm.ctx, pm.cancel = context.WithCancel(parentCtx)

	if err := pm.Connect(); err != nil {
		fmt.Printf("PLC inicial não conectou: %v\n", err)
	} else {
		fmt.Println("PLC conectado inicialmente")
	}

	if err := pm.initTickersSecure(); err != nil {
		fmt.Printf("Falha ao inicializar tickers: %v\n", err)
		return
	}

	pm.wg.Add(5)
	go pm.liveBitLoopSecure()
	go pm.statusWriteLoopSecure()
	go pm.commandReadLoopSecure()
	go pm.commandProcessorSecure()
	go pm.radarMonitorLoopSecure()

	fmt.Println("PLC Manager: Sistema BLINDADO iniciado (1 PLC + 3 Radares)")
}

func (pm *PLCManager) Start() {
	pm.StartWithContext(context.Background())
}

// STOP COM CLEANUP GARANTIDO
func (pm *PLCManager) Stop() {
	pm.stopOnce.Do(func() {
		fmt.Println("PLC Manager: Parando sistema BLINDADO...")

		atomic.StoreInt32(&pm.isShuttingDown, 1)
		pm.stopTickersSecure()
		pm.cancelRebootTimerSecure()

		if pm.cancel != nil {
			pm.cancel()
		}

		pm.closeCommandChanSecure()

		done := make(chan struct{})
		go func() {
			pm.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			fmt.Println("PLC Manager: Todas goroutines finalizadas")
		case <-time.After(10 * time.Second):
			fmt.Println("PLC Manager: Timeout - forçando parada")
		}

		if pm.plcSiemens != nil {
			pm.plcSiemens.Disconnect()
		}
		atomic.StoreInt32(&pm.isShuttingDown, 0)
		fmt.Println("PLC Manager: Sistema BLINDADO parado")
	})
}

// ============================================================================
// SISTEMA DE LOGGING INTELIGENTE POR ESTADOS DE CONEXÃO
// ============================================================================

func (pm *PLCManager) handleConnectionStateChangeSecure(connected bool, err error) {
	pm.connectionLogging.mutex.Lock()
	defer pm.connectionLogging.mutex.Unlock()

	now := time.Now()
	previousState := pm.connectionLogging.lastConnectionState

	// PRIMEIRA CONEXÃO (STARTUP) - SEM LOG DE ERRO
	if connected && !previousState && !pm.connectionLogging.isInDisconnectedPeriod {
		pm.connectionLogging.lastConnectionState = connected
		return
	}

	// MUDANÇA DE ESTADO: DESCONECTADO → CONECTADO (RECONEXÃO)
	if connected && !previousState && pm.connectionLogging.isInDisconnectedPeriod {
		disconnectionDuration := now.Sub(pm.connectionLogging.disconnectionStartTime)
		attempts := atomic.LoadInt32(&pm.connectionLogging.reconnectionAttempts)

		if attempts > MAX_RECONNECTION_ATTEMPTS_REPORT {
			attempts = MAX_RECONNECTION_ATTEMPTS_REPORT
		}

		message := fmt.Sprintf("PLC reconectado após %v - %d tentativas",
			pm.formatDurationSecure(disconnectionDuration), attempts)
		fmt.Printf("✅ %s\n", message)

		if pm.systemLogger != nil {
			pm.systemLogger.LogConfigurationChange("PLC_MANAGER", message)
		}

		pm.connectionLogging.isInDisconnectedPeriod = false
		atomic.StoreInt32(&pm.connectionLogging.reconnectionAttempts, 0)
	}

	// MUDANÇA DE ESTADO: CONECTADO → DESCONECTADO
	if !connected && previousState {
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

	// PERÍODO DE DESCONEXÃO CONTÍNUA - SILÊNCIO
	if !connected && !previousState && pm.connectionLogging.isInDisconnectedPeriod {
		if err != nil && pm.isConnectionError(err) {
			atomic.AddInt32(&pm.connectionLogging.reconnectionAttempts, 1)
		}

		if now.Sub(pm.connectionLogging.disconnectionStartTime) > CONNECTION_STATE_LOG_TIMEOUT {
			pm.logCriticalDisconnectionFallbackSecure(now)
		}
		return
	}

	pm.connectionLogging.lastConnectionState = connected
	pm.connectionLogging.lastErrorTime = now
}

func (pm *PLCManager) logCriticalDisconnectionFallbackSecure(now time.Time) {
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
// ERROR HANDLING OTIMIZADO COM ESTADOS DE CONEXÃO
// ============================================================================

func (pm *PLCManager) markErrorSecure(err error) {
	current := atomic.LoadInt32(&pm.consecutiveErrors)
	if current >= MAX_ERROR_COUNT {
		fmt.Printf("⚠️ Error count overflow protection: resetando de %d para 0\n", current)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)
		current = 0
	}

	errors := atomic.AddInt32(&pm.consecutiveErrors, 1)

	if pm.isConnectionError(err) && errors >= pm.maxErrors {
		atomic.StoreInt32(&pm.connected, 0)
		pm.handleConnectionStateChangeSecure(false, err)
	} else if !pm.isConnectionError(err) {
		pm.logErrorThrottledSecure("PLC_MANAGER", err)
	}
}

func (pm *PLCManager) markSuccess() {
	atomic.StoreInt32(&pm.consecutiveErrors, 0)

	wasConnected := atomic.LoadInt32(&pm.connected) == 1
	if !wasConnected && pm.getClient() != nil { // CORRIGIDO: Thread-safe
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
		"timeout",       // ← ADICIONAR ESTA LINHA
		"read timeout",  // ← ADICIONAR ESTA LINHA
		"write timeout", // ← ADICIONAR ESTA LINHA
	}
	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}
	return false
}

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
// CORREÇÃO 2: TICKERS COM FALLBACK E LEAK PROTECTION CORRIGIDO
// ============================================================================

func (pm *PLCManager) initTickersSecure() error {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	if pm.tickers.allStopped {
		return fmt.Errorf("tickers already stopped")
	}

	pm.tickers.liveBit = time.NewTicker(3 * time.Second)
	pm.tickers.status = time.NewTicker(1 * time.Second)
	pm.tickers.command = time.NewTicker(2 * time.Second)
	pm.tickers.radarMonitor = time.NewTicker(8 * time.Second)

	if pm.tickers.liveBit == nil || pm.tickers.status == nil ||
		pm.tickers.command == nil || pm.tickers.radarMonitor == nil {
		return fmt.Errorf("failed to create tickers")
	}

	pm.tickers.fallbacks = make([]*time.Ticker, 0)

	return nil
}

func (pm *PLCManager) stopTickersSecure() {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	if pm.tickers.allStopped {
		return
	}

	tickers := []*time.Ticker{
		pm.tickers.liveBit,
		pm.tickers.status,
		pm.tickers.command,
		pm.tickers.radarMonitor,
	}

	var wg sync.WaitGroup
	for i, ticker := range tickers {
		if ticker != nil {
			wg.Add(1)
			go func(t *time.Ticker, index int) {
				defer wg.Done()
				done := make(chan struct{})

				go func() {
					t.Stop()
					close(done)
				}()

				select {
				case <-done:
					// Sucesso
				case <-time.After(1 * time.Second):
					fmt.Printf("⚠️ Timeout stopping ticker %d\n", index)
				}
			}(ticker, i)
		}
	}

	wg.Wait()

	for _, fallback := range pm.tickers.fallbacks {
		if fallback != nil {
			go func(t *time.Ticker) {
				t.Stop()
			}(fallback)
		}
	}

	pm.tickers.liveBit = nil
	pm.tickers.status = nil
	pm.tickers.command = nil
	pm.tickers.radarMonitor = nil
	pm.tickers.fallbacks = nil
	pm.tickers.allStopped = true

	fmt.Println("✅ Todos os tickers parados com segurança")
}

func (pm *PLCManager) getTickerSecure(name string) <-chan time.Time {
	pm.tickers.mutex.Lock()

	if pm.tickers.allStopped {
		pm.tickers.mutex.Unlock()
		return pm.createFallbackTickerSecure()
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

	pm.tickers.mutex.Unlock()

	if ticker == nil {
		fmt.Printf("⚠️ Ticker %s é nil, criando fallback\n", name)
		return pm.createFallbackTickerSecure()
	}

	return ticker.C
}

// CORREÇÃO 3: FALLBACK TICKER SEM MEMORY LEAK
func (pm *PLCManager) createFallbackTickerSecure() <-chan time.Time {
	pm.tickers.mutex.Lock()
	defer pm.tickers.mutex.Unlock()

	// CORREÇÃO DO MEMORY LEAK - LIMITAR E LIMPAR TICKERS ANTIGOS
	if len(pm.tickers.fallbacks) >= MAX_FALLBACK_TICKERS {
		halfCount := len(pm.tickers.fallbacks) / 2
		for i := 0; i < halfCount; i++ {
			if pm.tickers.fallbacks[i] != nil {
				pm.tickers.fallbacks[i].Stop()
			}
		}
		pm.tickers.fallbacks = pm.tickers.fallbacks[halfCount:]
		fmt.Printf("⚠️ Limpeza de fallback tickers: removidos %d tickers antigos\n", halfCount)
	}

	fallbackTicker := time.NewTicker(5 * time.Second)
	pm.tickers.fallbacks = append(pm.tickers.fallbacks, fallbackTicker)

	go func() {
		select {
		case <-pm.ctx.Done():
			fallbackTicker.Stop()
		case <-time.After(30 * time.Second):
			fallbackTicker.Stop()
		}
	}()

	return fallbackTicker.C
}

func (pm *PLCManager) closeCommandChanSecure() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️  Channel já fechado: %v\n", r)
		}
	}()

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
// VERIFICAÇÃO DE OPERAÇÕES CORRIGIDA - THREAD-SAFE
// ============================================================================

// CORREÇÃO: Verificação de operação melhorada
func (pm *PLCManager) isOperationSafe() bool {
	if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
		return false
	}
	if atomic.LoadInt32(&pm.emergencyStop) == 1 {
		return false
	}
	if atomic.LoadInt32(&pm.connected) == 0 {
		return false
	}

	// CORREÇÃO: Usar método thread-safe do SiemensPLC
	if pm.plcSiemens != nil && !pm.plcSiemens.HasValidClient() {
		return false
	}

	// CORREÇÃO: Verificar se client está disponível de forma thread-safe
	client := pm.getClient()
	return client != nil
}

// NOVO: Método para verificar se sistema pode fazer operações PLC
func (pm *PLCManager) canPerformPLCOperations() bool {
	if !pm.isOperationSafe() {
		return false
	}

	// CORREÇÃO: Verificar se não está em processo de reconexão
	if atomic.LoadInt32(&pm.reconnecting) == 1 {
		return false
	}

	return true
}

func (pm *PLCManager) shouldSkipOperation() bool {
	return !pm.canPerformPLCOperations()
}

// ============================================================================
// PLC OPERATIONS COM TIMEOUT E CLIENT THREAD-SAFE
// ============================================================================

func (pm *PLCManager) readTagSecure(dbNumber, byteOffset int, dataType string, bitOffset ...int) (interface{}, error) {
	if !pm.canPerformPLCOperations() {
		return nil, fmt.Errorf("PLC operation not available")
	}

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
		client := pm.getClient() // CORRIGIDO: Thread-safe
		if client == nil {
			readErr = fmt.Errorf("client not available")
			return
		}
		readErr = client.AGReadDB(dbNumber, byteOffset, size, buffer)
	}()

	select {
	case <-done:
		if readErr != nil {
			pm.markErrorSecure(readErr)
			return nil, readErr
		}
		pm.markSuccess()

	case <-time.After(PLC_OPERATION_TIMEOUT):
		err := fmt.Errorf("PLC read timeout (%v)", PLC_OPERATION_TIMEOUT)
		pm.markErrorSecure(err)
		return nil, err
	}

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

func (pm *PLCManager) writeTagSecure(dbNumber, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	if !pm.canPerformPLCOperations() {
		return fmt.Errorf("PLC operation not available")
	}

	if dbNumber < 0 || dbNumber > 65535 {
		return fmt.Errorf("invalid dbNumber %d (must be 0-65535)", dbNumber)
	}
	if byteOffset < 0 || byteOffset > 65535 {
		return fmt.Errorf("invalid byteOffset %d (must be 0-65535)", byteOffset)
	}
	if value == nil {
		return fmt.Errorf("value cannot be nil")
	}

	if err := pm.validateWriteValue(value, dataType); err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}

	var buffer []byte

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
			if v < -16777216 || v > 16777216 {
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

	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic during write: %v", r)
			}
		}()

		client := pm.getClient() // CORRIGIDO: Thread-safe
		if client == nil {
			done <- fmt.Errorf("client not available")
			return
		}
		done <- client.AGWriteDB(dbNumber, byteOffset, len(buffer), buffer)
	}()

	select {
	case err := <-done:
		if err != nil {
			pm.markErrorSecure(err)
			return err
		}
		pm.markSuccess()
		return nil

	case <-time.After(PLC_OPERATION_TIMEOUT):
		err := fmt.Errorf("PLC write timeout (%v)", PLC_OPERATION_TIMEOUT)
		pm.markErrorSecure(err)
		return err
	}
}

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
// RECONNECTION COM TIMEOUT E CLIENT THREAD-SAFE
// ============================================================================

// CORREÇÃO PRINCIPAL: tryReconnectSecure COMPLETAMENTE CORRIGIDO
func (pm *PLCManager) tryReconnectSecure() bool {
	if pm.plcSiemens == nil {
		return false
	}

	// CORREÇÃO CRÍTICA: Proteção mais robusta contra reconexões simultâneas
	if !atomic.CompareAndSwapInt32(&pm.reconnecting, 0, 1) {
		// Já está reconectando - aguardar um pouco e retornar false
		time.Sleep(100 * time.Millisecond)
		return atomic.LoadInt32(&pm.connected) == 1
	}

	// GARANTIR que o flag será limpo mesmo se houver panic
	defer func() {
		atomic.StoreInt32(&pm.reconnecting, 0)
		if r := recover(); r != nil {
			fmt.Printf("Panic na reconexão: %v\n", r)
			atomic.StoreInt32(&pm.connected, 0)
		}
	}()

	// TIMEOUT para toda a operação de reconexão
	done := make(chan bool, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Panic na goroutine de reconexão: %v\n", r)
				done <- false
			}
		}()

		// CORREÇÃO: Usar método thread-safe para cleanup
		pm.plcSiemens.ForceCleanupResources()
		time.Sleep(2 * time.Second)

		if err := pm.plcSiemens.Connect(); err != nil {
			pm.handleConnectionStateChangeSecure(false, err)
			done <- false
			return
		}

		// CORREÇÃO: Validar conexão antes de continuar
		if !pm.plcSiemens.HasValidClient() {
			err := fmt.Errorf("client validation failed after reconnect")
			pm.handleConnectionStateChangeSecure(false, err)
			done <- false
			return
		}

		// OPERAÇÃO ATÔMICA CORRIGIDA - THREAD-SAFE
		pm.setClient(pm.plcSiemens.GetClient()) // CORRIGIDO: Thread-safe
		atomic.StoreInt32(&pm.connected, 1)
		atomic.StoreInt32(&pm.consecutiveErrors, 0)

		pm.handleConnectionStateChangeSecure(true, nil)
		done <- true
	}()

	select {
	case success := <-done:
		return success
	case <-time.After(PLC_RECONNECT_TIMEOUT):
		err := fmt.Errorf("reconnection timeout (%v)", PLC_RECONNECT_TIMEOUT)
		pm.handleConnectionStateChangeSecure(false, err)
		atomic.StoreInt32(&pm.connected, 0)
		return false
	case <-pm.ctx.Done():
		// Sistema está sendo desligado
		atomic.StoreInt32(&pm.connected, 0)
		return false
	}
}

// ============================================================================
// GOROUTINES BLINDADAS COM FALLBACK E TIMEOUT
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

		case <-time.After(5 * time.Second):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}

			if tickerFailed {
				current := atomic.LoadInt32(&pm.liveBit)
				atomic.StoreInt32(&pm.liveBit, 1-current)
				time.Sleep(3 * time.Second)
			} else {
				tickerFailed = true
				fmt.Println("⚠️ LiveBit ticker falhou - usando fallback")
			}
		}
	}
}

// CORREÇÃO: statusWriteLoopSecure com proteção melhorada
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

	// CORREÇÃO: Usar mutex para proteger variáveis compartilhadas
	var reconnectMutex sync.Mutex
	reconnectAttempts := 0
	lastReconnectTime := time.Time{}
	reconnectThrottle := 3 * time.Second

	for {
		select {
		case <-pm.getTickerSecure("status"):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}

			connected := pm.IsPLCConnected()

			// CORREÇÃO: Proteger acesso às variáveis com mutex
			reconnectMutex.Lock()
			canTryReconnect := !connected && time.Since(lastReconnectTime) > reconnectThrottle
			isReconnecting := atomic.LoadInt32(&pm.reconnecting) == 1
			reconnectMutex.Unlock()

			if canTryReconnect && !isReconnecting {

				// CORREÇÃO: Atualizar variáveis de forma thread-safe
				reconnectMutex.Lock()
				reconnectAttempts++
				lastReconnectTime = time.Now()

				// Throttling progressivo
				if reconnectAttempts > 10 {
					reconnectThrottle = 30 * time.Second
				} else if reconnectAttempts > 5 {
					reconnectThrottle = 10 * time.Second
				} else {
					reconnectThrottle = 3 * time.Second
				}
				reconnectMutex.Unlock()

				// CORREÇÃO: Fazer reconexão SEM goroutine para evitar race
				if pm.tryReconnectSecure() {
					// Reset counter apenas se reconexão foi bem-sucedida
					reconnectMutex.Lock()
					reconnectAttempts = 0
					reconnectThrottle = 3 * time.Second
					reconnectMutex.Unlock()
				}
			} else if connected {
				// Reset counter quando conexão está OK
				reconnectMutex.Lock()
				reconnectAttempts = 0
				reconnectThrottle = 3 * time.Second
				reconnectMutex.Unlock()
			}

			// Escrever status apenas se conectado e operacional
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

			if pm.shouldSkipOperation() {
				continue
			}

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
		case <-time.After(30 * time.Second):
			if atomic.LoadInt32(&pm.isShuttingDown) == 1 {
				return
			}
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
// OPERAÇÕES DE COMANDO E STATUS
// ============================================================================

func (pm *PLCManager) readCommandsSecure() (*models.PLCCommands, error) {
	commands := &models.PLCCommands{}

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

	pm.writeTagSecure(100, 4, "byte", statusByte)
}

func (pm *PLCManager) WriteMultiRadarData(data models.MultiRadarData) error {
	if pm.shouldSkipOperation() {
		return nil
	}

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
	if baseOffset < 0 || baseOffset > 65500 {
		return fmt.Errorf("invalid baseOffset %d", baseOffset)
	}

	if err := pm.writeTagSecure(100, baseOffset+0, "bool", data.MainObject != nil, 0); err != nil {
		return fmt.Errorf("failed to write ObjectDetected: %w", err)
	}

	amplitude := float32(0)
	if data.MainObject != nil {
		amplitude = float32(data.MainObject.Amplitude)
	}
	if err := pm.writeTagSecure(100, baseOffset+2, "real", amplitude); err != nil {
		return fmt.Errorf("failed to write Amplitude: %w", err)
	}

	distance := float32(0)
	if data.MainObject != nil && data.MainObject.Distancia != nil {
		distance = float32(*data.MainObject.Distancia)
	}
	if err := pm.writeTagSecure(100, baseOffset+6, "real", distance); err != nil {
		return fmt.Errorf("failed to write Distance: %w", err)
	}

	velocity := float32(0)
	if data.MainObject != nil && data.MainObject.Velocidade != nil {
		velocity = float32(*data.MainObject.Velocidade)
	}
	if err := pm.writeTagSecure(100, baseOffset+10, "real", velocity); err != nil {
		return fmt.Errorf("failed to write Velocity: %w", err)
	}

	count := int16(len(data.Amplitudes))
	if count > 1000 {
		count = 1000
	}
	if err := pm.writeTagSecure(100, baseOffset+14, "int", count); err != nil {
		return fmt.Errorf("failed to write ObjectsCount: %w", err)
	}

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
// PROCESS COMMANDS E REBOOT SYSTEM
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

	pm.rebootMutex.Lock()
	lastState := pm.lastResetErrorsState
	pm.rebootMutex.Unlock()

	if commands.ResetErrors != lastState {
		if commands.ResetErrors {
			fmt.Println("🔄 DB100.0.3 ATIVADO - Timer de reboot iniciado")
			pm.startRebootTimerSecure()
		} else {
			fmt.Println("DB100.0.3 DESATIVADO - Timer cancelado")
			pm.cancelRebootTimerSecure()
		}
		pm.rebootMutex.Lock()
		pm.lastResetErrorsState = commands.ResetErrors
		pm.rebootMutex.Unlock()
	}

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
// REBOOT TIMER COM TIMEOUT E PROTEÇÃO
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

	pm.writeTagSecure(100, 0, "bool", false, 3)
	time.Sleep(2 * time.Second)

	commands := [][]string{
		{"/bin/systemctl", "reboot"},
		{"/sbin/reboot"},
		{"/bin/sh", "-c", "sudo reboot"},
	}

	for i, cmd := range commands {
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
// RADAR MANAGEMENT PARA 3 RADARES
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

	if pm.radarCaldeiraEnabled && pm.radarCaldeiraConnected {
		if now.Sub(pm.lastRadarCaldeiraUpdate) > pm.radarTimeoutDuration {
			pm.radarCaldeiraConnected = false
			fmt.Println("⚠️ Radar CALDEIRA timeout - desconectado")
			if pm.systemLogger != nil {
				pm.systemLogger.LogRadarDisconnected("caldeira", "Radar Caldeira")
			}
		}
	}

	if pm.radarPortaJusanteEnabled && pm.radarPortaJusanteConnected {
		if now.Sub(pm.lastRadarPortaJusanteUpdate) > pm.radarTimeoutDuration {
			pm.radarPortaJusanteConnected = false
			fmt.Println("⚠️ Radar PORTA JUSANTE timeout - desconectado")
			if pm.systemLogger != nil {
				pm.systemLogger.LogRadarDisconnected("porta_jusante", "Radar Porta Jusante")
			}
		}
	}

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

// ============================================================================
// INTERFACE PÚBLICA THREAD-SAFE
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

func (pm *PLCManager) SetSiemensPLC(siemens *SiemensPLC) {
	// Compatibilidade - sistema interno gerencia automaticamente
}
