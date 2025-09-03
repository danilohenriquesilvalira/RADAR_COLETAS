package plc

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"backend/internal/logger"
	"backend/pkg/models"
)

type PLCController struct {
	plc    PLCClient
	reader *PLCReader
	writer *PLCWriter

	// ADICIONAR LOGGER SIMPLES
	systemLogger *logger.SystemLogger

	// PLC Connection
	plcSiemens *SiemensPLC

	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	commandChan chan models.SystemCommand

	stateMutex sync.RWMutex
	radarMutex sync.RWMutex
	timerMutex sync.Mutex

	// Estados essenciais
	liveBit          bool
	collectionActive bool
	emergencyStop    bool
	plcConnected     bool

	// Radares
	radarCaldeiraEnabled         bool
	radarPortaJusanteEnabled     bool
	radarPortaMontanteEnabled    bool
	radarCaldeiraConnected       bool
	radarPortaJusanteConnected   bool
	radarPortaMontanteConnected  bool
	lastRadarCaldeiraUpdate      time.Time
	lastRadarPortaJusanteUpdate  time.Time
	lastRadarPortaMontanteUpdate time.Time

	// Tickers
	liveBitTicker      *time.Ticker
	statusTicker       *time.Ticker
	commandTicker      *time.Ticker
	radarMonitorTicker *time.Ticker

	// Reboot system
	rebootTimer          *time.Timer
	rebootTimerActive    bool
	lastResetErrorsState bool
	rebootStartTime      time.Time

	// Timeout config
	radarTimeoutDuration time.Duration

	// ✅ CONTROLE DE LOG THROTTLING - ANTI SPAM
	logThrottleMutex         sync.Mutex
	lastLoggedReconnectError time.Time
	lastLoggedErrorMessage   string
	reconnectErrorCount      int
	logThrottleDuration      time.Duration
}

const (
	REBOOT_TIMEOUT_SECONDS    = 10
	REBOOT_CONFIRMATION_DELAY = 2 * time.Second
	RADAR_TIMEOUT_TOLERANCE   = 45 * time.Second
)

func NewPLCController(plcClient PLCClient) *PLCController {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())

	return &PLCController{
		plc:              plcClient,
		plcSiemens:       nil,
		systemLogger:     nil,
		ctx:              ctx,
		cancel:           cancel,
		reader:           NewPLCReader(plcClient),
		writer:           NewPLCWriter(plcClient),
		commandChan:      make(chan models.SystemCommand, 20),
		collectionActive: true,
		plcConnected:     true,

		radarCaldeiraEnabled:         true,
		radarPortaJusanteEnabled:     true,
		radarPortaMontanteEnabled:    true,
		lastRadarCaldeiraUpdate:      now,
		lastRadarPortaJusanteUpdate:  now,
		lastRadarPortaMontanteUpdate: now,
		radarTimeoutDuration:         RADAR_TIMEOUT_TOLERANCE,

		// ✅ INICIALIZAR THROTTLING - LOG A CADA 5 MINUTOS
		logThrottleDuration:      5 * time.Minute,
		lastLoggedReconnectError: time.Time{},
		reconnectErrorCount:      0,
		lastLoggedErrorMessage:   "",
	}
}

// MÉTODO SIMPLES PARA DEFINIR LOGGER
func (pc *PLCController) SetSystemLogger(logger *logger.SystemLogger) {
	pc.systemLogger = logger
}

func (pc *PLCController) SetSiemensPLC(siemens *SiemensPLC) {
	pc.plcSiemens = siemens
}

