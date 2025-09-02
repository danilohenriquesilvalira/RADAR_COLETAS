package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"backend/internal/plc"
	"backend/internal/radar"
)

var (
	// Loggers especializados
	systemLogger *log.Logger
	errorLogger  *log.Logger
	radarLogger  *log.Logger
	plcLogger    *log.Logger

	// Arquivo de log
	logFile *os.File
)

// Estrutura para métricas do sistema
type SystemMetrics struct {
	StartTime          time.Time
	PLCConnections     int64
	PLCDisconnections  int64
	RadarReconnections map[string]int64
	TotalPackets       int64
	TotalErrors        int64
	LastUpdate         time.Time
}

var metrics *SystemMetrics

func main() {
	// Configurar interceptação de sinais
	setupGracefulShutdown()

	// Inicializar sistema de logs
	initLogging()
	defer closeLogging()

	// Inicializar métricas
	initMetrics()

	// Header do sistema
	printSystemHeader()

	systemLogger.Println("========== SISTEMA RADAR SICK INICIADO ==========")
	systemLogger.Printf("Usuário: %s", getCurrentUser())
	systemLogger.Printf("Data/Hora: %s", time.Now().Format("2006-01-02 15:04:05"))

	// Criar gerenciador de radares
	radarManager := radar.NewRadarManager()

	// Adicionar os 3 radares
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

		if now.Sub(lastPLCAttempt) < 8*time.Second {
			return false
		}

		lastPLCAttempt = now
		plcLogger.Printf("Tentando reconectar PLC Siemens 192.168.1.33...")

		// Limpar conexões antigas
		if plcController != nil {
			plcController.Stop()
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
			errorLogger.Printf("PLC: Erro na conexão (tentativa %d): %v", consecutivePLCErrors, err)
			plcConnected = false
			return false
		}

		if !plcSiemens.IsConnected() {
			consecutivePLCErrors++
			errorLogger.Printf("PLC: Conexão não confirmada (tentativa %d)", consecutivePLCErrors)
			plcConnected = false
			return false
		}

		// Criar controlador
		plcController = plc.NewPLCController(plcSiemens.Client)
		go plcController.Start()
		time.Sleep(2 * time.Second)

		consecutivePLCErrors = 0
		plcConnected = true
		metrics.PLCConnections++
		plcLogger.Printf("PLC CONECTADO com sucesso!")
		return true
	}

	// Primeira conexão
	systemLogger.Println("Conectando ao PLC Siemens 192.168.1.33...")
	tryReconnectPLC()

	// Aguardar comandos iniciais
	time.Sleep(3 * time.Second)

	// Conectar radares baseado no PLC
	enabledRadars := getInitialRadarStates(plcConnected, plcController)
	connectEnabledRadars(radarManager, enabledRadars)

	systemLogger.Println("Sistema iniciado - Loop principal ativo")

	// Loop principal otimizado
	lastReconnectCheck := time.Now()
	lastMetricsUpdate := time.Now()

	for {
		// Atualizar timestamp
		metrics.LastUpdate = time.Now()

		// Reconectar PLC
		plcConnected = tryReconnectPLC()

		// Estados atuais
		collectionActive := true
		var currentEnabledRadars map[string]bool

		if plcConnected && plcController != nil {
			collectionActive = plcController.IsCollectionActive()
			currentEnabledRadars = plcController.GetRadarsEnabled()

			if plcController.IsEmergencyStop() {
				errorLogger.Println("PARADA DE EMERGÊNCIA ATIVADA VIA PLC")
				time.Sleep(2 * time.Second)
				continue
			}

			// Reconexão controlada dos radares
			if time.Since(lastReconnectCheck) >= 8*time.Second {
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
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Coletar dados
		multiRadarData := radarManager.CollectEnabledRadarsData(currentEnabledRadars)
		metrics.TotalPackets++

		// Atualizar PLC
		if plcConnected && plcController != nil {
			connectionStatus := radarManager.GetConnectionStatus()
			err := plcController.WriteMultiRadarData(multiRadarData)
			if err != nil {
				if isConnectionError(err) {
					plcLogger.Println("Conexão PLC perdida durante escrita - reconectando...")
					plcConnected = false
					metrics.PLCDisconnections++
				} else {
					errorLogger.Printf("Erro ao escrever DB100: %v", err)
					metrics.TotalErrors++
				}
			}
			plcController.SetRadarsConnected(connectionStatus)
		}

		// Exibir status a cada 2 segundos
		if time.Since(lastMetricsUpdate) >= 2*time.Second {
			displaySystemStatus(plcConnected, currentEnabledRadars, radarManager)
			lastMetricsUpdate = time.Now()
		}

		time.Sleep(200 * time.Millisecond)
	}
}

// Inicializar sistema de logging
func initLogging() {
	// Criar diretório logs se não existir
	if _, err := os.Stat("logs"); os.IsNotExist(err) {
		os.Mkdir("logs", 0755)
	}

	// Nome do arquivo com data
	logFileName := fmt.Sprintf("logs/radar_system_%s.log", time.Now().Format("2006-01-02"))

	var err error
	logFile, err = os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Erro ao criar arquivo de log: %v", err)
	}

	// Configurar loggers
	systemLogger = log.New(logFile, "[SYSTEM] ", log.LstdFlags|log.Lmicroseconds)
	errorLogger = log.New(logFile, "[ERROR]  ", log.LstdFlags|log.Lmicroseconds)
	radarLogger = log.New(logFile, "[RADAR]  ", log.LstdFlags|log.Lmicroseconds)
	plcLogger = log.New(logFile, "[PLC]    ", log.LstdFlags|log.Lmicroseconds)

	fmt.Printf("📝 Sistema de logs ativo: %s\n", logFileName)
}

