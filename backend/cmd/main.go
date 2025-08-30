package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"backend/internal/plc"
	"backend/internal/radar"
	"backend/internal/websocket"
	"backend/pkg/models"
)

// SystemConfig centraliza todas as configurações do sistema
type SystemConfig struct {
	WebDir           string
	WebServerPort    string
	PLCConfig        *plc.PLCConfig
	RadarConfigs     []radar.RadarConfig
	CollectionDelay  time.Duration
	ShutdownTimeout  time.Duration
	PLCHealthCheck   time.Duration
	OperationTimeout time.Duration
}

// DefaultSystemConfig retorna configuração padrão do sistema
func DefaultSystemConfig() *SystemConfig {
	return &SystemConfig{
		WebDir:           "./web",
		WebServerPort:    "8080",
		PLCConfig:        plc.DefaultConfig("192.168.1.33"),
		CollectionDelay:  200 * time.Millisecond,
		ShutdownTimeout:  30 * time.Second,
		PLCHealthCheck:   30 * time.Second,
		OperationTimeout: 2 * time.Second,
		RadarConfigs: []radar.RadarConfig{
			{
				ID:   "caldeira",
				Name: "Radar Caldeira",
				IP:   "192.168.1.84",
				Port: 2111,
			},
			{
				ID:   "porta_jusante",
				Name: "Radar Porta Jusante",
				IP:   "192.168.1.85",
				Port: 2111,
			},
			{
				ID:   "porta_montante",
				Name: "Radar Porta Montante",
				IP:   "192.168.1.86",
				Port: 2111,
			},
		},
	}
}

// SystemManager gerencia todos os componentes do sistema
type SystemManager struct {
	config        *SystemConfig
	logger        *slog.Logger
	radarManager  *radar.RadarManager
	wsManager     *websocket.WebSocketManager
	plcInstance   *plc.SiemensPLC
	plcController *plc.PLCController
	shutdown      chan struct{}
	wg            sync.WaitGroup
	mu            sync.RWMutex

	// Recovery counters
	mainLoopRestarts     int
	plcReconnectAttempts int
	systemStartTime      time.Time // Para calcular uptime
}

// NewSystemManager cria novo gerenciador do sistema
func NewSystemManager(config *SystemConfig, logger *slog.Logger) *SystemManager {
	return &SystemManager{
		config:          config,
		logger:          logger.With("component", "system_manager"),
		radarManager:    radar.NewRadarManager(),
		wsManager:       websocket.NewWebSocketManager(),
		shutdown:        make(chan struct{}),
		systemStartTime: time.Now(),
	}
}

// getSystemStartTime retorna hora de início
func (sm *SystemManager) getSystemStartTime() time.Time {
	return sm.systemStartTime
}

// getProductionStats retorna estatísticas de produção
func (sm *SystemManager) getProductionStats() ProductionStats {
	sm.mu.RLock()
	plcController := sm.plcController
	sm.mu.RUnlock()

	stats := ProductionStats{
		TotalPackets: 0,
		ErrorCount:   0,
		CPUUsage:     25.0,
		MemoryUsage:  40.0,
	}

	if plcController != nil {
		systemStats := plcController.GetSystemStats()
		if totalPackets, ok := systemStats["total_packets"].(int32); ok {
			stats.TotalPackets = int(totalPackets)
		}
		if totalErrors, ok := systemStats["total_errors"].(int32); ok {
			stats.ErrorCount = int(totalErrors)
		}
	}

	return stats
}

type ProductionStats struct {
	TotalPackets int
	ErrorCount   int
	CPUUsage     float64
	MemoryUsage  float64
}