// ✅ MÉTODO PARA LOG INTELIGENTE - ANTI SPAM
func (pc *PLCController) logReconnectErrorThrottled(operation string, err error) {
	pc.logThrottleMutex.Lock()
	defer pc.logThrottleMutex.Unlock()

	now := time.Now()
	errorMessage := err.Error()

	// Se é o mesmo erro e ainda está no período de throttle
	if pc.lastLoggedErrorMessage == errorMessage &&
		now.Sub(pc.lastLoggedReconnectError) < pc.logThrottleDuration {
		pc.reconnectErrorCount++
		return // ❌ NÃO LOGAR - EVITAR SPAM
	}

	// ✅ LOGAR com contador se houve repetições
	if pc.reconnectErrorCount > 0 {
		if pc.systemLogger != nil {
			pc.systemLogger.LogCriticalError("PLC_CONTROLLER", operation,
				fmt.Errorf("%v (repeated %d times in %v)",
					err, pc.reconnectErrorCount, now.Sub(pc.lastLoggedReconnectError)))
		}
		fmt.Printf("🔥 PLC: %s (repetido %d vezes nos últimos %v)\n",
			err, pc.reconnectErrorCount, now.Sub(pc.lastLoggedReconnectError))
	} else {
		// Primeiro erro ou erro diferente
		if pc.systemLogger != nil {
			pc.systemLogger.LogCriticalError("PLC_CONTROLLER", operation, err)
		}
	}

	// ✅ RESETAR CONTADORES
	pc.lastLoggedReconnectError = now
	pc.lastLoggedErrorMessage = errorMessage
	pc.reconnectErrorCount = 0
}

// ✅ RESETAR THROTTLING QUANDO RECONECTAR COM SUCESSO
func (pc *PLCController) resetLogThrottling() {
	pc.logThrottleMutex.Lock()
	defer pc.logThrottleMutex.Unlock()

	if pc.reconnectErrorCount > 0 {
		fmt.Printf("✅ PLC: Reconectou após %d tentativas falhas\n", pc.reconnectErrorCount)
	}

	pc.reconnectErrorCount = 0
	pc.lastLoggedReconnectError = time.Time{}
	pc.lastLoggedErrorMessage = ""
}

func (pc *PLCController) StartWithContext(parentCtx context.Context) {
	pc.ctx, pc.cancel = context.WithCancel(parentCtx)

	pc.liveBitTicker = time.NewTicker(3 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(2 * time.Second)
	pc.radarMonitorTicker = time.NewTicker(8 * time.Second)

	pc.wg.Add(5)
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.radarMonitorLoop()

	fmt.Println("PLC Controller: Sistema iniciado com reconexão automática")
}

func (pc *PLCController) Start() {
	pc.StartWithContext(context.Background())
}

func (pc *PLCController) Stop() {
	fmt.Println("PLC Controller: Iniciando parada...")

	pc.cancelRebootTimer()
	pc.cancel()

	if pc.liveBitTicker != nil {
		pc.liveBitTicker.Stop()
	}
	if pc.statusTicker != nil {
		pc.statusTicker.Stop()
	}
	if pc.commandTicker != nil {
		pc.commandTicker.Stop()
	}
	if pc.radarMonitorTicker != nil {
		pc.radarMonitorTicker.Stop()
	}

	close(pc.commandChan)

	done := make(chan struct{})
	go func() {
		pc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("PLC Controller: Parado com sucesso")
	case <-time.After(10 * time.Second):
		fmt.Println("PLC Controller: Timeout - forçando parada")
		if pc.systemLogger != nil {
			pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "SHUTDOWN_TIMEOUT", fmt.Errorf("shutdown timeout after 10s"))
		}
	}
}

func (pc *PLCController) testPLCConnection() error {
	if pc.plc == nil {
		return fmt.Errorf("PLC client is nil")
	}

	buffer := make([]byte, 1)
	return pc.plc.AGReadDB(100, 0, 1, buffer)
}

