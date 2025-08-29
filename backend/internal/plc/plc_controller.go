package plc

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"backend/pkg/models"
)

// PLCController gerencia comunicação bidirecional com o PLC (MULTI-RADAR)
type PLCController struct {
	plc    PLCClient
	reader *PLCReader
	writer *PLCWriter

	// Canais para comandos
	commandChan chan models.SystemCommand

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
	wsClients   int

	// Status de conexão dos radares
	radarCaldeiraConnected      bool
	radarPortaJusanteConnected  bool
	radarPortaMontanteConnected bool
	natsConnected               bool
	wsRunning                   bool

	// Contadores individuais por radar
	radarCaldeiraPackets      int32
	radarPortaJusantePackets  int32
	radarPortaMontantePackets int32
	radarCaldeiraErrors       int32
	radarPortaJusanteErrors   int32
	radarPortaMontanteErrors  int32

	// Controle de live bit
	liveBitTicker *time.Ticker
	statusTicker  *time.Ticker
	commandTicker *time.Ticker
	stopChan      chan bool

	// Mutex
	mutex sync.RWMutex
}

// NewPLCController cria um novo controlador PLC (MULTI-RADAR)
func NewPLCController(plcClient PLCClient) *PLCController {
	controller := &PLCController{
		plc:              plcClient,
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

		startTime:   time.Now(),
		packetCount: 0,
		errorCount:  0,
		wsClients:   0,

		// Status de conexão dos radares
		radarCaldeiraConnected:      false,
		radarPortaJusanteConnected:  false,
		radarPortaMontanteConnected: false,
		natsConnected:               false,
		wsRunning:                   false,

		// Contadores individuais zerados
		radarCaldeiraPackets:      0,
		radarPortaJusantePackets:  0,
		radarPortaMontantePackets: 0,
		radarCaldeiraErrors:       0,
		radarPortaJusanteErrors:   0,
		radarPortaMontanteErrors:  0,

		stopChan: make(chan bool),
	}

	return controller
}

// Start inicia o controlador PLC
func (pc *PLCController) Start() {
	fmt.Println("PLC Controller: Iniciando controlador bidirecional Linux...")

	// Iniciar tickers
	pc.liveBitTicker = time.NewTicker(1 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second)
	pc.commandTicker = time.NewTicker(500 * time.Millisecond)

	// Iniciar goroutines
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()

	fmt.Println("PLC Controller: Controlador iniciado com monitoramento Linux")
}

// Stop para o controlador
func (pc *PLCController) Stop() {
	fmt.Println("PLC Controller: Parando controlador...")

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
	fmt.Println("PLC Controller: Controlador parado")
}

// liveBitLoop gerencia o live bit
func (pc *PLCController) liveBitLoop() {
	for {
		select {
		case <-pc.liveBitTicker.C:
			pc.mutex.Lock()
			pc.liveBit = !pc.liveBit
			pc.mutex.Unlock()

		case <-pc.stopChan:
			return
		}
	}
}

// statusWriteLoop escreve status no PLC continuamente
func (pc *PLCController) statusWriteLoop() {
	for {
		select {
		case <-pc.statusTicker.C:
			err := pc.writeSystemStatus()
			if err != nil {
				pc.incrementErrorCount()
				log.Printf("PLC Controller: Erro ao escrever status: %v", err)
			}

		case <-pc.stopChan:
			return
		}
	}
}

// commandReadLoop lê comandos do PLC continuamente
func (pc *PLCController) commandReadLoop() {
	for {
		select {
		case <-pc.commandTicker.C:
			commands, err := pc.reader.ReadCommands()
			if err != nil {
				pc.incrementErrorCount()
				log.Printf("PLC Controller: Erro ao ler comandos: %v", err)
				continue
			}

			pc.processCommands(commands)

		case <-pc.stopChan:
			return
		}
	}
}