// Initialize configura todos os componentes
func (sm *SystemManager) Initialize(ctx context.Context) error {
	sm.logger.Info("Inicializando Sistema Radar SICK",
		"radars_count", len(sm.config.RadarConfigs),
		"plc_ip", sm.config.PLCConfig.IP,
	)

	// Configurar radares
	if err := sm.setupRadars(); err != nil {
		return fmt.Errorf("erro configurando radares: %w", err)
	}

	// Configurar diretório web
	if err := sm.setupWebDirectory(); err != nil {
		return fmt.Errorf("erro configurando diretório web: %w", err)
	}

	// Inicializar PLC
	if err := sm.initializePLC(ctx); err != nil {
		sm.logger.Warn("PLC não disponível", "error", err)
	}

	// Inicializar WebSocket
	sm.initializeWebSocket()

	return nil
}

// setupRadars configura todos os radares
func (sm *SystemManager) setupRadars() error {
	for _, config := range sm.config.RadarConfigs {
		sm.radarManager.AddRadar(config)
		sm.logger.Debug("Radar configurado", "id", config.ID, "name", config.Name)
	}
	return nil
}

// setupWebDirectory cria diretório web se necessário
func (sm *SystemManager) setupWebDirectory() error {
	if _, err := os.Stat(sm.config.WebDir); os.IsNotExist(err) {
		if err := os.Mkdir(sm.config.WebDir, 0755); err != nil {
			return fmt.Errorf("erro criando diretório web: %w", err)
		}
	}
	return nil
}

// initializePLC inicializa conexão com PLC
func (sm *SystemManager) initializePLC(ctx context.Context) error {
	// Criar instância do PLC
	plcInstance, err := plc.NewSiemensPLC(sm.config.PLCConfig, sm.logger)
	if err != nil {
		return fmt.Errorf("erro criando instância PLC: %w", err)
	}

	// Conectar ao PLC
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := plcInstance.Connect(connectCtx); err != nil {
		return fmt.Errorf("erro conectando PLC: %w", err)
	}

	// Obter cliente para controlador
	client, err := plcInstance.GetClient()
	if err != nil {
		plcInstance.Close()
		return fmt.Errorf("erro obtendo cliente PLC: %w", err)
	}

	// Criar controlador
	sm.mu.Lock()
	sm.plcInstance = plcInstance
	sm.plcController = plc.NewPLCController(client)
	sm.mu.Unlock()

	sm.logger.Info("PLC conectado com sucesso",
		"ip", sm.config.PLCConfig.IP,
		"rack", sm.config.PLCConfig.Rack,
		"slot", sm.config.PLCConfig.Slot,
	)

	return nil
}

// reconnectPLC reconecta PLC quando desconectado
func (sm *SystemManager) reconnectPLC(ctx context.Context) {
	sm.mu.Lock()
	sm.plcReconnectAttempts++
	attempts := sm.plcReconnectAttempts
	sm.mu.Unlock()

	if attempts > 10 {
		sm.logger.Error("Muitas tentativas de reconexão PLC - parando")
		return
	}

	sm.logger.Info("Tentando reconectar PLC", "attempt", attempts)

	// Limpar instância atual
	sm.mu.Lock()
	if sm.plcInstance != nil {
		sm.plcInstance.Close()
		sm.plcInstance = nil
		sm.plcController = nil
	}
	sm.mu.Unlock()

	// Tentar reconectar
	if err := sm.initializePLC(ctx); err != nil {
		sm.logger.Error("Falha na reconexão PLC", "error", err, "attempt", attempts)

		// Retry após delay
		time.Sleep(5 * time.Second)
		go sm.reconnectPLC(ctx)
	} else {
		sm.mu.Lock()
		sm.plcReconnectAttempts = 0 // Reset contador
		sm.mu.Unlock()
		sm.logger.Info("PLC reconectado com sucesso")
	}
}

// initializeWebSocket configura WebSocket
func (sm *SystemManager) initializeWebSocket() {
	sm.wsManager.LimparConexoesAnteriores()

	sm.logger.Info("WebSocket configurado",
		"port", sm.config.WebServerPort,
		"web_dir", sm.config.WebDir,
	)
}