// ✅ FORÇAR RECONEXÃO COM LOG THROTTLED
func (pc *PLCController) forceReconnect() bool {
	fmt.Println("PLC: Tentando reconexão completa...")

	// 1. Desconectar completamente
	if pc.plcSiemens != nil {
		pc.plcSiemens.Disconnect()
		time.Sleep(2 * time.Second)
	}

	// 2. Tentar reconectar do zero
	if pc.plcSiemens != nil {
		err := pc.plcSiemens.Connect()
		if err != nil {
			fmt.Printf("PLC: Falha na reconexão - %v\n", err)
			// ✅ USAR LOG THROTTLED - ANTI SPAM
			pc.logReconnectErrorThrottled("FORCE_RECONNECTION_FAILED", err)
			return false
		}

		// 3. Atualizar referências
		pc.plc = pc.plcSiemens.Client
		pc.reader = NewPLCReader(pc.plc)
		pc.writer = NewPLCWriter(pc.plc)
		pc.writer.ResetErrorState()

		// 4. Testar leitura
		if err := pc.testPLCConnection(); err != nil {
			fmt.Printf("PLC: Teste pós-reconexão falhou - %v\n", err)
			// ✅ USAR LOG THROTTLED - ANTI SPAM
			pc.logReconnectErrorThrottled("POST_RECONNECTION_TEST_FAILED", err)
			return false
		}

		fmt.Println("PLC: Reconexão completa realizada com sucesso")
		// ✅ RESETAR THROTTLING AO RECONECTAR COM SUCESSO
		pc.resetLogThrottling()
		return true
	}

	return false
}

// LiveBit loop
func (pc *PLCController) liveBitLoop() {
	defer pc.wg.Done()
	for {
		select {
		case <-pc.liveBitTicker.C:
			pc.stateMutex.Lock()
			pc.liveBit = !pc.liveBit
			pc.stateMutex.Unlock()
		case <-pc.ctx.Done():
			return
		}
	}
}

func (pc *PLCController) statusWriteLoop() {
	defer pc.wg.Done()

	operationsActive := false
	reconnectAttempts := 0
	lastReconnectTime := time.Time{}

	for {
		select {
		case <-pc.statusTicker.C:
			pc.stateMutex.RLock()
			plcConnected := pc.plcConnected
			pc.stateMutex.RUnlock()

			if !plcConnected {
				if operationsActive {
					fmt.Println("StatusWrite: PLC DESCONECTADO - parando operações")
					operationsActive = false
					reconnectAttempts = 0
				}

				// Tentar reconectar a cada 3 segundos
				if time.Since(lastReconnectTime) > 3*time.Second {
					reconnectAttempts++
					lastReconnectTime = time.Now()

					fmt.Printf("StatusWrite: Tentativa reconexão #%d\n", reconnectAttempts)

					var reconnected bool

					// SEMPRE fazer reconexão completa
					if reconnectAttempts%2 == 0 {
						fmt.Println("StatusWrite: FORÇANDO reconexão completa")
						reconnected = pc.forceReconnect()
					} else {
						// Teste simples primeiro
						reconnected = pc.testPLCConnection() == nil
					}

					if reconnected {
						pc.stateMutex.Lock()
						pc.plcConnected = true
						pc.stateMutex.Unlock()
						fmt.Println("StatusWrite: PLC RECONECTADO - retomando operações")
						// ✅ RESETAR THROTTLING QUANDO RECONECTAR
						pc.resetLogThrottling()
						operationsActive = true
						reconnectAttempts = 0
					} else if reconnectAttempts > 10 {
						// Reset após muitas tentativas
						fmt.Println("StatusWrite: Muitas tentativas - resetando contador")
						reconnectAttempts = 0
						time.Sleep(5 * time.Second)
					}
				}
				continue
			}

			if !operationsActive {
				operationsActive = true
				reconnectAttempts = 0
			}

			if pc.shouldSkipOperation() {
				continue
			}

			if err := pc.writeSystemStatus(); err != nil {
				if pc.isConnectionError(err) {
					pc.stateMutex.Lock()
					pc.plcConnected = false
					pc.stateMutex.Unlock()
					fmt.Println("StatusWrite: PLC desconectou - parando operações")

					if pc.systemLogger != nil {
						pc.systemLogger.LogPLCDisconnected(0, err)
					}

					operationsActive = false
				}
			}

		case <-pc.ctx.Done():
			return
		}
	}
}

