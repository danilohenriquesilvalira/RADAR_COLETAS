package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"backend/internal/plc"
	"backend/internal/radar"
)

var (
	// Loggers especializados
	systemLogger   *log.Logger
	errorLogger    *log.Logger
	radarLogger    *log.Logger
	plcLogger      *log.Logger
	networkLogger  *log.Logger
	criticalLogger *log.Logger

	// Arquivos de log
	systemLogFile   *os.File
	errorLogFile    *os.File
	radarLogFile    *os.File
	plcLogFile      *os.File
	networkLogFile  *os.File
	criticalLogFile *os.File

	// Estados anteriores para detectar mudanças
	lastRadarStates map[string]bool
	lastPLCState    bool
)

// Configuração de log rotation
const (
	MAX_LOG_FILES = 7  // Manter apenas 7 dias de logs
	MAX_LOG_SIZE  = 10 // 10MB por arquivo (rotacionar se passar)
)

// Estrutura para métricas do sistema
type SystemMetrics struct {
	StartTime          time.Time
	PLCConnections     int64
	PLCDisconnections  int64
	RadarReconnections map[string]int64
	TotalPackets       int64
	TotalErrors        int64
	NetworkErrors      int64
	LastUpdate         time.Time
}

var metrics *SystemMetrics

func main() {
	// PANIC RECOVERY
	defer func() {
		if r := recover(); r != nil {
			timestamp := time.Now().Format("2006-01-02 15:04:05")

			if criticalLogger != nil {
				criticalLogger.Printf("🔥 CRASH CRÍTICO: %s - %v", timestamp, r)
				criticalLogger.Printf("Stack Trace: %s", string(debug.Stack()))
			}

			fmt.Printf("\n🔥 CRASH DETECTADO: %s - Erro: %v\n", timestamp, r)
			closeAllLogs()
			os.Exit(1)
		}
	}()

	setupGracefulShutdown()

	// APLICAR LOG ROTATION ANTES DE INICIALIZAR
	cleanupOldLogs()

	// Inicializar logs usando SUA estrutura de pastas
	initLogsInYourStructure()
	defer closeAllLogs()

	// Iniciar goroutine de log rotation
	go logRotationWorker()

	initMetrics()
	initStates()
	printSystemHeader()

	// LOG INICIAL APENAS
	systemLogger.Println("========== SISTEMA RADAR SICK INICIADO ==========")
	systemLogger.Printf("Usuário: %s | Data: %s | Versão: v2.1.0",
		getCurrentUser(), time.Now().Format("2006-01-02 15:04:05"))
	systemLogger.Printf("LOG_ROTATION: Ativo - máximo %d dias, %dMB por arquivo", MAX_LOG_FILES, MAX_LOG_SIZE)

	// Criar gerenciador de radares
	radarManager := radar.NewRadarManager()
	addRadarsToManager(radarManager)

	// Variáveis de controle PLC
	var plcSiemens *plc.SiemensPLC
	var plcController *plc.PLCController
	plcConnected := false
	lastPLCAttempt := time.Time{}
	consecutivePLCErrors := 0

	// Função de reconexão PLC
	tryReconnectPLC := func() bool {
		now := time.Now()

		if plcConnected && plcSiemens != nil && plcSiemens.IsConnected() {
			return true
		}

		if now.Sub(lastPLCAttempt) < 12*time.Second {
			return false
		}

		lastPLCAttempt = now

		// Cleanup
		if plcController != nil {
			plcController.Stop()
			time.Sleep(300 * time.Millisecond)
			plcController = nil
		}
		if plcSiemens != nil && plcSiemens.IsConnected() {
			plcSiemens.Disconnect()
			metrics.PLCDisconnections++
		}

		// Nova conexão
		plcSiemens = plc.NewSiemensPLC("192.168.1.33")
		err := plcSiemens.Connect()
		if err != nil {
			consecutivePLCErrors++

			// LOG SÓ QUANDO MUDA DE ESTADO OU ERRO CRÍTICO
			if consecutivePLCErrors == 1 {
				errorLogger.Printf("PLC_CONNECTION_LOST: 192.168.1.33 - %v", err)
				networkLogger.Printf("NETWORK_FAILURE: PLC unreachable - %v", err)

				// Análise específica só em caso crítico
				if strings.Contains(err.Error(), "no route to host") {
					networkLogger.Printf("NETWORK_DIAGNOSIS: Sem rota para PLC - verificar switch/cabo")
				} else if strings.Contains(err.Error(), "connection refused") {
					networkLogger.Printf("NETWORK_DIAGNOSIS: PLC offline ou firewall bloqueando")
				}
			}

			metrics.NetworkErrors++
			plcConnected = false
			lastPLCState = false
			return false
		}

		if !plcSiemens.IsConnected() {
			consecutivePLCErrors++
			plcConnected = false
			return false
		}

		// ✅ PLC CONTROLLER COM RECOVERY
		plcController = plc.NewPLCController(plcSiemens.Client)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					criticalLogger.Printf("🔥 PLC_CONTROLLER_PANIC: %v", r)
					criticalLogger.Printf("PLC_STACK: %s", string(debug.Stack()))
					errorLogger.Printf("PLC_GOROUTINE_CRASH: PLCController falhou - %v", r)
					// Sistema continua funcionando, apenas reinicia PLC
					plcConnected = false
				}
			}()
			plcController.Start()
		}()

		time.Sleep(1500 * time.Millisecond)

		consecutivePLCErrors = 0
		plcConnected = true
		metrics.PLCConnections++

		// LOG SÓ QUANDO RECONECTA APÓS FALHA
		if !lastPLCState {
			plcLogger.Printf("PLC_RECONNECTED: Conectado após %d tentativas", consecutivePLCErrors)
			systemLogger.Printf("PLC_RECOVERY: Sistema PLC restaurado")
		}

		lastPLCState = true
		return true
	}

	// Primeira conexão
	tryReconnectPLC()
	time.Sleep(2 * time.Second)

	enabledRadars := getInitialRadarStates(plcConnected, plcController)
	connectEnabledRadars(radarManager, enabledRadars)

	systemLogger.Println("SYSTEM_READY: Loop principal iniciado")

	// Loop principal
	lastReconnectCheck := time.Now()
	lastMetricsUpdate := time.Now()

	for {
		metrics.LastUpdate = time.Now()

		// PLC
		plcConnected = tryReconnectPLC()

		// Estados
		collectionActive := true
		var currentEnabledRadars map[string]bool

		if plcConnected && plcController != nil {
			collectionActive = plcController.IsCollectionActive()
			currentEnabledRadars = plcController.GetRadarsEnabled()

			// LOG SÓ EMERGENCY STOP (CRÍTICO)
			if plcController.IsEmergencyStop() {
				criticalLogger.Println("🚨 EMERGENCY_STOP: Parada de emergência ativada via PLC")
				errorLogger.Println("SYSTEM_HALT: Sistema pausado por emergência")
				time.Sleep(3 * time.Second)
				continue
			}

			// Reconexão de radares
			if time.Since(lastReconnectCheck) >= 15*time.Second {
				radarManager.CheckAndReconnectEnabled(currentEnabledRadars)
				lastReconnectCheck = time.Now()
			}
		} else {
			currentEnabledRadars = map[string]bool{
				"caldeira":       false,
				"porta_jusante":  false,
				"porta_montante": false,
			}
		}

		if !collectionActive {
			time.Sleep(1 * time.Second)
			continue
		}

		// Coleta
		multiRadarData := radarManager.CollectEnabledRadarsData(currentEnabledRadars)
		metrics.TotalPackets++

		// PLC Update
		if plcConnected && plcController != nil {
			connectionStatus := radarManager.GetConnectionStatus()
			err := plcController.WriteMultiRadarData(multiRadarData)
			if err != nil {
				if isConnectionError(err) {
					// LOG SÓ QUANDO PERDE CONEXÃO
					plcLogger.Printf("PLC_WRITE_FAILURE: Conexão perdida durante escrita")
					errorLogger.Printf("PLC_COMMUNICATION_ERROR: %v", err)
					networkLogger.Printf("CONNECTION_INTERRUPTED: Durante operação PLC")
					plcConnected = false
					metrics.PLCDisconnections++
					metrics.NetworkErrors++
				} else {
					// LOG ERROS DE ESCRITA
					errorLogger.Printf("PLC_DB_WRITE_ERROR: Falha ao escrever DB100 - %v", err)
					metrics.TotalErrors++
				}
			}
			plcController.SetRadarsConnected(connectionStatus)
		}

		// LOG INTELIGENTE DE RADARES - SÓ MUDANÇAS DE ESTADO
		if time.Since(lastMetricsUpdate) >= 5*time.Second {
			displaySystemStatus(plcConnected, currentEnabledRadars, radarManager)

			logRadarStateChanges(radarManager, currentEnabledRadars)

			flushAllLogs()
			lastMetricsUpdate = time.Now()
		}

		time.Sleep(1 * time.Second)
	}
}

