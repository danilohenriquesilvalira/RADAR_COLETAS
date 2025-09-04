package logger

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type LogLevel int

const (
	LOG_ERROR LogLevel = iota
	LOG_WARN
	LOG_INFO
	LOG_DEBUG
)

type LogConfig struct {
	BasePath         string        // Caminho base para logs
	MaxFileSize      int64         // Tamanho máximo por arquivo (bytes)
	RetentionDays    int           // Dias para manter logs
	RotationInterval time.Duration // Intervalo de rotação
	EnableDebug      bool          // Habilitar logs de debug
	CleanupInterval  time.Duration // Intervalo entre limpezas

	// Controle de saída no console (stdout). Default: false (silencioso).
	ConsoleOutput bool

	// Throttling interno (defesa em profundidade)
	ThrottleInterval   time.Duration // intervalo para agrupar logs repetidos
	ThrottleMaxRepeats int           // limite de contagem antes de resetar
}

type SystemLogger struct {
	config LogConfig

	// Loggers por categoria
	errorLogger *log.Logger
	warnLogger  *log.Logger
	infoLogger  *log.Logger
	debugLogger *log.Logger

	// Arquivos ativos
	errorFile *os.File
	warnFile  *os.File
	infoFile  *os.File
	debugFile *os.File

	// Controle
	mu             sync.RWMutex
	lastRotation   time.Time
	cleanupCancel  context.CancelFunc
	isShuttingDown bool
	shutdownChan   chan struct{}

	// Throttling interno para evitar spam de mensagens idênticas
	throttleMu  sync.Mutex
	lastLog     map[string]time.Time // key -> last log time
	repeatCount map[string]int       // key -> number of repeats since last logged
}

// NewSystemLogger cria um novo logger com configuração padrão
func NewSystemLogger() *SystemLogger {
	config := LogConfig{
		BasePath:           "backend/logs",
		MaxFileSize:        50 * 1024 * 1024, // 50MB
		RetentionDays:      7,                // 7 dias
		RotationInterval:   24 * time.Hour,   // Rotação diária
		EnableDebug:        false,            // Debug desabilitado
		CleanupInterval:    1 * time.Hour,    // Limpeza a cada hora
		ConsoleOutput:      false,            // por padrão não imprimir no stdout
		ThrottleInterval:   30 * time.Second, // agrupar mensagens idênticas por 30s
		ThrottleMaxRepeats: 1000000,          // proteção contra overflow
	}
	return NewSystemLoggerWithConfig(config)
}

// NewSystemLoggerWithConfig cria um logger com configuração customizada
func NewSystemLoggerWithConfig(config LogConfig) *SystemLogger {
	logger := &SystemLogger{
		config:       config,
		lastRotation: time.Now(),
		shutdownChan: make(chan struct{}),
		lastLog:      make(map[string]time.Time),
		repeatCount:  make(map[string]int),
	}

	// Criar estrutura de diretórios SIMPLES
	if err := logger.createLogDirectories(); err != nil {
		log.Fatalf("Erro ao criar diretórios de log: %v", err)
	}

	// Inicializar arquivos de log
	if err := logger.initializeLogFiles(); err != nil {
		log.Fatalf("Erro ao inicializar arquivos de log: %v", err)
	}

	// Iniciar limpeza automática
	logger.startCleanupRoutine()

	return logger
}

// createLogDirectories cria a estrutura de diretórios SIMPLES
func (sl *SystemLogger) createLogDirectories() error {
	directories := []string{
		filepath.Join(sl.config.BasePath, "errors"),
		filepath.Join(sl.config.BasePath, "system"),
		filepath.Join(sl.config.BasePath, "warnings"),
		filepath.Join(sl.config.BasePath, "debug"),
	}

	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("erro ao criar diretório %s: %v", dir, err)
		}
	}

	return nil
}

