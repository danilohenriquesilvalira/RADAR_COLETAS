package radar

import (
	"context"
	"fmt"
	"sync"
	"time"

	"backend/internal/logger"
	"backend/pkg/models"
)

// RadarConfig representa a configuração de um radar
type RadarConfig struct {
	ID   string
	Name string
	IP   string
	Port int
}

// RadarManager gerencia múltiplos radares de forma thread-safe
type RadarManager struct {
	radars  map[string]*SICKRadar
	configs map[string]RadarConfig
	mutex   sync.RWMutex

	systemLogger *logger.SystemLogger

	lastReconnectAttempt map[string]time.Time
	radarMutexes         map[string]*sync.Mutex

	lastCleanup  time.Time
	cleanupMutex sync.Mutex
}

func NewRadarManager() *RadarManager {
	rm := &RadarManager{
		radars:               make(map[string]*SICKRadar),
		configs:              make(map[string]RadarConfig),
		lastReconnectAttempt: make(map[string]time.Time),
		radarMutexes:         make(map[string]*sync.Mutex),
		lastCleanup:          time.Now(),
	}

	go rm.cleanupWorker()
	return rm
}

func (rm *RadarManager) SetSystemLogger(logger *logger.SystemLogger) {
	rm.systemLogger = logger
}

func (rm *RadarManager) cleanupWorker() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rm.cleanupOldEntries()
	}
}

func (rm *RadarManager) cleanupOldEntries() {
	rm.cleanupMutex.Lock()
	defer rm.cleanupMutex.Unlock()

	now := time.Now()
	if now.Sub(rm.lastCleanup) < 25*time.Minute {
		return
	}

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	cutoff := now.Add(-2 * time.Hour)

	for radarID, lastTime := range rm.lastReconnectAttempt {
		if lastTime.Before(cutoff) {
			if _, exists := rm.configs[radarID]; !exists {
				delete(rm.lastReconnectAttempt, radarID)
				delete(rm.radarMutexes, radarID)
			}
		}
	}

	rm.lastCleanup = now
}

func (rm *RadarManager) AddRadar(config RadarConfig) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if len(rm.radars) >= 50 {
		err := fmt.Errorf("limite máximo de radares atingido (50)")
		if rm.systemLogger != nil {
			rm.systemLogger.LogCriticalError("RADAR_MANAGER", "MAX_RADARS_EXCEEDED", err)
		}
		return err
	}

	if config.ID == "" || len(config.ID) > 100 || config.Name == "" || len(config.Name) > 200 || config.IP == "" || len(config.IP) > 50 {
		err := fmt.Errorf("configuração inválida para radar")
		if rm.systemLogger != nil {
			rm.systemLogger.LogCriticalError("RADAR_MANAGER", "INVALID_RADAR_CONFIG", err)
		}
		return err
	}

	if _, exists := rm.radars[config.ID]; exists {
		err := fmt.Errorf("radar com ID %s já existe", config.ID)
		if rm.systemLogger != nil {
			rm.systemLogger.LogCriticalError("RADAR_MANAGER", "ADD_RADAR_DUPLICATE", err)
		}
		return err
	}

	radar := NewSICKRadar(config.IP, config.Port)
	rm.radars[config.ID] = radar
	rm.configs[config.ID] = config
	rm.lastReconnectAttempt[config.ID] = time.Time{}
	rm.radarMutexes[config.ID] = &sync.Mutex{}

	return nil
}

func (rm *RadarManager) getRadarSafe(radarID string) *SICKRadar {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.radars[radarID]
}

func (rm *RadarManager) getConfigSafe(radarID string) (RadarConfig, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	config, exists := rm.configs[radarID]
	return config, exists
}

// CORREÇÃO: getRadarMutex retorna (mutex, exists) para evitar nil pointer
func (rm *RadarManager) getRadarMutex(radarID string) (*sync.Mutex, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	mutex, exists := rm.radarMutexes[radarID]
	return mutex, exists
}

func (rm *RadarManager) GetRadar(radarID string) (*SICKRadar, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	radar, exists := rm.radars[radarID]
	return radar, exists
}