// 🔄 LOG ROTATION WORKER - RODA EM BACKGROUND
func logRotationWorker() {
	ticker := time.NewTicker(1 * time.Hour) // Verifica a cada hora
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Verificar tamanho dos arquivos atuais
			checkLogFileSizes()

			// Limpar logs antigos
			cleanupOldLogs()

			// Log da manutenção
			if systemLogger != nil {
				systemLogger.Printf("LOG_ROTATION: Manutenção automática executada")
			}
		}
	}
}

// 🧹 LIMPEZA DE LOGS ANTIGOS
func cleanupOldLogs() {
	logFolders := []string{
		"backend/logs/system",
		"backend/logs/radar",
		"backend/logs/plc",
		"backend/logs/critical",
		"backend/logs/error",
		"backend/logs/network",
	}

	cutoffDate := time.Now().AddDate(0, 0, -MAX_LOG_FILES)

	for _, folder := range logFolders {
		files, err := filepath.Glob(filepath.Join(folder, "*.log"))
		if err != nil {
			continue
		}

		for _, file := range files {
			fileInfo, err := os.Stat(file)
			if err != nil {
				continue
			}

			// Se arquivo é mais antigo que MAX_LOG_FILES dias, deletar
			if fileInfo.ModTime().Before(cutoffDate) {
				os.Remove(file)
				fmt.Printf("🗑️  Log antigo removido: %s\n", file)
			}
		}
	}

	// Mover logs antigos para archive se necessário
	moveOldLogsToArchive()
}

