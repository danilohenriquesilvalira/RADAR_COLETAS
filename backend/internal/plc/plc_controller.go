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

// PLCController gerencia comunicação bidirecional com o PLC (MULTI-RADAR)
type PLCController struct {
	plc    PLCClient
	reader *PLCReader
	writer *PLCWriter

	// Context para controle de goroutines
	commandChan chan models.SystemCommand
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	// Estados do sistema
	liveBit          bool
	collectionActive bool
	debugMode        bool
	emergencyStop    bool

	// Estados individuais dos radares
	radarCaldeiraEnabled      bool
	radarPortaJusanteEnabled  bool
	radarPortaMontanteEnabled bool

	// Estatísticas
	startTime   time.Time
	packetCount int32
	errorCount  int32

	// Status de conexão dos radares COM TIMESTAMP
	radarCaldeiraConnected       bool
	radarPortaJusanteConnected   bool
	radarPortaMontanteConnected  bool
	lastRadarCaldeiraUpdate      time.Time
	lastRadarPortaJusanteUpdate  time.Time
	lastRadarPortaMontanteUpdate time.Time

	// Contadores individuais por radar
	radarCaldeiraPackets      int32
	radarPortaJusantePackets  int32
	radarPortaMontantePackets int32
	radarCaldeiraErrors       int32
	radarPortaJusanteErrors   int32
	radarPortaMontanteErrors  int32

	// Controle de tickers
	liveBitTicker      *time.Ticker
	statusTicker       *time.Ticker
	commandTicker      *time.Ticker
	radarMonitorTicker *time.Ticker
	stopChan           chan bool

	// CONTROLE DE ERROS CONSECUTIVOS
	consecutiveErrors    int32
	lastSuccessfulOp     time.Time
	maxConsecutiveErrors int32

	// MONITORAMENTO DE CONEXÃO DOS RADARES
	radarTimeoutDuration time.Duration

	// DETECÇÃO DE RECONEXÃO E RESET
	lastConnectionCheck  time.Time
	needsDB100Reset      bool
	reconnectionDetected bool
	plcResetInProgress   bool

	// PROTEÇÃO THREAD-SAFE
	mutex sync.RWMutex

	// Sistema de monitoramento multiplataforma
	systemMonitor *SystemMonitor

	// SISTEMA DE REBOOT SEGURO PARA PRODUÇÃO
	rebootTimer          *time.Timer
	rebootTimerActive    bool
	lastResetErrorsState bool
	rebootMutex          sync.Mutex
	rebootStartTime      time.Time

	// PROTEÇÃO CONTRA OVERFLOW CRÍTICA
	lastOverflowCheck    time.Time
	overflowProtectionOn bool
	dailyStatsStartTime  time.Time
	lastDailyStatsReset  time.Time
}

type SystemMonitor struct {
	lastCPUTime  time.Time
	lastCPUIdle  uint64
	lastCPUTotal uint64
}

// CONSTANTES DE PROTEÇÃO CRÍTICA
const (
	REBOOT_TIMEOUT_SECONDS    = 10
	REBOOT_CONFIRMATION_DELAY = 2 * time.Second
	MAX_REBOOT_RETRIES        = 4

	// PROTEÇÃO CONTRA OVERFLOW
	MAX_PACKET_COUNT_CRITICAL = 2000000000 // 2 bilhões (93% do máximo int32)
	MAX_PACKET_COUNT_WARNING  = 1800000000 // 1.8 bilhões (warning)
	OVERFLOW_CHECK_INTERVAL   = 1 * time.Hour
	DAILY_STATS_INTERVAL      = 24 * time.Hour
)

// NewSystemMonitor cria um novo monitor de sistema
func NewSystemMonitor() *SystemMonitor {
	return &SystemMonitor{
		lastCPUTime: time.Now(),
	}
}

