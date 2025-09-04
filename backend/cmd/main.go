package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"backend/internal/logger"
	"backend/internal/plc"
	"backend/internal/radar"
)

var (
	// Logger profissional com configuração customizada
	systemLogger *logger.SystemLogger

	// Context global
	globalCtx    context.Context
	globalCancel context.CancelFunc
	mainWg       sync.WaitGroup

	// Metrics e controle
	startTime     time.Time
	forceShutdown int64

	// Controle de reconexão e status
	lastPLCReconnectTime      time.Time
	radarsReconnectedAfterPLC bool
	lastPLCStatus             bool
	plcDisconnectTime         time.Time
	radarDisconnectTimes      map[string]time.Time
)

func main() {
	startTime = time.Now()
	globalCtx, globalCancel = context.WithCancel(context.Background())

	// Inicializar controles
	radarDisconnectTimes = make(map[string]time.Time)

	// Inicializar logger profissional com configuração customizada
	systemLogger = initializeLogger()
	defer gracefulLoggerShutdown()

	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("CRASH DETECTADO: %v\n", r)
			systemLogger.LogCriticalError("MAIN", "PANIC_RECOVERY", fmt.Errorf("%v", r))
		}
	}()

	setupGracefulShutdown()
	printHeader()
	systemLogger.LogSystemStarted()

	// CRIAR COMPONENTES USANDO OS MÉTODOS QUE EXISTEM
	radarManager := radar.NewRadarManager()

	// ✅ CONECTAR LOGGER AO RADAR MANAGER
	radarManager.SetSystemLogger(systemLogger)

	addRadarsToManager(radarManager)

	// ✅ USAR PLCManager COM LOGGING INTELIGENTE
	plcManager := plc.NewPLCManager("192.168.1.33")
	plcManager.SetSystemLogger(systemLogger)

	// INICIALIZAR COMPONENTES
	mainWg.Add(1)
	go func() {
		defer mainWg.Done()
		defer func() {
			if r := recover(); r != nil {
				systemLogger.LogCriticalError("PLC_MANAGER", "RUNTIME_PANIC", fmt.Errorf("%v", r))
			}
		}()
		plcManager.StartWithContext(globalCtx)
	}()

	// CONECTAR RADARES INICIALMENTE
	fmt.Println("🔄 Conectando radares...")
	connectErrors := radarManager.ConnectAll()
	for id, err := range connectErrors {
		config, _ := radarManager.GetRadarConfig(id)
		systemLogger.LogCriticalError("RADAR", "INITIAL_CONNECTION", fmt.Errorf("%s: %v", config.Name, err))
	}

	// Inicializar status de controle
	lastPLCStatus = plcManager.IsPLCConnected()
	if !lastPLCStatus {
		plcDisconnectTime = time.Now()
	}

	// INICIAR MONITORAMENTO DE LOGS
	startLogMonitoring()

	// LOOP PRINCIPAL - APENAS COORDENAÇÃO
	coordinationTicker := time.NewTicker(1 * time.Second)
	statusTicker := time.NewTicker(3 * time.Second)
	logStatsTicker := time.NewTicker(5 * time.Minute) // Stats de log a cada 5 minutos
	defer coordinationTicker.Stop()
	defer statusTicker.Stop()
	defer logStatsTicker.Stop()

	fmt.Println("✅ Sistema operacional")

	for {
		select {
		case <-globalCtx.Done():
			fmt.Println("Sistema recebeu sinal de parada")
			systemLogger.LogSystemShutdown(time.Since(startTime))
			return

		case <-coordinationTicker.C:
			// COORDENAÇÃO ENTRE COMPONENTES
			if atomic.LoadInt64(&forceShutdown) == 1 {
				return
			}

			// 1. Verificar se PLC está ativo
			plcConnected := plcManager.IsPLCConnected()
			collectionActive := plcManager.IsCollectionActive()
			emergencyStop := plcManager.IsEmergencyStop()

			// DETECTAR MUDANÇAS DE STATUS PLC
			// PLC desconectou
			if lastPLCStatus && !plcConnected {
				plcDisconnectTime = time.Now()
				systemLogger.LogPLCDisconnected(0, fmt.Errorf("connection lost"))
			}

			// PLC reconectou
			if !lastPLCStatus && plcConnected {
				downtime := time.Since(plcDisconnectTime)
				systemLogger.LogPLCReconnected(downtime)

				fmt.Println("🎉 PLC RECONECTADO - Iniciando reconexão de radares...")
				lastPLCReconnectTime = time.Now()
				radarsReconnectedAfterPLC = false

				// FORÇAR RECONEXÃO IMEDIATA DOS RADARES
				go forceRadarReconnectionAfterPLC(radarManager)
			}

			lastPLCStatus = plcConnected

			// DETECTAR MUDANÇAS DE STATUS RADARES
			detectRadarStatusChanges(radarManager)

			// 2. Coordenar com RadarManager baseado no status do PLC
			if plcConnected && collectionActive && !emergencyStop {
				// PLC conectado e ativo - coletar dados
				enabledRadars := plcManager.GetRadarsEnabled()

				// RECONEXÃO PERIÓDICA DE RADARES (a cada 15 segundos)
				go func(radars map[string]bool) {
					ctx, cancel := context.WithTimeout(globalCtx, 30*time.Second)
					defer cancel()
					radarManager.CheckAndReconnectEnabledAsyncWithContext(ctx, radars)
				}(enabledRadars)

				// Coletar dados dos radares habilitados usando método que existe
				radarData := radarManager.CollectEnabledRadarsDataAsyncWithContext(globalCtx, enabledRadars)

				// Enviar dados para o PLC usando método que existe
				err := plcManager.WriteMultiRadarData(radarData)
				if err != nil {
					systemLogger.LogCriticalError("PLC", "DATA_WRITE", err)
				}

				// Informar status de conexão dos radares para o PLC
				connectionStatus := radarManager.GetConnectionStatus()
				plcManager.SetRadarsConnected(connectionStatus)
			} else if !plcConnected {
				// PLC desconectado - resetar flag de reconexão
				radarsReconnectedAfterPLC = false
			}

		case <-statusTicker.C:
			// EXIBIR STATUS CONSOLIDADO
			displayConsolidatedStatus(plcManager, radarManager)

		case <-logStatsTicker.C:
			// MONITORAR ESTATÍSTICAS DE LOG
			displayLogStats()
		}
	}
}