// 📦 MOVER LOGS ANTIGOS PARA ARCHIVE
func moveOldLogsToArchive() {
	archiveDir := "backend/logs/archive"
	if _, err := os.Stat(archiveDir); os.IsNotExist(err) {
		os.MkdirAll(archiveDir, 0755)
	}

	logFolders := []string{
		"backend/logs/system",
		"backend/logs/radar",
		"backend/logs/plc",
		"backend/logs/critical",
		"backend/logs/error",
		"backend/logs/network",
	}

	cutoffDate := time.Now().AddDate(0, 0, -3) // Mover logs de mais de 3 dias

	for _, folder := range logFolders {
		files, err := filepath.Glob(filepath.Join(folder, "*.log"))
		if err != nil {
			continue
		}

		for _, file := range files {
			fileInfo, err := os.Stat(file)
			if err != nil {
				continue
			}

			// Se arquivo tem mais de 3 dias, mover para archive
			if fileInfo.ModTime().Before(cutoffDate) {
				fileName := filepath.Base(file)
				archivePath := filepath.Join(archiveDir, fileName)

				// Só move se não existir no archive
				if _, err := os.Stat(archivePath); os.IsNotExist(err) {
					os.Rename(file, archivePath)
				}
			}
		}
	}
}

// 📏 VERIFICAR TAMANHO DOS ARQUIVOS ATUAIS
func checkLogFileSizes() {
	logFiles := []*os.File{
		systemLogFile,
		errorLogFile,
		radarLogFile,
		plcLogFile,
		networkLogFile,
		criticalLogFile,
	}

	for _, file := range logFiles {
		if file == nil {
			continue
		}

		fileInfo, err := file.Stat()
		if err != nil {
			continue
		}

		// Se arquivo > MAX_LOG_SIZE MB, rotacionar
		sizeMB := fileInfo.Size() / (1024 * 1024)
		if sizeMB > MAX_LOG_SIZE {
			rotateLogFile(file)
		}
	}
}