// Start inicia todos os serviços do sistema
func (sm *SystemManager) Start(ctx context.Context) error {
	sm.logger.Info("Iniciando serviços do sistema")

	// Inicializar componentes
	if err := sm.Initialize(ctx); err != nil {
		return fmt.Errorf("erro na inicialização: %w", err)
	}

	// Iniciar WebSocket manager com recovery
	sm.wg.Add(1)
	go sm.runWithRecovery("websocket_manager", func() {
		sm.wsManager.Run()
	})

	// Iniciar servidor HTTP com recovery
	sm.wg.Add(1)
	go sm.runWithRecovery("http_server", func() {
		sm.wsManager.ServeHTTP(sm.config.WebDir)
	})

	// Iniciar controlador PLC se disponível com recovery
	if sm.plcController != nil {
		sm.wg.Add(1)
		go sm.runWithRecovery("plc_controller", func() {
			sm.plcController.Start()
		})

		// Aguardar inicialização do PLC
		time.Sleep(2 * time.Second)
	}

	// Iniciar monitor de saúde do PLC
	sm.wg.Add(1)
	go sm.runWithRecovery("plc_health_monitor", func() {
		sm.monitorPLCHealth(ctx)
	})

	// Conectar radares baseado no status do PLC
	if err := sm.connectEnabledRadars(ctx); err != nil {
		sm.logger.Warn("Alguns radares falharam na conexão", "error", err)
	}

	// Iniciar loop principal com recovery
	sm.wg.Add(1)
	go sm.runWithRecovery("main_loop", func() {
		sm.runMainLoop(ctx)
	})

	sm.logger.Info("Sistema completamente iniciado",
		"web_url", fmt.Sprintf("http://localhost:%s", sm.config.WebServerPort),
	)

	return nil
}

// runWithRecovery executa função com panic recovery automático
func (sm *SystemManager) runWithRecovery(name string, fn func()) {
	defer sm.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			sm.logger.Error("Panic recuperado",
				"component", name,
				"panic", r,
			)

			// Para main loop, reiniciar automaticamente
			if name == "main_loop" {
				sm.mu.Lock()
				sm.mainLoopRestarts++
				restarts := sm.mainLoopRestarts
				sm.mu.Unlock()

				if restarts < 5 {
					sm.logger.Info("Reiniciando main loop", "restart_count", restarts)
					time.Sleep(1 * time.Second)
					go sm.runWithRecovery(name, fn)
				} else {
					sm.logger.Error("Muitos restarts do main loop - parando")
				}
			}
		}
	}()

	fn()
}

// monitorPLCHealth monitora saúde do PLC
func (sm *SystemManager) monitorPLCHealth(ctx context.Context) {
	ticker := time.NewTicker(sm.config.PLCHealthCheck)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.shutdown:
			return
		case <-ticker.C:
			sm.checkPLCHealth(ctx)
		}
	}
}

// checkPLCHealth verifica saúde do PLC
func (sm *SystemManager) checkPLCHealth(ctx context.Context) {
	sm.mu.RLock()
	plcInstance := sm.plcInstance
	sm.mu.RUnlock()

	if plcInstance == nil {
		return
	}

	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := plcInstance.HealthCheck(healthCtx); err != nil {
		sm.logger.Warn("PLC não saudável", "error", err)
		go sm.reconnectPLC(ctx)
	}
}