// ✅ initializeLogger CORRIGIDO - SEM EnableCompression
func initializeLogger() *logger.SystemLogger {
	config := logger.LogConfig{
		BasePath:         "backend/logs",
		MaxFileSize:      50 * 1024 * 1024, // 50MB para rotação mais frequente
		RetentionDays:    7,                // Manter 7 dias
		RotationInterval: 24 * time.Hour,   // Rotação diária
		EnableDebug:      false,            // ✅ DESABILITAR DEBUG PARA EVITAR LIXO
		CleanupInterval:  30 * time.Minute, // Limpeza a cada 30 minutos
	}

	return logger.NewSystemLoggerWithConfig(config)
}

// startLogMonitoring inicia monitoramento dos logs
func startLogMonitoring() {
	mainWg.Add(1)
	go func() {
		defer mainWg.Done()

		ticker := time.NewTicker(10 * time.Minute) // Verificar a cada 10 minutos
		defer ticker.Stop()

		for {
			select {
			case <-globalCtx.Done():
				return
			case <-ticker.C:
				stats := systemLogger.GetLogStats()

				// Verificar se logs estão crescendo muito
				if errorFileSize, ok := stats["error_file_size"].(int64); ok {
					if errorFileSize > 30*1024*1024 { // 30MB
						systemLogger.LogCriticalError("LOG_MONITOR", "ERROR_LOG_SIZE_WARNING",
							fmt.Errorf("error log size: %d bytes", errorFileSize))
					}
				}
			}
		}
	}()
}

