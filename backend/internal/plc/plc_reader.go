package plc

import (
	"encoding/binary"
	"fmt"
	"math"

	"backend/pkg/models"
)

// PLCReader lê dados do PLC usando a implementação correta
type PLCReader struct {
	client PLCClient
}

// PLCClient interface para operações do PLC
type PLCClient interface {
	AGWriteDB(dbNumber, byteOffset, size int, buffer []byte) error
	AGReadDB(dbNumber, byteOffset, size int, buffer []byte) error
}

// NewPLCReader cria um novo leitor PLC
func NewPLCReader(client PLCClient) *PLCReader {
	return &PLCReader{
		client: client,
	}
}

// ReadTag lê um valor do PLC usando o método correto
func (r *PLCReader) ReadTag(dbNumber int, byteOffset int, dataType string, bitOffset ...int) (interface{}, error) {
	var size int

	// Determinar o tamanho baseado no tipo de dado
	switch dataType {
	case "real":
		size = 4
	case "dint", "int32", "dword", "uint32":
		size = 4
	case "int", "int16", "word", "uint16":
		size = 2
	case "sint", "int8", "usint", "byte", "uint8", "bool":
		size = 1
	default:
		return nil, fmt.Errorf("tipo de dado não suportado: %s", dataType)
	}

	// Ler os bytes do PLC
	buf := make([]byte, size)
	if err := r.client.AGReadDB(dbNumber, byteOffset, size, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler dados do PLC (DB%d.%d): %w", dbNumber, byteOffset, err)
	}

	// Interpretar os bytes conforme o tipo de dado
	switch dataType {
	case "real":
		return math.Float32frombits(binary.BigEndian.Uint32(buf)), nil

	case "dint", "int32":
		return int32(binary.BigEndian.Uint32(buf)), nil

	case "dword", "uint32":
		return binary.BigEndian.Uint32(buf), nil

	case "int", "int16":
		return int16(binary.BigEndian.Uint16(buf)), nil

	case "word", "uint16":
		return binary.BigEndian.Uint16(buf), nil

	case "sint", "int8":
		return int8(buf[0]), nil

	case "usint", "byte", "uint8":
		return buf[0], nil

	case "bool":
		// Usa o bitOffset explicitamente para selecionar o bit correto
		bit := 0
		if len(bitOffset) > 0 && bitOffset[0] >= 0 && bitOffset[0] <= 7 {
			bit = bitOffset[0]
		}
		return ((buf[0] >> uint(bit)) & 0x01) == 1, nil
	}

	return nil, fmt.Errorf("tipo de dado não implementado: %s", dataType)
}

// ReadCommands lê comandos do DB100
func (r *PLCReader) ReadCommands() (*models.PLCCommands, error) {
	commands := &models.PLCCommands{}

	// Ler cada bit do byte 0 do DB100
	startCollection, err := r.ReadTag(100, 0, "bool", 0)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler StartCollection: %v", err)
	}
	commands.StartCollection = startCollection.(bool)

	stopCollection, err := r.ReadTag(100, 0, "bool", 1)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler StopCollection: %v", err)
	}
	commands.StopCollection = stopCollection.(bool)

	restartSystem, err := r.ReadTag(100, 0, "bool", 2)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartSystem: %v", err)
	}
	commands.RestartSystem = restartSystem.(bool)

	restartNATS, err := r.ReadTag(100, 0, "bool", 3)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartNATS: %v", err)
	}
	commands.RestartNATS = restartNATS.(bool)

	restartWebSocket, err := r.ReadTag(100, 0, "bool", 4)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartWebSocket: %v", err)
	}
	commands.RestartWebSocket = restartWebSocket.(bool)

	resetErrors, err := r.ReadTag(100, 0, "bool", 5)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler ResetErrors: %v", err)
	}
	commands.ResetErrors = resetErrors.(bool)

	enableDebugMode, err := r.ReadTag(100, 0, "bool", 6)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler EnableDebugMode: %v", err)
	}
	commands.EnableDebugMode = enableDebugMode.(bool)

	emergency, err := r.ReadTag(100, 0, "bool", 7)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler Emergency: %v", err)
	}
	commands.Emergency = emergency.(bool)

	return commands, nil
}

// ReadSystemStatus lê status do sistema (usado para verificação se necessário)
func (r *PLCReader) ReadSystemStatus() (*models.PLCSystemStatus, error) {
	status := &models.PLCSystemStatus{}

	// Ler byte de status (DB200.0)
	liveBit, err := r.ReadTag(200, 0, "bool", 0)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler LiveBit: %v", err)
	}
	status.LiveBit = liveBit.(bool)

	radarConnected, err := r.ReadTag(200, 0, "bool", 1)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RadarConnected: %v", err)
	}
	status.RadarConnected = radarConnected.(bool)

	plcConnected, err := r.ReadTag(200, 0, "bool", 2)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler PLCConnected: %v", err)
	}
	status.PLCConnected = plcConnected.(bool)

	natsConnected, err := r.ReadTag(200, 0, "bool", 3)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler NATSConnected: %v", err)
	}
	status.NATSConnected = natsConnected.(bool)

	webSocketRunning, err := r.ReadTag(200, 0, "bool", 4)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler WebSocketRunning: %v", err)
	}
	status.WebSocketRunning = webSocketRunning.(bool)

	collectionActive, err := r.ReadTag(200, 0, "bool", 5)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler CollectionActive: %v", err)
	}
	status.CollectionActive = collectionActive.(bool)

	systemHealthy, err := r.ReadTag(200, 0, "bool", 6)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler SystemHealthy: %v", err)
	}
	status.SystemHealthy = systemHealthy.(bool)

	debugModeActive, err := r.ReadTag(200, 0, "bool", 7)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler DebugModeActive: %v", err)
	}
	status.DebugModeActive = debugModeActive.(bool)

	// Ler contadores
	webSocketClients, err := r.ReadTag(200, 2, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler WebSocketClients: %v", err)
	}
	status.WebSocketClients = webSocketClients.(int32)

	radarPacketsTotal, err := r.ReadTag(200, 6, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RadarPacketsTotal: %v", err)
	}
	status.RadarPacketsTotal = radarPacketsTotal.(int32)

	errorCount, err := r.ReadTag(200, 10, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler ErrorCount: %v", err)
	}
	status.ErrorCount = errorCount.(int32)

	uptimeSeconds, err := r.ReadTag(200, 14, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler UptimeSeconds: %v", err)
	}
	status.UptimeSeconds = uptimeSeconds.(int32)

	// Ler dados do servidor
	cpuUsage, err := r.ReadTag(200, 18, "real")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler CPUUsage: %v", err)
	}
	status.CPUUsage = cpuUsage.(float32)

	memoryUsage, err := r.ReadTag(200, 22, "real")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler MemoryUsage: %v", err)
	}
	status.MemoryUsage = memoryUsage.(float32)

	diskUsage, err := r.ReadTag(200, 26, "real")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler DiskUsage: %v", err)
	}
	status.DiskUsage = diskUsage.(float32)

	// Ler timestamp
	timestampHigh, err := r.ReadTag(200, 30, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler TimestampHigh: %v", err)
	}
	status.TimestampHigh = timestampHigh.(int32)

	timestampLow, err := r.ReadTag(200, 34, "dint")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler TimestampLow: %v", err)
	}
	status.TimestampLow = timestampLow.(int32)

	return status, nil
}