func (pc *PLCController) commandReadLoop() {
	defer pc.wg.Done()

	operationsActive := false
	reconnectAttempts := 0
	lastReconnectTime := time.Time{}

	for {
		select {
		case <-pc.commandTicker.C:
			pc.stateMutex.RLock()
			plcConnected := pc.plcConnected
			pc.stateMutex.RUnlock()

			if !plcConnected {
				if operationsActive {
					fmt.Println("CommandRead: PLC DESCONECTADO - parando operações")
					operationsActive = false
					reconnectAttempts = 0
				}

				// Tentar reconectar a cada 3 segundos
				if time.Since(lastReconnectTime) > 3*time.Second {
					reconnectAttempts++
					lastReconnectTime = time.Now()

					fmt.Printf("CommandRead: Tentativa reconexão #%d\n", reconnectAttempts)

					var reconnected bool

					// SEMPRE fazer reconexão completa
					if reconnectAttempts%2 == 0 {
						fmt.Println("CommandRead: FORÇANDO reconexão completa")
						reconnected = pc.forceReconnect()
					} else {
						// Teste simples primeiro
						reconnected = pc.testPLCConnection() == nil
					}

					if reconnected {
						pc.stateMutex.Lock()
						pc.plcConnected = true
						pc.stateMutex.Unlock()
						fmt.Println("CommandRead: PLC RECONECTADO - retomando operações")
						// ✅ RESETAR THROTTLING QUANDO RECONECTAR
						pc.resetLogThrottling()
						operationsActive = true
						reconnectAttempts = 0
					} else if reconnectAttempts > 10 {
						// Reset após muitas tentativas
						fmt.Println("CommandRead: Muitas tentativas - resetando contador")
						reconnectAttempts = 0
						time.Sleep(5 * time.Second)
					}
				}
				continue
			}

			if !operationsActive {
				operationsActive = true
				reconnectAttempts = 0
			}

			if pc.shouldSkipOperation() {
				continue
			}

			commands, err := pc.reader.ReadCommands()
			if err != nil {
				if pc.isConnectionError(err) {
					pc.stateMutex.Lock()
					pc.plcConnected = false
					pc.stateMutex.Unlock()
					fmt.Println("CommandRead: PLC desconectou - parando operações")

					if pc.systemLogger != nil {
						pc.systemLogger.LogPLCDisconnected(0, err)
					}

					operationsActive = false
				}
				continue
			}

			pc.processCommands(commands)

		case <-pc.ctx.Done():
			return
		}
	}
}

// Command processor
func (pc *PLCController) commandProcessor() {
	defer pc.wg.Done()
	for {
		select {
		case cmd, ok := <-pc.commandChan:
			if !ok {
				return
			}
			pc.executeCommand(cmd)
		case <-pc.ctx.Done():
			return
		}
	}
}

// Radar monitor
func (pc *PLCController) radarMonitorLoop() {
	defer pc.wg.Done()
	for {
		select {
		case <-pc.radarMonitorTicker.C:
			pc.checkRadarTimeouts()
		case <-pc.ctx.Done():
			return
		}
	}
}

