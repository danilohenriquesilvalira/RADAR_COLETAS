package plc

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"backend/pkg/models"
)

// PLCController gerencia comunicação bidirecional com o PLC (THREAD-SAFE)
type PLCController struct {
	plc    PLCClient
	reader *PLCReader
	writer *PLCWriter

	// Context para controle hierárquico
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	commandChan chan models.SystemCommand

	// Mutex hierárquico
	stateMutex sync.RWMutex // Para estados gerais
	radarMutex sync.RWMutex // Para dados específicos de radar
	timerMutex sync.Mutex   // Para timers e reboot

	// Estados do sistema - SÓ O ESSENCIAL
	liveBit          bool
	collectionActive bool
	debugMode        bool
	emergencyStop    bool

	// Estados individuais dos radares - SÓ CONECTADO/DESCONECTADO
	radarCaldeiraEnabled      bool
	radarPortaJusanteEnabled  bool
	radarPortaMontanteEnabled bool

	// STATUS DE CONEXÃO SIMPLES
	radarCaldeiraConnected       bool
	radarPortaJusanteConnected   bool
	radarPortaMontanteConnected  bool
	lastRadarCaldeiraUpdate      time.Time
	lastRadarPortaJusanteUpdate  time.Time
	lastRadarPortaMontanteUpdate time.Time

	// Controle de tickers COM CONTEXT
	liveBitTicker      *time.Ticker
	statusTicker       *time.Ticker
	commandTicker      *time.Ticker
	radarMonitorTicker *time.Ticker

	// TIMEOUT INTELIGENTE DOS RADARES
	radarTimeoutDuration time.Duration

	// DETECÇÃO DE RECONEXÃO E RESET
	lastConnectionCheck  time.Time
	needsDB100Reset      bool
	reconnectionDetected bool
	plcResetInProgress   bool

	// SISTEMA DE REBOOT SEGURO
	rebootTimer          *time.Timer
	rebootTimerActive    bool
	lastResetErrorsState bool
	rebootStartTime      time.Time

	// Limpeza automática e controle de entradas
	lastMapCleanup            time.Time
	lastRadarReconnectAttempt map[string]time.Time
	radarReconnectInProgress  map[string]bool
	maxMapEntries             int
}

// CONSTANTES ESSENCIAIS
const (
	REBOOT_TIMEOUT_SECONDS    = 10
	REBOOT_CONFIRMATION_DELAY = 2 * time.Second
	MAX_REBOOT_RETRIES        = 4

	// TIMEOUT INTELIGENTE
	RADAR_TIMEOUT_TOLERANCE  = 45 * time.Second
	RADAR_RECONNECT_COOLDOWN = 20 * time.Second

	// Limites para maps
	MAX_MAP_ENTRIES      = 100
	MAP_CLEANUP_INTERVAL = 30 * time.Minute
)

// NewPLCController cria um novo controlador PLC THREAD-SAFE v3.2
func NewPLCController(plcClient PLCClient) *PLCController {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())

	controller := &PLCController{
		plc:              plcClient,
		ctx:              ctx,
		cancel:           cancel,
		reader:           NewPLCReader(plcClient),
		writer:           NewPLCWriter(plcClient),
		commandChan:      make(chan models.SystemCommand, 20),
		collectionActive: true,
		debugMode:        false,
		emergencyStop:    false,

		// Inicializar radares como habilitados por padrão
		radarCaldeiraEnabled:      true,
		radarPortaJusanteEnabled:  true,
		radarPortaMontanteEnabled: true,

		// Status de conexão dos radares
		radarCaldeiraConnected:       false,
		radarPortaJusanteConnected:   false,
		radarPortaMontanteConnected:  false,
		lastRadarCaldeiraUpdate:      now,
		lastRadarPortaJusanteUpdate:  now,
		lastRadarPortaMontanteUpdate: now,

		// TIMEOUT INTELIGENTE
		radarTimeoutDuration: RADAR_TIMEOUT_TOLERANCE,

		// CAMPOS DE RECONEXÃO
		lastConnectionCheck:  now,
		needsDB100Reset:      true,
		reconnectionDetected: false,
		plcResetInProgress:   false,

		// Controle de limpeza automática
		lastMapCleanup: now,
		maxMapEntries:  MAX_MAP_ENTRIES,

		// CONTROLE INDIVIDUAL DE RECONEXÃO COM LIMITE
		lastRadarReconnectAttempt: map[string]time.Time{
			"caldeira":       now,
			"porta_jusante":  now,
			"porta_montante": now,
		},
		radarReconnectInProgress: map[string]bool{
			"caldeira":       false,
			"porta_jusante":  false,
			"porta_montante": false,
		},
	}

	// Iniciar worker de limpeza automática
	controller.wg.Add(1)
	go controller.memoryCleanupWorker()

	fmt.Printf("🛡️ TIMEOUT INTELIGENTE: %v\n", RADAR_TIMEOUT_TOLERANCE)
	fmt.Printf("🛡️ MEMORY LEAK PROTECTION: Maps limitados a %d entradas\n", MAX_MAP_ENTRIES)
	log.Printf("PLC_CONTROLLER_v3.2_INIT: timeout=%v, memory_protection=%d",
		RADAR_TIMEOUT_TOLERANCE, MAX_MAP_ENTRIES)

	return controller
}

// Worker de limpeza automática para prevenir memory leaks
func (pc *PLCController) memoryCleanupWorker() {
	defer pc.wg.Done()

	ticker := time.NewTicker(MAP_CLEANUP_INTERVAL)
	defer ticker.Stop()

	for {
		select {
		case <-pc.ctx.Done():
			fmt.Println("🧹 Memory cleanup worker finalizado")
			return

		case <-ticker.C:
			pc.cleanupMapsMemory()
		}
	}
}

