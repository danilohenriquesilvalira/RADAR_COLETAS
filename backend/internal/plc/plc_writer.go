package plc

import (
	"encoding/binary"
	"fmt"
	"math"

	"backend/pkg/models"
)

// PLCWriter escreve dados no PLC usando a implementação correta
type PLCWriter struct {
	client PLCClient
}

// NewPLCWriter cria um novo escritor PLC
func NewPLCWriter(client PLCClient) *PLCWriter {
	return &PLCWriter{
		client: client,
	}
}

// WriteTag escreve um valor no PLC usando o método correto
func (w *PLCWriter) WriteTag(dbNumber int, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	var buf []byte

	switch dataType {
	case "real":
		buf = make([]byte, 4)
		var val float32

		switch v := value.(type) {
		case float32:
			val = v
		case float64:
			val = float32(v)
		case int:
			val = float32(v)
		case int64:
			val = float32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com float32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, math.Float32bits(val))

	case "dint", "int32":
		buf = make([]byte, 4)
		var val int32

		switch v := value.(type) {
		case int32:
			val = v
		case int:
			val = int32(v)
		case int64:
			val = int32(v)
		case float32:
			val = int32(v)
		case float64:
			val = int32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, uint32(val))

	case "dword", "uint32":
		buf = make([]byte, 4)
		var val uint32

		switch v := value.(type) {
		case uint32:
			val = v
		case uint:
			val = uint32(v)
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint32")
			}
			val = uint32(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint32")
			}
			val = uint32(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint32, recebido: %T", value)
		}

		binary.BigEndian.PutUint32(buf, val)

	case "int", "int16":
		buf = make([]byte, 2)
		var val int16

		switch v := value.(type) {
		case int16:
			val = v
		case int:
			val = int16(v)
		case float32:
			val = int16(v)
		case float64:
			val = int16(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int16, recebido: %T", value)
		}

		binary.BigEndian.PutUint16(buf, uint16(val))

	case "word", "uint16":
		buf = make([]byte, 2)
		var val uint16

		switch v := value.(type) {
		case uint16:
			val = v
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint16")
			}
			val = uint16(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint16")
			}
			val = uint16(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint16, recebido: %T", value)
		}

		binary.BigEndian.PutUint16(buf, val)

	case "sint", "int8":
		buf = make([]byte, 1)
		var val int8

		switch v := value.(type) {
		case int8:
			val = v
		case int:
			val = int8(v)
		case float64:
			val = int8(v)
		default:
			return fmt.Errorf("valor deve ser compatível com int8, recebido: %T", value)
		}

		buf[0] = byte(val)

	case "usint", "byte", "uint8":
		buf = make([]byte, 1)
		var val uint8

		switch v := value.(type) {
		case uint8:
			val = v
		case int:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint8")
			}
			val = uint8(v)
		case float64:
			if v < 0 {
				return fmt.Errorf("valor negativo não pode ser convertido para uint8")
			}
			val = uint8(v)
		default:
			return fmt.Errorf("valor deve ser compatível com uint8, recebido: %T", value)
		}

		buf[0] = val

	case "bool":
		buf = make([]byte, 1)

		// Primeiro ler o byte atual para preservar os outros bits
		if err := w.client.AGReadDB(dbNumber, byteOffset, 1, buf); err != nil {
			return fmt.Errorf("erro ao ler byte atual para escrita de bit: %w", err)
		}

		var val bool

		switch v := value.(type) {
		case bool:
			val = v
		case int:
			val = v != 0
		case float64:
			val = v != 0
		case string:
			val = v == "true" || v == "1" || v == "yes" || v == "sim"
		default:
			return fmt.Errorf("valor deve ser convertível para bool, recebido: %T", value)
		}

		// Determinar bit offset
		bit := 0
		if len(bitOffset) > 0 && bitOffset[0] >= 0 && bitOffset[0] <= 7 {
			bit = bitOffset[0]
		}

		if val {
			buf[0] |= (1 << uint(bit)) // set bit
		} else {
			buf[0] &= ^(1 << uint(bit)) // clear bit
		}

	default:
		return fmt.Errorf("tipo de dado não suportado: %s", dataType)
	}

	// Escrever os bytes no PLC
	return w.client.AGWriteDB(dbNumber, byteOffset, len(buf), buf)
}