// initializeLogFiles inicializa os arquivos de log
func (sl *SystemLogger) initializeLogFiles() error {
	dateStr := time.Now().Format("2006-01-02")

	var err error

	// Arquivo de ERROS
	errorPath := filepath.Join(sl.config.BasePath, "errors", fmt.Sprintf("errors_%s.log", dateStr))
	sl.errorFile, err = os.OpenFile(errorPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo de erro: %v", err)
	}
	sl.errorLogger = log.New(sl.errorFile, "[ERROR] ", log.LstdFlags|log.Lshortfile)

	// Arquivo de WARNINGS
	warnPath := filepath.Join(sl.config.BasePath, "warnings", fmt.Sprintf("warnings_%s.log", dateStr))
	sl.warnFile, err = os.OpenFile(warnPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo de warning: %v", err)
	}
	sl.warnLogger = log.New(sl.warnFile, "[WARN]  ", log.LstdFlags)

	// Arquivo de SISTEMA/INFO
	infoPath := filepath.Join(sl.config.BasePath, "system", fmt.Sprintf("system_%s.log", dateStr))
	sl.infoFile, err = os.OpenFile(infoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("erro ao criar arquivo de info: %v", err)
	}
	sl.infoLogger = log.New(sl.infoFile, "[INFO]  ", log.LstdFlags)

	// Arquivo de DEBUG (se habilitado)
	if sl.config.EnableDebug {
		debugPath := filepath.Join(sl.config.BasePath, "debug", fmt.Sprintf("debug_%s.log", dateStr))
		sl.debugFile, err = os.OpenFile(debugPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return fmt.Errorf("erro ao criar arquivo de debug: %v", err)
		}
		sl.debugLogger = log.New(sl.debugFile, "[DEBUG] ", log.LstdFlags|log.Lshortfile)
	} else {
		// Debug vai para stdout se não habilitado em arquivo
		sl.debugLogger = log.New(os.Stdout, "[DEBUG] ", log.LstdFlags)
	}

	return nil
}

// startCleanupRoutine inicia a rotina de limpeza automática
func (sl *SystemLogger) startCleanupRoutine() {
	ctx, cancel := context.WithCancel(context.Background())
	sl.cleanupCancel = cancel

	go func() {
		ticker := time.NewTicker(sl.config.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-sl.shutdownChan:
				return
			case <-ticker.C:
				sl.performMaintenance()
			}
		}
	}()
}

// performMaintenance executa manutenção automática
func (sl *SystemLogger) performMaintenance() {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if sl.isShuttingDown {
		return
	}

	// Verificar se precisa rotacionar
	if time.Since(sl.lastRotation) >= sl.config.RotationInterval {
		if err := sl.rotateLogsUnsafe(); err != nil {
			if sl.config.ConsoleOutput {
				fmt.Printf("Erro na rotação de logs: %v\n", err)
			}
		}
	}

	// Verificar tamanho dos arquivos
	sl.checkFileSizes()

	// LIMPAR LOGS ANTIGOS - SEM BACKUP
	if err := sl.cleanupOldLogsDirectly(); err != nil {
		if sl.config.ConsoleOutput {
			fmt.Printf("Erro na limpeza de logs: %v\n", err)
		}
	}
}

// checkFileSizes verifica se arquivos excederam o tamanho máximo
func (sl *SystemLogger) checkFileSizes() {
	files := []*os.File{sl.errorFile, sl.warnFile, sl.infoFile}
	if sl.debugFile != nil {
		files = append(files, sl.debugFile)
	}

	for _, file := range files {
		if file == nil {
			continue
		}

		if stat, err := file.Stat(); err == nil {
			if stat.Size() >= sl.config.MaxFileSize {
				if sl.config.ConsoleOutput {
					fmt.Printf("📋 Arquivo de log excedeu %dMB - forçando rotação\n", sl.config.MaxFileSize/1024/1024)
				}
				sl.rotateLogsUnsafe()
				break
			}
		}
	}
}

// rotateLogsUnsafe rotaciona os logs (deve ser chamado com lock)
func (sl *SystemLogger) rotateLogsUnsafe() error {
	// Fechar arquivos atuais
	sl.closeFilesUnsafe()

	// Reinicializar com nova data
	if err := sl.initializeLogFiles(); err != nil {
		return err
	}

	sl.lastRotation = time.Now()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("LOG_ROTATION_COMPLETED: timestamp=%s", sl.lastRotation.Format(time.RFC3339))
	}
	if sl.config.ConsoleOutput {
		fmt.Printf("LOG_ROTATION_COMPLETED: timestamp=%s\n", sl.lastRotation.Format(time.RFC3339))
	}

	return nil
}

// cleanupOldLogsDirectly remove logs antigos DIRETAMENTE - SEM BACKUP
func (sl *SystemLogger) cleanupOldLogsDirectly() error {
	cutoffDate := time.Now().AddDate(0, 0, -sl.config.RetentionDays)

	categories := []string{"errors", "system", "warnings", "debug"}

	totalCleaned := 0

	for _, category := range categories {
		categoryPath := filepath.Join(sl.config.BasePath, category)

		files, err := os.ReadDir(categoryPath)
		if err != nil {
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			filePath := filepath.Join(categoryPath, file.Name())

			// Verificar se arquivo é antigo
			info, err := file.Info()
			if err != nil {
				continue
			}

			if info.ModTime().Before(cutoffDate) {
				// Verificar se arquivo não está em uso
				if sl.isFileInUse(filePath) {
					continue
				}

				// Remover arquivo diretamente - sem backup
				if err := os.Remove(filePath); err != nil {
					if sl.errorLogger != nil {
						sl.errorLogger.Printf("CLEANUP_ERROR: file=%s error=%v", filePath, err)
					}
				} else {
					totalCleaned++
					if sl.infoLogger != nil {
						sl.infoLogger.Printf("LOG_CLEANUP: removed=%s age=%v category=%s",
							file.Name(), time.Since(info.ModTime()), category)
					}
				}
			}
		}
	}

	// Log apenas se limpou algo
	if totalCleaned > 0 && sl.config.ConsoleOutput {
		fmt.Printf("🧹 Limpeza automática: %d arquivos antigos removidos\n", totalCleaned)
	}

	return nil
}