// connectEnabledRadars conecta apenas radares habilitados pelo PLC
func (sm *SystemManager) connectEnabledRadars(ctx context.Context) error {
	var enabledRadars map[string]bool
	var connectionErrors []string

	// Obter status de habilitação do PLC
	if sm.plcController != nil {
		enabledRadars = sm.plcController.GetRadarsEnabled()
		sm.logger.Info("Status PLC obtido",
			"caldeira", enabledRadars["caldeira"],
			"porta_jusante", enabledRadars["porta_jusante"],
			"porta_montante", enabledRadars["porta_montante"],
		)
	} else {
		// Se PLC indisponível, tentar conectar todos
		enabledRadars = map[string]bool{
			"caldeira":       true,
			"porta_jusante":  true,
			"porta_montante": true,
		}
		sm.logger.Warn("PLC indisponível - tentando conectar todos os radares")
	}

	// Conectar radares habilitados
	for id, enabled := range enabledRadars {
		config, exists := sm.radarManager.GetRadarConfig(id)
		if !exists {
			continue
		}

		if !enabled {
			sm.logger.Info("Radar desabilitado", "id", id, "name", config.Name)
			continue
		}

		radarInstance, _ := sm.radarManager.GetRadar(id)
		sm.logger.Info("Conectando radar", "id", id, "name", config.Name)

		if err := sm.radarManager.ConnectRadarWithRetry(radarInstance, 3); err != nil {
			errorMsg := fmt.Sprintf("%s: %v", config.Name, err)
			connectionErrors = append(connectionErrors, errorMsg)
			sm.logger.Error("Falha na conexão do radar", "id", id, "error", err)
		} else {
			sm.logger.Info("Radar conectado", "id", id, "name", config.Name)
		}
	}

	if len(connectionErrors) > 0 {
		return fmt.Errorf("falhas de conexão: %v", connectionErrors)
	}

	return nil
}

// runMainLoop executa o loop principal do sistema
func (sm *SystemManager) runMainLoop(ctx context.Context) {
	ticker := time.NewTicker(sm.config.CollectionDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sm.shutdown:
			return
		case <-ticker.C:
			sm.processMainCycleWithTimeout(ctx)
		}
	}
}

// processMainCycleWithTimeout executa ciclo com timeout
func (sm *SystemManager) processMainCycleWithTimeout(ctx context.Context) {
	// Context com timeout para operação completa
	cycleCtx, cancel := context.WithTimeout(ctx, sm.config.OperationTimeout)
	defer cancel()

	// Verificar comandos do PLC
	collectionActive := true
	var enabledRadars map[string]bool

	sm.mu.RLock()
	plcController := sm.plcController
	sm.mu.RUnlock()

	if plcController != nil {
		// Verificar parada de emergência
		if plcController.IsEmergencyStop() {
			sm.logger.Warn("Parada de emergência ativada")
			return
		}

		// Verificar se coleta está ativa
		collectionActive = plcController.IsCollectionActive()
		enabledRadars = plcController.GetRadarsEnabled()
	}

	// Se coleta inativa, aguardar
	if !collectionActive {
		return
	}

	// Coletar dados com timeout
	multiRadarData, err := sm.collectRadarDataWithTimeout(cycleCtx, enabledRadars)
	if err != nil {
		sm.logger.Warn("Erro na coleta de dados", "error", err)
		return
	}

	// Atualizar dashboard
	sm.displayDashboard(enabledRadars)

	// Enviar dados via WebSocket (não bloqueante)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				sm.logger.Error("Panic no WebSocket broadcast", "error", r)
			}
		}()
		sm.wsManager.BroadcastMultiRadarData(multiRadarData)
	}()

	// Atualizar PLC se disponível (com timeout)
	if plcController != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					sm.logger.Error("Panic no update PLC", "error", r)
				}
			}()

			plcCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			sm.updatePLCDataWithTimeout(plcCtx, multiRadarData)
		}()
	}
}

// collectRadarDataWithTimeout coleta dados com timeout
func (sm *SystemManager) collectRadarDataWithTimeout(ctx context.Context, enabledRadars map[string]bool) (models.MultiRadarData, error) {
	done := make(chan models.MultiRadarData, 1)
	errChan := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				errChan <- fmt.Errorf("panic na coleta: %v", r)
			}
		}()

		data := sm.radarManager.CollectEnabledRadarsData(enabledRadars)
		select {
		case done <- data:
		case <-ctx.Done():
		}
	}()

	select {
	case data := <-done:
		return data, nil
	case err := <-errChan:
		return models.MultiRadarData{}, err
	case <-ctx.Done():
		return models.MultiRadarData{}, ctx.Err()
	}
}