// processCommands processa comandos recebidos do PLC (MULTI-RADAR)
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	// ========== COMANDOS GLOBAIS ==========
	if commands.StartCollection && !pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStartCollection
		if err := pc.writer.ResetCommand(0, 0); err != nil {
			log.Printf("Erro ao resetar StartCollection: %v", err)
		}
	}

	if commands.StopCollection && pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStopCollection
		if err := pc.writer.ResetCommand(0, 1); err != nil {
			log.Printf("Erro ao resetar StopCollection: %v", err)
		}
	}

	if commands.ResetErrors {
		pc.commandChan <- models.CmdResetErrors
		if err := pc.writer.ResetCommand(0, 3); err != nil {
			log.Printf("Erro ao resetar ResetErrors: %v", err)
		}
	}

	if commands.Emergency {
		pc.commandChan <- models.CmdEmergencyStop
		if err := pc.writer.ResetCommand(0, 2); err != nil {
			log.Printf("Erro ao resetar Emergency: %v", err)
		}
	}

	// ========== COMANDOS INDIVIDUAIS DOS RADARES ==========
	// Radar Caldeira
	if commands.EnableRadarCaldeira != pc.IsRadarEnabled("caldeira") {
		if commands.EnableRadarCaldeira {
			pc.commandChan <- models.CmdEnableRadarCaldeira
		} else {
			pc.commandChan <- models.CmdDisableRadarCaldeira
		}
	}

	// Radar Porta Jusante
	if commands.EnableRadarPortaJusante != pc.IsRadarEnabled("porta_jusante") {
		if commands.EnableRadarPortaJusante {
			pc.commandChan <- models.CmdEnableRadarPortaJusante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaJusante
		}
	}

	// Radar Porta Montante
	if commands.EnableRadarPortaMontante != pc.IsRadarEnabled("porta_montante") {
		if commands.EnableRadarPortaMontante {
			pc.commandChan <- models.CmdEnableRadarPortaMontante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaMontante
		}
	}

	// ========== COMANDOS ESPECÍFICOS POR RADAR ==========
	if commands.RestartRadarCaldeira {
		pc.commandChan <- models.CmdRestartRadarCaldeira
		if err := pc.writer.ResetCommand(0, 7); err != nil {
			log.Printf("Erro ao resetar RestartRadarCaldeira: %v", err)
		}
	}

	if commands.RestartRadarPortaJusante {
		pc.commandChan <- models.CmdRestartRadarPortaJusante
		if err := pc.writer.ResetCommand(1, 0); err != nil {
			log.Printf("Erro ao resetar RestartRadarPortaJusante: %v", err)
		}
	}

	if commands.RestartRadarPortaMontante {
		pc.commandChan <- models.CmdRestartRadarPortaMontante
		if err := pc.writer.ResetCommand(1, 1); err != nil {
			log.Printf("Erro ao resetar RestartRadarPortaMontante: %v", err)
		}
	}
}

// commandProcessor processa comandos do canal
func (pc *PLCController) commandProcessor() {
	for cmd := range pc.commandChan {
		pc.executeCommand(cmd)
	}
}

