package radar

import (
	"fmt"
	"sync"
	"time"

	"backend/pkg/models"
)

// RadarConfig representa a configuração de um radar
type RadarConfig struct {
	ID   string
	Name string
	IP   string
	Port int
}

// RadarManager gerencia múltiplos radares
type RadarManager struct {
	radars  map[string]*SICKRadar
	configs map[string]RadarConfig
	mutex   sync.RWMutex
	// CONTROLE DE RECONEXÃO PARA EVITAR CONFLITOS
	lastReconnectAttempt map[string]time.Time
	reconnectMutex       sync.Mutex
}

// NewRadarManager cria um novo gerenciador de radares
func NewRadarManager() *RadarManager {
	return &RadarManager{
		radars:               make(map[string]*SICKRadar),
		configs:              make(map[string]RadarConfig),
		lastReconnectAttempt: make(map[string]time.Time),
	}
}

// AddRadar adiciona um novo radar ao gerenciador
func (rm *RadarManager) AddRadar(config RadarConfig) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if _, exists := rm.radars[config.ID]; exists {
		return fmt.Errorf("radar com ID %s já existe", config.ID)
	}

	radar := NewSICKRadar(config.IP, config.Port)
	rm.radars[config.ID] = radar
	rm.configs[config.ID] = config
	rm.lastReconnectAttempt[config.ID] = time.Time{} // Inicializar

	fmt.Printf("✅ Radar %s (%s) adicionado - IP: %s:%d\n", config.Name, config.ID, config.IP, config.Port)
	return nil
}

// GetRadar retorna um radar específico
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

	result := make(map[string]*SICKRadar)
	for id, radar := range rm.radars {
		result[id] = radar
	}
	return result
}

// GetRadarConfig retorna a configuração de um radar
func (rm *RadarManager) GetRadarConfig(id string) (RadarConfig, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	config, exists := rm.configs[id]
	return config, exists
}

// ConnectAll tenta conectar todos os radares
func (rm *RadarManager) ConnectAll() map[string]error {
	rm.mutex.RLock()
	configs := make(map[string]RadarConfig)
	for id, config := range rm.configs {
		configs[id] = config
	}
	radars := make(map[string]*SICKRadar)
	for id, radar := range rm.radars {
		radars[id] = radar
	}
	rm.mutex.RUnlock()

	errors := make(map[string]error)

	for id, radar := range radars {
		config := configs[id]
		fmt.Printf("🔄 Conectando ao radar %s (%s)...\n", config.Name, config.ID)

		err := rm.connectRadarWithRetry(radar, 3)
		if err != nil {
			errors[id] = err
			fmt.Printf("❌ Falha ao conectar radar %s: %v\n", config.Name, err)
		} else {
			fmt.Printf("✅ Radar %s conectado com sucesso\n", config.Name)
		}
	}

	return errors
}

// ConnectRadarWithRetry tenta conectar um radar com retry (método público)
func (rm *RadarManager) ConnectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
	return rm.connectRadarWithRetry(radar, maxRetries)
}

// connectRadarWithRetry tenta conectar um radar com retry
func (rm *RadarManager) connectRadarWithRetry(radar *SICKRadar, maxRetries int) error {
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

// DisconnectAll desconecta todos os radares
func (rm *RadarManager) DisconnectAll() {
	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}
	rm.mutex.RUnlock()

	for id, radar := range radars {
		if radar.IsConnected() {
			radar.Disconnect()
			fmt.Printf("✅ Radar %s desconectado\n", configs[id].Name)
		}
	}
}

// GetConnectionStatus retorna o status de conexão de todos os radares
func (rm *RadarManager) GetConnectionStatus() map[string]bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	status := make(map[string]bool)
	for id, radar := range rm.radars {
		status[id] = radar.IsConnected()
	}
	return status
}

// CollectEnabledRadarsData coleta dados COM DETECÇÃO REAL DE DESCONEXÃO
func (rm *RadarManager) CollectEnabledRadarsData(enabledRadars map[string]bool) models.MultiRadarData {
	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}
	rm.mutex.RUnlock()

	var radarDataList []models.RadarData
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)

	for id, radar := range radars {
		config := configs[id]
		isEnabled := enabledRadars[id]

		// VERIFICAÇÃO REAL DE CONEXÃO
		actuallyConnected := isEnabled && radar.IsConnected()

		radarData := models.RadarData{
			RadarID:   id,
			RadarName: config.Name,
			Connected: actuallyConnected, // STATUS REAL
			Timestamp: timestamp,
		}

		// LÓGICA INTELIGENTE: Só coletar dados se estiver habilitado E REALMENTE conectado
		if isEnabled && actuallyConnected {
			data, err := radar.ReadData()
			if err != nil {
				// Erro na leitura - radar pode ter desconectado
				fmt.Printf("⚠️ Erro ao ler dados do radar %s: %v\n", config.Name, err)
				radarData.Connected = false // Marcar como desconectado
				radarData.Positions = []float64{}
				radarData.Velocities = []float64{}
				radarData.Azimuths = []float64{}
				radarData.Amplitudes = []float64{}
				radarData.MainObject = nil
			} else if data != nil && len(data) > 0 {
				positions, velocities, azimuths, amplitudes, objPrincipal := radar.ProcessData(data)
				radarData.Positions = positions
				radarData.Velocities = velocities
				radarData.Azimuths = azimuths
				radarData.Amplitudes = amplitudes
				radarData.MainObject = objPrincipal
			} else {
				// Sem dados - pode indicar problema
				radarData.Positions = []float64{}
				radarData.Velocities = []float64{}
				radarData.Azimuths = []float64{}
				radarData.Amplitudes = []float64{}
				radarData.MainObject = nil
			}
		} else {
			// Se desabilitado OU desconectado, retornar dados vazios
			radarData.Positions = []float64{}
			radarData.Velocities = []float64{}
			radarData.Azimuths = []float64{}
			radarData.Amplitudes = []float64{}
			radarData.MainObject = nil
		}

		radarDataList = append(radarDataList, radarData)
	}

	return models.MultiRadarData{
		Radars:    radarDataList,
		Timestamp: timestamp,
	}
}