// ✅ PROCESS COMMANDS - LOG APENAS EVENTOS CRÍTICOS
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	if commands == nil {
		return
	}

	// ✅ REBOOT LOGIC - LOG APENAS O ESSENCIAL
	pc.timerMutex.Lock()
	lastState := pc.lastResetErrorsState
	pc.timerMutex.Unlock()

	if commands.ResetErrors != lastState {
		if commands.ResetErrors {
			fmt.Println("DB100.0.3 ATIVADO - Timer de reboot iniciado")

			if pc.systemLogger != nil {
				pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "RESET_ERRORS_REBOOT_INITIATED",
					fmt.Errorf("system reboot will execute in %d seconds", REBOOT_TIMEOUT_SECONDS))
			}

			pc.startRebootTimer()
		} else {
			fmt.Println("DB100.0.3 DESATIVADO - Timer cancelado")
			pc.cancelRebootTimer()
		}
		pc.timerMutex.Lock()
		pc.lastResetErrorsState = commands.ResetErrors
		pc.timerMutex.Unlock()
	}

	// Process other commands
	if commands.StartCollection && !pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStartCollection
		pc.writer.ResetCommand(0, 0)
	}
	if commands.StopCollection && pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStopCollection
		pc.writer.ResetCommand(0, 1)
	}
	if commands.Emergency {
		if pc.systemLogger != nil {
			pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "EMERGENCY_STOP_ACTIVATED",
				fmt.Errorf("emergency stop command received from PLC"))
		}
		pc.commandChan <- models.CmdEmergencyStop
		pc.writer.ResetCommand(0, 2)
	}

	// Radar enable/disable commands
	if commands.EnableRadarCaldeira != pc.IsRadarEnabled("caldeira") {
		if commands.EnableRadarCaldeira {
			pc.commandChan <- models.CmdEnableRadarCaldeira
		} else {
			pc.commandChan <- models.CmdDisableRadarCaldeira
		}
	}
	if commands.EnableRadarPortaJusante != pc.IsRadarEnabled("porta_jusante") {
		if commands.EnableRadarPortaJusante {
			pc.commandChan <- models.CmdEnableRadarPortaJusante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaJusante
		}
	}
	if commands.EnableRadarPortaMontante != pc.IsRadarEnabled("porta_montante") {
		if commands.EnableRadarPortaMontante {
			pc.commandChan <- models.CmdEnableRadarPortaMontante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaMontante
		}
	}
}

// Execute command
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	switch cmd {
	case models.CmdStartCollection:
		pc.collectionActive = true
		pc.emergencyStop = false
	case models.CmdStopCollection:
		pc.collectionActive = false
	case models.CmdEmergencyStop:
		pc.emergencyStop = true
		pc.collectionActive = false
		if pc.systemLogger != nil {
			pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "EMERGENCY_STOP_EXECUTED",
				fmt.Errorf("emergency stop activated - all operations halted"))
		}
	case models.CmdResetErrors:
		pc.writer.ResetErrorState()
		pc.sendCleanRadarData()
	case models.CmdEnableRadarCaldeira:
		pc.radarCaldeiraEnabled = true
	case models.CmdDisableRadarCaldeira:
		pc.radarCaldeiraEnabled = false
		pc.radarCaldeiraConnected = false
	case models.CmdEnableRadarPortaJusante:
		pc.radarPortaJusanteEnabled = true
	case models.CmdDisableRadarPortaJusante:
		pc.radarPortaJusanteEnabled = false
		pc.radarPortaJusanteConnected = false
	case models.CmdEnableRadarPortaMontante:
		pc.radarPortaMontanteEnabled = true
	case models.CmdDisableRadarPortaMontante:
		pc.radarPortaMontanteEnabled = false
		pc.radarPortaMontanteConnected = false
	}
}

// Write system status
func (pc *PLCController) writeSystemStatus() error {
	pc.stateMutex.RLock()
	status := &models.PLCSystemStatus{
		LiveBit:                     pc.liveBit,
		CollectionActive:            pc.collectionActive,
		SystemHealthy:               !pc.emergencyStop,
		EmergencyActive:             pc.emergencyStop,
		RadarCaldeiraConnected:      pc.radarCaldeiraConnected && pc.radarCaldeiraEnabled,
		RadarPortaJusanteConnected:  pc.radarPortaJusanteConnected && pc.radarPortaJusanteEnabled,
		RadarPortaMontanteConnected: pc.radarPortaMontanteConnected && pc.radarPortaMontanteEnabled,
	}
	pc.stateMutex.RUnlock()

	return pc.writer.WriteSystemStatus(status)
}

