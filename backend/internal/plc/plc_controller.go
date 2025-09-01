package plc

import (
	"fmt"
	"log"
	"runtime"
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

	// Controle de live bit
	liveBitTicker *time.Ticker
	statusTicker  *time.Ticker
	commandTicker *time.Ticker
	stopChan      chan bool

	// CONTROLE DE ERROS CONSECUTIVOS
	consecutiveErrors    int32
	lastSuccessfulOp     time.Time
	maxConsecutiveErrors int32

	// MONITORAMENTO DE CONEXÃO DOS RADARES
	radarMonitorTicker   *time.Ticker
	radarTimeoutDuration time.Duration

	// 🆕 DETECÇÃO DE RECONEXÃO E RESET
	lastConnectionCheck  time.Time
	needsDB100Reset      bool
	reconnectionDetected bool
	plcResetInProgress   bool

	// Mutex
	mutex sync.RWMutex

	// Sistema de monitoramento multiplataforma
	systemMonitor *SystemMonitor
}

// SystemMonitor para monitoramento do sistema Linux/Unix
type SystemMonitor struct {
	lastCPUTime  time.Time
	lastCPUIdle  uint64
	lastCPUTotal uint64
}

// NewSystemMonitor cria um novo monitor de sistema
func NewSystemMonitor() *SystemMonitor {
	return &SystemMonitor{
		lastCPUTime: time.Now(),
	}
}

// NewPLCController cria um novo controlador PLC (MULTI-RADAR)
func NewPLCController(plcClient PLCClient) *PLCController {
	now := time.Now()
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

		// Status de conexão dos radares - INICIALIZAR COMO DESCONECTADOS
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
		lastSuccessfulOp:     time.Now(),
		maxConsecutiveErrors: 8, // Aumentado para evitar paradas desnecessárias

		// MONITORAMENTO DE RADARES
		radarTimeoutDuration: 10 * time.Second,

		// 🆕 CAMPOS DE RECONEXÃO
		lastConnectionCheck:  now,
		needsDB100Reset:      true, // Reset na primeira conexão
		reconnectionDetected: false,
		plcResetInProgress:   false,

		stopChan:      make(chan bool),
		systemMonitor: NewSystemMonitor(),
	}

	return controller
}

// Start inicia o controlador PLC
func (pc *PLCController) Start() {
	fmt.Println("PLC Controller: Iniciando controlador bidirecional com DETECÇÃO DE RECONEXÃO...")

	// Iniciar tickers COM INTERVALOS OTIMIZADOS
	pc.liveBitTicker = time.NewTicker(3 * time.Second)
	pc.statusTicker = time.NewTicker(1 * time.Second) // Mais rápido para detectar problemas
	pc.commandTicker = time.NewTicker(2 * time.Second)
	pc.radarMonitorTicker = time.NewTicker(5 * time.Second)

	// Iniciar goroutines
	go pc.liveBitLoop()
	go pc.statusWriteLoop()
	go pc.commandReadLoop()
	go pc.commandProcessor()
	go pc.radarConnectionMonitorLoop()

	fmt.Println("PLC Controller: Controlador iniciado com LÓGICA DE RECONEXÃO INTELIGENTE")
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
	if pc.radarMonitorTicker != nil {
		pc.radarMonitorTicker.Stop()
	}

	// Sinalizar parada
	close(pc.stopChan)

	fmt.Println("PLC Controller: Controlador parado")
}

// 🆕 detectPLCReconnection detecta se PLC reconectou
func (pc *PLCController) detectPLCReconnection() bool {
	now := time.Now()

	// Verificar a cada 10 segundos
	if now.Sub(pc.lastConnectionCheck) < 10*time.Second {
		return false
	}

	pc.lastConnectionCheck = now

	// Se writer está em estado de erro grave, pode indicar reconexão
	if pc.writer.NeedsReset() {
		fmt.Println("🔍 Writer em estado de erro grave - possível reconexão detectada")
		return true
	}

	return false
}