// Fechar sistema de logging
func closeLogging() {
	if logFile != nil {
		systemLogger.Println("========== SISTEMA ENCERRADO ==========")
		logFile.Close()
	}
}

// Inicializar métricas
func initMetrics() {
	metrics = &SystemMetrics{
		StartTime:          time.Now(),
		PLCConnections:     0,
		PLCDisconnections:  0,
		RadarReconnections: make(map[string]int64),
		TotalPackets:       0,
		TotalErrors:        0,
		LastUpdate:         time.Now(),
	}

	// Inicializar contadores de radar
	metrics.RadarReconnections["caldeira"] = 0
	metrics.RadarReconnections["porta_jusante"] = 0
	metrics.RadarReconnections["porta_montante"] = 0
}

// Header do sistema
func printSystemHeader() {
	fmt.Print("\033[2J\033[H") // Limpar tela
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    SISTEMA RADAR SICK                       ║")
	fmt.Println("║                   MONITORAMENTO ATIVO                       ║")
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ Usuário: %-15s                    Data: %s ║\n",
		getCurrentUser(), time.Now().Format("2006-01-02"))
	fmt.Printf("║ Hora: %-18s                 Versão: v2.0.0 ║\n",
		time.Now().Format("15:04:05"))
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

// Exibir status do sistema (fixo no terminal)
func displaySystemStatus(plcConnected bool, enabledRadars map[string]bool, radarManager *radar.RadarManager) {
	// Limpar área de status (mantém header)
	fmt.Print("\033[10H\033[J") // Move cursor para linha 10 e limpa resto

	// Status PLC
	plcStatus := "🔴 DESCONECTADO"
	if plcConnected {
		plcStatus = "🟢 CONECTADO"
	}

	// Status dos radares
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

	// Métricas do sistema
	uptime := time.Since(metrics.StartTime)
	fmt.Println()
	fmt.Println("📈 MÉTRICAS DO SISTEMA:")
	fmt.Printf("   ⏱️  Tempo ativo:      %s\n", formatDuration(uptime))
	fmt.Printf("   🔌 Conexões PLC:     %d\n", metrics.PLCConnections)
	fmt.Printf("   📦 Pacotes processados: %d\n", metrics.TotalPackets)
	fmt.Printf("   ❌ Erros registrados: %d\n", metrics.TotalErrors)
	fmt.Printf("   🕐 Última atualização: %s\n", metrics.LastUpdate.Format("15:04:05"))

	fmt.Println()
	fmt.Printf("📝 Logs: logs/radar_system_%s.log\n", time.Now().Format("2006-01-02"))
	fmt.Println("🔄 Sistema em execução... Pressione Ctrl+C para parar.")
}

// Adicionar radares ao manager
func addRadarsToManager(radarManager *radar.RadarManager) {
	radars := []radar.RadarConfig{
		{ID: "caldeira", Name: "Radar Caldeira", IP: "192.168.1.84", Port: 2111},
		{ID: "porta_jusante", Name: "Radar Porta Jusante", IP: "192.168.1.85", Port: 2111},
		{ID: "porta_montante", Name: "Radar Porta Montante", IP: "192.168.1.86", Port: 2111},
	}

	for _, config := range radars {
		if err := radarManager.AddRadar(config); err != nil {
			errorLogger.Printf("Erro ao adicionar radar %s: %v", config.Name, err)
		} else {
			systemLogger.Printf("Radar %s adicionado com sucesso", config.Name)
		}
	}
}

// Obter estados iniciais dos radares
func getInitialRadarStates(plcConnected bool, plcController *plc.PLCController) map[string]bool {
	if plcConnected && plcController != nil {
		enables := plcController.GetRadarsEnabled()
		systemLogger.Printf("Estados PLC: Caldeira=%t, Porta Jusante=%t, Porta Montante=%t",
			enables["caldeira"], enables["porta_jusante"], enables["porta_montante"])
		return enables
	}

	systemLogger.Println("PLC desconectado - todos radares desabilitados")
	return map[string]bool{
		"caldeira":       false,
		"porta_jusante":  false,
		"porta_montante": false,
	}
}

// Conectar radares habilitados
func connectEnabledRadars(radarManager *radar.RadarManager, enabledRadars map[string]bool) {
	for id, enabled := range enabledRadars {
		config, _ := radarManager.GetRadarConfig(id)

		if enabled {
			radar, _ := radarManager.GetRadar(id)
			radarLogger.Printf("Conectando radar habilitado: %s", config.Name)

			err := radarManager.ConnectRadarWithRetry(radar, 3)
			if err != nil {
				errorLogger.Printf("Falha ao conectar radar %s: %v", config.Name, err)
			} else {
				radarLogger.Printf("Radar %s conectado com sucesso", config.Name)
			}
		} else {
			systemLogger.Printf("Radar %s desabilitado - não conectando", config.Name)
		}
	}
}

// Configurar encerramento gracioso
func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		fmt.Println("\n\n🛑 Encerrando sistema...")
		if systemLogger != nil {
			systemLogger.Println("Encerramento solicitado pelo usuário")
		}
		closeLogging()
		os.Exit(0)
	}()
}

// Obter usuário atual
func getCurrentUser() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return "unknown"
}

// Formatar duração
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

// Verificar erro de conexão
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	connectionErrors := []string{
		"connection reset", "connection refused", "broken pipe",
		"network unreachable", "no route to host", "i/o timeout",
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