// Write radar data
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	pc.stateMutex.RLock()
	plcConnected := pc.plcConnected
	pc.stateMutex.RUnlock()

	if !plcConnected {
		return nil // Skip silently when PLC disconnected
	}

	if pc.shouldSkipOperation() {
		return nil
	}

	// Quick connection test before writing
	if err := pc.testPLCConnection(); err != nil {
		pc.stateMutex.Lock()
		pc.plcConnected = false
		pc.stateMutex.Unlock()
		return err // Retornar erro para main.go saber
	}

	for _, radarData := range data.Radars {
		if !pc.IsRadarEnabled(radarData.RadarID) {
			continue
		}

		pc.updateRadarStatus(radarData.RadarID, radarData.Connected)
		plcData := pc.writer.BuildPLCRadarData(radarData)

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

		if err := pc.writer.WriteRadarDataToDB100(plcData, baseOffset); err != nil {
			if pc.isConnectionError(err) {
				pc.stateMutex.Lock()
				pc.plcConnected = false
				pc.stateMutex.Unlock()
				return err // Retornar erro para main.go saber
			}
		}
	}
	return nil
}

// Clean radar data
func (pc *PLCController) sendCleanRadarData() {
	offsets := []int{6, 102, 198} // caldeira, porta_jusante, porta_montante
	for _, offset := range offsets {
		pc.writer.WriteRadarSickCleanDataToDB100(offset)
	}
}

// ✅ CHECK RADAR TIMEOUTS - LOG APENAS FALHAS
func (pc *PLCController) checkRadarTimeouts() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	if pc.radarCaldeiraEnabled && pc.radarCaldeiraConnected {
		if now.Sub(pc.lastRadarCaldeiraUpdate) > pc.radarTimeoutDuration {
			pc.radarCaldeiraConnected = false
			fmt.Println("Radar CALDEIRA timeout - desconectado")

			if pc.systemLogger != nil {
				pc.systemLogger.LogRadarDisconnected("caldeira", "Radar Caldeira")
			}
		}
	}

	if pc.radarPortaJusanteEnabled && pc.radarPortaJusanteConnected {
		if now.Sub(pc.lastRadarPortaJusanteUpdate) > pc.radarTimeoutDuration {
			pc.radarPortaJusanteConnected = false
			fmt.Println("Radar PORTA JUSANTE timeout - desconectado")

			if pc.systemLogger != nil {
				pc.systemLogger.LogRadarDisconnected("porta_jusante", "Radar Porta Jusante")
			}
		}
	}

	if pc.radarPortaMontanteEnabled && pc.radarPortaMontanteConnected {
		if now.Sub(pc.lastRadarPortaMontanteUpdate) > pc.radarTimeoutDuration {
			pc.radarPortaMontanteConnected = false
			fmt.Println("Radar PORTA MONTANTE timeout - desconectado")

			if pc.systemLogger != nil {
				pc.systemLogger.LogRadarDisconnected("porta_montante", "Radar Porta Montante")
			}
		}
	}
}

// Update radar status
func (pc *PLCController) updateRadarStatus(radarID string, connected bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()
	switch radarID {
	case "caldeira":
		if pc.radarCaldeiraEnabled && connected {
			pc.radarCaldeiraConnected = true
			pc.lastRadarCaldeiraUpdate = now
		}
	case "porta_jusante":
		if pc.radarPortaJusanteEnabled && connected {
			pc.radarPortaJusanteConnected = true
			pc.lastRadarPortaJusanteUpdate = now
		}
	case "porta_montante":
		if pc.radarPortaMontanteEnabled && connected {
			pc.radarPortaMontanteConnected = true
			pc.lastRadarPortaMontanteUpdate = now
		}
	}
}

// ✅ REBOOT TIMER - LOG APENAS EVENTOS CRÍTICOS
func (pc *PLCController) startRebootTimer() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	if pc.rebootTimerActive {
		return
	}

	pc.rebootStartTime = time.Now()
	pc.rebootTimerActive = true
	pc.rebootTimer = time.AfterFunc(REBOOT_TIMEOUT_SECONDS*time.Second, pc.executeReboot)
}

func (pc *PLCController) cancelRebootTimer() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	if pc.rebootTimerActive && pc.rebootTimer != nil {
		pc.rebootTimer.Stop()
		pc.rebootTimerActive = false
		fmt.Println("Reboot cancelado")
	}
}