// Limpeza automática de maps para prevenir memory leaks
func (pc *PLCController) cleanupMapsMemory() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()
	if now.Sub(pc.lastMapCleanup) < MAP_CLEANUP_INTERVAL-5*time.Minute {
		return
	}

	cutoff := now.Add(-2 * time.Hour)

	// Limpar lastRadarReconnectAttempt se muito grande
	if len(pc.lastRadarReconnectAttempt) > pc.maxMapEntries {
		newMap := make(map[string]time.Time)

		knownRadars := []string{"caldeira", "porta_jusante", "porta_montante"}
		for _, radarID := range knownRadars {
			if lastTime, exists := pc.lastRadarReconnectAttempt[radarID]; exists {
				newMap[radarID] = lastTime
			}
		}

		for radarID, lastTime := range pc.lastRadarReconnectAttempt {
			if len(newMap) >= pc.maxMapEntries {
				break
			}
			if lastTime.After(cutoff) {
				newMap[radarID] = lastTime
			}
		}

		pc.lastRadarReconnectAttempt = newMap
		fmt.Printf("🧹 Map lastRadarReconnectAttempt limpo: %d entradas\n", len(newMap))
		log.Printf("MEMORY_CLEANUP: lastRadarReconnectAttempt map cleaned")
	}

	// Limpar radarReconnectInProgress se muito grande
	if len(pc.radarReconnectInProgress) > pc.maxMapEntries {
		newMap := make(map[string]bool)

		knownRadars := []string{"caldeira", "porta_jusante", "porta_montante"}
		for _, radarID := range knownRadars {
			if inProgress, exists := pc.radarReconnectInProgress[radarID]; exists {
				newMap[radarID] = inProgress
			}
		}

		pc.radarReconnectInProgress = newMap
		fmt.Printf("🧹 Map radarReconnectInProgress limpo: %d entradas\n", len(newMap))
		log.Printf("MEMORY_CLEANUP: radarReconnectInProgress map cleaned")
	}

	pc.lastMapCleanup = now
}

// sendCleanRadarSickDataToPLC - THREAD-SAFE SEM DEADLOCK
func (pc *PLCController) sendCleanRadarSickDataToPLC() error {
	fmt.Println("🧹 ========== ENVIANDO DADOS RADAR SICK ZERADOS PARA PLC ==========")
	log.Printf("CLEAN_RADAR_SICK_DATA: Starting clean data transmission to PLC")

	successCount := 0
	errorCount := 0

	radarConfigs := []struct {
		radarID    string
		radarName  string
		baseOffset int
	}{
		{"caldeira", "Radar Caldeira", 6},
		{"porta_jusante", "Radar Porta Jusante", 102},
		{"porta_montante", "Radar Porta Montante", 198},
	}

	for _, config := range radarConfigs {
		if !pc.IsRadarEnabled(config.radarID) {
			fmt.Printf("⏭️  %s DESABILITADO - pulando limpeza\n", config.radarName)
			continue
		}

		fmt.Printf("🧹 Zerando dados: %s (DB100.%d)...\n", config.radarName, config.baseOffset)

		err := pc.writer.WriteRadarSickCleanDataToDB100(config.baseOffset)
		if err != nil {
			fmt.Printf("❌ ERRO ao zerar %s: %v\n", config.radarName, err)
			log.Printf("CLEAN_RADAR_SICK_ERROR: Failed to send clean data for %s at offset %d - %v",
				config.radarName, config.baseOffset, err)
			errorCount++
		} else {
			fmt.Printf("✅ %s - dados ZERADOS com sucesso\n", config.radarName)
			log.Printf("CLEAN_RADAR_SICK_SUCCESS: Clean data sent for %s at offset %d",
				config.radarName, config.baseOffset)
			successCount++
		}

		time.Sleep(100 * time.Millisecond)
	}

	fmt.Printf("🧹 LIMPEZA CONCLUÍDA: %d sucessos, %d erros\n", successCount, errorCount)
	log.Printf("CLEAN_RADAR_SICK_COMPLETE: %d radars cleaned successfully, %d errors", successCount, errorCount)
	fmt.Println("🧹 ================================================================")

	if errorCount > 0 {
		return fmt.Errorf("falhas na limpeza: %d de %d radares falharam", errorCount, successCount+errorCount)
	}

	return nil
}

// StartWithContext - INICIA COM CONTEXT EXTERNO
func (pc *PLCController) StartWithContext(parentCtx context.Context) {
	pc.ctx, pc.cancel = context.WithCancel(parentCtx)

	fmt.Println("🚀 PLC Controller v3.2: Iniciando controlador THREAD-SAFE RADAR SICK...")

	pc.liveBitTicker = time.NewTicker(3 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(2 * time.Second)
	pc.radarMonitorTicker = time.NewTicker(8 * time.Second)

	pc.wg.Add(4)
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.radarConnectionMonitorLoop()

	fmt.Printf("✅ PLC Controller v3.2: Sistema THREAD-SAFE iniciado - Timeout: %v\n", RADAR_TIMEOUT_TOLERANCE)
}

// Start - MANTÉM COMPATIBILIDADE
func (pc *PLCController) Start() {
	pc.StartWithContext(context.Background())
}

// Stop com shutdown gracioso
func (pc *PLCController) Stop() {
	fmt.Println("🛑 PLC Controller v3.2: Iniciando parada gracioso...")

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
		fmt.Println("   ✅ Todas as goroutines terminadas")
	case <-time.After(10 * time.Second):
		fmt.Println("   ⚠️ Timeout - forçando parada")
	}

	fmt.Println("✅ PLC Controller v3.2: Parado com sucesso")
}

// cancelRebootTimer
func (pc *PLCController) cancelRebootTimer() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	if pc.rebootTimerActive && pc.rebootTimer != nil {
		pc.rebootTimer.Stop()
		elapsed := time.Since(pc.rebootStartTime)
		pc.rebootTimerActive = false

		fmt.Printf("🚨 REBOOT CANCELADO: Bit DB100.0.3 solto após %.1fs (antes dos 10s)\n", elapsed.Seconds())
		log.Printf("PRODUCTION_REBOOT_CANCELLED: ResetErrors bit released after %.1fs", elapsed.Seconds())
	}
}

