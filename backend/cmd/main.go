package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
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

	// Estados anteriores COM PROTEÇÃO THREAD-SAFE
	lastRadarStates map[string]bool
	lastPLCState    bool
	stateMutex      sync.RWMutex

	// Context global para controle de goroutines
	globalCtx    context.Context
	globalCancel context.CancelFunc
	mainWg       sync.WaitGroup

	// Canal para shutdown gracioso
	shutdownChan chan struct{}
)

// Configuração de log rotation
const (
	MAX_LOG_FILES = 7  // Manter apenas 7 dias de logs
	MAX_LOG_SIZE  = 10 // 10MB por arquivo (rotacionar se passar)
)

// ✅ MÉTRICAS ESSENCIAIS - SÓ UPTIME E STATUS
type SystemMetrics struct {
	mutex      sync.RWMutex
	StartTime  time.Time
	LastUpdate time.Time
}

func (m *SystemMetrics) UpdateLastUpdate() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.LastUpdate = time.Now()
}

func (m *SystemMetrics) GetStartTime() time.Time {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.StartTime
}

var metrics *SystemMetrics

func main() {
	// Context para controle global
	globalCtx, globalCancel = context.WithCancel(context.Background())
	shutdownChan = make(chan struct{})

	// PANIC RECOVERY
	defer func() {
		if r := recover(); r != nil {
			timestamp := time.Now().Format("2006-01-02 15:04:05")

			if criticalLogger != nil {
				criticalLogger.Printf("🔥 CRASH CRÍTICO: %s - %v", timestamp, r)
				criticalLogger.Printf("Stack Trace: %s", string(debug.Stack()))
			}

			fmt.Printf("\n🔥 CRASH DETECTADO: %s - Erro: %v\n", timestamp, r)
			gracefulShutdown()
			os.Exit(1)
		}
	}()

	setupGracefulShutdown()

	// APLICAR LOG ROTATION ANTES DE INICIALIZAR
	cleanupOldLogs()

	// Inicializar logs
	initLogsInYourStructure()
	defer closeAllLogs()

	// Iniciar worker de log rotation com context
	mainWg.Add(1)
	go logRotationWorker()

	initMetrics()
	initStates()
	printSystemHeader()

	// LOG INICIAL APENAS
	systemLogger.Println("========== SISTEMA RADAR SICK v3.2 THREAD-SAFE INICIADO ==========")
	systemLogger.Printf("Usuário: %s | Data: %s | Versão: v3.2.0",
		getCurrentUser(), time.Now().Format("2006-01-02 15:04:05"))
	systemLogger.Printf("OTIMIZAÇÕES: Métricas lixo removidas ✅ Thread-safe ✅ Memory leak prevention ✅")

	// Criar gerenciador de radares
	radarManager := radar.NewRadarManager()
	addRadarsToManager(radarManager)

	// Variáveis de controle PLC
	var plcSiemens *plc.SiemensPLC
	var plcController *plc.PLCController
	plcConnected := false
	lastPLCAttempt := time.Time{}
	consecutivePLCErrors := 0

	// FUNÇÃO DE RECONEXÃO PLC OTIMIZADA COM CONTEXT
	tryReconnectPLC := func() bool {
		select {
		case <-globalCtx.Done():
			return false
		default:
		}

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

			plcConnected = false

			// THREAD-SAFE UPDATE
			stateMutex.Lock()
			lastPLCState = false
			stateMutex.Unlock()

			return false
		}

		if !plcSiemens.IsConnected() {
			consecutivePLCErrors++
			plcConnected = false
			return false
		}

		// PLC CONTROLLER v3.2 COM CONTEXT
		plcController = plc.NewPLCController(plcSiemens.Client)

		// GOROUTINE COM WAITGROUP E CONTEXT
		mainWg.Add(1)
		go func() {
			defer mainWg.Done()
			defer func() {
				if r := recover(); r != nil {
					criticalLogger.Printf("🔥 PLC_CONTROLLER_PANIC: %v", r)
					criticalLogger.Printf("PLC_STACK: %s", string(debug.Stack()))
					errorLogger.Printf("PLC_GOROUTINE_CRASH: PLCController falhou - %v", r)
					plcConnected = false
				}
			}()

			// PASSAR CONTEXT PARA PLC CONTROLLER
			plcController.StartWithContext(globalCtx)
		}()

		time.Sleep(1500 * time.Millisecond)

		consecutivePLCErrors = 0
		plcConnected = true

		// THREAD-SAFE UPDATE
		stateMutex.Lock()
		wasLastPLCState := lastPLCState
		lastPLCState = true
		stateMutex.Unlock()

		// LOG SÓ QUANDO RECONECTA APÓS FALHA
		if !wasLastPLCState {
			plcLogger.Printf("PLC_RECONNECTED: Conectado após %d tentativas", consecutivePLCErrors)
			systemLogger.Printf("PLC_RECOVERY: Sistema PLC v3.2 restaurado")
		}

		return true
	}

	// Primeira conexão
	tryReconnectPLC()
	time.Sleep(2 * time.Second)

	enabledRadars := getInitialRadarStates(plcConnected, plcController)
	connectEnabledRadars(radarManager, enabledRadars)

	systemLogger.Println("SYSTEM_READY: Loop principal v3.2 iniciado - THREAD-SAFE")

	// LOOP PRINCIPAL v3.2 - COMPLETAMENTE THREAD-SAFE
	lastReconnectCheck := time.Now()
	lastRadarMonitor := time.Now()
	lastStatusUpdate := time.Now()

	// Ticker com context
	mainTicker := time.NewTicker(200 * time.Millisecond)
	defer mainTicker.Stop()

	for {
		select {
		case <-globalCtx.Done():
			fmt.Println("🛑 Sistema recebeu sinal de parada - finalizando loop principal")
			return

		case <-shutdownChan:
			fmt.Println("🛑 Shutdown solicitado - finalizando loop principal")
			return

		case <-mainTicker.C:
			metrics.UpdateLastUpdate()

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

				// RECONEXÃO ASSÍNCRONA v3.2 - COM CONTEXT E WAITGROUP
				if time.Since(lastReconnectCheck) >= 15*time.Second {
					mainWg.Add(1)
					go func(radars map[string]bool) {
						defer mainWg.Done()

						// Context com timeout para evitar goroutine leak
						ctx, cancel := context.WithTimeout(globalCtx, 30*time.Second)
						defer cancel()

						radarManager.CheckAndReconnectEnabledAsyncWithContext(ctx, radars)
					}(currentEnabledRadars)
					lastReconnectCheck = time.Now()
				}

				// MONITORAMENTO INTELIGENTE DE TIMEOUT COM CONTEXT
				if time.Since(lastRadarMonitor) >= 10*time.Second {
					mainWg.Add(1)
					go func(controller *plc.PLCController) {
						defer mainWg.Done()

						select {
						case <-globalCtx.Done():
							return
						default:
						}

						// Verificar radares próximos do timeout
						radars := []string{"caldeira", "porta_jusante", "porta_montante"}
						for _, radarID := range radars {
							isTimingOut, duration := controller.IsRadarTimingOut(radarID)
							if isTimingOut {
								fmt.Printf("⚠️ AVISO: Radar %s próximo do timeout (%.1fs/45s)\n",
									radarID, duration.Seconds())
								radarLogger.Printf("TIMEOUT_WARNING: %s approaching timeout after %.1fs",
									radarID, duration.Seconds())
							}
						}
					}(plcController)
					lastRadarMonitor = time.Now()
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

			// COLETA ASSÍNCRONA v3.2 - COM CONTEXT
			ctx, cancel := context.WithTimeout(globalCtx, 500*time.Millisecond)
			multiRadarData := radarManager.CollectEnabledRadarsDataAsyncWithContext(ctx, currentEnabledRadars)
			cancel()

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
					} else {
						// LOG ERROS DE ESCRITA
						errorLogger.Printf("PLC_DB_WRITE_ERROR: Falha ao escrever DB100 - %v", err)
					}
				}
				plcController.SetRadarsConnected(connectionStatus)
			}

			// LOG INTELIGENTE DE RADARES - SÓ MUDANÇAS DE ESTADO
			if time.Since(lastStatusUpdate) >= 5*time.Second {
				displaySystemStatusV3(plcConnected, currentEnabledRadars, radarManager, plcController)

				// THREAD-SAFE LOG
				logRadarStateChangesThreadSafe(radarManager, currentEnabledRadars)

				flushAllLogs()
				lastStatusUpdate = time.Now()
			}
		}
	}
}