// 🔄 ROTACIONAR ARQUIVO ESPECÍFICO
func rotateLogFile(file *os.File) {
	if file == nil {
		return
	}

	fileName := file.Name()

	// Fechar arquivo atual
	file.Close()

	// Criar nome com timestamp
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	rotatedName := strings.Replace(fileName, ".log", fmt.Sprintf("_%s.log", timestamp), 1)

	// Renomear arquivo atual
	os.Rename(fileName, rotatedName)

	// Recriar arquivo
	newFile, err := os.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return
	}

	// Atualizar logger correspondente
	updateLoggerFile(fileName, newFile)

	fmt.Printf("🔄 Log rotacionado: %s -> %s\n", fileName, rotatedName)
}

// 🔄 ATUALIZAR LOGGER APÓS ROTAÇÃO
func updateLoggerFile(fileName string, newFile *os.File) {
	if strings.Contains(fileName, "system") {
		systemLogFile = newFile
		systemLogger = log.New(systemLogFile, "[SYSTEM] ", log.LstdFlags|log.Lmicroseconds)
	} else if strings.Contains(fileName, "error") {
		errorLogFile = newFile
		errorLogger = log.New(errorLogFile, "[ERROR] ", log.LstdFlags|log.Lmicroseconds)
	} else if strings.Contains(fileName, "radar") {
		radarLogFile = newFile
		radarLogger = log.New(radarLogFile, "[RADAR] ", log.LstdFlags|log.Lmicroseconds)
	} else if strings.Contains(fileName, "plc") {
		plcLogFile = newFile
		plcLogger = log.New(plcLogFile, "[PLC] ", log.LstdFlags|log.Lmicroseconds)
	} else if strings.Contains(fileName, "network") {
		networkLogFile = newFile
		networkLogger = log.New(networkLogFile, "[NETWORK] ", log.LstdFlags|log.Lmicroseconds)
	} else if strings.Contains(fileName, "critical") {
		criticalLogFile = newFile
		criticalLogger = log.New(criticalLogFile, "[CRITICAL] ", log.LstdFlags|log.Lmicroseconds)
	}
}

// LOG INTELIGENTE - SÓ MUDANÇAS DE ESTADO DOS RADARES
func logRadarStateChanges(radarManager *radar.RadarManager, currentEnabledRadars map[string]bool) {
	connectionStatus := radarManager.GetConnectionStatus()

	for radarID, enabled := range currentEnabledRadars {
		config, _ := radarManager.GetRadarConfig(radarID)

		// Estado atual
		currentState := enabled && connectionStatus[radarID]

		// Estado anterior
		lastState, exists := lastRadarStates[radarID]

		// LOG SÓ SE MUDOU O ESTADO
		if !exists || lastState != currentState {
			if enabled {
				if connectionStatus[radarID] {
					if !exists || !lastState {
						radarLogger.Printf("RADAR_ONLINE: %s (%s) conectado", config.Name, config.IP)
					}
				} else {
					if !exists || lastState {
						radarLogger.Printf("RADAR_OFFLINE: %s (%s) perdeu conexão", config.Name, config.IP)
						errorLogger.Printf("RADAR_CONNECTION_LOST: %s - verificar rede", config.Name)
						networkLogger.Printf("RADAR_UNREACHABLE: %s %s:2111", config.Name, config.IP)

						// Diagnóstico específico
						if config.IP == "192.168.1.84" {
							networkLogger.Printf("DIAGNOSIS: Radar Caldeira offline - verificar switch porta")
						} else if config.IP == "192.168.1.85" {
							networkLogger.Printf("DIAGNOSIS: Radar Porta Jusante offline")
						} else if config.IP == "192.168.1.86" {
							networkLogger.Printf("DIAGNOSIS: Radar Porta Montante offline")
						}
					}
				}
			}

			// Atualizar estado anterior
			lastRadarStates[radarID] = currentState
		}
	}
}