// ✅ displayLogStats CORRIGIDO - SEM ARCHIVE
func displayLogStats() {
	stats := systemLogger.GetLogStats()

	// ✅ CORRIGIDO - SEM ARCHIVE COUNT
	if errorCount, ok := stats["errors_file_count"].(int); ok && errorCount > 0 {
		fmt.Printf("📊 Logs: %d erros, %d sistema, %d warnings\n",
			errorCount,
			getStatInt(stats, "system_file_count"),
			getStatInt(stats, "warnings_file_count"))
	}
}

// getStatInt extrai valor int das estatísticas
func getStatInt(stats map[string]interface{}, key string) int {
	if val, ok := stats[key].(int); ok {
		return val
	}
	return 0
}

// gracefulLoggerShutdown shutdown seguro do logger
func gracefulLoggerShutdown() {
	if systemLogger != nil {
		// Forçar rotação final se necessário
		systemLogger.ForceRotation()

		// Fechar logger
		systemLogger.Close()

		fmt.Println("📝 Logger fechado com segurança")
	}
}

func detectRadarStatusChanges(radarManager *radar.RadarManager) {
	connectionStatus := radarManager.GetConnectionStatus()

	for radarID, isConnected := range connectionStatus {
		config, exists := radarManager.GetRadarConfig(radarID)
		if !exists {
			continue
		}

		// Radar desconectou
		if !isConnected {
			if _, wasTracked := radarDisconnectTimes[radarID]; !wasTracked {
				radarDisconnectTimes[radarID] = time.Now()
				systemLogger.LogRadarDisconnected(radarID, config.Name)
			}
		} else {
			// Radar reconectou
			if disconnectTime, wasDisconnected := radarDisconnectTimes[radarID]; wasDisconnected {
				downtime := time.Since(disconnectTime)
				systemLogger.LogRadarReconnected(radarID, config.Name, downtime)
				delete(radarDisconnectTimes, radarID)
			}
		}
	}
}

func forceRadarReconnectionAfterPLC(radarManager *radar.RadarManager) {
	time.Sleep(2 * time.Second) // Aguardar PLC estabilizar

	fmt.Println("🔄 Forçando reconexão de todos os radares após PLC...")

	connectErrors := radarManager.ConnectAll()

	successCount := 0
	for id, err := range connectErrors {
		config, _ := radarManager.GetRadarConfig(id)
		if err != nil {
			systemLogger.LogCriticalError("RADAR", "POST_PLC_RECONNECTION", fmt.Errorf("%s: %v", config.Name, err))
			fmt.Printf("❌ Erro reconectar radar %s: %v\n", config.Name, err)
		} else {
			successCount++
		}
	}

	fmt.Printf("✅ Reconexão pós-PLC: %d/3 radares conectados\n", successCount)
	radarsReconnectedAfterPLC = true
}