// NewPLCController cria um novo controlador PLC (MULTI-RADAR)
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

		startTime:   now,
		packetCount: 0,
		errorCount:  0,

		// Status de conexão dos radares
		radarCaldeiraConnected:       false,
		radarPortaJusanteConnected:   false,
		radarPortaMontanteConnected:  false,
		lastRadarCaldeiraUpdate:      now,
		lastRadarPortaJusanteUpdate:  now,
		lastRadarPortaMontanteUpdate: now,

		// Contadores individuais zerados
		radarCaldeiraPackets:      0,
		radarPortaJusantePackets:  0,
		radarPortaMontantePackets: 0,
		radarCaldeiraErrors:       0,
		radarPortaJusanteErrors:   0,
		radarPortaMontanteErrors:  0,

		// CONTROLE DE ERROS
		consecutiveErrors:    0,
		lastSuccessfulOp:     now,
		maxConsecutiveErrors: 8,

		// MONITORAMENTO DE RADARES
		radarTimeoutDuration: 10 * time.Second,

		// CAMPOS DE RECONEXÃO
		lastConnectionCheck:  now,
		needsDB100Reset:      true,
		reconnectionDetected: false,
		plcResetInProgress:   false,

		stopChan:      make(chan bool, 1),
		systemMonitor: NewSystemMonitor(),

		// SISTEMA DE REBOOT PARA PRODUÇÃO
		rebootTimer:          nil,
		rebootTimerActive:    false,
		lastResetErrorsState: false,
		rebootMutex:          sync.Mutex{},
		rebootStartTime:      time.Time{},

		// PROTEÇÃO CONTRA OVERFLOW
		lastOverflowCheck:    now,
		overflowProtectionOn: true, // SEMPRE ATIVO
		dailyStatsStartTime:  now,
		lastDailyStatsReset:  now,
	}

	// LOG INICIAL DE PROTEÇÃO
	fmt.Printf("🛡️ PROTEÇÃO OVERFLOW: Ativada - Máximo %d pacotes\n", MAX_PACKET_COUNT_CRITICAL)
	log.Printf("OVERFLOW_PROTECTION_INIT: Max packets=%d, Check interval=%v",
		MAX_PACKET_COUNT_CRITICAL, OVERFLOW_CHECK_INTERVAL)

	return controller
}

// 🆕 FUNÇÃO: sendCleanRadarSickDataToPLC - ENVIA DADOS ZERADOS
func (pc *PLCController) sendCleanRadarSickDataToPLC() error {
	fmt.Println("🧹 ========== ENVIANDO DADOS RADAR SICK ZERADOS PARA PLC ==========")
	log.Printf("CLEAN_RADAR_SICK_DATA: Starting clean data transmission to PLC")

	successCount := 0
	errorCount := 0

	// Lista de radares para limpar
	radarConfigs := []struct {
		radarID    string
		radarName  string
		baseOffset int
	}{
		{"caldeira", "Radar Caldeira", 6},
		{"porta_jusante", "Radar Porta Jusante", 102},
		{"porta_montante", "Radar Porta Montante", 198},
	}

	// Enviar dados zerados para cada radar habilitado
	for _, config := range radarConfigs {
		if !pc.IsRadarEnabled(config.radarID) {
			fmt.Printf("⏭️  %s DESABILITADO - pulando limpeza\n", config.radarName)
			continue
		}

		fmt.Printf("🧹 Zerando dados: %s (DB100.%d)...\n", config.radarName, config.baseOffset)

		// ENVIAR DADOS ZERADOS USANDO O PLCWriter
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

		// Pequena pausa entre radares para evitar sobrecarga
		time.Sleep(100 * time.Millisecond)
	}

	// LOG FINAL
	fmt.Printf("🧹 LIMPEZA CONCLUÍDA: %d sucessos, %d erros\n", successCount, errorCount)
	log.Printf("CLEAN_RADAR_SICK_COMPLETE: %d radars cleaned successfully, %d errors", successCount, errorCount)
	fmt.Println("🧹 ================================================================")

	if errorCount > 0 {
		return fmt.Errorf("falhas na limpeza: %d de %d radares falharam", errorCount, successCount+errorCount)
	}

	return nil
}

// checkOverflowProtection - PROTEÇÃO CRÍTICA
func (pc *PLCController) checkOverflowProtection() {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	now := time.Now()

	// Verificar apenas se passou o intervalo
	if now.Sub(pc.lastOverflowCheck) < OVERFLOW_CHECK_INTERVAL {
		return
	}

	pc.lastOverflowCheck = now

	// VERIFICAÇÃO CRÍTICA DE OVERFLOW
	needsCriticalReset := false
	needsWarning := false

	// Verificar contador principal
	if pc.packetCount > MAX_PACKET_COUNT_CRITICAL {
		needsCriticalReset = true
	} else if pc.packetCount > MAX_PACKET_COUNT_WARNING {
		needsWarning = true
	}

	// Verificar contadores individuais
	if pc.radarCaldeiraPackets > MAX_PACKET_COUNT_CRITICAL ||
		pc.radarPortaJusantePackets > MAX_PACKET_COUNT_CRITICAL ||
		pc.radarPortaMontantePackets > MAX_PACKET_COUNT_CRITICAL {
		needsCriticalReset = true
	}

	// RESET CRÍTICO IMEDIATO
	if needsCriticalReset {
		pc.executeOverflowProtection()
	} else if needsWarning {
		// WARNING LOG
		fmt.Printf("⚠️ OVERFLOW WARNING: packetCount=%d (%.1f%% do máximo)\n",
			pc.packetCount, float64(pc.packetCount)/float64(MAX_PACKET_COUNT_CRITICAL)*100)
		log.Printf("OVERFLOW_WARNING: packetCount=%d, caldeira=%d, jusante=%d, montante=%d",
			pc.packetCount, pc.radarCaldeiraPackets, pc.radarPortaJusantePackets, pc.radarPortaMontantePackets)
	}

	// ESTATÍSTICAS DIÁRIAS (OPCIONAL)
	if now.Sub(pc.lastDailyStatsReset) >= DAILY_STATS_INTERVAL {
		pc.logDailyStatistics()
		pc.lastDailyStatsReset = now
	}
}

