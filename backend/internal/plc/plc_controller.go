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

	// ✅ CORREÇÃO DEADLOCK: Context para controle hierárquico
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	commandChan chan models.SystemCommand

	// ✅ CORREÇÃO RACE CONDITION: Mutex hierárquico
	stateMutex   sync.RWMutex // Para estados gerais
	radarMutex   sync.RWMutex // Para dados específicos de radar
	timerMutex   sync.Mutex   // Para timers e reboot
	counterMutex sync.RWMutex // ✅ NOVO: Para contadores

	// Estados do sistema
	liveBit          bool
	collectionActive bool
	debugMode        bool
	emergencyStop    bool

	// Estados individuais dos radares
	radarCaldeiraEnabled      bool
	radarPortaJusanteEnabled  bool
	radarPortaMontanteEnabled bool

	// ✅ CORREÇÃO OVERFLOW: Estatísticas com int64
	startTime   time.Time
	packetCount int64 // MUDANÇA: int32 -> int64 para evitar overflow
	errorCount  int64 // MUDANÇA: int32 -> int64 para evitar overflow

	// STATUS DE CONEXÃO COM TIMEOUT INTELIGENTE
	radarCaldeiraConnected       bool
	radarPortaJusanteConnected   bool
	radarPortaMontanteConnected  bool
	lastRadarCaldeiraUpdate      time.Time
	lastRadarPortaJusanteUpdate  time.Time
	lastRadarPortaMontanteUpdate time.Time

	// ✅ CORREÇÃO OVERFLOW: Contadores individuais com int64
	radarCaldeiraPackets      int64 // MUDANÇA: int32 -> int64
	radarPortaJusantePackets  int64 // MUDANÇA: int32 -> int64
	radarPortaMontantePackets int64 // MUDANÇA: int32 -> int64
	radarCaldeiraErrors       int64 // MUDANÇA: int32 -> int64
	radarPortaJusanteErrors   int64 // MUDANÇA: int32 -> int64
	radarPortaMontanteErrors  int64 // MUDANÇA: int32 -> int64

	// Controle de tickers COM CONTEXT
	liveBitTicker      *time.Ticker
	statusTicker       *time.Ticker
	commandTicker      *time.Ticker
	radarMonitorTicker *time.Ticker

	// ✅ CORREÇÃO OVERFLOW: Controle de erros com int64
	consecutiveErrors    int64 // MUDANÇA: int32 -> int64
	lastSuccessfulOp     time.Time
	maxConsecutiveErrors int64 // MUDANÇA: int32 -> int64

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

	// ✅ CORREÇÃO MEMORY LEAK: Limpeza automática e controle de entradas
	lastOverflowCheck         time.Time
	overflowProtectionOn      bool
	dailyStatsStartTime       time.Time
	lastDailyStatsReset       time.Time
	lastRadarReconnectAttempt map[string]time.Time
	radarReconnectInProgress  map[string]bool
	lastMapCleanup            time.Time
	maxMapEntries             int
}

// ✅ CONSTANTES ATUALIZADAS PARA int64 E MEMORY LEAK PREVENTION
const (
	REBOOT_TIMEOUT_SECONDS    = 10
	REBOOT_CONFIRMATION_DELAY = 2 * time.Second
	MAX_REBOOT_RETRIES        = 4

	// ✅ PROTEÇÃO CONTRA OVERFLOW PARA int64
	MAX_PACKET_COUNT_CRITICAL = 9000000000000000000 // 9 quintilhões (para int64)
	MAX_PACKET_COUNT_WARNING  = 8000000000000000000 // 8 quintilhões (warning)
	OVERFLOW_CHECK_INTERVAL   = 1 * time.Hour
	DAILY_STATS_INTERVAL      = 24 * time.Hour

	// TIMEOUT INTELIGENTE
	RADAR_TIMEOUT_TOLERANCE  = 45 * time.Second
	RADAR_RECONNECT_COOLDOWN = 20 * time.Second

	// ✅ CORREÇÃO MEMORY LEAK: Limites para maps
	MAX_MAP_ENTRIES      = 100
	MAP_CLEANUP_INTERVAL = 30 * time.Minute
)

// NewSystemMonitor cria um novo monitor de sistema
type SystemMonitor struct {
	lastCPUTime time.Time
}

func NewSystemMonitor() *SystemMonitor {
	return &SystemMonitor{
		lastCPUTime: time.Now(),
	}
}

// NewPLCController cria um novo controlador PLC THREAD-SAFE v3.1
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

		// ✅ TIMEOUT INTELIGENTE
		radarTimeoutDuration: RADAR_TIMEOUT_TOLERANCE,

		// CAMPOS DE RECONEXÃO
		lastConnectionCheck:  now,
		needsDB100Reset:      true,
		reconnectionDetected: false,
		plcResetInProgress:   false,

		// ✅ CORREÇÃO MEMORY LEAK: Inicialização com limite
		lastOverflowCheck:    now,
		overflowProtectionOn: true,
		dailyStatsStartTime:  now,
		lastDailyStatsReset:  now,
		lastMapCleanup:       now,
		maxMapEntries:        MAX_MAP_ENTRIES,

		// ✅ CONTROLE INDIVIDUAL DE RECONEXÃO COM LIMITE
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

	// ✅ INICIAR WORKER DE LIMPEZA AUTOMÁTICA
	controller.wg.Add(1)
	go controller.memoryCleanupWorker()

	// LOG INICIAL DE PROTEÇÃO
	fmt.Printf("🛡️ TIMEOUT INTELIGENTE: %v (era 10s)\n", RADAR_TIMEOUT_TOLERANCE)
	fmt.Printf("🛡️ PROTEÇÃO OVERFLOW: Ativada - Máximo %d pacotes (int64)\n", MAX_PACKET_COUNT_CRITICAL)
	fmt.Printf("🛡️ MEMORY LEAK PROTECTION: Maps limitados a %d entradas\n", MAX_MAP_ENTRIES)
	log.Printf("PLC_CONTROLLER_v3.1_INIT: timeout=%v, overflow_protection=%d, memory_protection=%d",
		RADAR_TIMEOUT_TOLERANCE, MAX_PACKET_COUNT_CRITICAL, MAX_MAP_ENTRIES)

	return controller
}

