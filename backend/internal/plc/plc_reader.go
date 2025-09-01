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

// ReadCommands lê comandos da DB100 - OFFSETS CORRETOS
func (r *PLCReader) ReadCommands() (*models.PLCCommands, error) {
	commands := &models.PLCCommands{}

	// BYTE 0 - Comandos básicos
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

	emergency, err := r.ReadTag(100, 0, "bool", 2)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler Emergency: %v", err)
	}
	commands.Emergency = emergency.(bool)

	resetErrors, err := r.ReadTag(100, 0, "bool", 3)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler ResetErrors: %v", err)
	}
	commands.ResetErrors = resetErrors.(bool)

	enableRadarCaldeira, err := r.ReadTag(100, 0, "bool", 4)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler EnableRadarCaldeira: %v", err)
	}
	commands.EnableRadarCaldeira = enableRadarCaldeira.(bool)

	enableRadarPortaJusante, err := r.ReadTag(100, 0, "bool", 5)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler EnableRadarPortaJusante: %v", err)
	}
	commands.EnableRadarPortaJusante = enableRadarPortaJusante.(bool)

	enableRadarPortaMontante, err := r.ReadTag(100, 0, "bool", 6)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler EnableRadarPortaMontante: %v", err)
	}
	commands.EnableRadarPortaMontante = enableRadarPortaMontante.(bool)

	restartRadarCaldeira, err := r.ReadTag(100, 0, "bool", 7)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartRadarCaldeira: %v", err)
	}
	commands.RestartRadarCaldeira = restartRadarCaldeira.(bool)

	// BYTE 1 - Restarts restantes
	restartRadarPortaJusante, err := r.ReadTag(100, 1, "bool", 0)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartRadarPortaJusante: %v", err)
	}
	commands.RestartRadarPortaJusante = restartRadarPortaJusante.(bool)

	restartRadarPortaMontante, err := r.ReadTag(100, 1, "bool", 1)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RestartRadarPortaMontante: %v", err)
	}
	commands.RestartRadarPortaMontante = restartRadarPortaMontante.(bool)

	return commands, nil
}

// ReadSystemStatus lê status simplificado da DB100
func (r *PLCReader) ReadSystemStatus() (*models.PLCSystemStatus, error) {
	status := &models.PLCSystemStatus{}

	// Ler status da DB100.4 (apenas campos essenciais)
	liveBit, err := r.ReadTag(100, 4, "bool", 0)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler LiveBit: %v", err)
	}
	status.LiveBit = liveBit.(bool)

	collectionActive, err := r.ReadTag(100, 4, "bool", 1)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler CollectionActive: %v", err)
	}
	status.CollectionActive = collectionActive.(bool)

	systemHealthy, err := r.ReadTag(100, 4, "bool", 2)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler SystemHealthy: %v", err)
	}
	status.SystemHealthy = systemHealthy.(bool)

	emergencyActive, err := r.ReadTag(100, 4, "bool", 3)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler EmergencyActive: %v", err)
	}
	status.EmergencyActive = emergencyActive.(bool)

	radarCaldeiraConnected, err := r.ReadTag(100, 4, "bool", 4)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RadarCaldeiraConnected: %v", err)
	}
	status.RadarCaldeiraConnected = radarCaldeiraConnected.(bool)

	radarPortaJusanteConnected, err := r.ReadTag(100, 4, "bool", 5)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RadarPortaJusanteConnected: %v", err)
	}
	status.RadarPortaJusanteConnected = radarPortaJusanteConnected.(bool)

	radarPortaMontanteConnected, err := r.ReadTag(100, 4, "bool", 6)
	if err != nil {
		return nil, fmt.Errorf("erro ao ler RadarPortaMontanteConnected: %v", err)
	}
	status.RadarPortaMontanteConnected = radarPortaMontanteConnected.(bool)

	return status, nil
}