// ResetCommands reseta todos os comandos na DB100
func (w *PLCWriter) ResetCommands() error {
	// Resetar byte 0 e byte 1 dos comandos
	if err := w.WriteTag(100, 0, "byte", uint8(0)); err != nil {
		return fmt.Errorf("erro ao resetar byte 0: %v", err)
	}
	return w.WriteTag(100, 1, "byte", uint8(0))
}

// WriteSystemStatus escreve status do sistema na DB100 simplificada
func (w *PLCWriter) WriteSystemStatus(status *models.PLCSystemStatus) error {
	// DB100.4.0 - LiveBit
	if err := w.WriteTag(100, 4, "bool", status.LiveBit, 0); err != nil {
		return fmt.Errorf("erro ao escrever LiveBit: %v", err)
	}

	// DB100.4.1 - CollectionActive
	if err := w.WriteTag(100, 4, "bool", status.CollectionActive, 1); err != nil {
		return fmt.Errorf("erro ao escrever CollectionActive: %v", err)
	}

	// DB100.4.2 - SystemHealthy
	if err := w.WriteTag(100, 4, "bool", status.SystemHealthy, 2); err != nil {
		return fmt.Errorf("erro ao escrever SystemHealthy: %v", err)
	}

	// DB100.4.3 - EmergencyActive
	if err := w.WriteTag(100, 4, "bool", status.EmergencyActive, 3); err != nil {
		return fmt.Errorf("erro ao escrever EmergencyActive: %v", err)
	}

	// DB100.4.4 - RadarCaldeiraConnected
	if err := w.WriteTag(100, 4, "bool", status.RadarCaldeiraConnected, 4); err != nil {
		return fmt.Errorf("erro ao escrever RadarCaldeiraConnected: %v", err)
	}

	// DB100.4.5 - RadarPortaJusanteConnected
	if err := w.WriteTag(100, 4, "bool", status.RadarPortaJusanteConnected, 5); err != nil {
		return fmt.Errorf("erro ao escrever RadarPortaJusanteConnected: %v", err)
	}

	// DB100.4.6 - RadarPortaMontanteConnected
	if err := w.WriteTag(100, 4, "bool", status.RadarPortaMontanteConnected, 6); err != nil {
		return fmt.Errorf("erro ao escrever RadarPortaMontanteConnected: %v", err)
	}

	return nil
}

// WriteRadarDataToDB100 escreve dados do radar na DB100 - OFFSETS CORRETOS
func (w *PLCWriter) WriteRadarDataToDB100(data *models.PLCRadarData, radarBaseOffset int) error {
	// ObjectDetected (BOOL) - offset +0
	if err := w.WriteTag(100, radarBaseOffset+0, "bool", data.MainObjectDetected, 0); err != nil {
		return fmt.Errorf("erro ao escrever ObjectDetected: %v", err)
	}

	// Amplitude (REAL) - offset +2 
	if err := w.WriteTag(100, radarBaseOffset+2, "real", data.MainObjectAmplitude); err != nil {
		return fmt.Errorf("erro ao escrever Amplitude: %v", err)
	}

	// Distance (REAL) - offset +6
	if err := w.WriteTag(100, radarBaseOffset+6, "real", data.MainObjectDistance); err != nil {
		return fmt.Errorf("erro ao escrever Distance: %v", err)
	}

	// Velocity (REAL) - offset +10
	if err := w.WriteTag(100, radarBaseOffset+10, "real", data.MainObjectVelocity); err != nil {
		return fmt.Errorf("erro ao escrever Velocity: %v", err)
	}

	// ObjectsCount (INT) - offset +14
	if err := w.WriteTag(100, radarBaseOffset+14, "int", data.ObjectsDetected); err != nil {
		return fmt.Errorf("erro ao escrever ObjectsCount: %v", err)
	}

	// Positions Array (10 REALs) - offset +16 to +55 (40 bytes)
	for i := 0; i < 10; i++ {
		offset := radarBaseOffset + 16 + (i * 4)
		val := float32(0)
		if i < len(data.Positions) {
			val = data.Positions[i]
		}
		if err := w.WriteTag(100, offset, "real", val); err != nil {
			return fmt.Errorf("erro ao escrever Position[%d]: %v", i, err)
		}
	}

	// Velocities Array (10 REALs) - offset +56 to +95 (40 bytes) 
	for i := 0; i < 10; i++ {
		offset := radarBaseOffset + 56 + (i * 4)
		val := float32(0)
		if i < len(data.Velocities) {
			val = data.Velocities[i]
		}
		if err := w.WriteTag(100, offset, "real", val); err != nil {
			return fmt.Errorf("erro ao escrever Velocity[%d]: %v", i, err)
		}
	}

	return nil
}