// startRebootTimer
func (pc *PLCController) startRebootTimer() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	if pc.rebootTimerActive {
		return
	}

	pc.rebootStartTime = time.Now()
	fmt.Printf("🚨 REBOOT TIMER INICIADO: %ds para REBOOT COMPLETO do servidor (PRODUÇÃO)\n", REBOOT_TIMEOUT_SECONDS)
	fmt.Printf("🚨 TIMESTAMP: %s\n", pc.rebootStartTime.Format("2006-01-02 15:04:05"))

	log.Printf("PRODUCTION_REBOOT_TIMER_STARTED: 10 second countdown initiated at %s", pc.rebootStartTime.Format("2006-01-02 15:04:05"))

	pc.rebootTimerActive = true

	pc.rebootTimer = time.AfterFunc(REBOOT_TIMEOUT_SECONDS*time.Second, func() {
		pc.executeProductionReboot()
	})
}

// executeProductionReboot
func (pc *PLCController) executeProductionReboot() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	rebootTime := time.Now()
	uptime := rebootTime.Sub(pc.rebootStartTime)

	fmt.Println("🔥 ========== EXECUTANDO REBOOT COMPLETO DE PRODUÇÃO v3.2 ==========")
	fmt.Printf("🔥 TIMESTAMP: %s\n", rebootTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("🔥 USUÁRIO: danilohenriquesilvalira\n")
	fmt.Printf("🔥 TRIGGER: DB100.0.3 mantido por %.1fs\n", uptime.Seconds())

	log.Printf("PRODUCTION_REBOOT_EXECUTING_v3.2: Full server reboot triggered by PLC DB100.0.3 after %.1fs", uptime.Seconds())

	// STEP 1: RESETAR BIT DB100.0.3 NO PLC
	fmt.Println("🔥 STEP 1/5: Resetando bit DB100.0.3 no PLC (ANTI-LOOP)...")
	err := pc.writer.ResetCommand(0, 3)
	if err != nil {
		fmt.Printf("❌ ERRO ao resetar DB100.0.3: %v\n", err)
		log.Printf("PRODUCTION_REBOOT_ERROR: Failed to reset DB100.0.3 - %v", err)
	} else {
		fmt.Println("✅ Bit DB100.0.3 resetado com sucesso")
		log.Printf("PRODUCTION_REBOOT_SUCCESS: DB100.0.3 reset successful")
	}

	// STEP 2: AGUARDAR CONFIRMAÇÃO
	fmt.Printf("🔥 STEP 2/5: Aguardando %.1fs para confirmação...\n", REBOOT_CONFIRMATION_DELAY.Seconds())
	time.Sleep(REBOOT_CONFIRMATION_DELAY)

	// STEP 3: SYNC SISTEMA
	fmt.Println("🔥 STEP 3/5: Sincronizando sistema de arquivos...")
	syncCmd := exec.Command("/bin/sync")
	syncErr := syncCmd.Run()
	if syncErr != nil {
		fmt.Printf("⚠️ Aviso: Erro no sync - %v\n", syncErr)
	} else {
		fmt.Println("✅ Sistema sincronizado")
	}

	// STEP 4: RESETAR ESTADO LOCAL
	pc.rebootTimerActive = false
	pc.lastResetErrorsState = false

	// STEP 5: EXECUTAR REBOOT
	fmt.Println("🔥 STEP 5/5: Executando REBOOT COMPLETO do servidor...")
	log.Printf("PRODUCTION_REBOOT_FINAL_v3.2: Executing full server reboot now")

	success := false

	if !success {
		fmt.Println("🔥 TENTATIVA 1: Script personalizado /usr/local/bin/radar-reboot...")
		cmd := exec.Command("/usr/local/bin/radar-reboot")
		err := cmd.Run()
		if err == nil {
			fmt.Println("✅ Script personalizado executado com sucesso")
			log.Printf("PRODUCTION_REBOOT_SUCCESS: Custom script executed")
			success = true
		} else {
			fmt.Printf("❌ Script personalizado falhou: %v\n", err)
		}
	}

	if !success {
		fmt.Println("🔥 TENTATIVA 2: systemctl reboot...")
		cmd := exec.Command("/bin/systemctl", "reboot")
		err := cmd.Run()
		if err == nil {
			fmt.Println("✅ systemctl reboot executado")
			log.Printf("PRODUCTION_REBOOT_SUCCESS: systemctl reboot executed")
			success = true
		} else {
			fmt.Printf("❌ systemctl reboot falhou: %v\n", err)
		}
	}

	if !success {
		fmt.Println("🔥 TENTATIVA 3: /sbin/reboot direto...")
		cmd := exec.Command("/sbin/reboot")
		err := cmd.Run()
		if err == nil {
			fmt.Println("✅ /sbin/reboot executado")
			log.Printf("PRODUCTION_REBOOT_SUCCESS: Direct reboot executed")
			success = true
		} else {
			fmt.Printf("❌ /sbin/reboot falhou: %v\n", err)
		}
	}

	if !success {
		fmt.Println("🔥 TENTATIVA 4: reboot via shell...")
		cmd := exec.Command("/bin/sh", "-c", "reboot")
		err := cmd.Run()
		if err == nil {
			fmt.Println("✅ Shell reboot executado")
			log.Printf("PRODUCTION_REBOOT_SUCCESS: Shell reboot executed")
			success = true
		} else {
			fmt.Printf("❌ Shell reboot falhou: %v\n", err)
		}
	}

	if !success {
		fmt.Println("❌ ERRO CRÍTICO: TODAS as tentativas de reboot falharam!")
		log.Printf("PRODUCTION_REBOOT_CRITICAL_ERROR: All reboot attempts failed")

		emergencyLog := fmt.Sprintf("CRITICAL v3.2: Server reboot failed at %s - Manual intervention required",
			rebootTime.Format("2006-01-02 15:04:05"))

		emergencyFile, err := os.OpenFile("/var/log/radar-emergency.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			emergencyFile.WriteString(emergencyLog + "\n")
			emergencyFile.Close()
		}
	}

	fmt.Println("🔥 ========== REBOOT DE PRODUÇÃO v3.2 FINALIZADO ==========")
}