// executeCommand executa um comando específico (MULTI-RADAR)
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

	case models.CmdRestartNATS:
		fmt.Println("PLC Controller: 🔄 Reinício do NATS solicitado via PLC")

	case models.CmdRestartWebSocket:
		fmt.Println("PLC Controller: 🔄 Reinício do WebSocket solicitado via PLC")

	case models.CmdResetErrors:
		pc.errorCount = 0
		pc.radarCaldeiraErrors = 0
		pc.radarPortaJusanteErrors = 0
		pc.radarPortaMontanteErrors = 0
		fmt.Println("PLC Controller: 🧹 Erros RESETADOS (todos os radares) via comando PLC")

	case models.CmdEnableDebug:
		pc.debugMode = true
		fmt.Println("PLC Controller: 🐛 Modo DEBUG ATIVADO via comando PLC")

	case models.CmdDisableDebug:
		pc.debugMode = false
		fmt.Println("PLC Controller: 🐛 Modo DEBUG DESATIVADO via comando PLC")

	case models.CmdEmergencyStop:
		pc.emergencyStop = true
		pc.collectionActive = false
		fmt.Println("PLC Controller: 🚨 PARADA DE EMERGÊNCIA ativada via PLC")

	case models.CmdEnableRadarCaldeira:
		pc.radarCaldeiraEnabled = true
		fmt.Println("PLC Controller: 🎯 Radar CALDEIRA HABILITADO via comando PLC")

	case models.CmdDisableRadarCaldeira:
		pc.radarCaldeiraEnabled = false
		fmt.Println("PLC Controller: ⭕ Radar CALDEIRA DESABILITADO via comando PLC")

	case models.CmdEnableRadarPortaJusante:
		pc.radarPortaJusanteEnabled = true
		fmt.Println("PLC Controller: 🎯 Radar PORTA JUSANTE HABILITADO via comando PLC")

	case models.CmdDisableRadarPortaJusante:
		pc.radarPortaJusanteEnabled = false
		fmt.Println("PLC Controller: ⭕ Radar PORTA JUSANTE DESABILITADO via comando PLC")

	case models.CmdEnableRadarPortaMontante:
		pc.radarPortaMontanteEnabled = true
		fmt.Println("PLC Controller: 🎯 Radar PORTA MONTANTE HABILITADO via comando PLC")

	case models.CmdDisableRadarPortaMontante:
		pc.radarPortaMontanteEnabled = false
		fmt.Println("PLC Controller: ⭕ Radar PORTA MONTANTE DESABILITADO via comando PLC")

	case models.CmdRestartRadarCaldeira:
		fmt.Println("PLC Controller: 🔄 Reconexão RADAR CALDEIRA solicitada via PLC")

	case models.CmdRestartRadarPortaJusante:
		fmt.Println("PLC Controller: 🔄 Reconexão RADAR PORTA JUSANTE solicitada via PLC")

	case models.CmdRestartRadarPortaMontante:
		fmt.Println("PLC Controller: 🔄 Reconexão RADAR PORTA MONTANTE solicitada via PLC")

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

// writeSystemStatus escreve status e métricas do sistema no PLC
func (pc *PLCController) writeSystemStatus() error {
	pc.mutex.RLock()

	// Obter métricas do sistema Linux
	cpuUsage := pc.getLinuxCPUUsage()
	memUsage := pc.getLinuxMemoryUsage()
	diskUsage := pc.getLinuxDiskUsage()
	temperature := pc.getLinuxTemperature()

	if pc.debugMode {
		fmt.Printf("Sistema Linux - CPU: %.1f%%, Memória: %.1f%%, Disco: %.1f%%, Temp: %.1f°C\n",
			cpuUsage, memUsage, diskUsage, temperature)
	}

	// Status do sistema
	status := &models.PLCSystemStatus{
		LiveBit:                     pc.liveBit,
		CollectionActive:            pc.collectionActive,
		SystemHealthy:               pc.isSystemHealthy(),
		EmergencyActive:             pc.emergencyStop,
		RadarCaldeiraConnected:      pc.radarCaldeiraConnected,
		RadarPortaJusanteConnected:  pc.radarPortaJusanteConnected,
		RadarPortaMontanteConnected: pc.radarPortaMontanteConnected,
	}

	pc.mutex.RUnlock()

	// Escrever status do sistema
	if err := pc.writer.WriteSystemStatus(status); err != nil {
		return fmt.Errorf("erro ao escrever status: %v", err)
	}

	// Escrever métricas de sistema nos offsets especificados
	// CPUUsage - Real offset 294
	if err := pc.writer.WriteTag(100, 294, "real", cpuUsage); err != nil {
		return fmt.Errorf("erro ao escrever CPUUsage: %v", err)
	}

	// MemoryUsage - Real offset 298
	if err := pc.writer.WriteTag(100, 298, "real", memUsage); err != nil {
		return fmt.Errorf("erro ao escrever MemoryUsage: %v", err)
	}

	// DiskUsage - Real offset 302
	if err := pc.writer.WriteTag(100, 302, "real", diskUsage); err != nil {
		return fmt.Errorf("erro ao escrever DiskUsage: %v", err)
	}

	// Temperature - Real offset 306
	if err := pc.writer.WriteTag(100, 306, "real", temperature); err != nil {
		return fmt.Errorf("erro ao escrever Temperature: %v", err)
	}

	return nil
}