// executeOverflowProtection - RESET CRÍTICO DE EMERGÊNCIA
func (pc *PLCController) executeOverflowProtection() {
	// LOG CRÍTICO ANTES DO RESET
	fmt.Println("🔥 ========== OVERFLOW PROTECTION ATIVADA ==========")
	fmt.Printf("🔥 TIMESTAMP: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("🔥 USUÁRIO: danilohenriquesilvalira\n")
	fmt.Printf("🔥 CAUSA: Contadores próximos do overflow int32\n")

	// LOG DETALHADO DOS CONTADORES
	log.Printf("OVERFLOW_PROTECTION_TRIGGERED: packetCount=%d, caldeira=%d, jusante=%d, montante=%d",
		pc.packetCount, pc.radarCaldeiraPackets, pc.radarPortaJusantePackets, pc.radarPortaMontantePackets)

	// Calcular estatísticas antes do reset
	totalPackets := pc.packetCount
	totalErrors := pc.errorCount
	uptime := time.Since(pc.startTime)
	errorRate := float64(0)
	if totalPackets > 0 {
		errorRate = float64(totalErrors) / float64(totalPackets) * 100
	}

	// LOG ESTATÍSTICAS IMPORTANTES
	fmt.Printf("📊 STATS ANTES RESET: Packets=%d, Errors=%d, Uptime=%v, ErrorRate=%.2f%%\n",
		totalPackets, totalErrors, uptime, errorRate)
	log.Printf("OVERFLOW_STATS_BEFORE_RESET: packets=%d, errors=%d, uptime=%v, error_rate=%.2f%%",
		totalPackets, totalErrors, uptime, errorRate)

	// RESET DOS CONTADORES DE PACOTES (CRÍTICO)
	fmt.Println("🔄 RESETANDO contadores de pacotes...")
	pc.packetCount = 0
	pc.radarCaldeiraPackets = 0
	pc.radarPortaJusantePackets = 0
	pc.radarPortaMontantePackets = 0

	// MANTER CONTADORES DE ERRO (IMPORTANTES PARA DIAGNÓSTICO)
	// NÃO resetar: errorCount, consecutiveErrors, radarXXXErrors

	// ATUALIZAR TIMESTAMPS
	pc.dailyStatsStartTime = time.Now()

	fmt.Println("✅ OVERFLOW PROTECTION: Contadores de pacotes resetados com sucesso")
	log.Printf("OVERFLOW_PROTECTION_SUCCESS: Packet counters reset, error counters preserved")
	fmt.Println("🔥 ===============================================")
}

// logDailyStatistics - LOG ESTATÍSTICAS DIÁRIAS
func (pc *PLCController) logDailyStatistics() {
	totalPackets := pc.packetCount
	totalErrors := pc.errorCount
	uptime := time.Since(pc.dailyStatsStartTime)

	errorRate := float64(0)
	if totalPackets > 0 {
		errorRate = float64(totalErrors) / float64(totalPackets) * 100
	}

	// Calcular packets por radar
	caldeiraPercent := float64(0)
	jusantePercent := float64(0)
	montantePercent := float64(0)

	if totalPackets > 0 {
		caldeiraPercent = float64(pc.radarCaldeiraPackets) / float64(totalPackets) * 100
		jusantePercent = float64(pc.radarPortaJusantePackets) / float64(totalPackets) * 100
		montantePercent = float64(pc.radarPortaMontantePackets) / float64(totalPackets) * 100
	}

	fmt.Printf("📊 STATS DIÁRIAS: Total=%d, Errors=%d, ErrorRate=%.2f%%, Uptime=%v\n",
		totalPackets, totalErrors, errorRate, uptime)
	fmt.Printf("📡 RADARES: Caldeira=%d(%.1f%%), Jusante=%d(%.1f%%), Montante=%d(%.1f%%)\n",
		pc.radarCaldeiraPackets, caldeiraPercent,
		pc.radarPortaJusantePackets, jusantePercent,
		pc.radarPortaMontantePackets, montantePercent)

	log.Printf("DAILY_STATISTICS: total_packets=%d, total_errors=%d, error_rate=%.2f%%, uptime=%v",
		totalPackets, totalErrors, errorRate, uptime)
	log.Printf("DAILY_RADAR_STATS: caldeira=%d(%.1f%%), jusante=%d(%.1f%%), montante=%d(%.1f%%)",
		pc.radarCaldeiraPackets, caldeiraPercent,
		pc.radarPortaJusantePackets, jusantePercent,
		pc.radarPortaMontantePackets, montantePercent)
}

// Start inicia o controlador PLC
func (pc *PLCController) Start() {
	fmt.Println("🚀 PLC Controller: Iniciando controlador PRODUÇÃO v2.9 RADAR SICK LIMPO...")

	// Iniciar tickers
	pc.liveBitTicker = time.NewTicker(3 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(2 * time.Second)
	pc.radarMonitorTicker = time.NewTicker(5 * time.Second)

	// Iniciar goroutines
	pc.wg.Add(5)
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.radarConnectionMonitorLoop()

	fmt.Println("✅ PLC Controller: Sistema RADAR SICK PRODUÇÃO iniciado - SEM MÉTRICAS SERVIDOR")
}

// Stop para o controlador
func (pc *PLCController) Stop() {
	fmt.Println("🛑 PLC Controller: Iniciando parada gracioso...")

	// CANCELAR TIMER DE REBOOT SE ATIVO
	pc.cancelRebootTimer()

	// LOG FINAL ANTES DE PARAR
	pc.mutex.RLock()
	totalPackets := pc.packetCount
	totalErrors := pc.errorCount
	uptime := time.Since(pc.startTime)
	pc.mutex.RUnlock()

	fmt.Printf("📊 STATS FINAIS: Packets=%d, Errors=%d, Uptime=%v\n", totalPackets, totalErrors, uptime)
	log.Printf("FINAL_STATISTICS: packets=%d, errors=%d, uptime=%v", totalPackets, totalErrors, uptime)

	// Cancelar context
	pc.cancel()

	// Parar tickers
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

	// Fechar channel
	close(pc.commandChan)

	// Aguardar goroutines
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

	close(pc.stopChan)
	fmt.Println("✅ PLC Controller: Parado com sucesso")
}

// cancelRebootTimer cancela timer de reboot se ativo
func (pc *PLCController) cancelRebootTimer() {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	if pc.rebootTimerActive && pc.rebootTimer != nil {
		pc.rebootTimer.Stop()
		elapsed := time.Since(pc.rebootStartTime)
		pc.rebootTimerActive = false

		fmt.Printf("🚨 REBOOT CANCELADO: Bit DB100.0.3 solto após %.1fs (antes dos 10s)\n", elapsed.Seconds())
		log.Printf("PRODUCTION_REBOOT_CANCELLED: ResetErrors bit released after %.1fs", elapsed.Seconds())
	}
}

// startRebootTimer inicia timer de reboot de 10s
func (pc *PLCController) startRebootTimer() {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	// Se já tem timer ativo, não criar outro
	if pc.rebootTimerActive {
		return
	}

	pc.rebootStartTime = time.Now()
	fmt.Printf("🚨 REBOOT TIMER INICIADO: %ds para REBOOT COMPLETO do servidor (PRODUÇÃO)\n", REBOOT_TIMEOUT_SECONDS)
	fmt.Printf("🚨 TIMESTAMP: %s\n", pc.rebootStartTime.Format("2006-01-02 15:04:05"))

	// Log crítico para produção
	log.Printf("PRODUCTION_REBOOT_TIMER_STARTED: 10 second countdown initiated at %s", pc.rebootStartTime.Format("2006-01-02 15:04:05"))

	pc.rebootTimerActive = true

	pc.rebootTimer = time.AfterFunc(REBOOT_TIMEOUT_SECONDS*time.Second, func() {
		pc.executeProductionReboot()
	})
}

// executeProductionReboot - REBOOT COMPLETO PARA PRODUÇÃO
func (pc *PLCController) executeProductionReboot() {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	rebootTime := time.Now()
	uptime := rebootTime.Sub(pc.rebootStartTime)

	fmt.Println("🔥 ========== EXECUTANDO REBOOT COMPLETO DE PRODUÇÃO ==========")
	fmt.Printf("🔥 TIMESTAMP: %s\n", rebootTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("🔥 USUÁRIO: danilohenriquesilvalira\n")
	fmt.Printf("🔥 TRIGGER: DB100.0.3 mantido por %.1fs\n", uptime.Seconds())

	// Log crítico para auditoria
	log.Printf("PRODUCTION_REBOOT_EXECUTING: Full server reboot triggered by PLC DB100.0.3 after %.1fs", uptime.Seconds())

	// STEP 1: RESETAR BIT DB100.0.3 NO PLC (CRÍTICO PARA EVITAR LOOP!)
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

	// STEP 3: SYNC SISTEMA (FORÇA FLUSH DE DADOS)
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

	// STEP 5: EXECUTAR REBOOT COM MÚLTIPLAS TENTATIVAS
	fmt.Println("🔥 STEP 5/5: Executando REBOOT COMPLETO do servidor...")
	log.Printf("PRODUCTION_REBOOT_FINAL: Executing full server reboot now")

	success := false

	// TENTATIVA 1: Script personalizado
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

	// TENTATIVA 2: systemctl reboot
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

	// TENTATIVA 3: /sbin/reboot direto
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

	// TENTATIVA 4: reboot via sh
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

		// Log de emergência
		emergencyLog := fmt.Sprintf("CRITICAL: Server reboot failed at %s - Manual intervention required",
			rebootTime.Format("2006-01-02 15:04:05"))

		// Tentar escrever em arquivo de emergência
		emergencyFile, err := os.OpenFile("/var/log/radar-emergency.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			emergencyFile.WriteString(emergencyLog + "\n")
			emergencyFile.Close()
		}
	}

	fmt.Println("🔥 ========== REBOOT DE PRODUÇÃO FINALIZADO ==========")
}

// getCurrentUser obtém usuário atual
func getCurrentUser() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return "danilohenriquesilvalira"
}

// detectPLCReconnection detecta se PLC reconectou
func (pc *PLCController) detectPLCReconnection() bool {
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

// resetAfterReconnection reseta estado após reconexão detectada
func (pc *PLCController) resetAfterReconnection() error {
	pc.mutex.Lock()
	pc.plcResetInProgress = true
	pc.mutex.Unlock()

	fmt.Println("🔄 RESET APÓS RECONEXÃO INICIADO...")
	time.Sleep(2 * time.Second)

	pc.writer.ResetErrorState()

	pc.mutex.Lock()
	pc.consecutiveErrors = 0
	pc.errorCount = 0
	pc.needsDB100Reset = false
	pc.reconnectionDetected = false
	pc.plcResetInProgress = false
	pc.mutex.Unlock()

	fmt.Println("✅ RESET APÓS RECONEXÃO CONCLUÍDO")
	return nil
}

// liveBitLoop com WaitGroup
func (pc *PLCController) liveBitLoop() {
	defer pc.wg.Done()

	fmt.Println("🔄 LiveBit goroutine iniciada")
	defer fmt.Println("🔄 LiveBit goroutine finalizada")

	for {
		select {
		case <-pc.liveBitTicker.C:
			pc.mutex.Lock()
			pc.liveBit = !pc.liveBit
			pc.mutex.Unlock()

		case <-pc.ctx.Done():
			fmt.Println("   🔄 LiveBit recebeu sinal de parada")
			return
		}
	}
}

// statusWriteLoop com WaitGroup
func (pc *PLCController) statusWriteLoop() {
	defer pc.wg.Done()

	fmt.Println("📤 StatusWrite goroutine iniciada")
	defer fmt.Println("📤 StatusWrite goroutine finalizada")

	// VARIÁVEL PARA CONTROLE DE OVERFLOW CHECK
	lastOverflowCheck := time.Now()

	for {
		select {
		case <-pc.statusTicker.C:
			// VERIFICAÇÃO DE OVERFLOW A CADA HORA
			if time.Since(lastOverflowCheck) >= OVERFLOW_CHECK_INTERVAL {
				pc.checkOverflowProtection()
				lastOverflowCheck = time.Now()
			}

			if pc.detectPLCReconnection() {
				fmt.Println("🔄 Reconexão PLC detectada - executando reset...")
				err := pc.resetAfterReconnection()
				if err != nil {
					fmt.Printf("❌ Erro no reset: %v\n", err)
				}
				continue
			}

			pc.mutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.mutex.RUnlock()

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
					if pc.consecutiveErrors == 1 {
						log.Printf("🔌 PLC: Problema de conexão detectado")
					}
				}
				pc.incrementErrorCount()
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

	fmt.Println("📥 CommandRead goroutine iniciada")
	defer fmt.Println("📥 CommandRead goroutine finalizada")

	for {
		select {
		case <-pc.commandTicker.C:
			pc.mutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.mutex.RUnlock()

			if resetInProgress {
				continue
			}

			if pc.shouldSkipOperation() {
				continue
			}

			commands, err := pc.reader.ReadCommands()
			if err != nil {
				pc.markOperationError(err)
				pc.incrementErrorCount()
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

	fmt.Println("⚡ CommandProcessor goroutine iniciada")
	defer fmt.Println("⚡ CommandProcessor goroutine finalizada")

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

	fmt.Println("🌐 RadarMonitor goroutine iniciada")
	defer fmt.Println("🌐 RadarMonitor goroutine finalizada")

	for {
		select {
		case <-pc.radarMonitorTicker.C:
			pc.checkRadarConnectionTimeouts()

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

	// LÓGICA DE REBOOT SEGURO PARA DB100.0.3 (ResetErrors)
	if commands.ResetErrors != pc.lastResetErrorsState {
		if commands.ResetErrors {
			// Bit ativado - iniciar timer de 10s
			fmt.Printf("🚨 DB100.0.3 (ResetErrors) ATIVADO às %s - Timer de REBOOT COMPLETO iniciado\n",
				time.Now().Format("15:04:05"))
			log.Printf("PRODUCTION_ALERT: DB100.0.3 activated - full server reboot timer started")
			pc.startRebootTimer()
		} else {
			// Bit desativado - cancelar timer
			fmt.Printf("🚨 DB100.0.3 (ResetErrors) DESATIVADO às %s - Timer cancelado\n",
				time.Now().Format("15:04:05"))
			log.Printf("PRODUCTION_INFO: DB100.0.3 deactivated - reboot timer cancelled")
			pc.cancelRebootTimer()
		}
		pc.lastResetErrorsState = commands.ResetErrors
	}

	// Se timer de reboot está ativo, não processar comando normal ResetErrors
	pc.rebootMutex.Lock()
	rebootActive := pc.rebootTimerActive
	pc.rebootMutex.Unlock()

	if commands.ResetErrors && !rebootActive {
		// Processar comando normal de reset se não há timer ativo
		select {
		case pc.commandChan <- models.CmdResetErrors:
		case <-pc.ctx.Done():
			return
		}
		// NÃO resetar o bit se timer está rodando
		if err := pc.writer.ResetCommand(0, 3); err != nil {
			log.Printf("Erro ao resetar ResetErrors: %v", err)
		}
	}

	// ========== COMANDOS GLOBAIS ==========
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

	// ========== COMANDOS INDIVIDUAIS DOS RADARES ==========
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

	// ========== COMANDOS ESPECÍFICOS POR RADAR ==========
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

// executeCommand COM LIMPEZA DE DADOS RADAR SICK
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	switch cmd {
	case models.CmdStartCollection:
		pc.collectionActive = true
		pc.emergencyStop = false
		fmt.Println("PLC Controller: ✅ Coleta INICIADA via comando PLC")

	case models.CmdStopCollection:
		pc.collectionActive = false
		fmt.Println("PLC Controller: ⏹️ Coleta PARADA via comando PLC")

	case models.CmdRestartSystem:
		fmt.Println("PLC Controller: 🔄 Reinício do sistema solicitado via PLC")

	case models.CmdResetErrors:
		fmt.Println("🧹 ========== RESET COMPLETO INICIADO ==========")

		// 1️⃣ Reset de contadores (como antes)
		pc.errorCount = 0
		pc.consecutiveErrors = 0
		pc.radarCaldeiraErrors = 0
		pc.radarPortaJusanteErrors = 0
		pc.radarPortaMontanteErrors = 0
		pc.writer.ResetErrorState()

		fmt.Println("PLC Controller: 🧹 CONTADORES resetados")
		log.Printf("RESET_COUNTERS: All error counters reset to zero")

		// 2️⃣ NOVO: ENVIAR DADOS RADAR SICK ZERADOS PARA PLC
		pc.mutex.Unlock() // Unlock temporário para operação PLC

		fmt.Println("🧹 Iniciando limpeza dos dados RADAR SICK...")
		err := pc.sendCleanRadarSickDataToPLC()

		pc.mutex.Lock() // Lock novamente

		if err != nil {
			fmt.Printf("⚠️ AVISO: Erro na limpeza dos dados RADAR SICK: %v\n", err)
			log.Printf("RESET_WARNING: Error cleaning radar sick data - %v", err)
		} else {
			fmt.Println("PLC Controller: 🧹 DADOS RADAR SICK ZERADOS enviados ao PLC")
			log.Printf("RESET_COMPLETE: Error counters and radar sick data fully cleaned")
		}

		fmt.Println("🧹 ========== RESET COMPLETO FINALIZADO ==========")

	case models.CmdEmergencyStop:
		pc.emergencyStop = true
		pc.collectionActive = false
		fmt.Println("PLC Controller: 🚨 PARADA DE EMERGÊNCIA ativada via PLC")

	case models.CmdEnableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller: 🎯 Radar CALDEIRA HABILITADO via PLC")
		}

	case models.CmdDisableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = false
		pc.radarCaldeiraConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller: ⭕ Radar CALDEIRA DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller: 🎯 Radar PORTA JUSANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = false
		pc.radarPortaJusanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller: ⭕ Radar PORTA JUSANTE DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller: 🎯 Radar PORTA MONTANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = false
		pc.radarPortaMontanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller: ⭕ Radar PORTA MONTANTE DESABILITADO via PLC")
		}

	case models.CmdRestartRadarCaldeira:
		if pc.radarCaldeiraEnabled {
			fmt.Println("PLC Controller: 🔄 Reconexão RADAR CALDEIRA solicitada via PLC")
		}

	case models.CmdRestartRadarPortaJusante:
		if pc.radarPortaJusanteEnabled {
			fmt.Println("PLC Controller: 🔄 Reconexão RADAR PORTA JUSANTE solicitada via PLC")
		}

	case models.CmdRestartRadarPortaMontante:
		if pc.radarPortaMontanteEnabled {
			fmt.Println("PLC Controller: 🔄 Reconexão RADAR PORTA MONTANTE solicitada via PLC")
		}

	case models.CmdResetErrorsRadarCaldeira:
		pc.radarCaldeiraErrors = 0
		fmt.Println("PLC Controller: 🧹 Erros RADAR CALDEIRA resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaJusante:
		pc.radarPortaJusanteErrors = 0
		fmt.Println("PLC Controller: 🧹 Erros RADAR PORTA JUSANTE resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaMontante:
		pc.radarPortaMontanteErrors = 0
		fmt.Println("PLC Controller: 🧹 Erros RADAR PORTA MONTANTE resetados via comando PLC")
	}
}

// 🧹 writeSystemStatus SEM MÉTRICAS SERVIDOR - LIMPO
func (pc *PLCController) writeSystemStatus() error {
	pc.mutex.RLock()

	status := &models.PLCSystemStatus{
		LiveBit:                     pc.liveBit,
		CollectionActive:            pc.collectionActive,
		SystemHealthy:               pc.isSystemHealthy(),
		EmergencyActive:             pc.emergencyStop,
		RadarCaldeiraConnected:      pc.radarCaldeiraConnected && pc.radarCaldeiraEnabled,
		RadarPortaJusanteConnected:  pc.radarPortaJusanteConnected && pc.radarPortaJusanteEnabled,
		RadarPortaMontanteConnected: pc.radarPortaMontanteConnected && pc.radarPortaMontanteEnabled,
	}

	pc.mutex.RUnlock()

	return pc.writer.WriteSystemStatus(status)
}

// WriteMultiRadarData escreve dados de múltiplos radares no PLC COM PROTEÇÃO INTELIGENTE
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	pc.mutex.RLock()
	resetInProgress := pc.plcResetInProgress
	pc.mutex.RUnlock()

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
			pc.IncrementRadarErrors(radarData.RadarID)

			if pc.isConnectionError(err) {
				pc.mutex.Lock()
				pc.needsDB100Reset = true
				pc.mutex.Unlock()
			}

			errors = append(errors, fmt.Sprintf("erro ao escrever dados do radar %s: %v", radarData.RadarName, err))
		} else {
			pc.markOperationSuccess()
			pc.IncrementRadarPackets(radarData.RadarID)
			successfulWrites++
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("erros ao escrever dados dos radares: %s", strings.Join(errors, "; "))
	}

	return nil
}

// checkRadarConnectionTimeouts verifica se radares estão enviando dados recentemente
func (pc *PLCController) checkRadarConnectionTimeouts() {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	now := time.Now()

	if pc.radarCaldeiraEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarCaldeiraUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarCaldeiraConnected {
			fmt.Printf("⚠️ Radar CALDEIRA (HABILITADO): Sem dados há %.1fs - marcando como DESCONECTADO\n",
				timeSinceLastUpdate.Seconds())
			pc.radarCaldeiraConnected = false
		}
	}

	if pc.radarPortaJusanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaJusanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaJusanteConnected {
			fmt.Printf("⚠️ Radar PORTA JUSANTE (HABILITADO): Sem dados há %.1fs - marcando como DESCONECTADO\n",
				timeSinceLastUpdate.Seconds())
			pc.radarPortaJusanteConnected = false
		}
	}

	if pc.radarPortaMontanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaMontanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaMontanteConnected {
			fmt.Printf("⚠️ Radar PORTA MONTANTE (HABILITADO): Sem dados há %.1fs - marcando como DESCONECTADO\n",
				timeSinceLastUpdate.Seconds())
			pc.radarPortaMontanteConnected = false
		}
	}
}

// updateRadarConnectionStatus atualiza status de conexão com timestamp
func (pc *PLCController) updateRadarConnectionStatus(radarID string, connected bool) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	now := time.Now()

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
	pc.mutex.Lock()
	pc.consecutiveErrors = 0
	pc.lastSuccessfulOp = time.Now()
	pc.mutex.Unlock()
}

// markOperationError marca erro de operação
func (pc *PLCController) markOperationError(err error) {
	if pc.isConnectionError(err) {
		pc.mutex.Lock()
		pc.consecutiveErrors++
		pc.mutex.Unlock()
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

// shouldSkipOperation verifica se deve pular operação por muitos erros
func (pc *PLCController) shouldSkipOperation() bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.consecutiveErrors >= pc.maxConsecutiveErrors
}

// ========== MÉTODOS PÚBLICOS THREAD-SAFE ==========

func (pc *PLCController) IsCollectionActive() bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.collectionActive && !pc.emergencyStop
}

func (pc *PLCController) IsDebugMode() bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.debugMode
}

func (pc *PLCController) IsEmergencyStop() bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.emergencyStop
}