// Inicializar estados anteriores
func initStates() {
	lastRadarStates = make(map[string]bool)
	lastPLCState = false
}

// Inicializar logs usando SUA estrutura de pastas
func initLogsInYourStructure() {
	dateStr := time.Now().Format("2006-01-02")

	// Verificar se as pastas existem, se não, criar
	folders := []string{
		"backend/logs/system",
		"backend/logs/radar",
		"backend/logs/plc",
		"backend/logs/critical",
		"backend/logs/error",
		"backend/logs/network",
		"backend/logs/archive",
	}

	for _, folder := range folders {
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			os.MkdirAll(folder, 0755)
		}
	}

	var err error

	// SYSTEM LOG
	systemLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/system/system_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro system log: %v", err)
	}
	systemLogger = log.New(systemLogFile, "[SYSTEM] ", log.LstdFlags|log.Lmicroseconds)

	// RADAR LOG
	radarLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/radar/radar_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro radar log: %v", err)
	}
	radarLogger = log.New(radarLogFile, "[RADAR] ", log.LstdFlags|log.Lmicroseconds)

	// PLC LOG
	plcLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/plc/plc_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro plc log: %v", err)
	}
	plcLogger = log.New(plcLogFile, "[PLC] ", log.LstdFlags|log.Lmicroseconds)

	// CRITICAL LOG
	criticalLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/critical/critical_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro critical log: %v", err)
	}
	criticalLogger = log.New(criticalLogFile, "[CRITICAL] ", log.LstdFlags|log.Lmicroseconds)

	// ERROR LOG
	errorLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/error/error_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro error log: %v", err)
	}
	errorLogger = log.New(errorLogFile, "[ERROR] ", log.LstdFlags|log.Lmicroseconds)

	// NETWORK LOG
	networkLogFile, err = os.OpenFile(fmt.Sprintf("backend/logs/network/network_%s.log", dateStr),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro network log: %v", err)
	}
	networkLogger = log.New(networkLogFile, "[NETWORK] ", log.LstdFlags|log.Lmicroseconds)

	fmt.Printf("📝 Logs com ROTATION criados:\n")
	fmt.Printf("   📊 Sistema:   backend/logs/system/system_%s.log\n", dateStr)
	fmt.Printf("   📡 Radar:    backend/logs/radar/radar_%s.log\n", dateStr)
	fmt.Printf("   🎛️  PLC:      backend/logs/plc/plc_%s.log\n", dateStr)
	fmt.Printf("   🚨 Critical: backend/logs/critical/critical_%s.log\n", dateStr)
	fmt.Printf("   ❌ Error:    backend/logs/error/error_%s.log\n", dateStr)
	fmt.Printf("   🌐 Network:  backend/logs/network/network_%s.log\n", dateStr)
	fmt.Printf("   🔄 Rotation: %d dias, %dMB max por arquivo\n", MAX_LOG_FILES, MAX_LOG_SIZE)

	// Log inicial MÍNIMO
	systemLogger.Println("========== LOGS COM ROTATION - SÓ FALHAS E ALARMES ==========")
	radarLogger.Println("========== MUDANÇAS DE ESTADO DOS RADARES ==========")
	plcLogger.Println("========== EVENTOS DO PLC ==========")
	criticalLogger.Println("========== ALARMES CRÍTICOS ==========")
	errorLogger.Println("========== FALHAS DO SISTEMA ==========")
	networkLogger.Println("========== PROBLEMAS DE REDE ==========")
}

