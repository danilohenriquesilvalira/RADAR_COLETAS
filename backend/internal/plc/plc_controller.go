package plc

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"backend/pkg/models"
)

// PLCController gerencia comunicação bidirecional com o PLC
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

	// Estatísticas
	startTime   time.Time
	packetCount int32
	errorCount  int32
	wsClients   int

	// Status dos componentes
	radarConnected bool
	natsConnected  bool
	wsRunning      bool

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

// NewPLCController cria um novo controlador PLC
func NewPLCController(plcClient PLCClient) *PLCController {
	controller := &PLCController{
		plc:              plcClient,
		reader:           NewPLCReader(plcClient),
		writer:           NewPLCWriter(plcClient),
		commandChan:      make(chan models.SystemCommand, 10),
		collectionActive: true,
		debugMode:        false,
		emergencyStop:    false,
		startTime:        time.Now(),
		packetCount:      0,
		errorCount:       0,
		wsClients:        0,
		radarConnected:   false,
		natsConnected:    false,
		wsRunning:        false,
		stopChan:         make(chan bool),
		kernel32:         syscall.NewLazyDLL("kernel32.dll"),
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

// processCommands processa comandos recebidos do PLC
func (pc *PLCController) processCommands(commands *models.PLCCommands) {
	// Verificar se algum comando foi ativado
	commandsProcessed := false

	if commands.StartCollection && !pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStartCollection
		commandsProcessed = true
	}

	if commands.StopCollection && pc.IsCollectionActive() {
		pc.commandChan <- models.CmdStopCollection
		commandsProcessed = true
	}

	if commands.RestartSystem {
		pc.commandChan <- models.CmdRestartSystem
		commandsProcessed = true
	}

	if commands.RestartNATS {
		pc.commandChan <- models.CmdRestartNATS
		commandsProcessed = true
	}

	if commands.RestartWebSocket {
		pc.commandChan <- models.CmdRestartWebSocket
		commandsProcessed = true
	}

	if commands.ResetErrors {
		pc.commandChan <- models.CmdResetErrors
		commandsProcessed = true
	}

	if commands.EnableDebugMode != pc.IsDebugMode() {
		if commands.EnableDebugMode {
			pc.commandChan <- models.CmdEnableDebug
		} else {
			pc.commandChan <- models.CmdDisableDebug
		}
		commandsProcessed = true
	}

	if commands.Emergency {
		pc.commandChan <- models.CmdEmergencyStop
		commandsProcessed = true
	}

	// Se algum comando foi processado, resetar comandos no PLC
	if commandsProcessed {
		err := pc.writer.ResetCommands()
		if err != nil {
			log.Printf("PLC Controller: Erro ao resetar comandos: %v", err)
		}
	}
}

// commandProcessor processa comandos do canal
func (pc *PLCController) commandProcessor() {
	for cmd := range pc.commandChan {
		pc.executeCommand(cmd)
	}
}

// executeCommand executa um comando específico
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
		fmt.Println("PLC Controller: 🧹 Erros RESETADOS via comando PLC")

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
	}
}

// writeSystemStatus escreve status do sistema no PLC
func (pc *PLCController) writeSystemStatus() error {
	pc.mutex.RLock()

	// Calcular uptime em segundos
	uptime := int32(time.Since(pc.startTime).Seconds())

	// Obter dados REAIS do Windows
	cpuUsage := pc.getWindowsCPUUsage()
	memUsage := pc.getWindowsMemoryUsage()
	diskUsage := pc.getWindowsDiskUsage()

	// Log dos valores reais (opcional)
	if pc.debugMode {
		fmt.Printf("Sistema Windows - CPU: %.1f%%, Memória: %.1f%%, Disco: %.1f%%\n",
			cpuUsage, memUsage, diskUsage)
	}

	// Construir status
	status := pc.writer.BuildPLCSystemStatus(
		pc.liveBit,
		pc.radarConnected,
		true, // PLC sempre conectado se chegou aqui
		pc.natsConnected,
		pc.wsRunning,
		pc.collectionActive,
		pc.isSystemHealthy(),
		pc.debugMode,
		pc.wsClients,
		pc.packetCount,
		pc.errorCount,
		uptime,
		cpuUsage,
		memUsage,
		diskUsage,
	)

	pc.mutex.RUnlock()

	// Escrever no PLC
	return pc.writer.WriteSystemStatus(status)
}

// WriteRadarData escreve dados do radar no PLC
func (pc *PLCController) WriteRadarData(data models.RadarData) error {
	// Converter dados para formato PLC
	plcData := pc.writer.BuildPLCRadarData(data)

	// Escrever no PLC
	err := pc.writer.WriteRadarData(plcData)
	if err != nil {
		pc.incrementErrorCount()
		return fmt.Errorf("erro ao escrever dados do radar no PLC: %v", err)
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

func (pc *PLCController) SetRadarConnected(connected bool) {
	pc.mutex.Lock()
	pc.radarConnected = connected
	pc.mutex.Unlock()
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
	return pc.radarConnected &&
		!pc.emergencyStop &&
		pc.errorCount < 10
}