func (pc *PLCController) IncrementPacketCount() {
	pc.mutex.Lock()
	pc.packetCount++
	pc.mutex.Unlock()
}

func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.mutex.Lock()
	pc.radarCaldeiraConnected = connected
	if connected {
		pc.lastRadarCaldeiraUpdate = time.Now()
	}
	pc.mutex.Unlock()
}

func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	now := time.Now()

	if caldeira, exists := status["caldeira"]; exists {
		if pc.radarCaldeiraEnabled && caldeira {
			pc.radarCaldeiraConnected = true
			pc.lastRadarCaldeiraUpdate = now
		} else {
			pc.radarCaldeiraConnected = false
		}
	}
	if portaJusante, exists := status["porta_jusante"]; exists {
		if pc.radarPortaJusanteEnabled && portaJusante {
			pc.radarPortaJusanteConnected = true
			pc.lastRadarPortaJusanteUpdate = now
		} else {
			pc.radarPortaJusanteConnected = false
		}
	}
	if portaMontante, exists := status["porta_montante"]; exists {
		if pc.radarPortaMontanteEnabled && portaMontante {
			pc.radarPortaMontanteConnected = true
			pc.lastRadarPortaMontanteUpdate = now
		} else {
			pc.radarPortaMontanteConnected = false
		}
	}
}