// Flush de todos os logs
func flushAllLogs() {
	if systemLogFile != nil {
		systemLogFile.Sync()
	}
	if radarLogFile != nil {
		radarLogFile.Sync()
	}
	if plcLogFile != nil {
		plcLogFile.Sync()
	}
	if criticalLogFile != nil {
		criticalLogFile.Sync()
	}
	if errorLogFile != nil {
		errorLogFile.Sync()
	}
	if networkLogFile != nil {
		networkLogFile.Sync()
	}
}

// Fechar todos os logs
func closeAllLogs() {
	uptime := time.Since(metrics.StartTime)

	// Log final MÍNIMO
	if systemLogger != nil {
		systemLogger.Printf("SYSTEM_SHUTDOWN: Uptime=%v, Erros=%d", uptime, metrics.TotalErrors)
		systemLogger.Println("========== SISTEMA ENCERRADO ==========")
	}

	// Fechar arquivos
	if systemLogFile != nil {
		systemLogFile.Close()
	}
	if errorLogFile != nil {
		errorLogFile.Close()
	}
	if radarLogFile != nil {
		radarLogFile.Close()
	}
	if plcLogFile != nil {
		plcLogFile.Close()
	}
	if networkLogFile != nil {
		networkLogFile.Close()
	}
	if criticalLogFile != nil {
		criticalLogFile.Close()
	}
}

// Resto das funções otimizadas
func initMetrics() {
	metrics = &SystemMetrics{
		StartTime:          time.Now(),
		PLCConnections:     0,
		PLCDisconnections:  0,
		RadarReconnections: make(map[string]int64),
		TotalPackets:       0,
		TotalErrors:        0,
		NetworkErrors:      0,
		LastUpdate:         time.Now(),
	}

	metrics.RadarReconnections["caldeira"] = 0
	metrics.RadarReconnections["porta_jusante"] = 0
	metrics.RadarReconnections["porta_montante"] = 0
}

