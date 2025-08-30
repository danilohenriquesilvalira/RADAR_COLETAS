package plc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// SystemCommand representa um comando do sistema
type SystemCommand int

const (
	CmdSystemRestart SystemCommand = iota
	CmdSystemShutdown
	CmdNATSRestart
	CmdWebSocketRestart
	CmdApplicationRestart
)

// SystemControllerInterface define contrato para controle de sistema
type SystemControllerInterface interface {
	Start(ctx context.Context) error
	RequestCommand(cmd SystemCommand) error
	GetSystemHealth() *SystemHealth
	UpdateRadarHealth()
	UpdatePLCHealth()
	Shutdown(ctx context.Context) error
}

// SystemControllerConfig configurações do controlador
type SystemControllerConfig struct {
	RestartDelay        time.Duration
	ShutdownTimeout     time.Duration
	HealthCheckInterval time.Duration
	MaxRestartAttempts  int
}

// DefaultSystemControllerConfig retorna configuração padrão
func DefaultSystemControllerConfig() *SystemControllerConfig {
	return &SystemControllerConfig{
		RestartDelay:        1 * time.Second,
		ShutdownTimeout:     30 * time.Second,
		HealthCheckInterval: 5 * time.Second,
		MaxRestartAttempts:  3,
	}
}

// SystemController gerencia comandos de sistema com robustez
type SystemController struct {
	config       *SystemControllerConfig
	logger       *slog.Logger
	commands     chan SystemCommand
	healthCheck  *HealthChecker
	shutdown     chan struct{}
	wg           sync.WaitGroup
	mu           sync.RWMutex
	restartCount int
	lastRestart  time.Time
}

// NewSystemController cria controlador robusto
func NewSystemController(config *SystemControllerConfig, logger *slog.Logger) *SystemController {
	if config == nil {
		config = DefaultSystemControllerConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &SystemController{
		config:      config,
		logger:      logger.With("component", "system_controller"),
		commands:    make(chan SystemCommand, 10),
		healthCheck: NewHealthChecker(),
		shutdown:    make(chan struct{}),
	}
}

// Start inicia o controlador com context
func (sc *SystemController) Start(ctx context.Context) error {
	sc.logger.Info("Iniciando controlador de sistema")

	// Iniciar processador de comandos
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		sc.processCommands(ctx)
	}()

	// Iniciar monitor de saúde
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		sc.runHealthMonitor(ctx)
	}()

	sc.logger.Info("Controlador de sistema iniciado")
	return nil
}

// processCommands processa comandos do sistema
func (sc *SystemController) processCommands(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sc.shutdown:
			return
		case cmd := <-sc.commands:
			sc.executeCommand(cmd)
		}
	}
}

// executeCommand executa comando específico
func (sc *SystemController) executeCommand(cmd SystemCommand) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	switch cmd {
	case CmdSystemRestart:
		sc.logger.Info("Executando restart do sistema")
		sc.executeSystemRestart()

	case CmdSystemShutdown:
		sc.logger.Info("Executando shutdown do sistema")
		sc.executeSystemShutdown()

	case CmdNATSRestart:
		sc.logger.Info("Executando restart NATS")
		// Implementação específica para NATS

	case CmdWebSocketRestart:
		sc.logger.Info("Executando restart WebSocket")
		// Implementação específica para WebSocket

	case CmdApplicationRestart:
		sc.logger.Info("Executando restart da aplicação")
		sc.executeApplicationRestart()

	default:
		sc.logger.Warn("Comando desconhecido", "command", cmd)
	}
}

// RequestCommand solicita execução de comando (thread-safe)
func (sc *SystemController) RequestCommand(cmd SystemCommand) error {
	// Verificar limitação de restarts
	if cmd == CmdSystemRestart || cmd == CmdApplicationRestart {
		if err := sc.validateRestartRequest(); err != nil {
			return err
		}
	}

	select {
	case sc.commands <- cmd:
		sc.logger.Debug("Comando enviado", "command", cmd)
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout enviando comando: %v", cmd)
	}
}

// validateRestartRequest valida se restart pode ser executado
func (sc *SystemController) validateRestartRequest() error {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Verificar se muitos restarts recentes
	if sc.restartCount >= sc.config.MaxRestartAttempts {
		if time.Since(sc.lastRestart) < 5*time.Minute {
			return fmt.Errorf("muitos restarts recentes (%d). Aguarde 5 minutos",
				sc.restartCount)
		}
		// Reset contador após cooldown
		sc.restartCount = 0
	}

	return nil
}