// detectPLCReconnection
func (pc *PLCController) detectPLCReconnection() bool {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()

	if now.Sub(pc.lastConnectionCheck) < 10*time.Second {
		return false
	}

	pc.lastConnectionCheck = now

	if pc.writer.NeedsReset() {
		fmt.Println("🔍 Writer em estado de erro grave - possível reconexão detectada")
		return true
	}

	return false
}

// resetAfterReconnection
func (pc *PLCController) resetAfterReconnection() error {
	pc.stateMutex.Lock()
	pc.plcResetInProgress = true
	pc.stateMutex.Unlock()

	fmt.Println("🔄 RESET APÓS RECONEXÃO INICIADO...")
	time.Sleep(2 * time.Second)

	pc.writer.ResetErrorState()

	pc.stateMutex.Lock()
	pc.needsDB100Reset = false
	pc.reconnectionDetected = false
	pc.plcResetInProgress = false
	pc.stateMutex.Unlock()

	fmt.Println("✅ RESET APÓS RECONEXÃO CONCLUÍDO")
	return nil
}

// liveBitLoop com WaitGroup
func (pc *PLCController) liveBitLoop() {
	defer pc.wg.Done()

	fmt.Println("🔄 LiveBit goroutine v3.2 iniciada")
	defer fmt.Println("🔄 LiveBit goroutine v3.2 finalizada")

	for {
		select {
		case <-pc.liveBitTicker.C:
			pc.stateMutex.Lock()
			pc.liveBit = !pc.liveBit
			pc.stateMutex.Unlock()

		case <-pc.ctx.Done():
			fmt.Println("   🔄 LiveBit recebeu sinal de parada")
			return
		}
	}
}

// statusWriteLoop com WaitGroup
func (pc *PLCController) statusWriteLoop() {
	defer pc.wg.Done()

	fmt.Println("📤 StatusWrite goroutine v3.2 iniciada")
	defer fmt.Println("📤 StatusWrite goroutine v3.2 finalizada")

	for {
		select {
		case <-pc.statusTicker.C:
			if pc.detectPLCReconnection() {
				fmt.Println("🔄 Reconexão PLC detectada - executando reset...")
				err := pc.resetAfterReconnection()
				if err != nil {
					fmt.Printf("❌ Erro no reset: %v\n", err)
				}
				continue
			}

			pc.stateMutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.stateMutex.RUnlock()

			if resetInProgress {
				continue
			}

			if pc.shouldSkipOperation() {
				continue
			}

			err := pc.writeSystemStatus()
			if err != nil {
				pc.markOperationError(err)
				if pc.isConnectionError(err) {
					log.Printf("🔌 PLC: Problema de conexão detectado")
				}
			} else {
				pc.markOperationSuccess()
			}

		case <-pc.ctx.Done():
			fmt.Println("   📤 StatusWrite recebeu sinal de parada")
			return
		}
	}
}

// commandReadLoop com WaitGroup
func (pc *PLCController) commandReadLoop() {
	defer pc.wg.Done()

	fmt.Println("📥 CommandRead goroutine v3.2 iniciada")
	defer fmt.Println("📥 CommandRead goroutine v3.2 finalizada")

	for {
		select {
		case <-pc.commandTicker.C:
			pc.stateMutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.stateMutex.RUnlock()

			if resetInProgress {
				continue
			}

			if pc.shouldSkipOperation() {
				continue
			}

			commands, err := pc.reader.ReadCommands()
			if err != nil {
				pc.markOperationError(err)
				continue
			}

			pc.markOperationSuccess()
			pc.processCommands(commands)

		case <-pc.ctx.Done():
			fmt.Println("   📥 CommandRead recebeu sinal de parada")
			return
		}
	}
}

// commandProcessor com WaitGroup
func (pc *PLCController) commandProcessor() {
	defer pc.wg.Done()

	fmt.Println("⚡ CommandProcessor goroutine v3.2 iniciada")
	defer fmt.Println("⚡ CommandProcessor goroutine v3.2 finalizada")

	for {
		select {
		case cmd, ok := <-pc.commandChan:
			if !ok {
				fmt.Println("   ⚡ CommandProcessor: Channel fechado")
				return
			}
			pc.executeCommand(cmd)

		case <-pc.ctx.Done():
			fmt.Println("   ⚡ CommandProcessor recebeu sinal de parada")
			return
		}
	}
}

// radarConnectionMonitorLoop com WaitGroup
func (pc *PLCController) radarConnectionMonitorLoop() {
	defer pc.wg.Done()

	fmt.Println("🌐 RadarMonitor goroutine v3.2 iniciada")
	defer fmt.Println("🌐 RadarMonitor goroutine v3.2 finalizada")

	for {
		select {
		case <-pc.radarMonitorTicker.C:
			pc.checkRadarConnectionTimeoutsIntelligent()

		case <-pc.ctx.Done():
			fmt.Println("   🌐 RadarMonitor recebeu sinal de parada")
			return
		}
	}
}