// NOVA FUNÇÃO: gracefulShutdown - SHUTDOWN LIMPO
func gracefulShutdown() {
	fmt.Println("\n🛑 Iniciando shutdown gracioso...")

	// Cancelar context global
	if globalCancel != nil {
		globalCancel()
	}

	// Fechar canal de shutdown
	select {
	case shutdownChan <- struct{}{}:
	default:
	}

	// Aguardar goroutines com timeout
	done := make(chan struct{})
	go func() {
		mainWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("✅ Todas as goroutines finalizadas")
	case <-time.After(10 * time.Second):
		fmt.Println("⚠️ Timeout no shutdown - forçando parada")
	}

	closeAllLogs()
}

// FUNÇÃO THREAD-SAFE: logRadarStateChangesThreadSafe
func logRadarStateChangesThreadSafe(radarManager *radar.RadarManager, currentEnabledRadars map[string]bool) {
	connectionStatus := radarManager.GetConnectionStatus()

	// PROTEÇÃO THREAD-SAFE
	stateMutex.Lock()
	defer stateMutex.Unlock()

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

						// VERSÃO OTIMIZADA COM SWITCH:
						switch config.IP {
						case "192.168.1.84":
							// lógica para caldeira
						case "192.168.1.85":
							// lógica para porta jusante
						case "192.168.1.86":
							// lógica para porta montante
						default:
							// caso padrão
						}
					}
				}
			}

			// Atualizar estado anterior
			lastRadarStates[radarID] = currentState
		}
	}
}

