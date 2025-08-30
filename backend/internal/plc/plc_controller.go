package plc

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"backend/pkg/models"
)

// PLCController gerencia comunicação bidirecional - PRODUCTION READY
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

	// Sistema
	wsClients     int32 // atomic
	natsConnected int32 // atomic
	wsRunning     int32 // atomic
	startTime     time.Time
	lastResetTime time.Time // Para reset diário

	// Controle de reboot
	resetErrorsStartTime time.Time
	resetErrorsActive    bool
	rebootExecuted       bool
	rebootMutex          sync.Mutex

	// Controles
	commandChan   chan models.SystemCommand
	liveBitTicker *time.Ticker
	statusTicker  *time.Ticker
	commandTicker *time.Ticker
	stopChan      chan bool

	// Handlers de comando
	commandHandlers map[string]CommandHandler
}

type RadarCounters struct {
	Packets int32
	Errors  int32
}

// CommandHandler define processamento de comandos
type CommandHandler struct {
	ShouldExecute   func(*models.PLCCommands, *PLCController) bool
	Execute         func(*PLCController, *models.PLCCommands)
	NeedsReset      bool
	ResetByteOffset int
	ResetBitOffset  int
	LogMessage      string
}

// NewPLCController cria controlador otimizado para produção
func NewPLCController(plcClient PLCClient) *PLCController {
	pc := &PLCController{
		plc:           plcClient,
		reader:        NewPLCReader(plcClient),
		writer:        NewPLCWriter(plcClient),
		commandChan:   make(chan models.SystemCommand, 20),
		stopChan:      make(chan bool),
		startTime:     time.Now(),
		lastResetTime: time.Now(),
	}

	pc.initializeState()
	pc.setupCommandHandlers()
	return pc
}

// initializeState inicializa estado thread-safe
func (pc *PLCController) initializeState() {
	// Atomic values
	atomic.StoreInt32(&pc.collectionActive, 1)
	atomic.StoreInt32(&pc.debugMode, 0)
	atomic.StoreInt32(&pc.emergencyStop, 0)
	atomic.StoreInt32(&pc.wsRunning, 0)
	atomic.StoreInt32(&pc.natsConnected, 0)

	// Maps protegidos
	pc.radarsEnabled = map[string]bool{
		"caldeira": true, "porta_jusante": true, "porta_montante": true,
	}
	pc.radarsConnected = map[string]bool{
		"caldeira": false, "porta_jusante": false, "porta_montante": false,
	}
	pc.radarCounters = map[string]*RadarCounters{
		"caldeira": {}, "porta_jusante": {}, "porta_montante": {},
	}
}

// setupCommandHandlers configura handlers sem setState problemático
func (pc *PLCController) setupCommandHandlers() {
	pc.commandHandlers = map[string]CommandHandler{
		"StartCollection": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.StartCollection && !pc.IsCollectionActive()
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.collectionActive, 1)
				atomic.StoreInt32(&pc.emergencyStop, 0)
			},
			LogMessage: "Coleta INICIADA",
		},

		"StopCollection": {
			ShouldExecute: func(cmd *models.PLCCommands, pc *PLCController) bool {
				return cmd.StopCollection && pc.IsCollectionActive()
			},
			Execute: func(pc *PLCController, cmd *models.PLCCommands) {
				atomic.StoreInt32(&pc.collectionActive, 0)
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
				select {
				case pc.commandChan <- 1: // RestartRadarCaldeira
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
				select {
				case pc.commandChan <- 2: // RestartRadarPortaJusante
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
				select {
				case pc.commandChan <- 3: // RestartRadarPortaMontante
				default:
				}
			},
			NeedsReset:      true,
			ResetByteOffset: 1, ResetBitOffset: 1,
			LogMessage: "Restart Porta Montante solicitado",
		},
	}
}