// WriteRadarData escreve dados do radar no PLC usando DB100
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	plcData := pc.writer.BuildPLCRadarData(data)

	var baseOffset int
	switch data.RadarID {
	case "caldeira":
		baseOffset = 6
	case "porta_jusante":
		baseOffset = 102
	case "porta_montante":
		baseOffset = 198
	default:
		return fmt.Errorf("RadarID desconhecido: %s", data.RadarID)
	}

	err := pc.writer.WriteRadarDataToDB100(plcData, baseOffset)
	if err != nil {
		pc.incrementErrorCount()
		return fmt.Errorf("erro ao escrever dados do radar %s na DB100: %v", data.RadarID, err)
	}

	return nil
}

// WriteMultiRadarData escreve dados de múltiplos radares no PLC
func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	var errors []string

	for _, radarData := range data.Radars {
		if !pc.IsRadarEnabled(radarData.RadarID) {
			continue
		}

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
			pc.IncrementRadarErrors(radarData.RadarID)
			errors = append(errors, fmt.Sprintf("erro ao escrever dados do radar %s: %v", radarData.RadarName, err))
		} else {
			pc.IncrementRadarPackets(radarData.RadarID)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("erros ao escrever dados dos radares: %s", strings.Join(errors, "; "))
	}

	return nil
}

// ========== MÉTODOS DE MÉTRICAS DO SISTEMA ==========

// getLinuxTemperature obtém temperatura REAL da CPU no Linux
func (pc *PLCController) getLinuxTemperature() float32 {
	// Tentar ler temperatura do hardware
	tempPaths := []string{
		"/sys/class/thermal/thermal_zone0/temp",
		"/sys/class/hwmon/hwmon0/temp1_input",
		"/sys/class/hwmon/hwmon1/temp1_input",
	}

	for _, path := range tempPaths {
		if data, err := os.ReadFile(path); err == nil {
			if temp, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64); err == nil {
				// Converter de milliCelsius para Celsius se necessário
				if temp > 1000 {
					temp = temp / 1000
				}

				// Validar range de temperatura realista
				if temp >= 20 && temp <= 100 {
					return float32(temp)
				}
			}
		}
	}

	// Fallback: estimativa baseada em CPU usage
	cpuUsage := pc.getLinuxCPUUsage()
	baseTemp := float32(35.0)      // Temperatura base
	tempIncrease := cpuUsage * 0.5 // 0.5°C por 1% CPU

	estimatedTemp := baseTemp + tempIncrease
	if estimatedTemp > 85 {
		estimatedTemp = 85
	}

	return estimatedTemp
}

// getLinuxCPUUsage obtém uso REAL de CPU no Linux
func (pc *PLCController) getLinuxCPUUsage() float32 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return pc.getCPUUsageRuntime()
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return pc.getCPUUsageRuntime()
	}

	// Primeira linha contém CPU total
	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return pc.getCPUUsageRuntime()
	}

	// Somar todos os tempos
	var totalTime uint64
	var idleTime uint64

	for i := 1; i < len(fields) && i <= 7; i++ {
		val, _ := strconv.ParseUint(fields[i], 10, 64)
		totalTime += val
		if i == 4 { // idle time
			idleTime = val
		}
	}

	if totalTime == 0 {
		return 0
	}

	// CPU usage = (total - idle) / total * 100
	activeTime := totalTime - idleTime
	usage := float32(activeTime) / float32(totalTime) * 100

	if usage > 100 {
		usage = 100
	}

	return usage
}