// processCommands com LÓGICA DE REBOOT SEGURO
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	if commands == nil {
		return
	}

	pc.timerMutex.Lock()
	lastState := pc.lastResetErrorsState
	pc.timerMutex.Unlock()

	if commands.ResetErrors != lastState {
		if commands.ResetErrors {
			fmt.Printf("🚨 DB100.0.3 (ResetErrors) ATIVADO às %s - Timer de REBOOT COMPLETO iniciado\n",
				time.Now().Format("15:04:05"))
			log.Printf("PRODUCTION_ALERT: DB100.0.3 activated - full server reboot timer started")
			pc.startRebootTimer()
		} else {
			fmt.Printf("🚨 DB100.0.3 (ResetErrors) DESATIVADO às %s - Timer cancelado\n",
				time.Now().Format("15:04:05"))
			log.Printf("PRODUCTION_INFO: DB100.0.3 deactivated - reboot timer cancelled")
			pc.cancelRebootTimer()
		}

		pc.timerMutex.Lock()
		pc.lastResetErrorsState = commands.ResetErrors
		pc.timerMutex.Unlock()
	}

	pc.timerMutex.Lock()
	rebootActive := pc.rebootTimerActive
	pc.timerMutex.Unlock()

	if commands.ResetErrors && !rebootActive {
		select {
		case pc.commandChan <- models.CmdResetErrors:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(0, 3); err != nil {
			log.Printf("Erro ao resetar ResetErrors: %v", err)
		}
	}

	// COMANDOS GLOBAIS
	if commands.StartCollection && !pc.IsCollectionActive() {
		select {
		case pc.commandChan <- models.CmdStartCollection:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(0, 0); err != nil {
			log.Printf("Erro ao resetar StartCollection: %v", err)
		}
	}

	if commands.StopCollection && pc.IsCollectionActive() {
		select {
		case pc.commandChan <- models.CmdStopCollection:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(0, 1); err != nil {
			log.Printf("Erro ao resetar StopCollection: %v", err)
		}
	}

	if commands.Emergency {
		select {
		case pc.commandChan <- models.CmdEmergencyStop:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(0, 2); err != nil {
			log.Printf("Erro ao resetar Emergency: %v", err)
		}
	}

	// COMANDOS INDIVIDUAIS DOS RADARES
	if commands.EnableRadarCaldeira != pc.IsRadarEnabled("caldeira") {
		var cmd models.SystemCommand
		if commands.EnableRadarCaldeira {
			cmd = models.CmdEnableRadarCaldeira
		} else {
			cmd = models.CmdDisableRadarCaldeira
		}
		select {
		case pc.commandChan <- cmd:
		case <-pc.ctx.Done():
			return
		}
	}

	if commands.EnableRadarPortaJusante != pc.IsRadarEnabled("porta_jusante") {
		var cmd models.SystemCommand
		if commands.EnableRadarPortaJusante {
			cmd = models.CmdEnableRadarPortaJusante
		} else {
			cmd = models.CmdDisableRadarPortaJusante
		}
		select {
		case pc.commandChan <- cmd:
		case <-pc.ctx.Done():
			return
		}
	}

	if commands.EnableRadarPortaMontante != pc.IsRadarEnabled("porta_montante") {
		var cmd models.SystemCommand
		if commands.EnableRadarPortaMontante {
			cmd = models.CmdEnableRadarPortaMontante
		} else {
			cmd = models.CmdDisableRadarPortaMontante
		}
		select {
		case pc.commandChan <- cmd:
		case <-pc.ctx.Done():
			return
		}
	}

	// COMANDOS ESPECÍFICOS POR RADAR
	if commands.RestartRadarCaldeira {
		select {
		case pc.commandChan <- models.CmdRestartRadarCaldeira:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(0, 7); err != nil {
			log.Printf("Erro ao resetar RestartRadarCaldeira: %v", err)
		}
	}

	if commands.RestartRadarPortaJusante {
		select {
		case pc.commandChan <- models.CmdRestartRadarPortaJusante:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(1, 0); err != nil {
			log.Printf("Erro ao resetar RestartRadarPortaJusante: %v", err)
		}
	}

	if commands.RestartRadarPortaMontante {
		select {
		case pc.commandChan <- models.CmdRestartRadarPortaMontante:
		case <-pc.ctx.Done():
			return
		}
		if err := pc.writer.ResetCommand(1, 1); err != nil {
			log.Printf("Erro ao resetar RestartRadarPortaMontante: %v", err)
		}
	}
}

// executeCommand COM LIMPEZA DE DADOS RADAR SICK SEM DEADLOCK
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	switch cmd {
	case models.CmdStartCollection:
		pc.collectionActive = true
		pc.emergencyStop = false
		fmt.Println("PLC Controller v3.2: ✅ Coleta INICIADA via comando PLC")

	case models.CmdStopCollection:
		pc.collectionActive = false
		fmt.Println("PLC Controller v3.2: ⏹️ Coleta PARADA via comando PLC")

	case models.CmdRestartSystem:
		fmt.Println("PLC Controller v3.2: 🔄 Reinício do sistema solicitado via PLC")

	case models.CmdResetErrors:
		fmt.Println("🧹 ========== RESET COMPLETO v3.2 INICIADO ==========")

		pc.writer.ResetErrorState()

		fmt.Println("PLC Controller v3.2: 🧹 Estados resetados")
		log.Printf("RESET_COMPLETE_v3.2: System states reset")

		// ENVIAR DADOS RADAR SICK ZERADOS PARA PLC SEM DEADLOCK
		pc.stateMutex.Unlock()

		fmt.Println("🧹 Iniciando limpeza dos dados RADAR SICK...")
		err := pc.sendCleanRadarSickDataToPLC()

		pc.stateMutex.Lock()

		if err != nil {
			fmt.Printf("⚠️ AVISO: Erro na limpeza dos dados RADAR SICK: %v\n", err)
			log.Printf("RESET_WARNING_v3.2: Error cleaning radar sick data - %v", err)
		} else {
			fmt.Println("PLC Controller v3.2: 🧹 DADOS RADAR SICK ZERADOS enviados ao PLC")
			log.Printf("RESET_COMPLETE_v3.2: Radar sick data fully cleaned")
		}

		fmt.Println("🧹 ========== RESET COMPLETO v3.2 FINALIZADO ==========")

	case models.CmdEmergencyStop:
		pc.emergencyStop = true
		pc.collectionActive = false
		fmt.Println("PLC Controller v3.2: 🚨 PARADA DE EMERGÊNCIA ativada via PLC")

	case models.CmdEnableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.2: 🎯 Radar CALDEIRA HABILITADO via PLC")
		}

	case models.CmdDisableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = false
		pc.radarCaldeiraConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.2: ⭕ Radar CALDEIRA DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.2: 🎯 Radar PORTA JUSANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = false
		pc.radarPortaJusanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.2: ⭕ Radar PORTA JUSANTE DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.2: 🎯 Radar PORTA MONTANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = false
		pc.radarPortaMontanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.2: ⭕ Radar PORTA MONTANTE DESABILITADO via PLC")
		}

	case models.CmdRestartRadarCaldeira:
		if pc.radarCaldeiraEnabled {
			fmt.Println("PLC Controller v3.2: 🔄 Reconexão RADAR CALDEIRA solicitada via PLC")
		}

	case models.CmdRestartRadarPortaJusante:
		if pc.radarPortaJusanteEnabled {
			fmt.Println("PLC Controller v3.2: 🔄 Reconexão RADAR PORTA JUSANTE solicitada via PLC")
		}

	case models.CmdRestartRadarPortaMontante:
		if pc.radarPortaMontanteEnabled {
			fmt.Println("PLC Controller v3.2: 🔄 Reconexão RADAR PORTA MONTANTE solicitada via PLC")
		}

	case models.CmdResetErrorsRadarCaldeira:
		fmt.Println("PLC Controller v3.2: 🧹 Erros RADAR CALDEIRA resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaJusante:
		fmt.Println("PLC Controller v3.2: 🧹 Erros RADAR PORTA JUSANTE resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaMontante:
		fmt.Println("PLC Controller v3.2: 🧹 Erros RADAR PORTA MONTANTE resetados via comando PLC")
	}
}

// writeSystemStatus - SÓ STATUS ESSENCIAL
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

// WriteMultiRadarData com proteção completa
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	pc.stateMutex.RLock()
	resetInProgress := pc.plcResetInProgress
	pc.stateMutex.RUnlock()

	if resetInProgress {
		return nil
	}

	if pc.shouldSkipOperation() {
		return nil
	}

	var errors []string
	successfulWrites := 0

	for _, radarData := range data.Radars {
		if !pc.IsRadarEnabled(radarData.RadarID) {
			continue
		}

		pc.updateRadarConnectionStatus(radarData.RadarID, radarData.Connected)
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

		err := pc.writer.WriteRadarDataToDB100(plcData, baseOffset)
		if err != nil {
			pc.markOperationError(err)

			if pc.isConnectionError(err) {
				pc.stateMutex.Lock()
				pc.needsDB100Reset = true
				pc.stateMutex.Unlock()
			}

			errors = append(errors, fmt.Sprintf("erro ao escrever dados do radar %s: %v", radarData.RadarName, err))
		} else {
			pc.markOperationSuccess()
			successfulWrites++
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("erros ao escrever dados dos radares: %s", strings.Join(errors, "; "))
	}

	return nil
}

// checkRadarConnectionTimeoutsIntelligent - TIMEOUT INTELIGENTE THREAD-SAFE
func (pc *PLCController) checkRadarConnectionTimeoutsIntelligent() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	// CALDEIRA
	if pc.radarCaldeiraEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarCaldeiraUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarCaldeiraConnected {
			if !pc.isRadarReconnectInProgress("caldeira") {
				fmt.Printf("⚠️ Radar CALDEIRA (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.2: Radar CALDEIRA disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarCaldeiraConnected = false
			}
		}
	}

	// PORTA JUSANTE
	if pc.radarPortaJusanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaJusanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaJusanteConnected {
			if !pc.isRadarReconnectInProgress("porta_jusante") {
				fmt.Printf("⚠️ Radar PORTA JUSANTE (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.2: Radar PORTA JUSANTE disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarPortaJusanteConnected = false
			}
		}
	}

	// PORTA MONTANTE
	if pc.radarPortaMontanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaMontanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaMontanteConnected {
			if !pc.isRadarReconnectInProgress("porta_montante") {
				fmt.Printf("⚠️ Radar PORTA MONTANTE (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.2: Radar PORTA MONTANTE disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarPortaMontanteConnected = false
			}
		}
	}
}

// Verificar se radar está em processo de reconexão
func (pc *PLCController) isRadarReconnectInProgress(radarID string) bool {
	lastAttempt, exists := pc.lastRadarReconnectAttempt[radarID]
	if !exists {
		return false
	}
	return time.Since(lastAttempt) < RADAR_RECONNECT_COOLDOWN
}

// updateRadarConnectionStatus atualiza status de conexão com timestamp
func (pc *PLCController) updateRadarConnectionStatus(radarID string, connected bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	if connected {
		pc.lastRadarReconnectAttempt[radarID] = now
		pc.radarReconnectInProgress[radarID] = false
	}

	switch radarID {
	case "caldeira":
		if pc.radarCaldeiraEnabled && connected {
			pc.radarCaldeiraConnected = true
			pc.lastRadarCaldeiraUpdate = now
		} else if !pc.radarCaldeiraEnabled {
			pc.radarCaldeiraConnected = false
		} else {
			pc.radarCaldeiraConnected = false
		}
	case "porta_jusante":
		if pc.radarPortaJusanteEnabled && connected {
			pc.radarPortaJusanteConnected = true
			pc.lastRadarPortaJusanteUpdate = now
		} else if !pc.radarPortaJusanteEnabled {
			pc.radarPortaJusanteConnected = false
		} else {
			pc.radarPortaJusanteConnected = false
		}
	case "porta_montante":
		if pc.radarPortaMontanteEnabled && connected {
			pc.radarPortaMontanteConnected = true
			pc.lastRadarPortaMontanteUpdate = now
		} else if !pc.radarPortaMontanteEnabled {
			pc.radarPortaMontanteConnected = false
		} else {
			pc.radarPortaMontanteConnected = false
		}
	}
}

// markOperationSuccess marca operação bem-sucedida
func (pc *PLCController) markOperationSuccess() {
	// Operação bem-sucedida - sem contadores
}

// markOperationError marca erro de operação
func (pc *PLCController) markOperationError(err error) {
	if err != nil {
		fmt.Printf("⚠️ Erro operação (não crítico): %v\n", err)
	}
}

// isConnectionError verifica se é erro de conexão
func (pc *PLCController) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"i/o timeout", "connection reset", "broken pipe",
		"connection refused", "network unreachable", "no route to host",
		"invalid pdu", "invalid buffer",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}
	return false
}

// shouldSkipOperation verifica se deve pular operação - SÓ EMERGÊNCIA
func (pc *PLCController) shouldSkipOperation() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	return pc.emergencyStop
}