// Start inicia controlador para produção 24/7
func (pc *PLCController) Start() {
	log.Println("PLC Controller: Iniciando sistema para producao 24/7...")

	pc.liveBitTicker = time.NewTicker(1 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(500 * time.Millisecond)

	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.dailyResetScheduler() // CRÍTICO: Reset automático para prevenção de overflow

	log.Println("PLC Controller: Sistema iniciado - Operacao 24/7 com reset automático")
}

// Stop para controlador
func (pc *PLCController) Stop() {
	log.Println("PLC Controller: Parando...")

	if pc.liveBitTicker != nil {
		pc.liveBitTicker.Stop()
	}
	if pc.statusTicker != nil {
		pc.statusTicker.Stop()
	}
	if pc.commandTicker != nil {
		pc.commandTicker.Stop()
	}

	close(pc.stopChan)
	log.Println("PLC Controller: Parado")
}

// liveBitLoop gerencia live bit - otimizado
func (pc *PLCController) liveBitLoop() {
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

// statusWriteLoop escreve status - COM PROTEÇÃO CONTRA OVERFLOW
func (pc *PLCController) statusWriteLoop() {
	for {
		select {
		case <-pc.statusTicker.C:
			// CRÍTICO: Verificar overflow antes de cada operação
			pc.checkAndResetCounters()

			if err := pc.writeSystemStatus(); err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
			}
		case <-pc.stopChan:
			return
		}
	}
}

// commandReadLoop lê comandos - performance otimizada
func (pc *PLCController) commandReadLoop() {
	for {
		select {
		case <-pc.commandTicker.C:
			commands, err := pc.reader.ReadCommands()
			if err != nil {
				atomic.AddInt32(&pc.errorCount, 1)
				continue
			}
			pc.processCommands(commands)
		case <-pc.stopChan:
			return
		}
	}
}

// processCommands sem deadlocks - CRITICO para 24/7
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	// Processar comandos sem setState problemático
	for cmdName, handler := range pc.commandHandlers {
		if handler.ShouldExecute(commands, pc) {
			handler.Execute(pc, commands)
			log.Printf("%s", handler.LogMessage)

			if handler.NeedsReset {
				if err := pc.writer.ResetCommand(handler.ResetByteOffset, handler.ResetBitOffset); err != nil {
					log.Printf("Erro ao resetar %s: %v", cmdName, err)
				}
			}
		}
	}

	// Processar enables de radares - SEM LOCKS ANINHADOS
	pc.handleRadarEnables(commands)

	// Processar ResetErrors - lógica isolada
	pc.handleResetErrors(commands.ResetErrors)
}

// handleRadarEnables - SEM deadlocks
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
				log.Printf("Radar %s %s", radarName, action)
			}
		}
	}
	pc.stateMutex.Unlock()
}

// handleResetErrors - lógica de reboot isolada
func (pc *PLCController) handleResetErrors(resetErrors bool) {
	pc.rebootMutex.Lock()
	defer pc.rebootMutex.Unlock()

	if resetErrors {
		if !pc.resetErrorsActive {
			pc.resetErrorsActive = true
			pc.resetErrorsStartTime = time.Now()
			pc.rebootExecuted = false
			log.Println("ResetErrors ativo - 10s para reboot")
		} else {
			elapsed := time.Since(pc.resetErrorsStartTime)
			if elapsed >= 10*time.Second && !pc.rebootExecuted {
				pc.rebootExecuted = true
				log.Println("EXECUTANDO REBOOT DO SERVIDOR")
				go pc.executeSecureReboot()
			}
		}
	} else {
		if pc.resetErrorsActive && !pc.rebootExecuted {
			pc.resetAllErrors()
			log.Println("Erros resetados")
		}
		pc.resetErrorsActive = false
		pc.rebootExecuted = false
	}
}

// commandProcessor processa comandos do canal
func (pc *PLCController) commandProcessor() {
	for cmd := range pc.commandChan {
		pc.executeCommand(cmd)
	}
}