func (rm *RadarManager) GetAllRadars() map[string]*SICKRadar {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	result := make(map[string]*SICKRadar)
	for radarID, radar := range rm.radars {
		result[radarID] = radar
	}
	return result
}

func (rm *RadarManager) GetRadarConfig(radarID string) (RadarConfig, bool) {
	return rm.getConfigSafe(radarID)
}

func (rm *RadarManager) canAttemptReconnect(radarID string) bool {
	rm.mutex.RLock()
	lastAttempt := rm.lastReconnectAttempt[radarID]
	rm.mutex.RUnlock()

	if lastAttempt.IsZero() {
		return true
	}

	return time.Since(lastAttempt) >= 10*time.Second
}

func (rm *RadarManager) updateLastAttempt(radarID string) {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if len(rm.lastReconnectAttempt) >= 1000 {
		oldestTime := time.Now()
		oldestID := ""
		for id, t := range rm.lastReconnectAttempt {
			if t.Before(oldestTime) {
				oldestTime = t
				oldestID = id
			}
		}
		if oldestID != "" {
			delete(rm.lastReconnectAttempt, oldestID)
		}
	}

	rm.lastReconnectAttempt[radarID] = time.Now()
}

func (rm *RadarManager) ConnectAll() map[string]error {
	rm.mutex.RLock()
	configs := make(map[string]RadarConfig)
	radars := make(map[string]*SICKRadar)
	for radarID, config := range rm.configs {
		configs[radarID] = config
		radars[radarID] = rm.radars[radarID]
	}
	rm.mutex.RUnlock()

	errors := make(map[string]error)

	for radarID, radar := range radars {
		config := configs[radarID]

		err := rm.connectRadarWithRetry(radar, 3)
		if err != nil {
			errors[radarID] = err
			if rm.systemLogger != nil {
				rm.systemLogger.LogCriticalError("RADAR_MANAGER", "RADAR_CONNECTION_FAILED",
					fmt.Errorf("radar %s (%s): %v", config.Name, config.ID, err))
			}
		}
	}

	return errors
}

func (rm *RadarManager) ConnectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
	return rm.connectRadarWithRetry(radar, maxRetries)
}

func (rm *RadarManager) connectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
	if radar == nil {
		return fmt.Errorf("radar is nil")
	}

	if maxRetries < 1 {
		maxRetries = 1
	}
	if maxRetries > 10 {
		maxRetries = 10
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := radar.Connect()
		if err == nil {
			err = radar.StartMeasurement()
			if err == nil {
				return nil
			} else {
				radar.Disconnect()
			}
		}

		if attempt < maxRetries {
			time.Sleep(2 * time.Second)
		}
	}

	return fmt.Errorf("falha ao conectar após %d tentativas", maxRetries)
}

func (rm *RadarManager) DisconnectAll() {
	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for radarID, radar := range rm.radars {
		radars[radarID] = radar
		configs[radarID] = rm.configs[radarID]
	}
	rm.mutex.RUnlock()

	for radarID, radar := range radars {
		config := configs[radarID]
		if radar.IsConnected() {
			radar.Disconnect()
			if rm.systemLogger != nil {
				rm.systemLogger.LogRadarDisconnected(config.ID, config.Name)
			}
		}
	}
}

func (rm *RadarManager) GetConnectionStatus() map[string]bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	status := make(map[string]bool)
	for radarID, radar := range rm.radars {
		status[radarID] = radar.IsConnected()
	}
	return status
}