// isFileInUse verifica se um arquivo está em uso
func (sl *SystemLogger) isFileInUse(filePath string) bool {
	// Verificar se é um dos arquivos ativos
	activeFiles := []string{
		sl.getActiveFilePath(sl.errorFile),
		sl.getActiveFilePath(sl.warnFile),
		sl.getActiveFilePath(sl.infoFile),
	}

	if sl.debugFile != nil {
		activeFiles = append(activeFiles, sl.getActiveFilePath(sl.debugFile))
	}

	for _, activePath := range activeFiles {
		if activePath == filePath {
			return true
		}
	}

	return false
}

// getActiveFilePath obtém o caminho do arquivo ativo
func (sl *SystemLogger) getActiveFilePath(file *os.File) string {
	if file == nil {
		return ""
	}
	return file.Name()
}

// closeFilesUnsafe fecha arquivos (deve ser chamado com lock)
func (sl *SystemLogger) closeFilesUnsafe() {
	if sl.errorFile != nil {
		sl.errorFile.Close()
		sl.errorFile = nil
	}
	if sl.warnFile != nil {
		sl.warnFile.Close()
		sl.warnFile = nil
	}
	if sl.infoFile != nil {
		sl.infoFile.Close()
		sl.infoFile = nil
	}
	if sl.debugFile != nil {
		sl.debugFile.Close()
		sl.debugFile = nil
	}
}

// ====================== MÉTODOS DE LOGGING - SINK APENAS ======================

// LogPLCDisconnected grava o evento; não decide política.
// Console output só se ConsoleOutput == true.
func (sl *SystemLogger) LogPLCDisconnected(attempts int, lastError error) {
	sl.mu.RLock()
	if sl.errorLogger != nil {
		sl.errorLogger.Printf("PLC_DISCONNECTED: attempts=%d, error=%v", attempts, lastError)
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Printf("PLC_DISCONNECTED: attempts=%d, error=%v\n", attempts, lastError)
	}
}

// LogPLCReconnected grava o evento; console opcional.
func (sl *SystemLogger) LogPLCReconnected(downtime time.Duration) {
	sl.mu.RLock()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("PLC_RECONNECTED: downtime=%v", downtime)
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Printf("🔌 PLC reconectado após %v\n", downtime)
	}
}

func (sl *SystemLogger) LogRadarDisconnected(radarID, radarName string) {
	sl.mu.RLock()
	if sl.warnLogger != nil {
		sl.warnLogger.Printf("RADAR_DISCONNECTED: %s (%s)", radarName, radarID)
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Printf("📡 Radar %s desconectado\n", radarName)
	}
}

func (sl *SystemLogger) LogRadarReconnected(radarID, radarName string, downtime time.Duration) {
	sl.mu.RLock()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("RADAR_RECONNECTED: %s (%s) downtime=%v", radarName, radarID, downtime)
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Printf("📡 Radar %s reconectado após %v\n", radarName, downtime)
	}
}

func (sl *SystemLogger) LogSystemStarted() {
	sl.mu.RLock()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("SYSTEM_STARTED: version=4.0 user=%s", getCurrentUser())
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Println("🚀 Sistema iniciado")
	}
}

func (sl *SystemLogger) LogSystemShutdown(uptime time.Duration) {
	sl.mu.RLock()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("SYSTEM_SHUTDOWN: uptime=%v", uptime)
	}
	sl.mu.RUnlock()

	if sl.config.ConsoleOutput {
		fmt.Printf("🛑 Sistema encerrado - uptime: %v\n", uptime)
	}
}