// ========== MÉTODOS PÚBLICOS THREAD-SAFE ==========

// IsCollectionActive
func (pc *PLCController) IsCollectionActive() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.collectionActive && !pc.emergencyStop
}

// IsDebugMode
func (pc *PLCController) IsDebugMode() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.debugMode
}

// IsEmergencyStop
func (pc *PLCController) IsEmergencyStop() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.emergencyStop
}

// SetRadarConnected
func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarCaldeiraConnected = connected
	if connected {
		pc.lastRadarCaldeiraUpdate = time.Now()
	}
}

// SetRadarsConnected
func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	if caldeira, exists := status["caldeira"]; exists {
		if pc.radarCaldeiraEnabled && caldeira {
			pc.radarCaldeiraConnected = true
			pc.lastRadarCaldeiraUpdate = now
			pc.lastRadarReconnectAttempt["caldeira"] = now
			pc.radarReconnectInProgress["caldeira"] = false
		} else {
			pc.radarCaldeiraConnected = false
		}
	}
	if portaJusante, exists := status["porta_jusante"]; exists {
		if pc.radarPortaJusanteEnabled && portaJusante {
			pc.radarPortaJusanteConnected = true
			pc.lastRadarPortaJusanteUpdate = now
			pc.lastRadarReconnectAttempt["porta_jusante"] = now
			pc.radarReconnectInProgress["porta_jusante"] = false
		} else {
			pc.radarPortaJusanteConnected = false
		}
	}
	if portaMontante, exists := status["porta_montante"]; exists {
		if pc.radarPortaMontanteEnabled && portaMontante {
			pc.radarPortaMontanteConnected = true
			pc.lastRadarPortaMontanteUpdate = now
			pc.lastRadarReconnectAttempt["porta_montante"] = now
			pc.radarReconnectInProgress["porta_montante"] = false
		} else {
			pc.radarPortaMontanteConnected = false
		}
	}
}

