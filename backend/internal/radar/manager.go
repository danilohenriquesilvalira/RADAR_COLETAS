package radar

import (
	"fmt"
	"sync"
	"time"

	"backend/pkg/models"
)

// RadarConfig configuração de radar
type RadarConfig struct {
	ID   string
	Name string
	IP   string
	Port int
}

// RadarManager gerenciador otimizado mas compatível
type RadarManager struct {
	radars  map[string]*SICKRadar
	configs map[string]RadarConfig
	mutex   sync.RWMutex
}

// NewRadarManager cria gerenciador
func NewRadarManager() *RadarManager {
	return &RadarManager{
		radars:  make(map[string]*SICKRadar),
		configs: make(map[string]RadarConfig),
	}
}

// AddRadar adiciona radar com validação básica
func (rm *RadarManager) AddRadar(config RadarConfig) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if _, exists := rm.radars[config.ID]; exists {
		return fmt.Errorf("radar com ID %s já existe", config.ID)
	}

	if config.Port == 0 {
		config.Port = 2111
	}

	radar := NewSICKRadar(config.IP, config.Port)
	rm.radars[config.ID] = radar
	rm.configs[config.ID] = config

	fmt.Printf("✅ Radar %s (%s) adicionado - IP: %s:%d\n", config.Name, config.ID, config.IP, config.Port)
	return nil
}

// GetRadar retorna radar específico
func (rm *RadarManager) GetRadar(id string) (*SICKRadar, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	radar, exists := rm.radars[id]
	return radar, exists
}

// GetAllRadars retorna todos os radares
func (rm *RadarManager) GetAllRadars() map[string]*SICKRadar {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	result := make(map[string]*SICKRadar, len(rm.radars))
	for id, radar := range rm.radars {
		result[id] = radar
	}
	return result
}

// GetRadarConfig retorna configuração
func (rm *RadarManager) GetRadarConfig(id string) (RadarConfig, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	config, exists := rm.configs[id]
	return config, exists
}

// ConnectAll conecta todos com worker pool
func (rm *RadarManager) ConnectAll() map[string]error {
	// Copiar maps de forma thread-safe
	radarsMap, configsMap := rm.copyMapsForProcessing()

	errors := make(map[string]error)
	var wg sync.WaitGroup
	var errorMutex sync.Mutex

	// Worker pool para conexões paralelas
	for id, radar := range radarsMap {
		wg.Add(1)
		go func(radarID string, r *SICKRadar, config RadarConfig) {
			defer wg.Done()

			fmt.Printf("🔄 Conectando ao radar %s (%s)...\n", config.Name, config.ID)
			err := rm.connectRadarWithRetry(r, 3)

			errorMutex.Lock()
			if err != nil {
				errors[radarID] = err
				fmt.Printf("❌ Falha ao conectar radar %s: %v\n", config.Name, err)
			} else {
				fmt.Printf("✅ Radar %s conectado com sucesso\n", config.Name)
			}
			errorMutex.Unlock()
		}(id, radar, configsMap[id])
	}

	wg.Wait()
	return errors
}

// copyMapsForProcessing copia maps de forma thread-safe
func (rm *RadarManager) copyMapsForProcessing() (map[string]*SICKRadar, map[string]RadarConfig) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	radars := make(map[string]*SICKRadar, len(rm.radars))
	configs := make(map[string]RadarConfig, len(rm.configs))

	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}

	return radars, configs
}

// ConnectRadarWithRetry método público
func (rm *RadarManager) ConnectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
	return rm.connectRadarWithRetry(radar, maxRetries)
}

// connectRadarWithRetry conecta com backoff exponencial
func (rm *RadarManager) connectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := radar.Connect()
		if err == nil {
			if measureErr := radar.StartMeasurement(); measureErr == nil {
				return nil
			}
			radar.Disconnect()
		}

		if attempt < maxRetries {
			// Backoff exponencial: 1s, 2s, 4s...
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			time.Sleep(backoff)
		}
	}

	return fmt.Errorf("falha ao conectar após %d tentativas", maxRetries)
}

// DisconnectAll desconecta todos
func (rm *RadarManager) DisconnectAll() {
	radars, configs := rm.copyMapsForProcessing()

	for id, radar := range radars {
		if radar.IsConnected() {
			radar.Disconnect()
			fmt.Printf("✅ Radar %s desconectado\n", configs[id].Name)
		}
	}
}

// GetConnectionStatus retorna status atual
func (rm *RadarManager) GetConnectionStatus() map[string]bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	status := make(map[string]bool, len(rm.radars))
	for id, radar := range rm.radars {
		status[id] = radar.IsConnected()
	}
	return status
}

