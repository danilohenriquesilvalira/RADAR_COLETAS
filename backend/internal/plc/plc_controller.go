package plc

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

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
	radarCaldeiraEnabled     bool
	radarPortaJusanteEnabled bool
	radarPortaMontanteEnabled bool

	// Estatísticas
	startTime   time.Time
	packetCount int32
	errorCount  int32
	wsClients   int

	// Status de conexão dos radares
	radarCaldeiraConnected     bool
	radarPortaJusanteConnected bool
	radarPortaMontanteConnected bool
	natsConnected              bool
	wsRunning                  bool

	// Contadores individuais por radar
	radarCaldeiraPackets     int32
	radarPortaJusantePackets int32
	radarPortaMontantePackets int32
	radarCaldeiraErrors      int32
	radarPortaJusanteErrors  int32
	radarPortaMontanteErrors int32

	// Controle de live bit
	liveBitTicker *time.Ticker
	statusTicker  *time.Ticker
	commandTicker *time.Ticker
	stopChan      chan bool

	// Para monitoramento Windows
	kernel32 *syscall.LazyDLL

	// Mutex
	mutex sync.RWMutex
}

// Estruturas para Windows API
type MEMORYSTATUSEX struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

type FILETIME struct {
	dwLowDateTime  uint32
	dwHighDateTime uint32
}

// NewPLCController cria um novo controlador PLC (MULTI-RADAR)
func NewPLCController(plcClient PLCClient) *PLCController {
	controller := &PLCController{
		plc:              plcClient,
		reader:           NewPLCReader(plcClient),
		writer:           NewPLCWriter(plcClient),
		commandChan:      make(chan models.SystemCommand, 20), // Aumentado para mais comandos
		collectionActive: true,
		debugMode:        false,
		emergencyStop:    false,
		
		// Inicializar radares como habilitados por padrão
		radarCaldeiraEnabled:     true,
		radarPortaJusanteEnabled: true,
		radarPortaMontanteEnabled: true,
		
		startTime:   time.Now(),
		packetCount: 0,
		errorCount:  0,
		wsClients:   0,
		
		// Status de conexão dos radares
		radarCaldeiraConnected:     false,
		radarPortaJusanteConnected: false,
		radarPortaMontanteConnected: false,
		natsConnected:              false,
		wsRunning:                  false,
		
		// Contadores individuais zerados
		radarCaldeiraPackets:     0,
		radarPortaJusantePackets: 0,
		radarPortaMontantePackets: 0,
		radarCaldeiraErrors:      0,
		radarPortaJusanteErrors:  0,
		radarPortaMontanteErrors: 0,
		
		stopChan: make(chan bool),
		kernel32: syscall.NewLazyDLL("kernel32.dll"),
	}

	return controller
}

// Start inicia o controlador PLC
func (pc *PLCController) Start() {
	fmt.Println("PLC Controller: Iniciando controlador bidirecional...")

	// Iniciar tickers
	pc.liveBitTicker = time.NewTicker(1 * time.Second)        // Live bit a cada 1s
	pc.statusTicker = time.NewTicker(1 * time.Second)         // Status a cada 1s
	pc.commandTicker = time.NewTicker(500 * time.Millisecond) // Comandos a cada 500ms

	// Iniciar goroutines
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()

	fmt.Println("PLC Controller: Controlador iniciado com monitoramento Windows")
}

// Stop para o controlador
func (pc *PLCController) Stop() {
	fmt.Println("PLC Controller: Parando controlador...")

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

	// Sinalizar parada
	close(pc.stopChan)

	fmt.Println("PLC Controller: Controlador parado")
}