// SetRadarConnectedByID
func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.updateRadarConnectionStatus(radarID, connected)
}

// IsRadarEnabled
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
	default:
		return false
	}
}

// GetRadarsEnabled
func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

// GetRadarsConnected
func (pc *PLCController) GetRadarsConnected() map[string]bool {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraConnected,
		"porta_jusante":  pc.radarPortaJusanteConnected,
		"porta_montante": pc.radarPortaMontanteConnected,
	}
}

// isSystemHealthy - SÓ EMERGÊNCIA
func (pc *PLCController) isSystemHealthy() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	return !pc.emergencyStop
}

// GetRadarTimeoutDuration retorna o timeout atual configurado
func (pc *PLCController) GetRadarTimeoutDuration() time.Duration {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()
	return pc.radarTimeoutDuration
}

// SetRadarTimeoutDuration permite ajustar o timeout dinamicamente
func (pc *PLCController) SetRadarTimeoutDuration(duration time.Duration) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	oldTimeout := pc.radarTimeoutDuration
	pc.radarTimeoutDuration = duration

	fmt.Printf("🔧 TIMEOUT AJUSTADO v3.2: %v → %v\n", oldTimeout, duration)
	log.Printf("TIMEOUT_ADJUSTMENT_v3.2: Changed from %v to %v", oldTimeout, duration)
}

// GetRadarLastUpdate retorna último update de um radar específico
func (pc *PLCController) GetRadarLastUpdate(radarID string) time.Time {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	switch radarID {
	case "caldeira":
		return pc.lastRadarCaldeiraUpdate
	case "porta_jusante":
		return pc.lastRadarPortaJusanteUpdate
	case "porta_montante":
		return pc.lastRadarPortaMontanteUpdate
	default:
		return time.Time{}
	}
}

// IsRadarTimingOut verifica se radar está próximo do timeout
func (pc *PLCController) IsRadarTimingOut(radarID string) (bool, time.Duration) {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	now := time.Now()
	var lastUpdate time.Time

	switch radarID {
	case "caldeira":
		if !pc.radarCaldeiraEnabled || !pc.radarCaldeiraConnected {
			return false, 0
		}
		lastUpdate = pc.lastRadarCaldeiraUpdate
	case "porta_jusante":
		if !pc.radarPortaJusanteEnabled || !pc.radarPortaJusanteConnected {
			return false, 0
		}
		lastUpdate = pc.lastRadarPortaJusanteUpdate
	case "porta_montante":
		if !pc.radarPortaMontanteEnabled || !pc.radarPortaMontanteConnected {
			return false, 0
		}
		lastUpdate = pc.lastRadarPortaMontanteUpdate
	default:
		return false, 0
	}

	timeSinceUpdate := now.Sub(lastUpdate)
	warningThreshold := pc.radarTimeoutDuration * 80 / 100

	return timeSinceUpdate > warningThreshold, timeSinceUpdate
}

// GetSystemStatistics retorna SÓ STATUS ESSENCIAL
func (pc *PLCController) GetSystemStatistics() map[string]interface{} {
	pc.stateMutex.RLock()
	pc.radarMutex.RLock()

	stats := map[string]interface{}{
		"system_healthy":    pc.isSystemHealthy(),
		"collection_active": pc.collectionActive,
		"emergency_stop":    pc.emergencyStop,
		"radar_timeout":     pc.radarTimeoutDuration.String(),
		"radars": map[string]interface{}{
			"caldeira": map[string]interface{}{
				"enabled":   pc.radarCaldeiraEnabled,
				"connected": pc.radarCaldeiraConnected,
			},
			"porta_jusante": map[string]interface{}{
				"enabled":   pc.radarPortaJusanteEnabled,
				"connected": pc.radarPortaJusanteConnected,
			},
			"porta_montante": map[string]interface{}{
				"enabled":   pc.radarPortaMontanteEnabled,
				"connected": pc.radarPortaMontanteConnected,
			},
		},
	}

	pc.radarMutex.RUnlock()
	pc.stateMutex.RUnlock()

	return stats
}

// MarkRadarReconnectInProgress marca radar como em processo de reconexão
func (pc *PLCController) MarkRadarReconnectInProgress(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarReconnectInProgress[radarID] = true
	pc.lastRadarReconnectAttempt[radarID] = time.Now()

	fmt.Printf("🔄 Radar %s marcado como EM RECONEXÃO v3.2\n", radarID)
	log.Printf("RADAR_RECONNECT_START_v3.2: %s marked as reconnecting", radarID)
}