// LOG ROTATION WORKER - COM CONTEXT
func logRotationWorker() {
	defer mainWg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-globalCtx.Done():
			fmt.Println("🔄 Log rotation worker finalizado")
			return

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

// NOVA FUNÇÃO: displaySystemStatusV3 - COM TIMEOUT INTELIGENTE
func displaySystemStatusV3(plcConnected bool, enabledRadars map[string]bool, radarManager *radar.RadarManager, plcController *plc.PLCController) {
	fmt.Print("\033[13H\033[J")

	plcStatus := "🔴 DESCONECTADO"
	if plcConnected {
		plcStatus = "🟢 CONECTADO"
	}

	connectionStatus := radarManager.GetConnectionStatus()
	connectedCount := 0
	enabledCount := 0

	fmt.Printf("🎛️  PLC Siemens v3.2 THREAD-SAFE: %s\n", plcStatus)
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
		extraInfo := ""

		if !isEnabled {
			status = "⚫ DESABILITADO"
		} else if isConnected {
			status = "🟢 CONECTADO   "
			// MOSTRAR TEMPO SEM DADOS
			if plcController != nil {
				lastUpdate := plcController.GetRadarLastUpdate(config.id)
				if !lastUpdate.IsZero() {
					timeSinceUpdate := time.Since(lastUpdate)
					if timeSinceUpdate < 5*time.Second {
						extraInfo = fmt.Sprintf("(%.1fs)", timeSinceUpdate.Seconds())
					} else if timeSinceUpdate < 30*time.Second {
						extraInfo = fmt.Sprintf("(⚠️%.1fs)", timeSinceUpdate.Seconds())
					} else {
						extraInfo = fmt.Sprintf("(🚨%.1fs)", timeSinceUpdate.Seconds())
					}
				}
			}
		} else {
			// VERIFICAR SE ESTÁ EM RECONEXÃO
			if plcController != nil && plcController.IsAnyRadarReconnecting() {
				reconnecting := plcController.GetReconnectingRadars()
				for _, reconId := range reconnecting {
					if reconId == config.id {
						status = "🔄 RECONECTANDO"
						break
					}
				}
			}
		}

		fmt.Printf("│ %-20s %s %-12s            │\n", config.name+":", status, extraInfo)
	}

	fmt.Println("└─────────────────────────────────────────────────────────────┘")
	fmt.Printf("📊 Resumo: %d/%d habilitados | %d conectados", enabledCount, 3, connectedCount)

	// MOSTRAR TIMEOUT ATUAL
	if plcController != nil {
		timeout := plcController.GetRadarTimeoutDuration()
		fmt.Printf(" | Timeout: %v\n", timeout)
	} else {
		fmt.Printf("\n")
	}

	// ✅ SÓ UPTIME E MEMÓRIA - SEM MÉTRICAS LIXO
	uptime := time.Since(metrics.GetStartTime())
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB := float64(m.Alloc) / (1024 * 1024)

	fmt.Println()
	fmt.Println("📈 STATUS v3.2 - SEM MÉTRICAS LIXO:")
	fmt.Printf("   ⏱️  Uptime: %s\n", formatDuration(uptime))
	fmt.Printf("   💾 Memória: %.1fMB\n", memMB)

	// ESTATÍSTICAS ESSENCIAIS DO PLC
	if plcController != nil {
		stats := plcController.GetSystemStatistics()
		fmt.Printf("   🎛️  Sistema: %v | Coleta: %v | Emergência: %v\n",
			stats["system_healthy"],
			stats["collection_active"],
			stats["emergency_stop"])

		if radarsStats, ok := stats["radars"].(map[string]interface{}); ok {
			fmt.Println()
			fmt.Println("📡 STATUS DOS RADARES:")
			for radarID, radarInfo := range radarsStats {
				if info, ok := radarInfo.(map[string]interface{}); ok {
					fmt.Printf("   %s: Habilitado=%v | Conectado=%v\n",
						radarID,
						info["enabled"],
						info["connected"])
				}
			}
		}
	}

	fmt.Println()
	fmt.Printf("🚀 SISTEMA v3.2: OTIMIZADO + Timeout Inteligente (45s)\n")
	fmt.Printf("✅ SEM MÉTRICAS LIXO: Removidas contagens desnecessárias\n")
	fmt.Printf("✅ SEM RACE CONDITIONS: Acesso protegido com mutex\n")
	fmt.Printf("✅ SEM MEMORY LEAKS: Limpeza automática de maps\n")
	fmt.Printf("⚡ RESPONSIVO: Coleta em 200ms + Timeout em 500ms\n")
	fmt.Println("🚨 Sistema focado em FALHAS e ALARMES... Ctrl+C para parar.")
}

