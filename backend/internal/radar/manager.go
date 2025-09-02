package radar

import (
	"context"
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

// RadarManager gerencia múltiplos radares COM PROTEÇÃO COMPLETA THREAD-SAFE
type RadarManager struct {
	radars  map[string]*SICKRadar
	configs map[string]RadarConfig
	mutex   sync.RWMutex

	// ✅ CONTROLE INDIVIDUAL POR RADAR - THREAD-SAFE
	lastReconnectAttempt map[string]time.Time
	radarMutexes         map[string]*sync.Mutex

	// ✅ CORREÇÃO MEMORY LEAK: Limpeza automática
	lastCleanup  time.Time
	cleanupMutex sync.Mutex
}

// NewRadarManager cria um novo gerenciador de radares THREAD-SAFE
func NewRadarManager() *RadarManager {
	rm := &RadarManager{
		radars:               make(map[string]*SICKRadar),
		configs:              make(map[string]RadarConfig),
		lastReconnectAttempt: make(map[string]time.Time),
		radarMutexes:         make(map[string]*sync.Mutex),
		lastCleanup:          time.Now(),
	}

	// ✅ Iniciar worker de limpeza automática
	go rm.cleanupWorker()

	return rm
}

// ✅ CORREÇÃO MEMORY LEAK: Worker de limpeza automática
func (rm *RadarManager) cleanupWorker() {
	ticker := time.NewTicker(30 * time.Minute) // Limpar a cada 30 minutos
	defer ticker.Stop()

	for range ticker.C {
		rm.cleanupOldEntries()
	}
}

// ✅ CORREÇÃO MEMORY LEAK: Limpeza de entradas antigas
func (rm *RadarManager) cleanupOldEntries() {
	rm.cleanupMutex.Lock()
	defer rm.cleanupMutex.Unlock()

	now := time.Now()
	if now.Sub(rm.lastCleanup) < 25*time.Minute {
		return // Evitar limpeza muito frequente
	}

	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	cutoff := now.Add(-2 * time.Hour) // Remover entradas > 2 horas

	// Limpar entradas antigas de lastReconnectAttempt
	for radarID, lastTime := range rm.lastReconnectAttempt {
		if lastTime.Before(cutoff) {
			// Manter apenas se ainda estiver nos configs
			if _, exists := rm.configs[radarID]; !exists {
				delete(rm.lastReconnectAttempt, radarID)
				delete(rm.radarMutexes, radarID)
			}
		}
	}

	rm.lastCleanup = now

	fmt.Printf("🧹 RadarManager: Limpeza automática executada - %d radares ativos\n", len(rm.configs))
}

// AddRadar adiciona um novo radar ao gerenciador - THREAD-SAFE
func (rm *RadarManager) AddRadar(config RadarConfig) error {
	rm.mutex.Lock()
	defer rm.mutex.Unlock()

	if _, exists := rm.radars[config.ID]; exists {
		return fmt.Errorf("radar com ID %s já existe", config.ID)
	}

	radar := NewSICKRadar(config.IP, config.Port)
	rm.radars[config.ID] = radar
	rm.configs[config.ID] = config
	rm.lastReconnectAttempt[config.ID] = time.Time{}
	rm.radarMutexes[config.ID] = &sync.Mutex{}

	fmt.Printf("✅ Radar %s (%s) adicionado - IP: %s:%d\n", config.Name, config.ID, config.IP, config.Port)
	return nil
}

// ✅ GET RADAR SEGURO - THREAD-SAFE
func (rm *RadarManager) getRadarSafe(id string) *SICKRadar {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.radars[id]
}

// ✅ GET CONFIG SEGURO - THREAD-SAFE
func (rm *RadarManager) getConfigSafe(id string) (RadarConfig, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	config, exists := rm.configs[id]
	return config, exists
}

// ✅ GET RADAR MUTEX SEGURO
func (rm *RadarManager) getRadarMutex(id string) *sync.Mutex {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()
	return rm.radarMutexes[id]
}

// GetRadar retorna um radar específico - THREAD-SAFE
func (rm *RadarManager) GetRadar(id string) (*SICKRadar, bool) {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	radar, exists := rm.radars[id]
	return radar, exists
}

// GetAllRadars retorna todos os radares - THREAD-SAFE
func (rm *RadarManager) GetAllRadars() map[string]*SICKRadar {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	result := make(map[string]*SICKRadar)
	for id, radar := range rm.radars {
		result[id] = radar
	}
	return result
}

// GetRadarConfig retorna a configuração de um radar - THREAD-SAFE
func (rm *RadarManager) GetRadarConfig(id string) (RadarConfig, bool) {
	return rm.getConfigSafe(id)
}

// ✅ VERIFICAR SE PODE TENTAR RECONEXÃO - THREAD-SAFE
func (rm *RadarManager) canAttemptReconnect(radarID string) bool {
	rm.mutex.RLock()
	lastAttempt := rm.lastReconnectAttempt[radarID]
	rm.mutex.RUnlock()

	return time.Since(lastAttempt) >= 10*time.Second
}

// ✅ ATUALIZAR ÚLTIMO ATTEMPT - THREAD-SAFE
func (rm *RadarManager) updateLastAttempt(radarID string) {
	rm.mutex.Lock()
	rm.lastReconnectAttempt[radarID] = time.Now()
	rm.mutex.Unlock()
}

// ConnectAll tenta conectar todos os radares - THREAD-SAFE
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

// ConnectRadarWithRetry tenta conectar um radar com retry - THREAD-SAFE
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

// DisconnectAll desconecta todos os radares - THREAD-SAFE
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

// GetConnectionStatus retorna o status de conexão de todos os radares - THREAD-SAFE
func (rm *RadarManager) GetConnectionStatus() map[string]bool {
	rm.mutex.RLock()
	defer rm.mutex.RUnlock()

	status := make(map[string]bool)
	for id, radar := range rm.radars {
		status[id] = radar.IsConnected()
	}
	return status
}

// ✅ NOVA FUNÇÃO: CollectEnabledRadarsDataAsyncWithContext - COM CONTEXT PARA EVITAR LEAKS
func (rm *RadarManager) CollectEnabledRadarsDataAsyncWithContext(ctx context.Context, enabledRadars map[string]bool) models.MultiRadarData {
	var radarDataList []models.RadarData
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)

	// ✅ CANAL PARA COLETA PARALELA COM CONTEXT
	type radarResult struct {
		data models.RadarData
		id   string
	}

	resultChan := make(chan radarResult, len(enabledRadars))
	var wg sync.WaitGroup

	// ✅ CONTEXT para cancelamento
	localCtx, cancel := context.WithTimeout(ctx, 450*time.Millisecond)
	defer cancel()

	// 🚀 COLETA PARALELA - CADA RADAR INDEPENDENTE COM CONTEXT
	for radarID, isEnabled := range enabledRadars {
		if !isEnabled {
			// Radar desabilitado - adicionar dados vazios
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

				// ✅ ENVIO COM CONTEXT CHECK
				select {
				case resultChan <- radarResult{data: radarData, id: radarID}:
				case <-localCtx.Done():
					return models.MultiRadarData{Radars: radarDataList, Timestamp: timestamp}
				}
			}
			continue
		}

		wg.Add(1)
		// 🚀 GOROUTINE INDIVIDUAL PARA CADA RADAR COM CONTEXT
		go func(id string) {
			defer wg.Done()

			// ✅ CHECK CONTEXT PRIMEIRO
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

			// ✅ USAR MUTEX INDIVIDUAL DO RADAR
			radarMutex := rm.getRadarMutex(id)
			if radarMutex == nil {
				return
			}

			// ✅ TIMEOUT INDIVIDUAL POR RADAR
			radarCtx, radarCancel := context.WithTimeout(localCtx, 200*time.Millisecond)
			defer radarCancel()

			done := make(chan bool, 1)
			var radarData models.RadarData

			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("🔥 PANIC no radar %s: %v\n", id, r)
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

				// ✅ COLETA INDIVIDUAL COM TIMEOUT
				if actuallyConnected {
					data, err := radar.ReadData()
					if err != nil {
						fmt.Printf("⚠️ Erro ao ler dados do radar %s: %v\n", config.Name, err)
						radarData.Connected = false
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
						radarData.Positions = []float64{}
						radarData.Velocities = []float64{}
						radarData.Azimuths = []float64{}
						radarData.Amplitudes = []float64{}
						radarData.MainObject = nil
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

			// ✅ AGUARDAR COM CONTEXT
			select {
			case <-done:
				// ✅ ENVIO COM CONTEXT CHECK
				select {
				case resultChan <- radarResult{data: radarData, id: id}:
				case <-radarCtx.Done():
				}
			case <-radarCtx.Done():
				fmt.Printf("⚠️ Timeout na coleta do radar %s\n", config.Name)
			}
		}(radarID)
	}

	// ✅ GOROUTINE PARA FECHAR CANAL COM CONTEXT
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// ✅ COLETAR RESULTADOS COM CONTEXT
	results := make(map[string]models.RadarData)

	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				// Canal fechado - todos os resultados coletados
				goto buildResponse
			}
			results[result.id] = result.data

		case <-localCtx.Done():
			// Context cancelado
			fmt.Printf("⚠️ Context cancelado na coleta - %d radares coletados\n", len(results))
			goto buildResponse
		}
	}

