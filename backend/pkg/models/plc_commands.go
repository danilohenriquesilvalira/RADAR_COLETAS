package models

import "time"

// PLCCommands - Comandos que o PLC envia (DB100)
type PLCCommands struct {
	StartCollection  bool `json:"startCollection"`  // DB100.0.0
	StopCollection   bool `json:"stopCollection"`   // DB100.0.1
	RestartSystem    bool `json:"restartSystem"`    // DB100.0.2
	RestartNATS      bool `json:"restartNATS"`      // DB100.0.3
	RestartWebSocket bool `json:"restartWebSocket"` // DB100.0.4
	ResetErrors      bool `json:"resetErrors"`      // DB100.0.5
	EnableDebugMode  bool `json:"enableDebugMode"`  // DB100.0.6
	Emergency        bool `json:"emergency"`        // DB100.0.7
}

// PLCSystemStatus - Status enviado para o PLC (DB200)
type PLCSystemStatus struct {
	// Byte 0 - Status bits
	LiveBit          bool `json:"liveBit"`          // DB200.0.0
	RadarConnected   bool `json:"radarConnected"`   // DB200.0.1
	PLCConnected     bool `json:"plcConnected"`     // DB200.0.2
	NATSConnected    bool `json:"natsConnected"`    // DB200.0.3
	WebSocketRunning bool `json:"webSocketRunning"` // DB200.0.4
	CollectionActive bool `json:"collectionActive"` // DB200.0.5
	SystemHealthy    bool `json:"systemHealthy"`    // DB200.0.6
	DebugModeActive  bool `json:"debugModeActive"`  // DB200.0.7

	// Contadores DINT (4 bytes cada)
	WebSocketClients  int32 `json:"webSocketClients"`  // DB200.2
	RadarPacketsTotal int32 `json:"radarPacketsTotal"` // DB200.6
	ErrorCount        int32 `json:"errorCount"`        // DB200.10
	UptimeSeconds     int32 `json:"uptimeSeconds"`     // DB200.14

	// Dados do servidor REAL (4 bytes cada)
	CPUUsage    float32 `json:"cpuUsage"`    // DB200.18
	MemoryUsage float32 `json:"memoryUsage"` // DB200.22
	DiskUsage   float32 `json:"diskUsage"`   // DB200.26

	// Timestamp dividido em duas palavras DINT
	TimestampHigh int32 `json:"timestampHigh"` // DB200.30
	TimestampLow  int32 `json:"timestampLow"`  // DB200.34
}

// PLCRadarData - Dados do radar para o PLC (DB300)
type PLCRadarData struct {
	// Objeto principal
	MainObjectDetected  bool    `json:"mainObjectDetected"`  // DB300.0.0
	MainObjectAmplitude float32 `json:"mainObjectAmplitude"` // DB300.2
	MainObjectDistance  float32 `json:"mainObjectDistance"`  // DB300.6
	MainObjectVelocity  float32 `json:"mainObjectVelocity"`  // DB300.10
	MainObjectAngle     float32 `json:"mainObjectAngle"`     // DB300.14

	// Estatísticas
	ObjectsDetected int16   `json:"objectsDetected"` // DB300.18
	MaxAmplitude    float32 `json:"maxAmplitude"`    // DB300.20
	MinDistance     float32 `json:"minDistance"`     // DB300.24
	MaxDistance     float32 `json:"maxDistance"`     // DB300.28

	// Arrays (primeiros 5 objetos)
	Positions  [5]float32 `json:"positions"`  // DB300.32-48
	Velocities [5]float32 `json:"velocities"` // DB300.52-68

	// Timestamp dividido
	DataTimestampHigh int32 `json:"dataTimestampHigh"` // DB300.72
	DataTimestampLow  int32 `json:"dataTimestampLow"`  // DB300.76
}

// SystemCommand - Comandos internos
type SystemCommand int

const (
	CmdStartCollection SystemCommand = iota
	CmdStopCollection
	CmdRestartSystem
	CmdRestartNATS
	CmdRestartWebSocket
	CmdResetErrors
	CmdEnableDebug
	CmdDisableDebug
	CmdEmergencyStop
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