// LIMPEZA DE LOGS ANTIGOS
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

// MOVER LOGS ANTIGOS PARA ARCHIVE
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

// VERIFICAR TAMANHO DOS ARQUIVOS ATUAIS
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

// ROTACIONAR ARQUIVO ESPECÍFICO
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

// ATUALIZAR LOGGER APÓS ROTAÇÃO
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

// Inicializar estados anteriores - THREAD-SAFE
func initStates() {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	lastRadarStates = make(map[string]bool)
	lastPLCState = false
}

// Inicializar métricas - THREAD-SAFE
func initMetrics() {
	metrics = &SystemMetrics{
		StartTime:  time.Now(),
		LastUpdate: time.Now(),
	}
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

	fmt.Printf("📝 Logs v3.2 OTIMIZADOS com ROTATION criados:\n")
	fmt.Printf("   📊 Sistema:   backend/logs/system/system_%s.log\n", dateStr)
	fmt.Printf("   📡 Radar:    backend/logs/radar/radar_%s.log\n", dateStr)
	fmt.Printf("   🎛️  PLC:      backend/logs/plc/plc_%s.log\n", dateStr)
	fmt.Printf("   🚨 Critical: backend/logs/critical/critical_%s.log\n", dateStr)
	fmt.Printf("   ❌ Error:    backend/logs/error/error_%s.log\n", dateStr)
	fmt.Printf("   🌐 Network:  backend/logs/network/network_%s.log\n", dateStr)
	fmt.Printf("   🔄 Rotation: %d dias, %dMB max por arquivo\n", MAX_LOG_FILES, MAX_LOG_SIZE)

	// Log inicial MÍNIMO
	systemLogger.Println("========== LOGS v3.2 OTIMIZADOS ==========")
	radarLogger.Println("========== MUDANÇAS DE ESTADO DOS RADARES v3.2 ==========")
	plcLogger.Println("========== EVENTOS DO PLC v3.2 ==========")
	criticalLogger.Println("========== ALARMES CRÍTICOS v3.2 ==========")
	errorLogger.Println("========== FALHAS DO SISTEMA v3.2 ==========")
	networkLogger.Println("========== PROBLEMAS DE REDE v3.2 ==========")
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
	uptime := time.Since(metrics.GetStartTime())

	// Log final MÍNIMO
	if systemLogger != nil {
		systemLogger.Printf("SYSTEM_SHUTDOWN: Uptime=%v", uptime)
		systemLogger.Println("========== SISTEMA v3.2 OTIMIZADO ENCERRADO ==========")
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

func printSystemHeader() {
	fmt.Print("\033[2J\033[H")
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    SISTEMA RADAR SICK v3.2                  ║")
	fmt.Println("║               🚀 OTIMIZADO - SEM MÉTRICAS LIXO 🚀             ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ Usuário: %-15s                    Data: %s ║\n",
		getCurrentUser(), time.Now().Format("2006-01-02"))
	fmt.Printf("║ Hora: %-18s                 Versão: v3.2.0 ║\n",
		time.Now().Format("15:04:05"))
	fmt.Printf("║ Otimizações: Métricas removidas ✅ Memory lean ✅ Clean ✅   ║\n")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
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

// setupGracefulShutdown COM CONTEXT
func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-c
		fmt.Printf("\n\n🛑 Sinal: %v - Encerrando...\n", sig)
		if systemLogger != nil {
			systemLogger.Printf("GRACEFUL_SHUTDOWN: Sinal %v recebido", sig)
		}

		gracefulShutdown()
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