// ✅ ATUALIZADA PARA ACEITAR PLCManager
func displayConsolidatedStatus(plcManager *plc.PLCManager, radarManager *radar.RadarManager) {
	fmt.Print("\033[12H\033[J") // Limpar a partir da linha 12

	// Status PLC usando métodos que existem
	plcConnected := plcManager.IsPLCConnected()
	collectionActive := plcManager.IsCollectionActive()
	emergencyStop := plcManager.IsEmergencyStop()

	fmt.Println("========================================")
	if plcConnected {
		fmt.Println("PLC: CONECTADO")
		if emergencyStop {
			fmt.Println("Coleta: PARADA DE EMERGÊNCIA")
		} else if collectionActive {
			fmt.Println("Coleta: ATIVA")
		} else {
			fmt.Println("Coleta: PARADA")
		}

		// MOSTRAR STATUS DE RECONEXÃO PÓS-PLC
		if !lastPLCReconnectTime.IsZero() {
			timeSinceReconnect := time.Since(lastPLCReconnectTime)
			if timeSinceReconnect < 30*time.Second {
				if radarsReconnectedAfterPLC {
					fmt.Printf("Reconexão: COMPLETA (há %s)\n", formatDuration(timeSinceReconnect))
				} else {
					fmt.Printf("Reconexão: EM ANDAMENTO (há %s)\n", formatDuration(timeSinceReconnect))
				}
			}
		}
	} else {
		fmt.Println("PLC: DESCONECTADO")
		if !plcDisconnectTime.IsZero() {
			downtime := time.Since(plcDisconnectTime)
			fmt.Printf("Downtime: %v\n", formatDuration(downtime))
		}
	}
	fmt.Println("========================================")

	// Status Radares usando métodos que existem
	connectionStatus := radarManager.GetConnectionStatus()
	enabledRadars := plcManager.GetRadarsEnabled()

	radars := []struct{ id, name string }{
		{"caldeira", "Radar Caldeira"},
		{"porta_jusante", "Radar Porta Jusante"},
		{"porta_montante", "Radar Porta Montante"},
	}

	connectedCount := 0
	enabledCount := 0

	for _, r := range radars {
		isEnabled := enabledRadars[r.id]
		isConnected := connectionStatus[r.id]

		if isEnabled {
			enabledCount++
		}
		if isConnected && isEnabled {
			connectedCount++
		}

		status := "DESCONECTADO"
		if !isEnabled {
			status = "DESABILITADO"
		} else if isConnected {
			status = "CONECTADO"
		} else if disconnectTime, exists := radarDisconnectTimes[r.id]; exists {
			downtime := time.Since(disconnectTime)
			status = fmt.Sprintf("DESCONECTADO (%v)", formatDuration(downtime))
		}

		fmt.Printf("%-20s: %s\n", r.name, status)
	}

	fmt.Println("========================================")
	fmt.Printf("Radares: %d/%d habilitados | %d conectados\n", enabledCount, len(radars), connectedCount)

	uptime := time.Since(startTime)
	fmt.Printf("Uptime: %s\n", formatDuration(uptime))

	fmt.Println("========================================")
}

func gracefulShutdown() {
	fmt.Println("🛑 Iniciando shutdown...")
	atomic.StoreInt64(&forceShutdown, 1)

	if globalCancel != nil {
		globalCancel()
	}

	done := make(chan struct{})
	go func() {
		mainWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("✅ Shutdown concluído")
		systemLogger.LogSystemShutdown(time.Since(startTime))
	case <-time.After(10 * time.Second):
		fmt.Println("⚠️ Timeout no shutdown - forçando")
		systemLogger.LogCriticalError("MAIN", "SHUTDOWN_TIMEOUT", fmt.Errorf("shutdown timeout after 10s"))
	}
}

func printHeader() {
	fmt.Print("\033[2J\033[H")
	fmt.Println("========================================")
	fmt.Println("      SISTEMA RADAR SICK v4.0")
	fmt.Println("========================================")
	fmt.Printf("Usuario: %s\n", getCurrentUser())
	fmt.Printf("Data: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println("Status: Inicializando componentes...")
	fmt.Println("========================================")
	fmt.Println()
}

func addRadarsToManager(radarManager *radar.RadarManager) {
	radars := []radar.RadarConfig{
		{ID: "caldeira", Name: "Radar Caldeira", IP: "192.168.1.84", Port: 2111},
		{ID: "porta_jusante", Name: "Radar Porta Jusante", IP: "192.168.1.85", Port: 2111},
		{ID: "porta_montante", Name: "Radar Porta Montante", IP: "192.168.1.86", Port: 2111},
	}
	for _, config := range radars {
		if err := radarManager.AddRadar(config); err != nil {
			systemLogger.LogCriticalError("RADAR_MANAGER", "ADD_RADAR", fmt.Errorf("%s: %v", config.Name, err))
		} else {
			fmt.Printf("Radar %s (%s) configurado\n", config.Name, config.ID)
		}
	}
}

func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-c
		fmt.Printf("\nSinal recebido: %v\n", sig)
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
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