// CollectAllData coleta dados de todos os radares conectados (método legado)
func (rm *RadarManager) CollectAllData() models.MultiRadarData {
	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}
	rm.mutex.RUnlock()

	var radarDataList []models.RadarData
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)

	for id, radar := range radars {
		config := configs[id]
		radarData := models.RadarData{
			RadarID:   id,
			RadarName: config.Name,
			Connected: radar.IsConnected(),
			Timestamp: timestamp,
		}

		if radar.IsConnected() {
			data, err := radar.ReadData()
			if err == nil && data != nil && len(data) > 0 {
				positions, velocities, azimuths, amplitudes, objPrincipal := radar.ProcessData(data)
				radarData.Positions = positions
				radarData.Velocities = velocities
				radarData.Azimuths = azimuths
				radarData.Amplitudes = amplitudes
				radarData.MainObject = objPrincipal
			}
		}

		radarDataList = append(radarDataList, radarData)
	}

	return models.MultiRadarData{
		Radars:    radarDataList,
		Timestamp: timestamp,
	}
}

// StartReconnectionMonitor inicia o monitoramento e reconexão automática
func (rm *RadarManager) StartReconnectionMonitor() {
	go func() {
		for {
			rm.checkAndReconnect()
			time.Sleep(5 * time.Second)
		}
	}()
}

// CheckAndReconnectEnabled - VERSÃO CONTROLADA SEM CONFLITOS
func (rm *RadarManager) CheckAndReconnectEnabled(enabledRadars map[string]bool) {
	rm.reconnectMutex.Lock()
	defer rm.reconnectMutex.Unlock()

	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}
	rm.mutex.RUnlock()

	now := time.Now()

	for id, radar := range radars {
		config := configs[id]
		isEnabled := enabledRadars[id]

		if isEnabled {
			// ✅ HABILITADO: Verificar se precisa reconectar COM THROTTLING
			if !radar.IsConnected() {
				// EVITAR SPAM DE RECONEXÕES - só tentar a cada 10 segundos
				lastAttempt := rm.lastReconnectAttempt[id]
				if now.Sub(lastAttempt) < 10*time.Second {
					continue // PULAR se tentou recentemente
				}

				fmt.Printf("🔄 Radar %s HABILITADO mas DESCONECTADO - tentando reconexão controlada...\n", config.Name)

				// ATUALIZAR timestamp ANTES de tentar
				rm.lastReconnectAttempt[id] = now

				// RECONEXÃO CONTROLADA com menos tentativas
				err := rm.connectRadarWithRetry(radar, 2) // REDUZIDO de 3 para 2
				if err != nil {
					fmt.Printf("❌ Falha na reconexão controlada do radar %s: %v\n", config.Name, err)
				} else {
					fmt.Printf("🎉 Radar %s reconectado com sucesso!\n", config.Name)
				}
			}
		} else {
			// ❌ DESABILITADO: Se conectado, desconectar SUAVEMENTE
			if radar.IsConnected() {
				fmt.Printf("⭕ Radar %s DESABILITADO - desconectando suavemente...\n", config.Name)
				radar.Disconnect()
				fmt.Printf("✅ Radar %s desconectado - economia de recursos\n", config.Name)
			}
		}
	}
}

// checkAndReconnect verifica e reconecta radares desconectados (método legado)
func (rm *RadarManager) checkAndReconnect() {
	rm.mutex.RLock()
	radars := make(map[string]*SICKRadar)
	configs := make(map[string]RadarConfig)
	for id, radar := range rm.radars {
		radars[id] = radar
		configs[id] = rm.configs[id]
	}
	rm.mutex.RUnlock()

	for id, radar := range radars {
		if !radar.IsConnected() {
			config := configs[id]
			fmt.Printf("🔄 Tentando reconectar radar %s (%s)...\n", config.Name, config.ID)

			err := rm.connectRadarWithRetry(radar, 2)
			if err != nil {
				fmt.Printf("❌ Falha na reconexão do radar %s: %v\n", config.Name, err)
			} else {
				fmt.Printf("✅ Radar %s reconectado com sucesso\n", config.Name)
			}
		}
	}
}