// executeSystemRestart executa restart do sistema
func (sc *SystemController) executeSystemRestart() {
	sc.incrementRestartCount()

	sc.logger.Warn("Iniciando restart do sistema",
		"attempt", sc.restartCount,
		"delay", sc.config.RestartDelay,
	)

	// Delay configurável
	time.Sleep(sc.config.RestartDelay)

	// Tentar método do sistema operacional
	if err := sc.restartViaSystemCommand(); err != nil {
		sc.logger.Error("Falha no restart via SO", "error", err)
		sc.restartViaExitCode()
	}
}

// restartViaSystemCommand executa restart via SO
func (sc *SystemController) restartViaSystemCommand() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("shutdown", "/r", "/t", "5", "/c", "Radar System Restart")
	case "linux":
		cmd = exec.Command("sudo", "reboot")
	default:
		return fmt.Errorf("SO não suportado: %s", runtime.GOOS)
	}

	sc.logger.Info("Executando comando de restart", "command", cmd.String())

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("erro executando restart: %w", err)
	}

	return nil
}

// restartViaExitCode restart via código de saída
func (sc *SystemController) restartViaExitCode() {
	sc.logger.Info("Restart via exit code especial")
	time.Sleep(500 * time.Millisecond)
	os.Exit(99) // Código para supervisores detectarem restart
}

// executeApplicationRestart restart apenas da aplicação
func (sc *SystemController) executeApplicationRestart() {
	sc.incrementRestartCount()

	executable, err := os.Executable()
	if err != nil {
		sc.logger.Error("Erro obtendo executável", "error", err)
		return
	}

	cmd := exec.Command(executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		sc.logger.Error("Erro iniciando nova instância", "error", err)
		return
	}

	sc.logger.Info("Nova instância iniciada", "pid", cmd.Process.Pid)

	// Sair graciosamente
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// executeSystemShutdown shutdown gracioso
func (sc *SystemController) executeSystemShutdown() {
	sc.logger.Info("Executando shutdown gracioso")
	time.Sleep(500 * time.Millisecond)
	os.Exit(0)
}

// incrementRestartCount incrementa contador de restarts
func (sc *SystemController) incrementRestartCount() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.restartCount++
	sc.lastRestart = time.Now()
}

// runHealthMonitor monitora saúde do sistema
func (sc *SystemController) runHealthMonitor(ctx context.Context) {
	ticker := time.NewTicker(sc.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sc.shutdown:
			return
		case <-ticker.C:
			sc.performHealthCheck()
		}
	}
}

// performHealthCheck executa verificação de saúde
func (sc *SystemController) performHealthCheck() {
	health := sc.healthCheck.GetSystemHealth()

	if !health.OverallHealthy {
		sc.logger.Warn("Sistema não saudável",
			"radar_healthy", health.RadarDataHealthy,
			"plc_healthy", health.PLCResponseHealthy,
			"uptime", health.SystemUptime,
		)
	}
}

// GetSystemHealth retorna status de saúde
func (sc *SystemController) GetSystemHealth() *SystemHealth {
	return sc.healthCheck.GetSystemHealth()
}

// UpdateRadarHealth atualiza saúde dos radares
func (sc *SystemController) UpdateRadarHealth() {
	sc.healthCheck.UpdateRadarData()
}

// UpdatePLCHealth atualiza saúde do PLC
func (sc *SystemController) UpdatePLCHealth() {
	sc.healthCheck.UpdatePLCResponse()
}

// Shutdown para o controlador graciosamente
func (sc *SystemController) Shutdown(ctx context.Context) error {
	sc.logger.Info("Iniciando shutdown do controlador")

	close(sc.shutdown)

	// Aguardar goroutines finalizarem
	done := make(chan struct{})
	go func() {
		sc.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		sc.logger.Info("Controlador finalizado")
		return nil
	case <-ctx.Done():
		sc.logger.Warn("Timeout no shutdown do controlador")
		return ctx.Err()
	}
}

// HealthChecker verifica saúde do sistema (refatorado)
type HealthChecker struct {
	lastRadarData   time.Time
	lastPLCResponse time.Time
	systemStartTime time.Time
	mu              sync.RWMutex
}

// NewHealthChecker cria verificador de saúde
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		systemStartTime: time.Now(),
	}
}

// UpdateRadarData thread-safe update
func (hc *HealthChecker) UpdateRadarData() {
	hc.mu.Lock()
	hc.lastRadarData = time.Now()
	hc.mu.Unlock()
}

// UpdatePLCResponse thread-safe update
func (hc *HealthChecker) UpdatePLCResponse() {
	hc.mu.Lock()
	hc.lastPLCResponse = time.Now()
	hc.mu.Unlock()
}