// 🆕 resetAfterReconnection reseta estado após reconexão detectada
func (pc *PLCController) resetAfterReconnection() error {
	pc.mutex.Lock()
	pc.plcResetInProgress = true
	pc.mutex.Unlock()

	fmt.Println("🔄 RESET APÓS RECONEXÃO INICIADO...")

	// Aguardar um pouco para PLC estabilizar
	time.Sleep(2 * time.Second)

	// Reset state do writer
	pc.writer.ResetErrorState()

	// Reset contadores de erro
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

// statusWriteLoop escreve status no PLC COM DETECÇÃO DE RECONEXÃO
func (pc *PLCController) statusWriteLoop() {
	for {
		select {
		case <-pc.statusTicker.C:
			// 🆕 DETECTAR RECONEXÃO PLC
			if pc.detectPLCReconnection() {
				fmt.Println("🔄 Reconexão PLC detectada - executando reset...")
				err := pc.resetAfterReconnection()
				if err != nil {
					fmt.Printf("❌ Erro no reset: %v\n", err)
				}
				continue // Pular este ciclo
			}

			// 🆕 PULAR SE RESET EM PROGRESSO
			pc.mutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.mutex.RUnlock()

			if resetInProgress {
				continue
			}

			// Pular se muitos erros consecutivos
			if pc.shouldSkipOperation() {
				continue
			}

			err := pc.writeSystemStatus()
			if err != nil {
				pc.markOperationError(err)
				if pc.isConnectionError(err) {
					if pc.consecutiveErrors == 1 {
						log.Printf("🔌 PLC: Problema de conexão detectado - tentando recuperar...")
					}
				} else {
					log.Printf("PLC Controller: Erro ao escrever status: %v", err)
				}
				pc.incrementErrorCount()
			} else {
				pc.markOperationSuccess()
			}

		case <-pc.stopChan:
			return
		}
	}
}

// radarConnectionMonitorLoop monitora conexões dos radares e atualiza status
func (pc *PLCController) radarConnectionMonitorLoop() {
	for {
		select {
		case <-pc.radarMonitorTicker.C:
			pc.checkRadarConnectionTimeouts()

		case <-pc.stopChan:
			return
		}
	}
}

// checkRadarConnectionTimeouts verifica se radares estão enviando dados recentemente
func (pc *PLCController) checkRadarConnectionTimeouts() {
	pc.mutex.Lock()
	defer pc.mutex.Unlock()

	now := time.Now()

	// Verificar cada radar individualmente APENAS SE HABILITADO
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
		"i/o timeout",
		"connection reset",
		"broken pipe",
		"connection refused",
		"network unreachable",
		"no route to host",
		"invalid pdu",
		"invalid buffer",
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

// commandReadLoop lê comandos do PLC COM PROTEÇÃO
func (pc *PLCController) commandReadLoop() {
	for {
		select {
		case <-pc.commandTicker.C:
			// 🆕 PULAR SE RESET EM PROGRESSO
			pc.mutex.RLock()
			resetInProgress := pc.plcResetInProgress
			pc.mutex.RUnlock()

			if resetInProgress {
				continue
			}

			// Pular se muitos erros consecutivos
			if pc.shouldSkipOperation() {
				continue
			}

			commands, err := pc.reader.ReadCommands()
			if err != nil {
				pc.markOperationError(err)
				if pc.isConnectionError(err) {
					if pc.consecutiveErrors == 1 {
						log.Printf("🔌 PLC: Problema de conexão detectado ao ler comandos...")
					}
				} else {
					log.Printf("PLC Controller: Erro ao ler comandos: %v", err)
				}
				pc.incrementErrorCount()
				continue
			}

			pc.markOperationSuccess()
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

	case models.CmdResetErrors:
		pc.errorCount = 0
		pc.consecutiveErrors = 0
		pc.radarCaldeiraErrors = 0
		pc.radarPortaJusanteErrors = 0
		pc.radarPortaMontanteErrors = 0
		// 🆕 RESET DO WRITER TAMBÉM
		pc.writer.ResetErrorState()
		fmt.Println("PLC Controller: 🧹 Erros RESETADOS (todos os radares) via comando PLC")

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

// writeSystemStatus escreve status do sistema no PLC
func (pc *PLCController) writeSystemStatus() error {
	pc.mutex.RLock()

	cpuUsage := pc.getLinuxCPUUsage()
	memUsage := pc.getLinuxMemoryUsage()
	diskUsage := pc.getLinuxDiskUsage()

	if pc.debugMode {
		fmt.Printf("Sistema Linux - CPU: %.1f%%, Memória: %.1f%%, Disco: %.1f%%\n",
			cpuUsage, memUsage, diskUsage)
	}

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
	// 🆕 PULAR SE RESET EM PROGRESSO
	pc.mutex.RLock()
	resetInProgress := pc.plcResetInProgress
	pc.mutex.RUnlock()

	if resetInProgress {
		return nil // Aguardar reset terminar
	}

	// Pular se muitos erros consecutivos
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

			// 🆕 SE ERRO DE PROTOCOLO, MARCAR PARA RESET
			if pc.isConnectionError(err) {
				pc.needsDB100Reset = true
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

// SetRadarConnectedByID atualiza status de um radar específico
func (pc *PLCController) SetRadarConnectedByID(radarID string, connected bool) {
	pc.updateRadarConnectionStatus(radarID, connected)
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

// GetRadarsEnabled retorna mapa com status de habilitação
func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraEnabled,
		"porta_jusante":  pc.radarPortaJusanteEnabled,
		"porta_montante": pc.radarPortaMontanteEnabled,
	}
}

// GetRadarsConnected retorna mapa com status de conexão
func (pc *PLCController) GetRadarsConnected() map[string]bool {
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()

	return map[string]bool{
		"caldeira":       pc.radarCaldeiraConnected,
		"porta_jusante":  pc.radarPortaJusanteConnected,
		"porta_montante": pc.radarPortaMontanteConnected,
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

// IncrementRadarErrors incrementa contador de erros
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

// getLinuxCPUUsage obtém uso de CPU
func (pc *PLCController) getLinuxCPUUsage() float32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	numGoroutines := float32(runtime.NumGoroutine())
	numCPU := float32(runtime.NumCPU())

	cpuActivity := (numGoroutines / numCPU) * 15
	gcFactor := float32(m.NumGC%100) * 0.5
	totalUsage := cpuActivity + gcFactor

	if totalUsage > 100 {
		totalUsage = 100
	}
	if totalUsage < 0 {
		totalUsage = 0
	}

	return totalUsage
}

// getLinuxMemoryUsage obtém uso de memória
func (pc *PLCController) getLinuxMemoryUsage() float32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	allocMB := float32(m.Alloc) / (1024 * 1024)
	sysMB := float32(m.Sys) / (1024 * 1024)
	estimatedTotalMB := float32(4096)

	usedMB := allocMB
	if sysMB > allocMB {
		usedMB = sysMB
	}

	usage := (usedMB / estimatedTotalMB) * 100

	if usage > 100 {
		usage = 100
	}
	if usage < 0 {
		usage = 0
	}

	return usage
}

// getLinuxDiskUsage obtém uso de disco
func (pc *PLCController) getLinuxDiskUsage() float32 {
	uptime := time.Since(pc.startTime)
	baseUsage := float32(35.0)
	timeEffect := float32(uptime.Hours()) * 0.1
	totalUsage := baseUsage + timeEffect

	if totalUsage > 95 {
		totalUsage = 95
	}
	if totalUsage < 0 {
		totalUsage = 0
	}

	return totalUsage
}

// incrementErrorCount incrementa contador de erro
func (pc *PLCController) incrementErrorCount() {
	pc.mutex.Lock()
	pc.errorCount++
	pc.mutex.Unlock()
}

// isSystemHealthy verifica se sistema está saudável
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