func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.updateRadarConnectionStatus(radarID, connected)
}

func (pc *PLCController) IsRadarEnabled(radarID string) bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

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

func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

func (pc *PLCController) GetRadarsConnected() map[string]bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraConnected,
		"porta_jusante":  pc.radarPortaJusanteConnected,
		"porta_montante": pc.radarPortaMontanteConnected,
	}
}

func (pc *PLCController) IncrementRadarPackets(radarID string) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	pc.packetCount++

	switch radarID {
	case "caldeira":
		pc.radarCaldeiraPackets++
	case "porta_jusante":
		pc.radarPortaJusantePackets++
	case "porta_montante":
		pc.radarPortaMontantePackets++
	}
}

func (pc *PLCController) IncrementRadarErrors(radarID string) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	pc.errorCount++

	switch radarID {
	case "caldeira":
		pc.radarCaldeiraErrors++
	case "porta_jusante":
		pc.radarPortaJusanteErrors++
	case "porta_montante":
		pc.radarPortaMontanteErrors++
	}
}

func (pc *PLCController) incrementErrorCount() {
	pc.mutex.Lock()
	pc.errorCount++
	pc.mutex.Unlock()
}

func (pc *PLCController) isSystemHealthy() bool {
	atLeastOneRadarHealthy := false
	if pc.radarCaldeiraEnabled && pc.radarCaldeiraConnected {
		atLeastOneRadarHealthy = true
	}
	if pc.radarPortaJusanteEnabled && pc.radarPortaJusanteConnected {
		atLeastOneRadarHealthy = true
	}
	if pc.radarPortaMontanteEnabled && pc.radarPortaMontanteConnected {
		atLeastOneRadarHealthy = true
	}

	return atLeastOneRadarHealthy && !pc.emergencyStop && pc.errorCount < 50
}