// liveBitLoop gerencia o live bit
func (pc *PLCController) liveBitLoop() {
	for {
		select {
		case <-pc.liveBitTicker.C:
			pc.mutex.Lock()
			pc.liveBit = !pc.liveBit // Toggle do live bit
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
		// Resetar apenas este comando específico
		if err := pc.writer.ResetCommand(0, 0); err != nil {
			log.Printf("Erro ao resetar StartCollection: %v", err)
		}
	}

	if commands.StopCollection && pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStopCollection
		// Resetar apenas este comando específico
		if err := pc.writer.ResetCommand(0, 1); err != nil {
			log.Printf("Erro ao resetar StopCollection: %v", err)
		}
	}

	if commands.ResetErrors {
		pc.commandChan <- models.CmdResetErrors
		// Resetar apenas este comando específico
		if err := pc.writer.ResetCommand(0, 3); err != nil {
			log.Printf("Erro ao resetar ResetErrors: %v", err)
		}
	}

	if commands.Emergency {
		pc.commandChan <- models.CmdEmergencyStop
		// Resetar apenas este comando específico
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
		// NÃO resetar o enable - deve persistir o estado
	}

	// Radar Porta Jusante
	if commands.EnableRadarPortaJusante != pc.IsRadarEnabled("porta_jusante") {
		if commands.EnableRadarPortaJusante {
			pc.commandChan <- models.CmdEnableRadarPortaJusante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaJusante
		}
		// NÃO resetar o enable - deve persistir o estado
	}

	// Radar Porta Montante
	if commands.EnableRadarPortaMontante != pc.IsRadarEnabled("porta_montante") {
		if commands.EnableRadarPortaMontante {
			pc.commandChan <- models.CmdEnableRadarPortaMontante
		} else {
			pc.commandChan <- models.CmdDisableRadarPortaMontante
		}
		// NÃO resetar o enable - deve persistir o estado
	}

	// ========== COMANDOS ESPECÍFICOS POR RADAR ==========
	if commands.RestartRadarCaldeira {
		pc.commandChan <- models.CmdRestartRadarCaldeira
		// Resetar apenas este comando específico
		if err := pc.writer.ResetCommand(0, 7); err != nil {
			log.Printf("Erro ao resetar RestartRadarCaldeira: %v", err)
		}
	}

	if commands.RestartRadarPortaJusante {
		pc.commandChan <- models.CmdRestartRadarPortaJusante
		// Resetar apenas este comando específico
		if err := pc.writer.ResetCommand(1, 0); err != nil {
			log.Printf("Erro ao resetar RestartRadarPortaJusante: %v", err)
		}
	}

	if commands.RestartRadarPortaMontante {
		pc.commandChan <- models.CmdRestartRadarPortaMontante
		// Resetar apenas este comando específico
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
	// ========== COMANDOS GLOBAIS ==========
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

	// ========== COMANDOS INDIVIDUAIS DOS RADARES ==========
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

	// ========== COMANDOS ESPECÍFICOS POR RADAR ==========
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

// writeSystemStatus escreve status do sistema no PLC
func (pc *PLCController) writeSystemStatus() error {
	pc.mutex.RLock()


	// Obter dados REAIS do Windows
	cpuUsage := pc.getWindowsCPUUsage()
	memUsage := pc.getWindowsMemoryUsage()
	diskUsage := pc.getWindowsDiskUsage()

	// Log dos valores reais (opcional)
	if pc.debugMode {
		fmt.Printf("Sistema Windows - CPU: %.1f%%, Memória: %.1f%%, Disco: %.1f%%\n",
			cpuUsage, memUsage, diskUsage)
	}

	// Construir status simplificado para DB100
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

	// Escrever no PLC
	return pc.writer.WriteSystemStatus(status)
}

// WriteRadarData escreve dados do radar no PLC usando DB100
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	// Converter dados para formato PLC
	plcData := pc.writer.BuildPLCRadarData(data)

	// Determinar offset na DB100 baseado no RadarID
	var baseOffset int
	switch data.RadarID {
	case "caldeira":
		baseOffset = 6 // DB100.6
	case "porta_jusante": 
		baseOffset = 102 // DB100.102
	case "porta_montante":
		baseOffset = 198 // DB100.198
	default:
		return fmt.Errorf("RadarID desconhecido: %s", data.RadarID)
	}

	// Escrever na DB100 no offset correto
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
		// Verificar se o radar está habilitado
		if !pc.IsRadarEnabled(radarData.RadarID) {
			continue // Pular radares desabilitados
		}
		
		// Converter dados para formato PLC
		plcData := pc.writer.BuildPLCRadarData(radarData)
		
		// Determinar offset na DB100 baseado no RadarID
		var baseOffset int
		switch radarData.RadarID {
		case "caldeira":
			baseOffset = 6 // DB100.6
		case "porta_jusante":
			baseOffset = 102 // DB100.102
		case "porta_montante":
			baseOffset = 198 // DB100.198
		default:
			continue // ID desconhecido
		}
		
		// Escrever na DB100 no offset correto
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

// ========== MÉTODOS PÚBLICOS PARA CONTROLE EXTERNO ==========

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

// ========== MÉTODOS PARA MÚLTIPLOS RADARES ==========

// SetRadarConnected - compatibilidade com código antigo
func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.mutex.Lock()
	pc.radarCaldeiraConnected = connected // Para compatibilidade
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
		"caldeira":      pc.radarCaldeiraEnabled,
		"porta_jusante": pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

// IncrementRadarPackets incrementa contador de um radar específico
func (pc *PLCController) IncrementRadarPackets(radarID string) {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()
	
	pc.packetCount++ // Contador global
	
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
	
	pc.errorCount++ // Contador global
	
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

// ========== MÉTODOS COM DADOS REAIS DO WINDOWS ==========

// getWindowsCPUUsage obtém uso REAL de CPU no Windows
func (pc *PLCController) getWindowsCPUUsage() float32 {
	// Método 1: Usar GetSystemTimes (mais preciso)
	cpuUsage := pc.getCPUUsageViaSystemTimes()
	if cpuUsage >= 0 {
		return cpuUsage
	}

	// Método 2: Fallback usando runtime stats
	return pc.getCPUUsageViaRuntime()
}

// getCPUUsageViaSystemTimes usa GetSystemTimes do Windows
func (pc *PLCController) getCPUUsageViaSystemTimes() float32 {
	getSystemTimes := pc.kernel32.NewProc("GetSystemTimes")

	var idleTime1, kernelTime1, userTime1 FILETIME
	var idleTime2, kernelTime2, userTime2 FILETIME

	// Primeira medição
	ret, _, _ := getSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleTime1)),
		uintptr(unsafe.Pointer(&kernelTime1)),
		uintptr(unsafe.Pointer(&userTime1)),
	)

	if ret == 0 {
		return -1 // Erro
	}

	// Aguardar um pouco
	time.Sleep(100 * time.Millisecond)

	// Segunda medição
	ret, _, _ = getSystemTimes.Call(
		uintptr(unsafe.Pointer(&idleTime2)),
		uintptr(unsafe.Pointer(&kernelTime2)),
		uintptr(unsafe.Pointer(&userTime2)),
	)

	if ret == 0 {
		return -1 // Erro
	}

	// Calcular diferenças
	idle1 := uint64(idleTime1.dwHighDateTime)<<32 + uint64(idleTime1.dwLowDateTime)
	idle2 := uint64(idleTime2.dwHighDateTime)<<32 + uint64(idleTime2.dwLowDateTime)

	kernel1 := uint64(kernelTime1.dwHighDateTime)<<32 + uint64(kernelTime1.dwLowDateTime)
	kernel2 := uint64(kernelTime2.dwHighDateTime)<<32 + uint64(kernelTime2.dwLowDateTime)

	user1 := uint64(userTime1.dwHighDateTime)<<32 + uint64(userTime1.dwLowDateTime)
	user2 := uint64(userTime2.dwHighDateTime)<<32 + uint64(userTime2.dwLowDateTime)

	idleDiff := idle2 - idle1
	totalDiff := (kernel2 - kernel1) + (user2 - user1)

	if totalDiff == 0 {
		return 0
	}

	// CPU usage = (total - idle) / total * 100
	cpuUsage := float32(totalDiff-idleDiff) / float32(totalDiff) * 100

	return cpuUsage
}