// displayDashboard mostra status fixo para produção
func (sm *SystemManager) displayDashboard(enabledRadars map[string]bool) {
	// Proteger contra panic
	defer func() {
		if r := recover(); r != nil {
			sm.logger.Error("Panic no dashboard", "error", r)
		}
	}()

	// Usar cursor positioning ao invés de clear
	fmt.Print("\033[H") // Move cursor para topo
	fmt.Println("===== STATUS SISTEMA RADAR SICK - PRODUÇÃO 24/7 =====")

	// Timestamp atual
	now := time.Now()
	fmt.Printf("🕐 %s | Uptime: %v\n",
		now.Format("15:04:05"),
		time.Since(sm.getSystemStartTime()),
	)

	// Status PLC (linha fixa)
	sm.mu.RLock()
	plcConnected := sm.plcInstance != nil && sm.plcInstance.IsConnected()
	plcIP := sm.config.PLCConfig.IP
	sm.mu.RUnlock()

	plcStatus := "DESCONECTADO"
	plcIcon := "🔴"
	if plcConnected {
		plcStatus = "CONECTADO"
		plcIcon = "🟢"
	}
	fmt.Printf("🏭 PLC Siemens %-15s: %s %-12s\n", plcIP, plcIcon, plcStatus)

	// Status Radares (posições fixas)
	connectionStatus := sm.radarManager.GetConnectionStatus()
	connectedCount := 0
	enabledCount := 0

	radarOrder := []string{"caldeira", "porta_jusante", "porta_montante"}
	for _, id := range radarOrder {
		config, _ := sm.radarManager.GetRadarConfig(id)
		connected := connectionStatus[id]
		isEnabled := enabledRadars != nil && enabledRadars[id]

		if isEnabled {
			enabledCount++
		}
		if connected && isEnabled {
			connectedCount++
		}

		var status string
		var icon string
		if !isEnabled {
			status = "DESABILITADO"
			icon = "⚫"
		} else if connected {
			status = "CONECTADO"
			icon = "🟢"
		} else {
			status = "DESCONECTADO"
			icon = "🔴"
		}

		// Linha fixa por radar
		fmt.Printf("📡 %-20s: %s %-12s IP: %s\n",
			config.Name, icon, status, config.IP,
		)
	}

	// Estatísticas de sistema (linha fixa)
	wsClients := sm.wsManager.GetConnectedCount()
	fmt.Printf("\n📊 Status: %d/%d habilitados | %d conectados | WS: %d clientes\n",
		enabledCount, len(sm.config.RadarConfigs), connectedCount, wsClients,
	)

	// Métricas de recovery (se houver)
	sm.mu.RLock()
	mainRestarts := sm.mainLoopRestarts
	plcReconnects := sm.plcReconnectAttempts
	sm.mu.RUnlock()

	if mainRestarts > 0 || plcReconnects > 0 {
		fmt.Printf("🔄 Recovery: MainLoop=%d | PLC=%d\n", mainRestarts, plcReconnects)
	}

	// Estatísticas de produção
	if plcConnected {
		stats := sm.getProductionStats()
		fmt.Printf("📈 Produção: %d pacotes | %d erros | CPU: %.1f%% | Mem: %.1f%%\n",
			stats.TotalPackets, stats.ErrorCount, stats.CPUUsage, stats.MemoryUsage,
		)
	}

	// Limpar linhas extras (manter terminal limpo)
	fmt.Print("\033[J") // Clear do cursor até fim da tela
	fmt.Print("========================================================\n")
}

// updatePLCDataWithTimeout atualiza dados no PLC com timeout
func (sm *SystemManager) updatePLCDataWithTimeout(ctx context.Context, multiRadarData models.MultiRadarData) {
	sm.mu.RLock()
	plcController := sm.plcController
	sm.mu.RUnlock()

	if plcController == nil {
		return
	}

	// Escrever dados dos radares
	if err := plcController.WriteMultiRadarData(multiRadarData); err != nil {
		sm.logger.Error("Erro escrevendo dados no PLC", "error", err)
		return
	}

	// Atualizar status dos componentes
	connectionStatus := sm.radarManager.GetConnectionStatus()
	plcController.SetRadarsConnected(connectionStatus)
	plcController.SetNATSConnected(false)
	plcController.SetWebSocketRunning(true)
	plcController.UpdateWebSocketClients(sm.wsManager.GetConnectedCount())
}