// CollectEnabledRadarsDataAsyncWithContext coleta dados dos radares habilitados de forma paralela
func (rm *RadarManager) CollectEnabledRadarsDataAsyncWithContext(ctx context.Context, enabledRadars map[string]bool) models.MultiRadarData {
	var radarDataList []models.RadarData

	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	if timestamp < 0 || timestamp > (1<<53-1) {
		timestamp = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano() / int64(time.Millisecond)
	}

	type radarResult struct {
		data models.RadarData
		id   string
	}

	resultChan := make(chan radarResult, len(enabledRadars))
	var wg sync.WaitGroup

	localCtx, cancel := context.WithTimeout(ctx, 450*time.Millisecond)
	defer cancel()

	for radarID, isEnabled := range enabledRadars {
		if !isEnabled {
			config, exists := rm.getConfigSafe(radarID)
			if exists {
				radarData := models.RadarData{
					RadarID:    radarID,
					RadarName:  config.Name,
					Connected:  false,
					Timestamp:  timestamp,
					Positions:  []float64{},
					Velocities: []float64{},
					Azimuths:   []float64{},
					Amplitudes: []float64{},
					MainObject: nil,
				}

				select {
				case resultChan <- radarResult{data: radarData, id: radarID}:
				case <-localCtx.Done():
					return models.MultiRadarData{Radars: radarDataList, Timestamp: timestamp}
				}
			}
			continue
		}

		wg.Add(1)
		go func(id string) {
			defer func() {
				wg.Done()
				if r := recover(); r != nil {
					if rm.systemLogger != nil {
						rm.systemLogger.LogCriticalError("RADAR_MANAGER", "RADAR_PANIC",
							fmt.Errorf("panic in radar %s: %v", id, r))
					}
				}
			}()

			select {
			case <-localCtx.Done():
				return
			default:
			}

			radar := rm.getRadarSafe(id)
			config, exists := rm.getConfigSafe(id)

			if radar == nil || !exists {
				return
			}

			// CORREÇÃO: Verificar se mutex existe antes de usar
			radarMutex, mutexExists := rm.getRadarMutex(id)
			if !mutexExists || radarMutex == nil {
				return
			}

			radarCtx, radarCancel := context.WithTimeout(localCtx, 200*time.Millisecond)
			defer radarCancel()

			done := make(chan bool, 1)
			var radarData models.RadarData

			go func() {
				defer func() {
					if r := recover(); r != nil {
						if rm.systemLogger != nil {
							rm.systemLogger.LogCriticalError("RADAR_MANAGER", "RADAR_PANIC",
								fmt.Errorf("panic in radar %s: %v", id, r))
						}
					}
				}()

				radarMutex.Lock()
				defer radarMutex.Unlock()

				actuallyConnected := radar.IsConnected()

				radarData = models.RadarData{
					RadarID:   id,
					RadarName: config.Name,
					Connected: actuallyConnected,
					Timestamp: timestamp,
				}

				if actuallyConnected {
					// Usar o novo método integrado ReadAndProcessData
					positions, velocities, azimuths, amplitudes, objPrincipal, err := radar.ReadAndProcessData()
					if err != nil {
						if rm.systemLogger != nil {
							rm.systemLogger.LogCriticalError("RADAR_MANAGER", "RADAR_READ_ERROR",
								fmt.Errorf("radar %s read failed: %v", config.Name, err))
						}
						radarData.Connected = false
						radarData.Positions = []float64{}
						radarData.Velocities = []float64{}
						radarData.Azimuths = []float64{}
						radarData.Amplitudes = []float64{}
						radarData.MainObject = nil
					} else {
						// Limitar arrays de resposta se necessário
						if len(positions) > 1000 {
							positions = positions[:1000]
						}
						if len(velocities) > 1000 {
							velocities = velocities[:1000]
						}
						if len(azimuths) > 1000 {
							azimuths = azimuths[:1000]
						}
						if len(amplitudes) > 1000 {
							amplitudes = amplitudes[:1000]
						}

						radarData.Positions = positions
						radarData.Velocities = velocities
						radarData.Azimuths = azimuths
						radarData.Amplitudes = amplitudes
						radarData.MainObject = objPrincipal
					}
				} else {
					radarData.Positions = []float64{}
					radarData.Velocities = []float64{}
					radarData.Azimuths = []float64{}
					radarData.Amplitudes = []float64{}
					radarData.MainObject = nil
				}

				done <- true
			}()

			select {
			case <-done:
				select {
				case resultChan <- radarResult{data: radarData, id: id}:
				case <-radarCtx.Done():
				}
			case <-radarCtx.Done():
				if rm.systemLogger != nil {
					rm.systemLogger.LogCriticalError("RADAR_MANAGER", "RADAR_COLLECTION_TIMEOUT",
						fmt.Errorf("radar %s collection timeout", config.Name))
				}
			}
		}(radarID)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	results := make(map[string]models.RadarData)

	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				goto buildResponse
			}
			results[result.id] = result.data

		case <-localCtx.Done():
			goto buildResponse
		}
	}