// LogCriticalError agora com throttling por mensagem (defesa em profundidade)
// Política de "quando logar" permanece no PLCManager; logger apenas grava quando chamado.
func (sl *SystemLogger) LogCriticalError(component, operation string, err error) {
	if err == nil {
		return
	}

	key := fmt.Sprintf("%s|%s|%s", component, operation, err.Error())
	now := time.Now()

	// Checar throttle
	sl.throttleMu.Lock()
	last, exists := sl.lastLog[key]
	if exists && now.Sub(last) < sl.config.ThrottleInterval {
		// contar repetição e silenciar
		count := sl.repeatCount[key]
		if count >= sl.config.ThrottleMaxRepeats {
			// overflow protection - reset
			sl.repeatCount[key] = 0
			sl.lastLog[key] = now
			sl.throttleMu.Unlock()
			return
		}
		sl.repeatCount[key] = count + 1
		sl.throttleMu.Unlock()
		return
	}

	// Se chegou aqui, vamos logar agora. Mas primeiro ver se havia repetições acumuladas
	repeats := sl.repeatCount[key]
	if repeats > 0 {
		aggregated := fmt.Errorf("%v (repeated %d times since %s)", err, repeats, last.Format(time.RFC3339))
		// reset counters
		sl.repeatCount[key] = 0
		sl.lastLog[key] = now
		sl.throttleMu.Unlock()

		// registrar agregada
		sl.mu.RLock()
		if sl.errorLogger != nil {
			sl.errorLogger.Printf("CRITICAL_ERROR: component=%s operation=%s error=%v", component, operation, aggregated)
		}
		sl.mu.RUnlock()
		if sl.config.ConsoleOutput {
			fmt.Printf("🔥 ERRO CRÍTICO em %s.%s: %v\n", component, operation, aggregated)
		}
		return
	}

	// Sem repetições pendentes — logar normalmente e atualizar lastLog
	sl.lastLog[key] = now
	sl.throttleMu.Unlock()

	sl.mu.RLock()
	if sl.errorLogger != nil {
		sl.errorLogger.Printf("CRITICAL_ERROR: component=%s operation=%s error=%v", component, operation, err)
	}
	sl.mu.RUnlock()
	if sl.config.ConsoleOutput {
		fmt.Printf("🔥 ERRO CRÍTICO em %s.%s: %v\n", component, operation, err)
	}
}

func (sl *SystemLogger) LogConfigurationChange(component, change string) {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("CONFIG_CHANGE: component=%s change=%s", component, change)
	}
	if sl.config.ConsoleOutput {
		fmt.Printf("CONFIG_CHANGE: component=%s change=%s\n", component, change)
	}
}

// LogDebug adiciona log de debug
func (sl *SystemLogger) LogDebug(component, message string) {
	if !sl.config.EnableDebug {
		return
	}
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	if sl.debugLogger != nil {
		sl.debugLogger.Printf("DEBUG: component=%s message=%s", component, message)
	}
	if sl.config.ConsoleOutput {
		fmt.Printf("DEBUG: component=%s message=%s\n", component, message)
	}
}

// GetLogStats SIMPLIFICADO - SEM ARCHIVE
func (sl *SystemLogger) GetLogStats() map[string]interface{} {
	sl.mu.RLock()
	defer sl.mu.RUnlock()

	stats := make(map[string]interface{})

	// Tamanhos dos arquivos ativos
	if sl.errorFile != nil {
		if stat, err := sl.errorFile.Stat(); err == nil {
			stats["error_file_size"] = stat.Size()
		}
	}

	if sl.infoFile != nil {
		if stat, err := sl.infoFile.Stat(); err == nil {
			stats["info_file_size"] = stat.Size()
		}
	}

	// CONTAGEM SIMPLES
	categories := []string{"errors", "system", "warnings", "debug"}
	for _, category := range categories {
		categoryPath := filepath.Join(sl.config.BasePath, category)
		if files, err := os.ReadDir(categoryPath); err == nil {
			stats[fmt.Sprintf("%s_file_count", category)] = len(files)
		}
	}

	stats["last_rotation"] = sl.lastRotation

	return stats
}

// ForceRotation força a rotação dos logs
func (sl *SystemLogger) ForceRotation() error {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	if sl.isShuttingDown {
		return fmt.Errorf("logger is shutting down")
	}

	return sl.rotateLogsUnsafe()
}

// Close fecha o logger com segurança
func (sl *SystemLogger) Close() {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	sl.isShuttingDown = true

	// Parar rotina de limpeza
	if sl.cleanupCancel != nil {
		sl.cleanupCancel()
	}

	// fechar canal (não bloquear se já fechado)
	select {
	case <-sl.shutdownChan:
		// já fechado
	default:
		close(sl.shutdownChan)
	}

	// Log de shutdown
	if sl.infoLogger != nil {
		sl.infoLogger.Printf("LOGGER_SHUTDOWN: timestamp=%s", time.Now().Format(time.RFC3339))
	}
	if sl.config.ConsoleOutput {
		fmt.Printf("LOGGER_SHUTDOWN: timestamp=%s\n", time.Now().Format(time.RFC3339))
	}

	// Fechar arquivos
	sl.closeFilesUnsafe()
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