func (pc *PLCController) executeReboot() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	fmt.Println("EXECUTANDO REBOOT DO SERVIDOR")
	log.Printf("SERVER_REBOOT: Full server reboot triggered by PLC")

	if pc.systemLogger != nil {
		pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "SYSTEM_REBOOT_EXECUTED",
			fmt.Errorf("full server reboot executed - reset errors button"))
	}

	// Reset PLC bit first
	pc.writer.ResetCommand(0, 3)
	time.Sleep(REBOOT_CONFIRMATION_DELAY)

	// Try different reboot commands
	commands := [][]string{
		{"/bin/systemctl", "reboot"},
		{"/sbin/reboot"},
		{"/bin/sh", "-c", "reboot"},
	}

	for _, cmd := range commands {
		if err := exec.Command(cmd[0], cmd[1:]...).Run(); err == nil {
			fmt.Printf("Reboot executado: %v\n", cmd)
			return
		}
	}

	fmt.Println("ERRO: Todas as tentativas de reboot falharam")
	pc.rebootTimerActive = false

	if pc.systemLogger != nil {
		pc.systemLogger.LogCriticalError("PLC_CONTROLLER", "REBOOT_FAILED",
			fmt.Errorf("all reboot attempts failed"))
	}
}

// Helper functions
func (pc *PLCController) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"i/o timeout", "connection reset", "broken pipe",
		"connection refused", "network unreachable", "no route to host",
		"invalid pdu", "invalid buffer", "connection timed out",
		"forcibly closed", "use of closed network connection",
	}
	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}
	return false
}

func (pc *PLCController) shouldSkipOperation() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.emergencyStop
}

// Public methods (mantidos iguais)
func (pc *PLCController) IsCollectionActive() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.collectionActive && !pc.emergencyStop
}

func (pc *PLCController) IsEmergencyStop() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.emergencyStop
}

func (pc *PLCController) IsPLCConnected() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.plcConnected
}

func (pc *PLCController) IsRadarEnabled(radarID string) bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	switch radarID {
	case "caldeira":
		return pc.radarCaldeiraEnabled
	case "porta_jusante":
		return pc.radarPortaJusanteEnabled
	case "porta_montante":
		return pc.radarPortaMontanteEnabled
	}
	return false
}

func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()
	now := time.Now()

	if caldeira, exists := status["caldeira"]; exists && pc.radarCaldeiraEnabled && caldeira {
		pc.radarCaldeiraConnected = true
		pc.lastRadarCaldeiraUpdate = now
	}
	if portaJusante, exists := status["porta_jusante"]; exists && pc.radarPortaJusanteEnabled && portaJusante {
		pc.radarPortaJusanteConnected = true
		pc.lastRadarPortaJusanteUpdate = now
	}
	if portaMontante, exists := status["porta_montante"]; exists && pc.radarPortaMontanteEnabled && portaMontante {
		pc.radarPortaMontanteConnected = true
		pc.lastRadarPortaMontanteUpdate = now
	}
}

func (pc *PLCController) IsRadarTimingOut(radarID string) (bool, time.Duration) {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	now := time.Now()
	var lastUpdate time.Time
	var enabled, connected bool

	switch radarID {
	case "caldeira":
		enabled, connected = pc.radarCaldeiraEnabled, pc.radarCaldeiraConnected
		lastUpdate = pc.lastRadarCaldeiraUpdate
	case "porta_jusante":
		enabled, connected = pc.radarPortaJusanteEnabled, pc.radarPortaJusanteConnected
		lastUpdate = pc.lastRadarPortaJusanteUpdate
	case "porta_montante":
		enabled, connected = pc.radarPortaMontanteEnabled, pc.radarPortaMontanteConnected
		lastUpdate = pc.lastRadarPortaMontanteUpdate
	}

	if !enabled || !connected {
		return false, 0
	}

	timeSinceUpdate := now.Sub(lastUpdate)
	warningThreshold := pc.radarTimeoutDuration * 80 / 100
	return timeSinceUpdate > warningThreshold, timeSinceUpdate
}