// BuildPLCRadarData converte RadarData para PLCRadarData
func (w *PLCWriter) BuildPLCRadarData(data models.RadarData) *models.PLCRadarData {
	plcData := &models.PLCRadarData{
		MainObjectDetected: data.MainObject != nil,
		ObjectsDetected:    int16(len(data.Amplitudes)),
	}

	// Converter timestamp
	plcData.DataTimestampHigh, plcData.DataTimestampLow = models.ConvertTimestampToPLC(data.Timestamp)

	// Dados do objeto principal
	if data.MainObject != nil {
		plcData.MainObjectAmplitude = float32(data.MainObject.Amplitude)

		if data.MainObject.Distancia != nil {
			plcData.MainObjectDistance = float32(*data.MainObject.Distancia)
		}
		if data.MainObject.Velocidade != nil {
			plcData.MainObjectVelocity = float32(*data.MainObject.Velocidade)
		}
		if data.MainObject.Angulo != nil {
			plcData.MainObjectAngle = float32(*data.MainObject.Angulo)
		}
	}

	// Estatísticas
	if len(data.Amplitudes) > 0 {
		maxAmp := data.Amplitudes[0]
		for _, amp := range data.Amplitudes {
			if amp > maxAmp {
				maxAmp = amp
			}
		}
		plcData.MaxAmplitude = float32(maxAmp)
	}

	if len(data.Positions) > 0 {
		minDist := data.Positions[0]
		maxDist := data.Positions[0]
		for _, pos := range data.Positions {
			if pos < minDist {
				minDist = pos
			}
			if pos > maxDist {
				maxDist = pos
			}
		}
		plcData.MinDistance = float32(minDist)
		plcData.MaxDistance = float32(maxDist)
	}

	// Arrays (primeiros 5 elementos)
	for i := 0; i < 5; i++ {
		if i < len(data.Positions) {
			plcData.Positions[i] = float32(data.Positions[i])
		}
		if i < len(data.Velocities) {
			plcData.Velocities[i] = float32(data.Velocities[i])
		}
	}

	return plcData
}

// BuildPLCSystemStatus converte dados do sistema para PLCSystemStatus simplificado
func (w *PLCWriter) BuildPLCSystemStatus(liveBit, collectionActive, systemHealthy, emergencyActive bool, radarCaldeiraConnected, radarPortaJusanteConnected, radarPortaMontanteConnected bool) *models.PLCSystemStatus {
	return &models.PLCSystemStatus{
		LiveBit:                     liveBit,
		CollectionActive:            collectionActive,
		SystemHealthy:               systemHealthy,
		EmergencyActive:             emergencyActive,
		RadarCaldeiraConnected:      radarCaldeiraConnected,
		RadarPortaJusanteConnected:  radarPortaJusanteConnected,
		RadarPortaMontanteConnected: radarPortaMontanteConnected,
	}
}


// WriteMultiRadarDataToDB100 escreve dados dos 3 radares na DB100 - OFFSETS CORRETOS
func (w *PLCWriter) WriteMultiRadarDataToDB100(multiRadarData *models.PLCMultiRadarData) error {
	// Caldeira - DB100.6 a DB100.101 (96 bytes)
	if err := w.WriteRadarDataToDB100(&multiRadarData.RadarCaldeira, 6); err != nil {
		return fmt.Errorf("erro ao escrever dados Caldeira: %v", err)
	}

	// Porta Jusante - DB100.102 a DB100.197 (96 bytes)
	if err := w.WriteRadarDataToDB100(&multiRadarData.RadarPortaJusante, 102); err != nil {
		return fmt.Errorf("erro ao escrever dados Porta Jusante: %v", err)
	}

	// Porta Montante - DB100.198 a DB100.293 (96 bytes)
	if err := w.WriteRadarDataToDB100(&multiRadarData.RadarPortaMontante, 198); err != nil {
		return fmt.Errorf("erro ao escrever dados Porta Montante: %v", err)
	}

	return nil
}