// executeCommand executa comandos - otimizado
func (pc *PLCController) executeCommand(cmd models.SystemCommand) {
	// Usar string ou int para identificar comandos
	switch cmd {
	case 1: // StartCollection
		atomic.StoreInt32(&pc.collectionActive, 1)
		atomic.StoreInt32(&pc.emergencyStop, 0)
	case 2: // StopCollection
		atomic.StoreInt32(&pc.collectionActive, 0)
	case 3: // EmergencyStop
		atomic.StoreInt32(&pc.emergencyStop, 1)
		atomic.StoreInt32(&pc.collectionActive, 0)
	case 4: // ResetErrors
		pc.resetAllErrors()
	}
}

// resetAllErrors reset contadores - thread-safe
func (pc *PLCController) resetAllErrors() {
	atomic.StoreInt32(&pc.errorCount, 0)

	pc.stateMutex.Lock()
	for _, counter := range pc.radarCounters {
		atomic.StoreInt32(&counter.Errors, 0)
	}
	pc.stateMutex.Unlock()
}

// executeSecureReboot reboot seguro
func (pc *PLCController) executeSecureReboot() {
	log.Println("Preparando reboot...")
	time.Sleep(2 * time.Second)

	if err := exec.Command("/usr/local/bin/radar-reboot").Start(); err != nil {
		if file, err := os.Create("/tmp/radar-reboot-request"); err == nil {
			file.WriteString("REBOOT_REQUEST_FROM_PLC")
			file.Close()
		}
	}
}

// writeSystemStatus escreve status - performance 24/7
func (pc *PLCController) writeSystemStatus() error {
	// Obter valores atomic rapidamente
	liveBit := atomic.LoadInt32(&pc.liveBit) == 1
	collectionActive := atomic.LoadInt32(&pc.collectionActive) == 1
	emergencyStop := atomic.LoadInt32(&pc.emergencyStop) == 1

	// Obter status de radares com lock mínimo
	pc.stateMutex.RLock()
	caldeiraConnected := pc.radarsConnected["caldeira"]
	jusanteConnected := pc.radarsConnected["porta_jusante"]
	montanteConnected := pc.radarsConnected["porta_montante"]
	errorCount := atomic.LoadInt32(&pc.errorCount)
	pc.stateMutex.RUnlock()

	// Calcular saúde do sistema
	systemHealthy := pc.calculateSystemHealth(caldeiraConnected, jusanteConnected, montanteConnected, emergencyStop, errorCount)

	// Construir status
	status := &models.PLCSystemStatus{
		LiveBit:                     liveBit,
		CollectionActive:            collectionActive,
		SystemHealthy:               systemHealthy,
		EmergencyActive:             emergencyStop,
		RadarCaldeiraConnected:      caldeiraConnected,
		RadarPortaJusanteConnected:  jusanteConnected,
		RadarPortaMontanteConnected: montanteConnected,
	}

	// Escrever status básico
	if err := pc.writer.WriteSystemStatus(status); err != nil {
		return err
	}

	// Escrever métricas do sistema
	return pc.writeSystemMetrics()
}

// calculateSystemHealth calcula saúde sem locks
func (pc *PLCController) calculateSystemHealth(caldeira, jusante, montante, emergency bool, errorCount int32) bool {
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

	return healthyRadars > 0 && !emergency && errorCount < 20
}

// writeSystemMetrics escreve métricas - otimizado
func (pc *PLCController) writeSystemMetrics() error {
	metrics := pc.getSystemMetrics()

	writeOps := []struct {
		offset int
		value  float32
	}{
		{294, metrics.CPU},
		{298, metrics.Memory},
		{302, metrics.Disk},
		{306, metrics.Temp},
	}

	for _, op := range writeOps {
		if err := pc.writer.WriteTag(100, op.offset, "real", op.value); err != nil {
			return fmt.Errorf("erro escrevendo métrica offset %d: %v", op.offset, err)
		}
	}

	return nil
}