// ClearRadarReconnectInProgress remove flag de reconexão
func (pc *PLCController) ClearRadarReconnectInProgress(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarReconnectInProgress[radarID] = false

	fmt.Printf("✅ Radar %s não está mais em reconexão v3.2\n", radarID)
	log.Printf("RADAR_RECONNECT_END_v3.2: %s reconnection flag cleared", radarID)
}

// IsAnyRadarReconnecting verifica se algum radar está reconectando
func (pc *PLCController) IsAnyRadarReconnecting() bool {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	for _, inProgress := range pc.radarReconnectInProgress {
		if inProgress {
			return true
		}
	}
	return false
}

// GetReconnectingRadars retorna lista de radares em reconexão
func (pc *PLCController) GetReconnectingRadars() []string {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	var reconnecting []string
	for radarID, inProgress := range pc.radarReconnectInProgress {
		if inProgress {
			reconnecting = append(reconnecting, radarID)
		}
	}
	return reconnecting
}

// ForceRadarTimeout força timeout de um radar específico (para testes)
func (pc *PLCController) ForceRadarTimeout(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	oldTime := time.Now().Add(-pc.radarTimeoutDuration - 10*time.Second)

	switch radarID {
	case "caldeira":
		pc.lastRadarCaldeiraUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.2: Radar CALDEIRA forçado ao timeout\n")
	case "porta_jusante":
		pc.lastRadarPortaJusanteUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.2: Radar PORTA JUSANTE forçado ao timeout\n")
	case "porta_montante":
		pc.lastRadarPortaMontanteUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.2: Radar PORTA MONTANTE forçado ao timeout\n")
	}

	log.Printf("FORCE_TIMEOUT_TEST_v3.2: %s forced to timeout state", radarID)
}

// ResetAllRadarTimestamps reseta todos os timestamps
func (pc *PLCController) ResetAllRadarTimestamps() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()
	pc.lastRadarCaldeiraUpdate = now
	pc.lastRadarPortaJusanteUpdate = now
	pc.lastRadarPortaMontanteUpdate = now

	for radarID := range pc.radarReconnectInProgress {
		pc.radarReconnectInProgress[radarID] = false
		pc.lastRadarReconnectAttempt[radarID] = now
	}

	fmt.Println("🔄 TODOS os timestamps de radar resetados v3.2")
	log.Printf("RADAR_TIMESTAMPS_RESET_v3.2: All radar timestamps reset to current time")
}

// GetMemoryStats retorna estatísticas de uso de memória
func (pc *PLCController) GetMemoryStats() map[string]interface{} {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	return map[string]interface{}{
		"lastRadarReconnectAttempt_entries": len(pc.lastRadarReconnectAttempt),
		"radarReconnectInProgress_entries":  len(pc.radarReconnectInProgress),
		"max_map_entries_limit":             pc.maxMapEntries,
		"last_cleanup":                      pc.lastMapCleanup.Format("2006-01-02 15:04:05"),
		"memory_protection_active":          true,
	}
}

// GetDetailedSystemHealth retorna saúde detalhada do sistema - SÓ STATUS
func (pc *PLCController) GetDetailedSystemHealth() map[string]interface{} {
	pc.stateMutex.RLock()
	pc.radarMutex.RLock()

	health := map[string]interface{}{
		"overall_healthy":        pc.isSystemHealthy(),
		"collection_active":      pc.collectionActive,
		"emergency_stop":         pc.emergencyStop,
		"plc_reset_in_progress":  pc.plcResetInProgress,
		"radar_timeout_duration": pc.radarTimeoutDuration.String(),
		"radars_health": map[string]interface{}{
			"caldeira": map[string]interface{}{
				"enabled":                   pc.radarCaldeiraEnabled,
				"connected":                 pc.radarCaldeiraConnected,
				"last_update":               pc.lastRadarCaldeiraUpdate.Format("2006-01-02 15:04:05"),
				"seconds_since_last_update": int64(time.Since(pc.lastRadarCaldeiraUpdate).Seconds()),
				"is_timing_out":             time.Since(pc.lastRadarCaldeiraUpdate) > pc.radarTimeoutDuration*80/100,
				"reconnect_in_progress":     pc.radarReconnectInProgress["caldeira"],
			},
			"porta_jusante": map[string]interface{}{
				"enabled":                   pc.radarPortaJusanteEnabled,
				"connected":                 pc.radarPortaJusanteConnected,
				"last_update":               pc.lastRadarPortaJusanteUpdate.Format("2006-01-02 15:04:05"),
				"seconds_since_last_update": int64(time.Since(pc.lastRadarPortaJusanteUpdate).Seconds()),
				"is_timing_out":             time.Since(pc.lastRadarPortaJusanteUpdate) > pc.radarTimeoutDuration*80/100,
				"reconnect_in_progress":     pc.radarReconnectInProgress["porta_jusante"],
			},
			"porta_montante": map[string]interface{}{
				"enabled":                   pc.radarPortaMontanteEnabled,
				"connected":                 pc.radarPortaMontanteConnected,
				"last_update":               pc.lastRadarPortaMontanteUpdate.Format("2006-01-02 15:04:05"),
				"seconds_since_last_update": int64(time.Since(pc.lastRadarPortaMontanteUpdate).Seconds()),
				"is_timing_out":             time.Since(pc.lastRadarPortaMontanteUpdate) > pc.radarTimeoutDuration*80/100,
				"reconnect_in_progress":     pc.radarReconnectInProgress["porta_montante"],
			},
		},
	}

	pc.radarMutex.RUnlock()
	pc.stateMutex.RUnlock()

	return health
}

// String retorna representação string do PLCController - SÓ STATUS
func (pc *PLCController) String() string {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	return fmt.Sprintf("PLCController v3.2 [Collection: %v, Emergency: %v, SystemHealthy: %v]",
		pc.collectionActive,
		pc.emergencyStop,
		!pc.emergencyStop)
}