func printSystemHeader() {
	fmt.Print("\033[2J\033[H")
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    SISTEMA RADAR SICK                       ║")
	fmt.Println("║              🔄 LOGS COM ROTATION 🔄                       ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ Usuário: %-15s                    Data: %s ║\n",
		getCurrentUser(), time.Now().Format("2006-01-02"))
	fmt.Printf("║ Hora: %-18s                 Versão: v2.1.0 ║\n",
		time.Now().Format("15:04:05"))
	fmt.Printf("║ Logs: AUTO-LIMPEZA (%d dias) + ROTATION (%dMB)          ║\n", MAX_LOG_FILES, MAX_LOG_SIZE)
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func displaySystemStatus(plcConnected bool, enabledRadars map[string]bool, radarManager *radar.RadarManager) {
	fmt.Print("\033[13H\033[J")

	plcStatus := "🔴 DESCONECTADO"
	if plcConnected {
		plcStatus = "🟢 CONECTADO"
	}

	connectionStatus := radarManager.GetConnectionStatus()
	connectedCount := 0
	enabledCount := 0

	fmt.Printf("🎛️  PLC Siemens:     %s\n", plcStatus)
	fmt.Println("┌─────────────────────────────────────────────────────────────┐")

	radarConfigs := []struct{ id, name string }{
		{"caldeira", "Radar Caldeira"},
		{"porta_jusante", "Radar Porta Jusante"},
		{"porta_montante", "Radar Porta Montante"},
	}

	for _, config := range radarConfigs {
		isEnabled := enabledRadars[config.id]
		isConnected := connectionStatus[config.id]

		if isEnabled {
			enabledCount++
		}
		if isConnected && isEnabled {
			connectedCount++
		}

		status := "🔴 DESCONECTADO"
		if !isEnabled {
			status = "⚫ DESABILITADO"
		} else if isConnected {
			status = "🟢 CONECTADO   "
		}

		fmt.Printf("│ %-20s %s                    │\n", config.name+":", status)
	}

	fmt.Println("└─────────────────────────────────────────────────────────────┘")
	fmt.Printf("📊 Resumo: %d/%d habilitados | %d conectados\n", enabledCount, 3, connectedCount)

	uptime := time.Since(metrics.StartTime)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB := float64(m.Alloc) / (1024 * 1024)

	fmt.Println()
	fmt.Println("📈 MÉTRICAS:")
	fmt.Printf("   ⏱️  Uptime: %s\n", formatDuration(uptime))
	fmt.Printf("   🔌 PLC: %d conexões (%d desconexões)\n", metrics.PLCConnections, metrics.PLCDisconnections)
	fmt.Printf("   📦 Pacotes: %d\n", metrics.TotalPackets)
	fmt.Printf("   ❌ Erros: %d (Rede: %d)\n", metrics.TotalErrors, metrics.NetworkErrors)
	fmt.Printf("   💾 Memória: %.1fMB\n", memMB)

	fmt.Println()
	fmt.Printf("📝 Logs com ROTATION: backend/logs/*/\n")
	fmt.Printf("🔄 Auto-limpeza: %d dias | Rotação: %dMB\n", MAX_LOG_FILES, MAX_LOG_SIZE)
	fmt.Println("🚨 Sistema focado em FALHAS e ALARMES... Ctrl+C para parar.")
}

func addRadarsToManager(radarManager *radar.RadarManager) {
	radars := []radar.RadarConfig{
		{ID: "caldeira", Name: "Radar Caldeira", IP: "192.168.1.84", Port: 2111},
		{ID: "porta_jusante", Name: "Radar Porta Jusante", IP: "192.168.1.85", Port: 2111},
		{ID: "porta_montante", Name: "Radar Porta Montante", IP: "192.168.1.86", Port: 2111},
	}

	// LOG SÓ SE DER ERRO
	for _, config := range radars {
		if err := radarManager.AddRadar(config); err != nil {
			errorLogger.Printf("RADAR_SETUP_ERROR: %s - %v", config.Name, err)
		}
	}
}

func getInitialRadarStates(plcConnected bool, plcController *plc.PLCController) map[string]bool {
	if plcConnected && plcController != nil {
		enables := plcController.GetRadarsEnabled()
		return enables
	}

	// LOG SÓ SE PLC ESTIVER OFFLINE
	if !plcConnected {
		errorLogger.Println("PLC_OFFLINE: Estados de radar indisponíveis")
	}

	return map[string]bool{
		"caldeira":       false,
		"porta_jusante":  false,
		"porta_montante": false,
	}
}

func connectEnabledRadars(radarManager *radar.RadarManager, enabledRadars map[string]bool) {
	for id, enabled := range enabledRadars {
		config, _ := radarManager.GetRadarConfig(id)

		if enabled {
			radar, _ := radarManager.GetRadar(id)
			err := radarManager.ConnectRadarWithRetry(radar, 2)
			if err != nil {
				// LOG SÓ FALHAS DE CONEXÃO
				errorLogger.Printf("RADAR_INITIAL_CONNECTION_FAILED: %s - %v", config.Name, err)
				networkLogger.Printf("RADAR_STARTUP_FAIL: %s %s:%d", config.Name, config.IP, config.Port)
			}
		}
	}
}

func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-c
		fmt.Printf("\n\n🛑 Sinal: %v - Encerrando...\n", sig)
		if systemLogger != nil {
			systemLogger.Printf("GRACEFUL_SHUTDOWN: Sinal %v recebido", sig)
		}
		closeAllLogs()
		fmt.Println("✅ Sistema encerrado!")
		os.Exit(0)
	}()
}

func getCurrentUser() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return "danilohenriquesilvalira"
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"connection reset", "connection refused", "broken pipe",
		"network unreachable", "no route to host", "i/o timeout",
		"forcibly closed", "use of closed network connection",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || !netErr.Temporary()
	}

	return false
}
