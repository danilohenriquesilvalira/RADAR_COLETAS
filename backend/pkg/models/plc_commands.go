package models

import "time"

// PLCCommands - Comandos PLC → Servidor (DB100) - ESTRUTURA SIMPLES
type PLCCommands struct {
	// ========== COMANDOS ESSENCIAIS ==========
	StartCollection bool `json:"startCollection"` // DB100.0.0 - Iniciar coleta
	StopCollection  bool `json:"stopCollection"`  // DB100.0.1 - Parar coleta
	Emergency       bool `json:"emergency"`       // DB100.0.2 - Parada emergência
	ResetErrors     bool `json:"resetErrors"`     // DB100.0.3 - Reset erros
	
	// ========== CONTROLE INDIVIDUAL RADARES ==========
	EnableRadarCaldeira      bool `json:"enableRadarCaldeira"`      // DB100.1.0 - Enable Caldeira
	EnableRadarPortaJusante  bool `json:"enableRadarPortaJusante"`  // DB100.1.1 - Enable Porta Jusante
	EnableRadarPortaMontante bool `json:"enableRadarPortaMontante"` // DB100.1.2 - Enable Porta Montante
	
	// ========== RESTART INDIVIDUAL ==========
	RestartRadarCaldeira     bool `json:"restartRadarCaldeira"`     // DB100.2.0 - Restart Caldeira
	RestartRadarPortaJusante bool `json:"restartRadarPortaJusante"` // DB100.2.1 - Restart Porta Jusante
	RestartRadarPortaMontante bool `json:"restartRadarPortaMontante"` // DB100.2.2 - Restart Porta Montante
}

// PLCSystemStatus - Status Servidor → PLC - ESTRUTURA SUPER SIMPLES  
type PLCSystemStatus struct {
	// ========== STATUS ESSENCIAL ==========
	LiveBit          bool `json:"liveBit"`          // DB100.4.0 - Bit de vida
	CollectionActive bool `json:"collectionActive"` // DB100.4.1 - Coleta ativa
	SystemHealthy    bool `json:"systemHealthy"`    // DB100.4.2 - Sistema OK
	EmergencyActive  bool `json:"emergencyActive"`  // DB100.4.3 - Emergência ativa

	// ========== STATUS DOS RADARES ==========
	RadarCaldeiraConnected     bool `json:"radarCaldeiraConnected"`     // DB100.5.0 - Caldeira OK
	RadarPortaJusanteConnected bool `json:"radarPortaJusanteConnected"` // DB100.5.1 - Porta Jusante OK
	RadarPortaMontanteConnected bool `json:"radarPortaMontanteConnected"` // DB100.5.2 - Porta Montante OK

	// ========== RESERVA ==========
	Reserved2 int32 `json:"reserved2"` // DB100.8  - Reservado
	Reserved3 int32 `json:"reserved3"` // DB100.12 - Reservado
}

// PLCRadarData - Dados de um radar individual para o PLC
type PLCRadarData struct {
	// Objeto principal
	MainObjectDetected  bool    `json:"mainObjectDetected"`  // X.0.0
	MainObjectAmplitude float32 `json:"mainObjectAmplitude"` // X.2
	MainObjectDistance  float32 `json:"mainObjectDistance"`  // X.6
	MainObjectVelocity  float32 `json:"mainObjectVelocity"`  // X.10
	MainObjectAngle     float32 `json:"mainObjectAngle"`     // X.14

	// Estatísticas
	ObjectsDetected int16   `json:"objectsDetected"` // X.18
	MaxAmplitude    float32 `json:"maxAmplitude"`    // X.20
	MinDistance     float32 `json:"minDistance"`     // X.24
	MaxDistance     float32 `json:"maxDistance"`     // X.28

	// Arrays (primeiros 5 objetos)
	Positions  [5]float32 `json:"positions"`  // X.32-48
	Velocities [5]float32 `json:"velocities"` // X.52-68

	// Timestamp dividido
	DataTimestampHigh int32 `json:"dataTimestampHigh"` // X.72
	DataTimestampLow  int32 `json:"dataTimestampLow"`  // X.76
}

// PLCMultiRadarData - Dados de todos os radares para o PLC
type PLCMultiRadarData struct {
	// Radar Caldeira - DB300 (80 bytes)
	RadarCaldeira PLCRadarData `json:"radarCaldeira"`

	// Radar Porta Jusante - DB400 (80 bytes) 
	RadarPortaJusante PLCRadarData `json:"radarPortaJusante"`

	// Radar Porta Montante - DB500 (80 bytes)
	RadarPortaMontante PLCRadarData `json:"radarPortaMontante"`
}

// SystemCommand - Comandos internos (expandido para múltiplos radares)
type SystemCommand int

const (
	// Comandos globais
	CmdStartCollection SystemCommand = iota
	CmdStopCollection
	CmdRestartSystem
	CmdRestartNATS
	CmdRestartWebSocket
	CmdResetErrors
	CmdEnableDebug
	CmdDisableDebug
	CmdEmergencyStop
	
	// Comandos de controle individual de radares
	CmdEnableRadarCaldeira
	CmdDisableRadarCaldeira
	CmdEnableRadarPortaJusante
	CmdDisableRadarPortaJusante
	CmdEnableRadarPortaMontante
	CmdDisableRadarPortaMontante
	
	// Comandos específicos por radar
	CmdRestartRadarCaldeira
	CmdRestartRadarPortaJusante
	CmdRestartRadarPortaMontante
	CmdResetErrorsRadarCaldeira
	CmdResetErrorsRadarPortaJusante
	CmdResetErrorsRadarPortaMontante
)

// SystemError - Estrutura de erro
type SystemError struct {
	Code      int16     `json:"code"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Component string    `json:"component"`
}

// SystemStats - Estatísticas do sistema
type SystemStats struct {
	StartTime        time.Time `json:"startTime"`
	LastRadarPacket  time.Time `json:"lastRadarPacket"`
	TotalPackets     int64     `json:"totalPackets"`
	ErrorCount       int64     `json:"errorCount"`
	WebSocketClients int       `json:"webSocketClients"`
	CPUUsage         float64   `json:"cpuUsage"`
	MemoryUsage      float64   `json:"memoryUsage"`
	DiskUsage        float64   `json:"diskUsage"`
}

// ConvertTimestampToPLC converte timestamp int64 para high/low int32
func ConvertTimestampToPLC(timestamp int64) (high, low int32) {
	high = int32(timestamp >> 32)
	low = int32(timestamp & 0xFFFFFFFF)
	return
}

// ConvertTimestampFromPLC converte high/low int32 para timestamp int64
func ConvertTimestampFromPLC(high, low int32) int64 {
	return (int64(high) << 32) | int64(uint32(low))
}