// CollectEnabledRadarsData - MANTÉM FUNCIONALIDADE ORIGINAL
func (rm *RadarManager) CollectEnabledRadarsData(enabledRadars map[string]bool) models.MultiRadarData {
	radars, configs := rm.copyMapsForProcessing()

	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	radarDataList := make([]models.RadarData, 0, len(radars))

	// Processar cada radar
	for id, radar := range radars {
		config := configs[id]
		isEnabled := enabledRadars[id]

		radarData := models.RadarData{
			RadarID:   id,
			RadarName: config.Name,
			Connected: radar.IsConnected() && isEnabled,
			Timestamp: timestamp,
		}

		// Coletar dados se habilitado E conectado
		if isEnabled && radar.IsConnected() {
			rm.collectSingleRadarData(&radarData, radar)
		} else {
			rm.setEmptyRadarData(&radarData)
		}

		radarDataList = append(radarDataList, radarData)
	}

	return models.MultiRadarData{
		Radars:    radarDataList,
		Timestamp: timestamp,
	}
}

// collectSingleRadarData coleta dados de um radar
func (rm *RadarManager) collectSingleRadarData(radarData *models.RadarData, radar *SICKRadar) {
	data, err := radar.ReadData()
	if err == nil && data != nil && len(data) > 0 {
		positions, velocities, azimuths, amplitudes, objPrincipal := radar.ProcessData(data)
		radarData.Positions = positions
		radarData.Velocities = velocities
		radarData.Azimuths = azimuths
		radarData.Amplitudes = amplitudes
		radarData.MainObject = objPrincipal
	} else {
		rm.setEmptyRadarData(radarData)
	}
}

// setEmptyRadarData define dados vazios
func (rm *RadarManager) setEmptyRadarData(radarData *models.RadarData) {
	radarData.Positions = []float64{}
	radarData.Velocities = []float64{}
	radarData.Azimuths = []float64{}
	radarData.Amplitudes = []float64{}
	radarData.MainObject = nil
}

// CollectAllData método legado - FUNCIONALIDADE ORIGINAL
func (rm *RadarManager) CollectAllData() models.MultiRadarData {
	radars, configs := rm.copyMapsForProcessing()

	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	radarDataList := make([]models.RadarData, 0, len(radars))

	for id, radar := range radars {
		config := configs[id]
		radarData := models.RadarData{
			RadarID:   id,
			RadarName: config.Name,
			Connected: radar.IsConnected(),
			Timestamp: timestamp,
		}

		if radar.IsConnected() {
			rm.collectSingleRadarData(&radarData, radar)
		} else {
			rm.setEmptyRadarData(&radarData)
		}

		radarDataList = append(radarDataList, radarData)
	}

	return models.MultiRadarData{
		Radars:    radarDataList,
		Timestamp: timestamp,
	}
}

// StartReconnectionMonitor inicia monitoramento
func (rm *RadarManager) StartReconnectionMonitor() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			rm.checkAndReconnect()
		}
	}()
}

// CheckAndReconnectEnabled - MANTÉM LÓGICA ORIGINAL
func (rm *RadarManager) CheckAndReconnectEnabled(enabledRadars map[string]bool) {
	radars, configs := rm.copyMapsForProcessing()

	for id, radar := range radars {
		config := configs[id]
		isEnabled := enabledRadars[id]

		if isEnabled && !radar.IsConnected() {
			// Reconexão em goroutine para não bloquear
			go func(r *SICKRadar, cfg RadarConfig, radarID string) {
				fmt.Printf("🔄 Radar %s HABILITADO - iniciando reconexão...\n", cfg.Name)
				err := rm.connectRadarWithRetry(r, 2)
				if err != nil {
					fmt.Printf("❌ Falha na reconexão do radar %s: %v\n", cfg.Name, err)
				} else {
					fmt.Printf("✅ Radar %s reconectado com sucesso\n", cfg.Name)
				}
			}(radar, config, id)

		} else if !isEnabled && radar.IsConnected() {
			// Desconectar imediatamente se desabilitado
			fmt.Printf("⚠️ Radar %s DESABILITADO - desconectando...\n", config.Name)
			radar.Disconnect()
			fmt.Printf("✅ Radar %s desconectado (economia de recursos)\n", config.Name)
		}
	}
}

// checkAndReconnect verifica conexões perdidas
func (rm *RadarManager) checkAndReconnect() {
	radars, configs := rm.copyMapsForProcessing()

	for id, radar := range radars {
		if !radar.IsConnected() {
			config := configs[id]
			go func(r *SICKRadar, cfg RadarConfig) {
				fmt.Printf("🔄 Tentando reconectar radar %s (%s)...\n", cfg.Name, cfg.ID)
				err := rm.connectRadarWithRetry(r, 2)
				if err != nil {
					fmt.Printf("❌ Falha na reconexão do radar %s: %v\n", cfg.Name, err)
				} else {
					fmt.Printf("✅ Radar %s reconectado com sucesso\n", cfg.Name)
				}
			}(radar, config)
		}
	}
}