buildResponse:
	// ✅ CONSTRUIR RESPOSTA ORDENADA
	for radarID := range enabledRadars {
		if data, exists := results[radarID]; exists {
			radarDataList = append(radarDataList, data)
		} else {
			// Radar não respondeu - adicionar dados vazios
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

// ✅ NOVA FUNÇÃO: CheckAndReconnectEnabledAsyncWithContext - COM CONTEXT
func (rm *RadarManager) CheckAndReconnectEnabledAsyncWithContext(ctx context.Context, enabledRadars map[string]bool) {
	var wg sync.WaitGroup

	for radarID, isEnabled := range enabledRadars {
		if !isEnabled {
			// ❌ DESABILITADO: Desconectar se conectado
			wg.Add(1)
			go func(id string) {
				defer wg.Done()

				// ✅ CHECK CONTEXT
				select {
				case <-ctx.Done():
					return
				default:
				}

				radar := rm.getRadarSafe(id)
				config, exists := rm.getConfigSafe(id)

				if radar != nil && exists && radar.IsConnected() {
					// ✅ USAR MUTEX INDIVIDUAL
					radarMutex := rm.getRadarMutex(id)
					if radarMutex != nil {
						radarMutex.Lock()
						fmt.Printf("⭕ Radar %s DESABILITADO - desconectando suavemente...\n", config.Name)
						radar.Disconnect()
						fmt.Printf("✅ Radar %s desconectado - economia de recursos\n", config.Name)
						radarMutex.Unlock()
					}
				}
			}(radarID)
			continue
		}

		// ✅ HABILITADO: Verificar reconexão assíncrona
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// ✅ CHECK CONTEXT
			select {
			case <-ctx.Done():
				return
			default:
			}

			radar := rm.getRadarSafe(id)
			config, exists := rm.getConfigSafe(id)

			if radar == nil || !exists {
				return
			}

			// ✅ THROTTLING INDIVIDUAL
			if !rm.canAttemptReconnect(id) {
				return
			}

			// ✅ VERIFICAR SE PRECISA RECONECTAR
			if radar.IsConnected() {
				return // Já conectado
			}

			// ✅ USAR MUTEX INDIVIDUAL
			radarMutex := rm.getRadarMutex(id)
			if radarMutex == nil {
				return
			}

			// ✅ TIMEOUT INDIVIDUAL PARA RECONEXÃO
			reconCtx, reconCancel := context.WithTimeout(ctx, 15*time.Second)
			defer reconCancel()

			done := make(chan error, 1)

			go func() {
				radarMutex.Lock()
				defer radarMutex.Unlock()

				fmt.Printf("🔄 Radar %s HABILITADO mas DESCONECTADO - tentando reconexão assíncrona...\n", config.Name)

				// ✅ ATUALIZAR TIMESTAMP ANTES DE TENTAR
				rm.updateLastAttempt(id)

				// ✅ RECONEXÃO RÁPIDA
				err := rm.connectRadarWithRetry(radar, 1)
				done <- err
			}()

			// ✅ AGUARDAR COM CONTEXT
			select {
			case err := <-done:
				if err != nil {
					fmt.Printf("❌ Falha na reconexão assíncrona do radar %s: %v\n", config.Name, err)
				} else {
					fmt.Printf("🎉 Radar %s reconectado com sucesso via async!\n", config.Name)
				}
			case <-reconCtx.Done():
				fmt.Printf("⚠️ Timeout na reconexão do radar %s\n", config.Name)
			}
		}(radarID)
	}

	// ✅ AGUARDAR TODAS AS GOROUTINES COM CONTEXT
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Todas completaram
	case <-ctx.Done():
		fmt.Println("⚠️ Context cancelado durante reconexão de radares")
	}
}

// ✅ FUNÇÕES DE COMPATIBILIDADE - MANTÉM API ORIGINAL
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

// CollectEnabledRadarsData - MANTÉM COMPATIBILIDADE
func (rm *RadarManager) CollectEnabledRadarsData(enabledRadars map[string]bool) models.MultiRadarData {
	return rm.CollectEnabledRadarsDataAsync(enabledRadars)
}

// CheckAndReconnectEnabled - MANTÉM COMPATIBILIDADE
func (rm *RadarManager) CheckAndReconnectEnabled(enabledRadars map[string]bool) {
	rm.CheckAndReconnectEnabledAsync(enabledRadars)
}

// CollectAllData coleta dados de todos os radares conectados - THREAD-SAFE
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

// StartReconnectionMonitor inicia o monitoramento e reconexão automática - DEPRECATED
func (rm *RadarManager) StartReconnectionMonitor() {
	fmt.Println("⚠️ StartReconnectionMonitor está deprecated - use CheckAndReconnectEnabledAsyncWithContext")
}

// checkAndReconnect - DEPRECATED
func (rm *RadarManager) checkAndReconnect() {
	fmt.Println("⚠️ checkAndReconnect está deprecated")
}