// getCPUUsageViaRuntime método fallback usando runtime
func (pc *PLCController) getCPUUsageViaRuntime() float32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Estimativa baseada em goroutines e GC
	numGoroutines := float32(runtime.NumGoroutine())
	numCPU := float32(runtime.NumCPU())

	// Fator baseado em atividade
	cpuEstimate := (numGoroutines / numCPU) * 10

	// Adicionar fator de GC
	gcFactor := float32(m.NumGC) * 0.1

	totalEstimate := cpuEstimate + gcFactor

	// Limitar entre 0 e 100
	if totalEstimate > 100 {
		totalEstimate = 100
	}
	if totalEstimate < 0 {
		totalEstimate = 0
	}

	return totalEstimate
}

// getWindowsMemoryUsage obtém uso REAL de memória no Windows
func (pc *PLCController) getWindowsMemoryUsage() float32 {
	globalMemoryStatusEx := pc.kernel32.NewProc("GlobalMemoryStatusEx")

	var memStatus MEMORYSTATUSEX
	memStatus.dwLength = uint32(unsafe.Sizeof(memStatus))

	ret, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&memStatus)))

	if ret != 0 {
		// Sucesso - usar dados reais do Windows
		return float32(memStatus.dwMemoryLoad)
	}

	// Fallback: usar runtime.MemStats
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Estimar baseado na aplicação (menos preciso)
	allocMB := float32(m.Alloc) / (1024 * 1024)

	// Assumir sistema de 8GB como base para cálculo percentual
	estimatedTotal := float32(8192) // 8GB em MB

	return (allocMB / estimatedTotal) * 100
}

// getWindowsDiskUsage obtém uso REAL de disco no Windows
func (pc *PLCController) getWindowsDiskUsage() float32 {
	getDiskFreeSpaceEx := pc.kernel32.NewProc("GetDiskFreeSpaceExW")

	// Obter informações do disco C:
	pathPtr, _ := syscall.UTF16PtrFromString("C:\\")

	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64

	ret, _, _ := getDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(&freeBytesAvailable)),
		uintptr(unsafe.Pointer(&totalNumberOfBytes)),
		uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
	)

	if ret != 0 && totalNumberOfBytes > 0 {
		// Sucesso - calcular percentual usado
		usedBytes := totalNumberOfBytes - totalNumberOfFreeBytes
		usagePercent := float32(usedBytes) / float32(totalNumberOfBytes) * 100
		return usagePercent
	}

	// Fallback: retornar valor estimado
	return 45.0
}

// ========== MÉTODOS AUXILIARES PRIVADOS ==========

func (pc *PLCController) incrementErrorCount() {
	pc.mutex.Lock()
	pc.errorCount++
	pc.mutex.Unlock()
}

func (pc *PLCController) isSystemHealthy() bool {
	// Sistema está saudável se:
	// - Pelo menos 1 radar habilitado está conectado
	// - Não está em parada de emergência  
	// - Contador de erros não está muito alto
	
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
	
	return atLeastOneRadarHealthy &&
		!pc.emergencyStop &&
		pc.errorCount < 20 // Aumentado para múltiplos radares
}