// ✅ NOVO: Worker de limpeza automática para prevenir memory leaks
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

// ✅ NOVO: Limpeza automática de maps para prevenir memory leaks
func (pc *PLCController) cleanupMapsMemory() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()
	if now.Sub(pc.lastMapCleanup) < MAP_CLEANUP_INTERVAL-5*time.Minute {
		return // Evitar limpeza muito frequente
	}

	cutoff := now.Add(-2 * time.Hour) // Remover entradas > 2 horas

	// ✅ LIMPAR lastRadarReconnectAttempt se muito grande
	if len(pc.lastRadarReconnectAttempt) > pc.maxMapEntries {
		newMap := make(map[string]time.Time)

		// Manter apenas entradas recentes e radares conhecidos
		knownRadars := []string{"caldeira", "porta_jusante", "porta_montante"}
		for _, radarID := range knownRadars {
			if lastTime, exists := pc.lastRadarReconnectAttempt[radarID]; exists {
				newMap[radarID] = lastTime
			}
		}

		// Adicionar outras entradas recentes (máximo até o limite)
		for radarID, lastTime := range pc.lastRadarReconnectAttempt {
			if len(newMap) >= pc.maxMapEntries {
				break
			}
			if lastTime.After(cutoff) {
				newMap[radarID] = lastTime
			}
		}

		pc.lastRadarReconnectAttempt = newMap

		fmt.Printf("🧹 Map lastRadarReconnectAttempt limpo: %d -> %d entradas\n",
			len(pc.lastRadarReconnectAttempt), len(newMap))
		log.Printf("MEMORY_CLEANUP: lastRadarReconnectAttempt map cleaned")
	}

	// ✅ LIMPAR radarReconnectInProgress se muito grande
	if len(pc.radarReconnectInProgress) > pc.maxMapEntries {
		newMap := make(map[string]bool)

		// Manter apenas radares conhecidos
		knownRadars := []string{"caldeira", "porta_jusante", "porta_montante"}
		for _, radarID := range knownRadars {
			if inProgress, exists := pc.radarReconnectInProgress[radarID]; exists {
				newMap[radarID] = inProgress
			}
		}

		pc.radarReconnectInProgress = newMap

		fmt.Printf("🧹 Map radarReconnectInProgress limpo: %d -> %d entradas\n",
			len(pc.radarReconnectInProgress), len(newMap))
		log.Printf("MEMORY_CLEANUP: radarReconnectInProgress map cleaned")
	}

	pc.lastMapCleanup = now

	fmt.Printf("🧹 Limpeza automática de memória executada - Maps: %d + %d entradas\n",
		len(pc.lastRadarReconnectAttempt), len(pc.radarReconnectInProgress))
}

// ✅ NOVO: sendCleanRadarSickDataToPLC - THREAD-SAFE SEM DEADLOCK
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

		// ✅ ENVIAR DADOS ZERADOS SEM LOCK (writer já é thread-safe)
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