// Shutdown para o sistema graciosamente
func (sm *SystemManager) Shutdown(ctx context.Context) error {
	sm.logger.Info("Iniciando shutdown do sistema")

	// Sinalizar shutdown
	close(sm.shutdown)

	// Criar canal para aguardar finalização
	done := make(chan struct{})
	go func() {
		sm.wg.Wait()
		close(done)
	}()

	// Aguardar finalização ou timeout
	select {
	case <-done:
		sm.logger.Info("Goroutines finalizadas com sucesso")
	case <-ctx.Done():
		sm.logger.Warn("Timeout no shutdown das goroutines")
	}

	// Desconectar componentes
	sm.disconnectComponents()

	sm.logger.Info("Sistema finalizado")
	return nil
}

// disconnectComponents desconecta todos os componentes
func (sm *SystemManager) disconnectComponents() {
	// Desconectar PLC
	sm.mu.Lock()
	if sm.plcController != nil {
		sm.plcController.Stop()
	}
	if sm.plcInstance != nil {
		if err := sm.plcInstance.Close(); err != nil {
			sm.logger.Error("Erro desconectando PLC", "error", err)
		} else {
			sm.logger.Info("PLC desconectado")
		}
		sm.plcInstance = nil
		sm.plcController = nil
	}
	sm.mu.Unlock()

	// Desconectar radares
	sm.radarManager.DisconnectAll()
	sm.logger.Info("Radares desconectados")
}

// setupLogger configura logging estruturado
func setupLogger() *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo, // Reduzido para produção
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}

// printStartupBanner exibe banner de inicialização
func printStartupBanner(config *SystemConfig) {
	fmt.Println("\n===== SISTEMA RADAR SICK - PRODUCTION 24/7 =====")
	fmt.Printf("Servidor Web/WebSocket: http://localhost:%s\n", config.WebServerPort)
	fmt.Println("Sistema com auto-recovery e health monitoring")
	fmt.Printf("PLC Siemens: %s\n", config.PLCConfig.IP)
	fmt.Printf("Radares configurados: %d\n", len(config.RadarConfigs))
	fmt.Printf("Timeout operações: %v\n", config.OperationTimeout)
	fmt.Println("=================================================")
}

// setupGracefulShutdown configura captura de sinais para shutdown gracioso
func setupGracefulShutdown() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-signalChan
		fmt.Printf("\n🛑 Sinal %s recebido. Finalizando sistema graciosamente...\n", sig)
		cancel()
	}()

	return ctx, cancel
}

func main() {
	// Configurar logger
	logger := setupLogger()

	// Carregar configuração
	config := DefaultSystemConfig()

	// Exibir banner
	printStartupBanner(config)

	// Configurar shutdown gracioso
	ctx, cancel := setupGracefulShutdown()
	defer cancel()

	// Criar gerenciador do sistema
	systemManager := NewSystemManager(config, logger)

	// Iniciar sistema
	if err := systemManager.Start(ctx); err != nil {
		logger.Error("Erro iniciando sistema", "error", err)
		os.Exit(1)
	}

	logger.Info("🚀 Sistema PRODUCTION iniciado")
	logger.Info("📡 Monitoramento inteligente com auto-recovery")
	logger.Info("⚡ Health check PLC a cada 30s")
	logger.Info("🛡️ Panic recovery em todas as goroutines críticas")
	logger.Info("⏱️ Timeouts em operações bloqueantes")
	logger.Info("🎛️ Pressione Ctrl+C para parar graciosamente")

	// Aguardar sinal de shutdown
	<-ctx.Done()

	// Executar shutdown gracioso
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		config.ShutdownTimeout,
	)
	defer shutdownCancel()

	if err := systemManager.Shutdown(shutdownCtx); err != nil {
		logger.Error("Erro durante shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Sistema finalizado com sucesso")
}