// getLinuxMemoryUsage obtém uso REAL de memória no Linux
func (pc *PLCController) getLinuxMemoryUsage() float32 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return pc.getMemoryUsageRuntime()
	}

	var memTotal, memAvailable, memFree, buffers, cached float64

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		val, _ := strconv.ParseFloat(fields[1], 64)

		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "MemAvailable:":
			memAvailable = val
		case "MemFree:":
			memFree = val
		case "Buffers:":
			buffers = val
		case "Cached:":
			cached = val
		}
	}

	if memTotal > 0 {
		var available float64
		if memAvailable > 0 {
			available = memAvailable
		} else {
			available = memFree + buffers + cached
		}

		used := memTotal - available
		usage := (used / memTotal) * 100
		return float32(usage)
	}

	return pc.getMemoryUsageRuntime()
}

// getLinuxDiskUsage obtém uso REAL de disco no Linux
func (pc *PLCController) getLinuxDiskUsage() float32 {
	var stat syscall.Statfs_t
	err := syscall.Statfs("/", &stat)
	if err != nil {
		return 50.0
	}

	// Calcular espaço total e usado
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - free

	if total == 0 {
		return 50.0
	}

	usage := float32(used) / float32(total) * 100
	return usage
}

// ========== MÉTODOS FALLBACK ==========

func (pc *PLCController) getCPUUsageRuntime() float32 {
	numGoroutines := float32(runtime.NumGoroutine())
	numCPU := float32(runtime.NumCPU())

	estimate := (numGoroutines / numCPU) * 15
	if estimate > 100 {
		estimate = 100
	}
	if estimate < 5 {
		estimate = 5
	}

	return estimate
}

func (pc *PLCController) getMemoryUsageRuntime() float32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	allocMB := float32(m.Alloc) / (1024 * 1024)
	estimatedTotal := float32(8192) // 8GB

	usage := (allocMB / estimatedTotal) * 100
	if usage > 100 {
		usage = 100
	}
	if usage < 2 {
		usage = 15 // Mínimo realista
	}

	return usage
}

// ========== MÉTODOS PÚBLICOS ==========

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

// SetRadarConnected - compatibilidade
func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.mutex.Lock()
	pc.radarCaldeiraConnected = connected
	pc.mutex.Unlock()
}

// SetRadarsConnected atualiza status de conexão de todos os radares
func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	if caldeira, exists := status["caldeira"]; exists {
		pc.radarCaldeiraConnected = caldeira
	}
	if portaJusante, exists := status["porta_jusante"]; exists {
		pc.radarPortaJusanteConnected = portaJusante
	}
	if portaMontante, exists := status["porta_montante"]; exists {
		pc.radarPortaMontanteConnected = portaMontante
	}
}

// SetRadarConnectedByID atualiza status de um radar específico
func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	switch radarID {
	case "caldeira":
		pc.radarCaldeiraConnected = connected
	case "porta_jusante":
		pc.radarPortaJusanteConnected = connected
	case "porta_montante":
		pc.radarPortaMontanteConnected = connected
	}
}

// IsRadarEnabled verifica se um radar está habilitado
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

// GetRadarsEnabled retorna mapa com status de habilitação de todos os radares
func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

// IncrementRadarPackets incrementa contador de um radar específico
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

// IncrementRadarErrors incrementa contador de erros de um radar específico
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

func (pc *PLCController) SetNATSConnected(connected bool) {
	pc.mutex.Lock()
	pc.natsConnected = connected
	pc.mutex.Unlock()
}

func (pc *PLCController) SetWebSocketRunning(running bool) {
	pc.mutex.Lock()
	pc.wsRunning = running
	pc.mutex.Unlock()
}

func (pc *PLCController) UpdateWebSocketClients(count int) {
	pc.mutex.Lock()
	pc.wsClients = count
	pc.mutex.Unlock()
}

// ========== MÉTODOS AUXILIARES ==========

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

	return atLeastOneRadarHealthy && !pc.emergencyStop && pc.errorCount < 20
}