// ✅ THREAD-SAFE: checkOverflowProtection - PROTEÇÃO CRÍTICA
func (pc *PLCController) checkOverflowProtection() {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()

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
		fmt.Printf("⚠️ OVERFLOW WARNING: packetCount=%d (%.1f%% do máximo int64)\n",
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

// ✅ THREAD-SAFE: executeOverflowProtection - RESET CRÍTICO
func (pc *PLCController) executeOverflowProtection() {
	// LOG CRÍTICO ANTES DO RESET
	fmt.Println("🔥 ========== OVERFLOW PROTECTION ATIVADA (int64) ==========")
	fmt.Printf("🔥 TIMESTAMP: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("🔥 USUÁRIO: danilohenriquesilvalira\n")
	fmt.Printf("🔥 CAUSA: Contadores próximos do overflow int64\n")

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

	fmt.Println("✅ OVERFLOW PROTECTION: Contadores de pacotes resetados com sucesso (int64)")
	log.Printf("OVERFLOW_PROTECTION_SUCCESS: Packet counters reset (int64), error counters preserved")
	fmt.Println("🔥 ===============================================")
}

// ✅ THREAD-SAFE: logDailyStatistics
func (pc *PLCController) logDailyStatistics() {
	pc.counterMutex.RLock()
	defer pc.counterMutex.RUnlock()

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

	fmt.Printf("📊 STATS DIÁRIAS v3.1: Total=%d, Errors=%d, ErrorRate=%.2f%%, Uptime=%v\n",
		totalPackets, totalErrors, errorRate, uptime)
	fmt.Printf("📡 RADARES: Caldeira=%d(%.1f%%), Jusante=%d(%.1f%%), Montante=%d(%.1f%%)\n",
		pc.radarCaldeiraPackets, caldeiraPercent,
		pc.radarPortaJusantePackets, jusantePercent,
		pc.radarPortaMontantePackets, montantePercent)

	log.Printf("DAILY_STATISTICS_v3.1: total_packets=%d, total_errors=%d, error_rate=%.2f%%, uptime=%v",
		totalPackets, totalErrors, errorRate, uptime)
	log.Printf("DAILY_RADAR_STATS_v3.1: caldeira=%d(%.1f%%), jusante=%d(%.1f%%), montante=%d(%.1f%%)",
		pc.radarCaldeiraPackets, caldeiraPercent,
		pc.radarPortaJusantePackets, jusantePercent,
		pc.radarPortaMontantePackets, montantePercent)
}

// ✅ NOVA FUNÇÃO: StartWithContext - INICIA COM CONTEXT EXTERNO
func (pc *PLCController) StartWithContext(parentCtx context.Context) {
	// ✅ COMBINAR CONTEXT EXTERNO COM INTERNO
	pc.ctx, pc.cancel = context.WithCancel(parentCtx)

	fmt.Println("🚀 PLC Controller v3.1: Iniciando controlador THREAD-SAFE RADAR SICK...")

	// Iniciar tickers
	pc.liveBitTicker = time.NewTicker(3 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(2 * time.Second)
	pc.radarMonitorTicker = time.NewTicker(8 * time.Second)

	// Iniciar goroutines COM WAITGROUP
	pc.wg.Add(4) // 4 goroutines principais (memory cleanup já foi adicionado)
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.radarConnectionMonitorLoop()

	fmt.Printf("✅ PLC Controller v3.1: Sistema THREAD-SAFE iniciado - Timeout: %v\n", RADAR_TIMEOUT_TOLERANCE)
}

// Start - MANTÉM COMPATIBILIDADE (usa context interno)
func (pc *PLCController) Start() {
	pc.StartWithContext(context.Background())
}

// ✅ THREAD-SAFE: Stop com shutdown gracioso
func (pc *PLCController) Stop() {
	fmt.Println("🛑 PLC Controller v3.1: Iniciando parada gracioso...")

	// ✅ CANCELAR TIMER DE REBOOT SE ATIVO
	pc.cancelRebootTimer()

	// ✅ THREAD-SAFE: LOG FINAL ANTES DE PARAR
	pc.counterMutex.RLock()
	totalPackets := pc.packetCount
	totalErrors := pc.errorCount
	uptime := time.Since(pc.startTime)
	pc.counterMutex.RUnlock()

	fmt.Printf("📊 STATS FINAIS v3.1: Packets=%d, Errors=%d, Uptime=%v\n", totalPackets, totalErrors, uptime)
	log.Printf("FINAL_STATISTICS_v3.1: packets=%d, errors=%d, uptime=%v", totalPackets, totalErrors, uptime)

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

	// ✅ AGUARDAR GOROUTINES COM TIMEOUT
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

	fmt.Println("✅ PLC Controller v3.1: Parado com sucesso")
}

// ✅ THREAD-SAFE: cancelRebootTimer
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

// ✅ THREAD-SAFE: startRebootTimer
func (pc *PLCController) startRebootTimer() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

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

// ✅ THREAD-SAFE: executeProductionReboot
func (pc *PLCController) executeProductionReboot() {
	pc.timerMutex.Lock()
	defer pc.timerMutex.Unlock()

	rebootTime := time.Now()
	uptime := rebootTime.Sub(pc.rebootStartTime)

	fmt.Println("🔥 ========== EXECUTANDO REBOOT COMPLETO DE PRODUÇÃO v3.1 ==========")
	fmt.Printf("🔥 TIMESTAMP: %s\n", rebootTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("🔥 USUÁRIO: danilohenriquesilvalira\n")
	fmt.Printf("🔥 TRIGGER: DB100.0.3 mantido por %.1fs\n", uptime.Seconds())

	// Log crítico para auditoria
	log.Printf("PRODUCTION_REBOOT_EXECUTING_v3.1: Full server reboot triggered by PLC DB100.0.3 after %.1fs", uptime.Seconds())

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
	log.Printf("PRODUCTION_REBOOT_FINAL_v3.1: Executing full server reboot now")

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
		emergencyLog := fmt.Sprintf("CRITICAL v3.1: Server reboot failed at %s - Manual intervention required",
			rebootTime.Format("2006-01-02 15:04:05"))

		// Tentar escrever em arquivo de emergência
		emergencyFile, err := os.OpenFile("/var/log/radar-emergency.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			emergencyFile.WriteString(emergencyLog + "\n")
			emergencyFile.Close()
		}
	}

	fmt.Println("🔥 ========== REBOOT DE PRODUÇÃO v3.1 FINALIZADO ==========")
}

// ❌ DELETAR ESTA FUNÇÃO COMPLETA:
// func getCurrentUser() string {
//     if user := os.Getenv("USER"); user != "" {
//         return user
//     }
//     if user := os.Getenv("USERNAME"); user != "" {
//         return user
//     }
//     return "danilohenriquesilvalira"
// }

// ✅ THREAD-SAFE: detectPLCReconnection
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

// ✅ THREAD-SAFE: resetAfterReconnection
func (pc *PLCController) resetAfterReconnection() error {
	pc.stateMutex.Lock()
	pc.plcResetInProgress = true
	pc.stateMutex.Unlock()

	fmt.Println("🔄 RESET APÓS RECONEXÃO INICIADO...")
	time.Sleep(2 * time.Second)

	pc.writer.ResetErrorState()

	pc.stateMutex.Lock()
	pc.consecutiveErrors = 0
	pc.errorCount = 0
	pc.needsDB100Reset = false
	pc.reconnectionDetected = false
	pc.plcResetInProgress = false
	pc.stateMutex.Unlock()

	fmt.Println("✅ RESET APÓS RECONEXÃO CONCLUÍDO")
	return nil
}

// ✅ THREAD-SAFE: liveBitLoop com WaitGroup
func (pc *PLCController) liveBitLoop() {
	defer pc.wg.Done()

	fmt.Println("🔄 LiveBit goroutine v3.1 iniciada")
	defer fmt.Println("🔄 LiveBit goroutine v3.1 finalizada")

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

// ✅ THREAD-SAFE: statusWriteLoop com WaitGroup
func (pc *PLCController) statusWriteLoop() {
	defer pc.wg.Done()

	fmt.Println("📤 StatusWrite goroutine v3.1 iniciada")
	defer fmt.Println("📤 StatusWrite goroutine v3.1 finalizada")

	// VARIÁVEL PARA CONTROLE DE OVERFLOW CHECK
	lastOverflowCheck := time.Now()

	for {
		select {
		case <-pc.statusTicker.C:
			// ✅ VERIFICAÇÃO DE OVERFLOW COM THREAD SAFETY
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

			// ✅ THREAD-SAFE CHECK
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
					if pc.getConsecutiveErrors() == 1 {
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

// ✅ THREAD-SAFE: commandReadLoop com WaitGroup
func (pc *PLCController) commandReadLoop() {
	defer pc.wg.Done()

	fmt.Println("📥 CommandRead goroutine v3.1 iniciada")
	defer fmt.Println("📥 CommandRead goroutine v3.1 finalizada")

	for {
		select {
		case <-pc.commandTicker.C:
			// ✅ THREAD-SAFE CHECK
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

// ✅ THREAD-SAFE: commandProcessor com WaitGroup
func (pc *PLCController) commandProcessor() {
	defer pc.wg.Done()

	fmt.Println("⚡ CommandProcessor goroutine v3.1 iniciada")
	defer fmt.Println("⚡ CommandProcessor goroutine v3.1 finalizada")

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

// ✅ THREAD-SAFE: radarConnectionMonitorLoop com WaitGroup
func (pc *PLCController) radarConnectionMonitorLoop() {
	defer pc.wg.Done()

	fmt.Println("🌐 RadarMonitor goroutine v3.1 iniciada")
	defer fmt.Println("🌐 RadarMonitor goroutine v3.1 finalizada")

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

// ✅ THREAD-SAFE: processCommands com LÓGICA DE REBOOT SEGURO
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	if commands == nil {
		return
	}

	// ✅ THREAD-SAFE: LÓGICA DE REBOOT SEGURO PARA DB100.0.3
	pc.timerMutex.Lock()
	lastState := pc.lastResetErrorsState
	pc.timerMutex.Unlock()

	if commands.ResetErrors != lastState {
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

		pc.timerMutex.Lock()
		pc.lastResetErrorsState = commands.ResetErrors
		pc.timerMutex.Unlock()
	}

	// ✅ THREAD-SAFE: Se timer de reboot está ativo, não processar comando normal ResetErrors
	pc.timerMutex.Lock()
	rebootActive := pc.rebootTimerActive
	pc.timerMutex.Unlock()

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

// ✅ THREAD-SAFE: executeCommand COM LIMPEZA DE DADOS RADAR SICK SEM DEADLOCK
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	switch cmd {
	case models.CmdStartCollection:
		pc.collectionActive = true
		pc.emergencyStop = false
		fmt.Println("PLC Controller v3.1: ✅ Coleta INICIADA via comando PLC")

	case models.CmdStopCollection:
		pc.collectionActive = false
		fmt.Println("PLC Controller v3.1: ⏹️ Coleta PARADA via comando PLC")

	case models.CmdRestartSystem:
		fmt.Println("PLC Controller v3.1: 🔄 Reinício do sistema solicitado via PLC")

	case models.CmdResetErrors:
		fmt.Println("🧹 ========== RESET COMPLETO v3.1 INICIADO ==========")

		// 1️⃣ Reset de contadores THREAD-SAFE
		pc.counterMutex.Lock()
		pc.errorCount = 0
		pc.consecutiveErrors = 0
		pc.radarCaldeiraErrors = 0
		pc.radarPortaJusanteErrors = 0
		pc.radarPortaMontanteErrors = 0
		pc.counterMutex.Unlock()

		pc.writer.ResetErrorState()

		fmt.Println("PLC Controller v3.1: 🧹 CONTADORES resetados")
		log.Printf("RESET_COUNTERS_v3.1: All error counters reset to zero")

		// 2️⃣ NOVO: ENVIAR DADOS RADAR SICK ZERADOS PARA PLC SEM DEADLOCK
		pc.stateMutex.Unlock() // ✅ UNLOCK ANTES DE OPERAÇÃO EXTERNA

		fmt.Println("🧹 Iniciando limpeza dos dados RADAR SICK...")
		err := pc.sendCleanRadarSickDataToPLC()

		pc.stateMutex.Lock() // ✅ LOCK NOVAMENTE

		if err != nil {
			fmt.Printf("⚠️ AVISO: Erro na limpeza dos dados RADAR SICK: %v\n", err)
			log.Printf("RESET_WARNING_v3.1: Error cleaning radar sick data - %v", err)
		} else {
			fmt.Println("PLC Controller v3.1: 🧹 DADOS RADAR SICK ZERADOS enviados ao PLC")
			log.Printf("RESET_COMPLETE_v3.1: Error counters and radar sick data fully cleaned")
		}

		fmt.Println("🧹 ========== RESET COMPLETO v3.1 FINALIZADO ==========")

	case models.CmdEmergencyStop:
		pc.emergencyStop = true
		pc.collectionActive = false
		fmt.Println("PLC Controller v3.1: 🚨 PARADA DE EMERGÊNCIA ativada via PLC")

	case models.CmdEnableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.1: 🎯 Radar CALDEIRA HABILITADO via PLC")
		}

	case models.CmdDisableRadarCaldeira:
		wasEnabled := pc.radarCaldeiraEnabled
		pc.radarCaldeiraEnabled = false
		pc.radarCaldeiraConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.1: ⭕ Radar CALDEIRA DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.1: 🎯 Radar PORTA JUSANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaJusante:
		wasEnabled := pc.radarPortaJusanteEnabled
		pc.radarPortaJusanteEnabled = false
		pc.radarPortaJusanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.1: ⭕ Radar PORTA JUSANTE DESABILITADO via PLC")
		}

	case models.CmdEnableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = true
		if !wasEnabled {
			fmt.Println("PLC Controller v3.1: 🎯 Radar PORTA MONTANTE HABILITADO via PLC")
		}

	case models.CmdDisableRadarPortaMontante:
		wasEnabled := pc.radarPortaMontanteEnabled
		pc.radarPortaMontanteEnabled = false
		pc.radarPortaMontanteConnected = false
		if wasEnabled {
			fmt.Println("PLC Controller v3.1: ⭕ Radar PORTA MONTANTE DESABILITADO via PLC")
		}

	case models.CmdRestartRadarCaldeira:
		if pc.radarCaldeiraEnabled {
			fmt.Println("PLC Controller v3.1: 🔄 Reconexão RADAR CALDEIRA solicitada via PLC")
		}

	case models.CmdRestartRadarPortaJusante:
		if pc.radarPortaJusanteEnabled {
			fmt.Println("PLC Controller v3.1: 🔄 Reconexão RADAR PORTA JUSANTE solicitada via PLC")
		}

	case models.CmdRestartRadarPortaMontante:
		if pc.radarPortaMontanteEnabled {
			fmt.Println("PLC Controller v3.1: 🔄 Reconexão RADAR PORTA MONTANTE solicitada via PLC")
		}

	case models.CmdResetErrorsRadarCaldeira:
		pc.counterMutex.Lock()
		pc.radarCaldeiraErrors = 0
		pc.counterMutex.Unlock()
		fmt.Println("PLC Controller v3.1: 🧹 Erros RADAR CALDEIRA resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaJusante:
		pc.counterMutex.Lock()
		pc.radarPortaJusanteErrors = 0
		pc.counterMutex.Unlock()
		fmt.Println("PLC Controller v3.1: 🧹 Erros RADAR PORTA JUSANTE resetados via comando PLC")

	case models.CmdResetErrorsRadarPortaMontante:
		pc.counterMutex.Lock()
		pc.radarPortaMontanteErrors = 0
		pc.counterMutex.Unlock()
		fmt.Println("PLC Controller v3.1: 🧹 Erros RADAR PORTA MONTANTE resetados via comando PLC")
	}
}

// ✅ THREAD-SAFE: writeSystemStatus SEM MÉTRICAS SERVIDOR - LIMPO
func (pc *PLCController) writeSystemStatus() error {
	pc.stateMutex.RLock()

	status := &models.PLCSystemStatus{
		LiveBit:                     pc.liveBit,
		CollectionActive:            pc.collectionActive,
		SystemHealthy:               pc.isSystemHealthy(),
		EmergencyActive:             pc.emergencyStop,
		RadarCaldeiraConnected:      pc.radarCaldeiraConnected && pc.radarCaldeiraEnabled,
		RadarPortaJusanteConnected:  pc.radarPortaJusanteConnected && pc.radarPortaJusanteEnabled,
		RadarPortaMontanteConnected: pc.radarPortaMontanteConnected && pc.radarPortaMontanteEnabled,
	}

	pc.stateMutex.RUnlock()

	return pc.writer.WriteSystemStatus(status)
}

// ✅ THREAD-SAFE: WriteMultiRadarData com proteção completa
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
			pc.IncrementRadarErrors(radarData.RadarID)

			if pc.isConnectionError(err) {
				pc.stateMutex.Lock()
				pc.needsDB100Reset = true
				pc.stateMutex.Unlock()
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

// ✅ NOVA FUNÇÃO: checkRadarConnectionTimeoutsIntelligent - TIMEOUT INTELIGENTE THREAD-SAFE
func (pc *PLCController) checkRadarConnectionTimeoutsIntelligent() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	// ✅ VERIFICAÇÃO INTELIGENTE - SÓ DESCONECTAR SE REALMENTE SEM DADOS

	// CALDEIRA - Verificação inteligente
	if pc.radarCaldeiraEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarCaldeiraUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarCaldeiraConnected {
			// ✅ VERIFICAÇÃO ADICIONAL - Evitar falsos positivos
			if !pc.isRadarReconnectInProgress("caldeira") {
				fmt.Printf("⚠️ Radar CALDEIRA (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.1: Radar CALDEIRA disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarCaldeiraConnected = false
			}
		}
	}

	// PORTA JUSANTE - Verificação inteligente
	if pc.radarPortaJusanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaJusanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaJusanteConnected {
			if !pc.isRadarReconnectInProgress("porta_jusante") {
				fmt.Printf("⚠️ Radar PORTA JUSANTE (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.1: Radar PORTA JUSANTE disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarPortaJusanteConnected = false
			}
		}
	}

	// PORTA MONTANTE - Verificação inteligente
	if pc.radarPortaMontanteEnabled {
		timeSinceLastUpdate := now.Sub(pc.lastRadarPortaMontanteUpdate)
		if timeSinceLastUpdate > pc.radarTimeoutDuration && pc.radarPortaMontanteConnected {
			if !pc.isRadarReconnectInProgress("porta_montante") {
				fmt.Printf("⚠️ Radar PORTA MONTANTE (HABILITADO): Sem dados há %.1fs (timeout: %v) - marcando como DESCONECTADO\n",
					timeSinceLastUpdate.Seconds(), pc.radarTimeoutDuration)
				log.Printf("INTELLIGENT_TIMEOUT_v3.1: Radar PORTA MONTANTE disconnected after %.1fs", timeSinceLastUpdate.Seconds())
				pc.radarPortaMontanteConnected = false
			}
		}
	}
}

// ✅ FUNÇÃO AUXILIAR THREAD-SAFE: Verificar se radar está em processo de reconexão
func (pc *PLCController) isRadarReconnectInProgress(radarID string) bool {
	// Verificar se houve tentativa de reconexão recente
	lastAttempt, exists := pc.lastRadarReconnectAttempt[radarID]
	if !exists {
		return false
	}

	// Se última tentativa foi há menos de RADAR_RECONNECT_COOLDOWN, considerar como "em progresso"
	return time.Since(lastAttempt) < RADAR_RECONNECT_COOLDOWN
}

// checkRadarConnectionTimeouts - FUNÇÃO LEGADO MANTIDA PARA COMPATIBILIDADE
// ❌ DELETAR ESTE MÉTODO COMPLETO:
// func (pc *PLCController) checkRadarConnectionTimeouts() {
//     pc.checkRadarConnectionTimeoutsIntelligent()
// }

// ✅ THREAD-SAFE: updateRadarConnectionStatus atualiza status de conexão com timestamp
func (pc *PLCController) updateRadarConnectionStatus(radarID string, connected bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	// ✅ ATUALIZAR TIMESTAMP DE RECONEXÃO SE CONECTADO
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

// ✅ THREAD-SAFE: markOperationSuccess marca operação bem-sucedida
func (pc *PLCController) markOperationSuccess() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	pc.consecutiveErrors = 0
	pc.lastSuccessfulOp = time.Now()
}

// ✅ THREAD-SAFE: markOperationError marca erro de operação
func (pc *PLCController) markOperationError(err error) {
	if pc.isConnectionError(err) {
		pc.stateMutex.Lock()
		pc.consecutiveErrors++
		pc.stateMutex.Unlock()
	}
}

// ✅ THREAD-SAFE: isConnectionError verifica se é erro de conexão
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

// ✅ THREAD-SAFE: shouldSkipOperation verifica se deve pular operação por muitos erros
func (pc *PLCController) shouldSkipOperation() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.consecutiveErrors >= pc.maxConsecutiveErrors
}

// ✅ THREAD-SAFE: getConsecutiveErrors para uso interno
func (pc *PLCController) getConsecutiveErrors() int64 {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.consecutiveErrors
}

// ========== MÉTODOS PÚBLICOS THREAD-SAFE ==========

// ✅ THREAD-SAFE: IsCollectionActive
func (pc *PLCController) IsCollectionActive() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.collectionActive && !pc.emergencyStop
}

// ✅ THREAD-SAFE: IsDebugMode
func (pc *PLCController) IsDebugMode() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.debugMode
}

// ✅ THREAD-SAFE: IsEmergencyStop
func (pc *PLCController) IsEmergencyStop() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()
	return pc.emergencyStop
}

// ✅ THREAD-SAFE: IncrementPacketCount
func (pc *PLCController) IncrementPacketCount() {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()
	pc.packetCount++
}

// ✅ THREAD-SAFE: SetRadarConnected
func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarCaldeiraConnected = connected
	if connected {
		pc.lastRadarCaldeiraUpdate = time.Now()
	}
}

// ✅ THREAD-SAFE: SetRadarsConnected
func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()

	if caldeira, exists := status["caldeira"]; exists {
		if pc.radarCaldeiraEnabled && caldeira {
			pc.radarCaldeiraConnected = true
			pc.lastRadarCaldeiraUpdate = now
			// ✅ MARCAR COMO RECONECTADO
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
			// ✅ MARCAR COMO RECONECTADO
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
			// ✅ MARCAR COMO RECONECTADO
			pc.lastRadarReconnectAttempt["porta_montante"] = now
			pc.radarReconnectInProgress["porta_montante"] = false
		} else {
			pc.radarPortaMontanteConnected = false
		}
	}
}

// ✅ THREAD-SAFE: SetRadarConnectedByID
func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.updateRadarConnectionStatus(radarID, connected)
}

// ✅ THREAD-SAFE: IsRadarEnabled
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

// ✅ THREAD-SAFE: GetRadarsEnabled
func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

// ✅ THREAD-SAFE: GetRadarsConnected
func (pc *PLCController) GetRadarsConnected() map[string]bool {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraConnected,
		"porta_jusante":  pc.radarPortaJusanteConnected,
		"porta_montante": pc.radarPortaMontanteConnected,
	}
}

// ✅ THREAD-SAFE: IncrementRadarPackets
func (pc *PLCController) IncrementRadarPackets(radarID string) {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()

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

// ✅ THREAD-SAFE: IncrementRadarErrors
func (pc *PLCController) IncrementRadarErrors(radarID string) {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()

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

// ✅ THREAD-SAFE: incrementErrorCount
func (pc *PLCController) incrementErrorCount() {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()
	pc.errorCount++
}

// ✅ THREAD-SAFE: isSystemHealthy
func (pc *PLCController) isSystemHealthy() bool {
	pc.stateMutex.RLock()
	defer pc.stateMutex.RUnlock()

	pc.counterMutex.RLock()
	defer pc.counterMutex.RUnlock()

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

// ✅ FUNÇÕES AUXILIARES PARA MONITORAMENTO INTELIGENTE THREAD-SAFE

// ✅ THREAD-SAFE: GetRadarTimeoutDuration retorna o timeout atual configurado
func (pc *PLCController) GetRadarTimeoutDuration() time.Duration {
	pc.radarMutex.RLock()
	defer pc.radarMutex.RUnlock()
	return pc.radarTimeoutDuration
}

// ✅ THREAD-SAFE: SetRadarTimeoutDuration permite ajustar o timeout dinamicamente
func (pc *PLCController) SetRadarTimeoutDuration(duration time.Duration) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	oldTimeout := pc.radarTimeoutDuration
	pc.radarTimeoutDuration = duration

	fmt.Printf("🔧 TIMEOUT AJUSTADO v3.1: %v → %v\n", oldTimeout, duration)
	log.Printf("TIMEOUT_ADJUSTMENT_v3.1: Changed from %v to %v", oldTimeout, duration)
}

// ✅ THREAD-SAFE: GetRadarLastUpdate retorna último update de um radar específico
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

// ✅ THREAD-SAFE: IsRadarTimingOut verifica se radar está próximo do timeout
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
	warningThreshold := pc.radarTimeoutDuration * 80 / 100 // 80% do timeout

	return timeSinceUpdate > warningThreshold, timeSinceUpdate
}

// ✅ THREAD-SAFE: GetSystemStatistics retorna estatísticas completas do sistema
func (pc *PLCController) GetSystemStatistics() map[string]interface{} {
	pc.stateMutex.RLock()
	pc.radarMutex.RLock()
	pc.counterMutex.RLock()

	uptime := time.Since(pc.startTime)
	errorRate := float64(0)
	if pc.packetCount > 0 {
		errorRate = float64(pc.errorCount) / float64(pc.packetCount) * 100
	}

	stats := map[string]interface{}{
		"uptime":             uptime.String(),
		"total_packets":      pc.packetCount,
		"total_errors":       pc.errorCount,
		"error_rate_percent": errorRate,
		"consecutive_errors": pc.consecutiveErrors,
		"radar_timeout":      pc.radarTimeoutDuration.String(),
		"system_healthy":     pc.isSystemHealthy(),
		"collection_active":  pc.collectionActive,
		"emergency_stop":     pc.emergencyStop,
		"radars": map[string]interface{}{
			"caldeira": map[string]interface{}{
				"enabled":     pc.radarCaldeiraEnabled,
				"connected":   pc.radarCaldeiraConnected,
				"packets":     pc.radarCaldeiraPackets,
				"errors":      pc.radarCaldeiraErrors,
				"last_update": pc.lastRadarCaldeiraUpdate.Format("2006-01-02 15:04:05"),
			},
			"porta_jusante": map[string]interface{}{
				"enabled":     pc.radarPortaJusanteEnabled,
				"connected":   pc.radarPortaJusanteConnected,
				"packets":     pc.radarPortaJusantePackets,
				"errors":      pc.radarPortaJusanteErrors,
				"last_update": pc.lastRadarPortaJusanteUpdate.Format("2006-01-02 15:04:05"),
			},
			"porta_montante": map[string]interface{}{
				"enabled":     pc.radarPortaMontanteEnabled,
				"connected":   pc.radarPortaMontanteConnected,
				"packets":     pc.radarPortaMontantePackets,
				"errors":      pc.radarPortaMontanteErrors,
				"last_update": pc.lastRadarPortaMontanteUpdate.Format("2006-01-02 15:04:05"),
			},
		},
	}

	pc.counterMutex.RUnlock()
	pc.radarMutex.RUnlock()
	pc.stateMutex.RUnlock()

	return stats
}

// ✅ THREAD-SAFE: MarkRadarReconnectInProgress marca radar como em processo de reconexão
func (pc *PLCController) MarkRadarReconnectInProgress(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarReconnectInProgress[radarID] = true
	pc.lastRadarReconnectAttempt[radarID] = time.Now()

	fmt.Printf("🔄 Radar %s marcado como EM RECONEXÃO v3.1\n", radarID)
	log.Printf("RADAR_RECONNECT_START_v3.1: %s marked as reconnecting", radarID)
}

// ✅ THREAD-SAFE: ClearRadarReconnectInProgress remove flag de reconexão
func (pc *PLCController) ClearRadarReconnectInProgress(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	pc.radarReconnectInProgress[radarID] = false

	fmt.Printf("✅ Radar %s não está mais em reconexão v3.1\n", radarID)
	log.Printf("RADAR_RECONNECT_END_v3.1: %s reconnection flag cleared", radarID)
}

// ✅ THREAD-SAFE: IsAnyRadarReconnecting verifica se algum radar está reconectando
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

// ✅ THREAD-SAFE: GetReconnectingRadars retorna lista de radares em reconexão
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

// ✅ THREAD-SAFE: ForceRadarTimeout força timeout de um radar específico (para testes)
func (pc *PLCController) ForceRadarTimeout(radarID string) {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	// Força timestamp antigo para simular timeout
	oldTime := time.Now().Add(-pc.radarTimeoutDuration - 10*time.Second)

	switch radarID {
	case "caldeira":
		pc.lastRadarCaldeiraUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.1: Radar CALDEIRA forçado ao timeout\n")
	case "porta_jusante":
		pc.lastRadarPortaJusanteUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.1: Radar PORTA JUSANTE forçado ao timeout\n")
	case "porta_montante":
		pc.lastRadarPortaMontanteUpdate = oldTime
		fmt.Printf("🧪 TESTE v3.1: Radar PORTA MONTANTE forçado ao timeout\n")
	}

	log.Printf("FORCE_TIMEOUT_TEST_v3.1: %s forced to timeout state", radarID)
}

// ✅ THREAD-SAFE: ResetAllRadarTimestamps reseta todos os timestamps
func (pc *PLCController) ResetAllRadarTimestamps() {
	pc.radarMutex.Lock()
	defer pc.radarMutex.Unlock()

	now := time.Now()
	pc.lastRadarCaldeiraUpdate = now
	pc.lastRadarPortaJusanteUpdate = now
	pc.lastRadarPortaMontanteUpdate = now

	// Limpar flags de reconexão
	for radarID := range pc.radarReconnectInProgress {
		pc.radarReconnectInProgress[radarID] = false
		pc.lastRadarReconnectAttempt[radarID] = now
	}

	fmt.Println("🔄 TODOS os timestamps de radar resetados v3.1")
	log.Printf("RADAR_TIMESTAMPS_RESET_v3.1: All radar timestamps reset to current time")
}

// ✅ THREAD-SAFE: GetMemoryStats retorna estatísticas de uso de memória
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

// ✅ THREAD-SAFE: GetCounterStats retorna estatísticas dos contadores
func (pc *PLCController) GetCounterStats() map[string]interface{} {
	pc.counterMutex.RLock()
	defer pc.counterMutex.RUnlock()

	return map[string]interface{}{
		"total_packets":             pc.packetCount,
		"total_errors":              pc.errorCount,
		"consecutive_errors":        pc.consecutiveErrors,
		"caldeira_packets":          pc.radarCaldeiraPackets,
		"caldeira_errors":           pc.radarCaldeiraErrors,
		"porta_jusante_packets":     pc.radarPortaJusantePackets,
		"porta_jusante_errors":      pc.radarPortaJusanteErrors,
		"porta_montante_packets":    pc.radarPortaMontantePackets,
		"porta_montante_errors":     pc.radarPortaMontanteErrors,
		"max_packet_count_warning":  MAX_PACKET_COUNT_WARNING,
		"max_packet_count_critical": MAX_PACKET_COUNT_CRITICAL,
		"overflow_protection":       pc.overflowProtectionOn,
	}
}

// ✅ THREAD-SAFE: ForceOverflowProtection força ativação da proteção (para testes)
func (pc *PLCController) ForceOverflowProtection() {
	pc.counterMutex.Lock()
	defer pc.counterMutex.Unlock()

	fmt.Println("🧪 TESTE v3.1: Forçando ativação da proteção de overflow")
	log.Printf("FORCE_OVERFLOW_TEST_v3.1: Manually triggering overflow protection")

	// Simular contadores altos
	pc.packetCount = MAX_PACKET_COUNT_CRITICAL + 1

	// Executar proteção
	pc.executeOverflowProtection()
}

// ✅ THREAD-SAFE: ForceMemoryCleanup força limpeza de memória (para testes)
func (pc *PLCController) ForceMemoryCleanup() {
	fmt.Println("🧪 TESTE v3.1: Forçando limpeza de memória")
	log.Printf("FORCE_MEMORY_CLEANUP_TEST_v3.1: Manually triggering memory cleanup")

	pc.cleanupMapsMemory()
}

// ✅ THREAD-SAFE: GetDetailedSystemHealth retorna saúde detalhada do sistema
func (pc *PLCController) GetDetailedSystemHealth() map[string]interface{} {
	pc.stateMutex.RLock()
	pc.radarMutex.RLock()
	pc.counterMutex.RLock()

	health := map[string]interface{}{
		"overall_healthy":        pc.isSystemHealthy(),
		"collection_active":      pc.collectionActive,
		"emergency_stop":         pc.emergencyStop,
		"plc_reset_in_progress":  pc.plcResetInProgress,
		"consecutive_errors":     pc.consecutiveErrors,
		"max_consecutive_errors": pc.maxConsecutiveErrors,
		"should_skip_operations": pc.consecutiveErrors >= pc.maxConsecutiveErrors,
		"uptime_seconds":         int64(time.Since(pc.startTime).Seconds()),
		"last_successful_op":     pc.lastSuccessfulOp.Format("2006-01-02 15:04:05"),
		"radar_timeout_duration": pc.radarTimeoutDuration.String(),
		"overflow_protection_on": pc.overflowProtectionOn,
		"memory_cleanup_active":  true,
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

	pc.counterMutex.RUnlock()
	pc.radarMutex.RUnlock()
	pc.stateMutex.RUnlock()

	return health
}

// ✅ FUNÇÃO FINAL: String retorna representação string do PLCController
func (pc *PLCController) String() string {
	pc.stateMutex.RLock()
	pc.counterMutex.RLock()

	result := fmt.Sprintf("PLCController v3.1 [Uptime: %v, Packets: %d, Errors: %d, Collection: %v, Emergency: %v]",
		time.Since(pc.startTime),
		pc.packetCount,
		pc.errorCount,
		pc.collectionActive,
		pc.emergencyStop)

	pc.counterMutex.RUnlock()
	pc.stateMutex.RUnlock()

	return result
}