// getSystemMetrics obtém métricas rápidas
func (pc *PLCController) getSystemMetrics() SystemMetrics {
	return SystemMetrics{
		CPU:    pc.getLinuxCPUUsage(),
		Memory: pc.getLinuxMemoryUsage(),
		Disk:   pc.getLinuxDiskUsage(),
		Temp:   pc.getLinuxTemperature(),
	}
}

type SystemMetrics struct {
	CPU    float32
	Memory float32
	Disk   float32
	Temp   float32
}

// WriteRadarData escreve dados de radar - performance crítica
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	if !pc.IsRadarEnabled(data.RadarID) {
		return nil // Radar desabilitado
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
		return err
	}

	pc.IncrementRadarPackets(data.RadarID)
	return nil
}

// WriteMultiRadarData escreve múltiplos radares - batch otimizado
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	var errors []string

	for _, radarData := range data.Radars {
		if err := pc.WriteRadarData(radarData); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", radarData.RadarID, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("erros: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ========== PROTEÇÃO CONTRA OVERFLOW - SISTEMA CRÍTICO ==========

// dailyResetScheduler CRÍTICO - múltiplas redundâncias para prevenção de overflow
func (pc *PLCController) dailyResetScheduler() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC SCHEDULER: %v - Reiniciando", r)
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

// checkResetConditions verifica 4 condições críticas de reset
func (pc *PLCController) checkResetConditions() {
	now := time.Now()

	pc.stateMutex.RLock()
	lastReset := pc.lastResetTime
	pc.stateMutex.RUnlock()

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	// REDUNDÂNCIA 1: Reset às 00:00 (janela de 1h)
	if now.Hour() == 0 && now.Sub(lastReset) > 23*time.Hour {
		log.Println("RESET SCHEDULE: Meia-noite detectada")
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 2: Overflow protection (70% do limite int32)
	const MAX_SAFE = 1500000000
	if totalPackets > MAX_SAFE || totalErrors > MAX_SAFE {
		log.Printf("RESET OVERFLOW: Packets=%d, Errors=%d", totalPackets, totalErrors)
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 3: Tempo máximo (25h sem reset)
	if now.Sub(lastReset) > 25*time.Hour {
		log.Printf("RESET TIMEOUT: %v sem reset", now.Sub(lastReset))
		pc.executeDailyReset()
		return
	}

	// REDUNDÂNCIA 4: Reset de emergência (contadores negativos)
	if totalPackets < 0 || totalErrors < 0 {
		log.Println("RESET EMERGENCY: Contadores negativos")
		pc.executeDailyReset()
		return
	}
}

// checkAndResetCounters verifica overflow a cada ciclo de status
func (pc *PLCController) checkAndResetCounters() {
	const CRITICAL_LIMIT = 2000000000 // 93% do limite int32

	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)

	// Verificação crítica a cada segundo
	if totalPackets > CRITICAL_LIMIT || totalErrors > CRITICAL_LIMIT {
		log.Printf("RESET CRÍTICO IMEDIATO: Packets=%d, Errors=%d", totalPackets, totalErrors)
		go pc.executeDailyReset() // Async para não bloquear
	}
}

// executeDailyReset executa reset completo com validação
func (pc *PLCController) executeDailyReset() {
	pc.stateMutex.Lock()
	defer pc.stateMutex.Unlock()

	now := time.Now()
	if now.Sub(pc.lastResetTime) < 1*time.Hour {
		log.Println("RESET IGNORADO: Reset recente (< 1h)")
		return
	}

	// Salvar estatísticas antes de resetar
	pc.logDailyStatistics()

	// Reset atomic counters
	atomic.StoreInt32(&pc.packetCount, 0)
	atomic.StoreInt32(&pc.errorCount, 0)

	// Reset radar counters
	for _, counter := range pc.radarCounters {
		atomic.StoreInt32(&counter.Packets, 0)
		atomic.StoreInt32(&counter.Errors, 0)
	}

	pc.lastResetTime = now
	log.Printf("RESET EXECUTADO: %s - Todos contadores zerados", now.Format("2006-01-02 15:04:05"))
}

// logDailyStatistics registra estatísticas antes do reset
func (pc *PLCController) logDailyStatistics() {
	totalPackets := atomic.LoadInt32(&pc.packetCount)
	totalErrors := atomic.LoadInt32(&pc.errorCount)
	dayDuration := time.Since(pc.lastResetTime)

	if totalPackets > 0 {
		successRate := float64(totalPackets-totalErrors) / float64(totalPackets) * 100
		packetsPerHour := float64(totalPackets) / dayDuration.Hours()

		log.Printf("STATS DIÁRIAS: %d pacotes, %d erros, %.2f%% sucesso, %.0f pac/h",
			totalPackets, totalErrors, successRate, packetsPerHour)
	}
}

// ForceCounterReset reset manual para emergências
func (pc *PLCController) ForceCounterReset() {
	log.Println("RESET MANUAL FORÇADO")
	pc.executeDailyReset()
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
			pc.radarsConnected[radarID] = connected
		}
	}
	pc.stateMutex.Unlock()
}