// GetSystemHealth retorna status completo de saúde
func (hc *HealthChecker) GetSystemHealth() *SystemHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	now := time.Now()

	radarAge := time.Duration(0)
	plcAge := time.Duration(0)

	if !hc.lastRadarData.IsZero() {
		radarAge = now.Sub(hc.lastRadarData)
	}

	if !hc.lastPLCResponse.IsZero() {
		plcAge = now.Sub(hc.lastPLCResponse)
	}

	health := &SystemHealth{
		SystemUptime:       now.Sub(hc.systemStartTime),
		LastRadarDataAge:   radarAge,
		LastPLCResponseAge: plcAge,
		RadarDataHealthy:   radarAge < 10*time.Second && !hc.lastRadarData.IsZero(),
		PLCResponseHealthy: plcAge < 30*time.Second && !hc.lastPLCResponse.IsZero(),
	}

	health.OverallHealthy = health.RadarDataHealthy && health.PLCResponseHealthy
	return health
}

// SystemHealth representa saúde do sistema
type SystemHealth struct {
	SystemUptime       time.Duration `json:"systemUptime"`
	LastRadarDataAge   time.Duration `json:"lastRadarDataAge"`
	LastPLCResponseAge time.Duration `json:"lastPLCResponseAge"`
	RadarDataHealthy   bool          `json:"radarDataHealthy"`
	PLCResponseHealthy bool          `json:"plcResponseHealthy"`
	OverallHealthy     bool          `json:"overallHealthy"`
}

// RestartManager gerencia operações de restart
type RestartManager struct {
	config      *SystemControllerConfig
	logger      *slog.Logger
	attempts    int
	lastAttempt time.Time
	mu          sync.RWMutex
}

// NewRestartManager cria gerenciador de restart
func NewRestartManager(config *SystemControllerConfig, logger *slog.Logger) *RestartManager {
	return &RestartManager{
		config: config,
		logger: logger.With("component", "restart_manager"),
	}
}

// ExecuteSystemRestart executa restart do sistema
func (rm *RestartManager) ExecuteSystemRestart() error {
	if err := rm.validateRestart(); err != nil {
		return err
	}

	rm.recordAttempt()

	rm.logger.Warn("Executando restart do sistema",
		"attempt", rm.attempts,
		"os", runtime.GOOS,
	)

	time.Sleep(rm.config.RestartDelay)

	if err := rm.executeOSRestart(); err != nil {
		rm.logger.Error("Falha no restart via SO", "error", err)
		rm.fallbackExitRestart()
	}

	return nil
}

// validateRestart valida se restart pode ser executado
func (rm *RestartManager) validateRestart() error {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if rm.attempts >= rm.config.MaxRestartAttempts {
		if time.Since(rm.lastAttempt) < 5*time.Minute {
			return fmt.Errorf("muitos restarts (%d). Cooldown: 5min", rm.attempts)
		}
		rm.attempts = 0 // Reset após cooldown
	}

	return nil
}

// recordAttempt registra tentativa de restart
func (rm *RestartManager) recordAttempt() {
	rm.mu.Lock()
	rm.attempts++
	rm.lastAttempt = time.Now()
	rm.mu.Unlock()
}

// executeOSRestart executa restart via SO
func (rm *RestartManager) executeOSRestart() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("shutdown", "/r", "/t", "5", "/c", "Radar System Restart")
	case "linux":
		cmd = exec.Command("sudo", "reboot")
	default:
		return fmt.Errorf("SO não suportado: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// fallbackExitRestart restart via exit code
func (rm *RestartManager) fallbackExitRestart() {
	rm.logger.Info("Usando exit code para restart")
	time.Sleep(500 * time.Millisecond)
	os.Exit(99)
}

// ExecuteApplicationRestart restart apenas da aplicação
func (rm *RestartManager) ExecuteApplicationRestart() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("erro obtendo executável: %w", err)
	}

	cmd := exec.Command(executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("erro iniciando nova instância: %w", err)
	}

	rm.logger.Info("Nova instância iniciada", "pid", cmd.Process.Pid)

	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()

	return nil
}

// SystemController refatorado (mantém interface original)
func (sc *SystemController) RequestSystemRestart() {
	if err := sc.RequestCommand(CmdSystemRestart); err != nil {
		sc.logger.Error("Erro solicitando restart", "error", err)
	}
}

func (sc *SystemController) RequestNATSRestart() {
	if err := sc.RequestCommand(CmdNATSRestart); err != nil {
		sc.logger.Error("Erro solicitando restart NATS", "error", err)
	}
}

func (sc *SystemController) RequestWebSocketRestart() {
	if err := sc.RequestCommand(CmdWebSocketRestart); err != nil {
		sc.logger.Error("Erro solicitando restart WebSocket", "error", err)
	}
}

func (sc *SystemController) RestartApplication() {
	if err := sc.RequestCommand(CmdApplicationRestart); err != nil {
		sc.logger.Error("Erro solicitando restart aplicação", "error", err)
	}
}