buildResponse:
	for radarID := range enabledRadars {
		if data, exists := results[radarID]; exists {
			radarDataList = append(radarDataList, data)
		} else {
			config, exists := rm.getConfigSafe(radarID)
			if exists {
				radarData := models.RadarData{
					RadarID:    radarID,
					RadarName:  config.Name,
					Connected:  false,
					Timestamp:  timestamp,
					Positions:  []float64{},
					Velocities: []float64{},
					Azimuths:   []float64{},
					Amplitudes: []float64{},
					MainObject: nil,
				}
				radarDataList = append(radarDataList, radarData)
			}
		}
	}

	return models.MultiRadarData{
		Radars:    radarDataList,
		Timestamp: timestamp,
	}
}

func (rm *RadarManager) CheckAndReconnectEnabledAsyncWithContext(ctx context.Context, enabledRadars map[string]bool) {
	var wg sync.WaitGroup

	for radarID, isEnabled := range enabledRadars {
		if !isEnabled {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()

				select {
				case <-ctx.Done():
					return
				default:
				}

				radar := rm.getRadarSafe(id)
				if radar != nil && radar.IsConnected() {
					// CORREÇÃO: Verificar se mutex existe antes de usar
					radarMutex, mutexExists := rm.getRadarMutex(id)
					if mutexExists && radarMutex != nil {
						radarMutex.Lock()
						radar.Disconnect()
						radarMutex.Unlock()
					}
				}
			}(radarID)
			continue
		}

		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
			}

			radar := rm.getRadarSafe(id)
			config, exists := rm.getConfigSafe(id)

			if radar == nil || !exists || !rm.canAttemptReconnect(id) || radar.IsConnected() {
				return
			}

			// CORREÇÃO: Verificar se mutex existe antes de usar
			radarMutex, mutexExists := rm.getRadarMutex(id)
			if !mutexExists || radarMutex == nil {
				return
			}

			reconCtx, reconCancel := context.WithTimeout(ctx, 15*time.Second)
			defer reconCancel()

			done := make(chan error, 1)

			go func() {
				radarMutex.Lock()
				defer radarMutex.Unlock()

				rm.updateLastAttempt(id)
				err := rm.connectRadarWithRetry(radar, 1)
				done <- err
			}()

			select {
			case err := <-done:
				if err != nil && rm.systemLogger != nil {
					rm.systemLogger.LogCriticalError("RADAR_MANAGER", "ASYNC_RECONNECTION_FAILED",
						fmt.Errorf("radar %s async reconnection failed: %v", config.Name, err))
				}
			case <-reconCtx.Done():
				if rm.systemLogger != nil {
					rm.systemLogger.LogCriticalError("RADAR_MANAGER", "ASYNC_RECONNECTION_TIMEOUT",
						fmt.Errorf("radar %s async reconnection timeout", config.Name))
				}
			}
		}(radarID)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Métodos de compatibilidade
func (rm *RadarManager) CollectEnabledRadarsDataAsync(enabledRadars map[string]bool) models.MultiRadarData {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return rm.CollectEnabledRadarsDataAsyncWithContext(ctx, enabledRadars)
}

func (rm *RadarManager) CheckAndReconnectEnabledAsync(enabledRadars map[string]bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rm.CheckAndReconnectEnabledAsyncWithContext(ctx, enabledRadars)
}

func (rm *RadarManager) CollectEnabledRadarsData(enabledRadars map[string]bool) models.MultiRadarData {
	return rm.CollectEnabledRadarsDataAsync(enabledRadars)
}

func (rm *RadarManager) CheckAndReconnectEnabled(enabledRadars map[string]bool) {
	rm.CheckAndReconnectEnabledAsync(enabledRadars)
}

func (rm *RadarManager) CollectAllData() models.MultiRadarData {
	rm.mutex.RLock()
	allRadars := make(map[string]bool)
	for radarID := range rm.radars {
		allRadars[radarID] = true
	}
	rm.mutex.RUnlock()

	return rm.CollectEnabledRadarsDataAsync(allRadars)
}

func (rm *RadarManager) StartReconnectionMonitor() {
	// Deprecated - use CheckAndReconnectEnabledAsyncWithContext
}