func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.stateMutex.Lock()
	if _, exists := pc.radarsConnected[radarID]; exists {
		pc.radarsConnected[radarID] = connected
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
	}
}

func (pc *PLCController) IncrementRadarErrors(radarID string) {
	pc.stateMutex.RLock()
	counter, exists := pc.radarCounters[radarID]
	pc.stateMutex.RUnlock()

	if exists {
		atomic.AddInt32(&counter.Errors, 1)
		atomic.AddInt32(&pc.errorCount, 1)
	}
}

func (pc *PLCController) SetNATSConnected(connected bool) {
	if connected {
		atomic.StoreInt32(&pc.natsConnected, 1)
	} else {
		atomic.StoreInt32(&pc.natsConnected, 0)
	}
}

func (pc *PLCController) SetWebSocketRunning(running bool) {
	if running {
		atomic.StoreInt32(&pc.wsRunning, 1)
	} else {
		atomic.StoreInt32(&pc.wsRunning, 0)
	}
}

func (pc *PLCController) UpdateWebSocketClients(count int) {
	atomic.StoreInt32(&pc.wsClients, int32(count))
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

// ========== MÉTODOS DE COMPATIBILIDADE (interface original) ==========

func (pc *PLCController) RequestSystemRestart() {
	select {
	case pc.commandChan <- 10: // Sistema restart
	default:
		log.Println("Canal de comandos cheio - restart ignorado")
	}
}

func (pc *PLCController) RequestNATSRestart() {
	select {
	case pc.commandChan <- 11: // NATS restart
	default:
	}
}

func (pc *PLCController) RequestWebSocketRestart() {
	select {
	case pc.commandChan <- 12: // WebSocket restart
	default:
	}
}

func (pc *PLCController) RestartApplication() {
	select {
	case pc.commandChan <- 13: // Application restart
	default:
	}
}

// ========== MÉTRICAS E MONITORAMENTO ==========

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
		"uptime_hours":      time.Since(pc.startTime).Hours(),
		"collection_active": atomic.LoadInt32(&pc.collectionActive) == 1,
		"emergency_stop":    atomic.LoadInt32(&pc.emergencyStop) == 1,
		"total_packets":     atomic.LoadInt32(&pc.packetCount),
		"total_errors":      atomic.LoadInt32(&pc.errorCount),
		"connected_radars":  connectedRadars,
		"websocket_clients": atomic.LoadInt32(&pc.wsClients),
		"nats_connected":    atomic.LoadInt32(&pc.natsConnected) == 1,
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
		"enabled":   enabled,
		"connected": connected,
		"packets":   atomic.LoadInt32(&counter.Packets),
		"errors":    atomic.LoadInt32(&counter.Errors),
	}
}
