package plc

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

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

// ResetCommands reseta todos os comandos no DB100
func (w *PLCWriter) ResetCommands() error {
	// Escrever 0 no byte 0 do DB100 para resetar todos os bits
	return w.WriteTag(100, 0, "byte", uint8(0))
}

// WriteSystemStatus escreve status do sistema no DB200
func (w *PLCWriter) WriteSystemStatus(status *models.PLCSystemStatus) error {
	// DB200.0 - Escrever cada bit individualmente
	if err := w.WriteTag(200, 0, "bool", status.LiveBit, 0); err != nil {
		return fmt.Errorf("erro ao escrever LiveBit: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.RadarConnected, 1); err != nil {
		return fmt.Errorf("erro ao escrever RadarConnected: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.PLCConnected, 2); err != nil {
		return fmt.Errorf("erro ao escrever PLCConnected: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.NATSConnected, 3); err != nil {
		return fmt.Errorf("erro ao escrever NATSConnected: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.WebSocketRunning, 4); err != nil {
		return fmt.Errorf("erro ao escrever WebSocketRunning: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.CollectionActive, 5); err != nil {
		return fmt.Errorf("erro ao escrever CollectionActive: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.SystemHealthy, 6); err != nil {
		return fmt.Errorf("erro ao escrever SystemHealthy: %v", err)
	}

	if err := w.WriteTag(200, 0, "bool", status.DebugModeActive, 7); err != nil {
		return fmt.Errorf("erro ao escrever DebugModeActive: %v", err)
	}

	// DB200.2 - WebSocket Clients (DINT)
	if err := w.WriteTag(200, 2, "dint", status.WebSocketClients); err != nil {
		return fmt.Errorf("erro ao escrever WebSocketClients: %v", err)
	}

	// DB200.6 - Radar Packets Total (DINT)
	if err := w.WriteTag(200, 6, "dint", status.RadarPacketsTotal); err != nil {
		return fmt.Errorf("erro ao escrever RadarPacketsTotal: %v", err)
	}

	// DB200.10 - Error Count (DINT)
	if err := w.WriteTag(200, 10, "dint", status.ErrorCount); err != nil {
		return fmt.Errorf("erro ao escrever ErrorCount: %v", err)
	}

	// DB200.14 - Uptime Seconds (DINT)
	if err := w.WriteTag(200, 14, "dint", status.UptimeSeconds); err != nil {
		return fmt.Errorf("erro ao escrever UptimeSeconds: %v", err)
	}

	// DB200.18 - CPU Usage (REAL)
	if err := w.WriteTag(200, 18, "real", status.CPUUsage); err != nil {
		return fmt.Errorf("erro ao escrever CPUUsage: %v", err)
	}

	// DB200.22 - Memory Usage (REAL)
	if err := w.WriteTag(200, 22, "real", status.MemoryUsage); err != nil {
		return fmt.Errorf("erro ao escrever MemoryUsage: %v", err)
	}

	// DB200.26 - Disk Usage (REAL)
	if err := w.WriteTag(200, 26, "real", status.DiskUsage); err != nil {
		return fmt.Errorf("erro ao escrever DiskUsage: %v", err)
	}

	// DB200.30 - Timestamp High (DINT)
	if err := w.WriteTag(200, 30, "dint", status.TimestampHigh); err != nil {
		return fmt.Errorf("erro ao escrever TimestampHigh: %v", err)
	}

	// DB200.34 - Timestamp Low (DINT)
	if err := w.WriteTag(200, 34, "dint", status.TimestampLow); err != nil {
		return fmt.Errorf("erro ao escrever TimestampLow: %v", err)
	}

	return nil
}

// WriteRadarData escreve dados do radar no DB300
func (w *PLCWriter) WriteRadarData(data *models.PLCRadarData) error {
	// DB300.0.0 - Main Object Detected (BOOL)
	if err := w.WriteTag(300, 0, "bool", data.MainObjectDetected, 0); err != nil {
		return fmt.Errorf("erro ao escrever MainObjectDetected: %v", err)
	}

	// DB300.2 - Amplitude (REAL)
	if err := w.WriteTag(300, 2, "real", data.MainObjectAmplitude); err != nil {
		return fmt.Errorf("erro ao escrever MainObjectAmplitude: %v", err)
	}

	// DB300.6 - Distance (REAL)
	if err := w.WriteTag(300, 6, "real", data.MainObjectDistance); err != nil {
		return fmt.Errorf("erro ao escrever MainObjectDistance: %v", err)
	}

	// DB300.10 - Velocity (REAL)
	if err := w.WriteTag(300, 10, "real", data.MainObjectVelocity); err != nil {
		return fmt.Errorf("erro ao escrever MainObjectVelocity: %v", err)
	}

	// DB300.14 - Angle (REAL)
	if err := w.WriteTag(300, 14, "real", data.MainObjectAngle); err != nil {
		return fmt.Errorf("erro ao escrever MainObjectAngle: %v", err)
	}

	// DB300.18 - Objects Detected (INT)
	if err := w.WriteTag(300, 18, "int", data.ObjectsDetected); err != nil {
		return fmt.Errorf("erro ao escrever ObjectsDetected: %v", err)
	}

	// DB300.20 - Max Amplitude (REAL)
	if err := w.WriteTag(300, 20, "real", data.MaxAmplitude); err != nil {
		return fmt.Errorf("erro ao escrever MaxAmplitude: %v", err)
	}

	// DB300.24 - Min Distance (REAL)
	if err := w.WriteTag(300, 24, "real", data.MinDistance); err != nil {
		return fmt.Errorf("erro ao escrever MinDistance: %v", err)
	}

	// DB300.28 - Max Distance (REAL)
	if err := w.WriteTag(300, 28, "real", data.MaxDistance); err != nil {
		return fmt.Errorf("erro ao escrever MaxDistance: %v", err)
	}

	// DB300.32-48 - Positions Array (5 REALs)
	for i := 0; i < 5; i++ {
		offset := 32 + (i * 4)
		if err := w.WriteTag(300, offset, "real", data.Positions[i]); err != nil {
			return fmt.Errorf("erro ao escrever Position[%d]: %v", i, err)
		}
	}

	// DB300.52-68 - Velocities Array (5 REALs)
	for i := 0; i < 5; i++ {
		offset := 52 + (i * 4)
		if err := w.WriteTag(300, offset, "real", data.Velocities[i]); err != nil {
			return fmt.Errorf("erro ao escrever Velocity[%d]: %v", i, err)
		}
	}

	// DB300.72 - Data Timestamp High (DINT)
	if err := w.WriteTag(300, 72, "dint", data.DataTimestampHigh); err != nil {
		return fmt.Errorf("erro ao escrever DataTimestampHigh: %v", err)
	}

	// DB300.76 - Data Timestamp Low (DINT)
	if err := w.WriteTag(300, 76, "dint", data.DataTimestampLow); err != nil {
		return fmt.Errorf("erro ao escrever DataTimestampLow: %v", err)
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

// BuildPLCSystemStatus converte dados do sistema para PLCSystemStatus
func (w *PLCWriter) BuildPLCSystemStatus(liveBit bool, radarConnected, plcConnected, natsConnected, wsRunning, collectionActive, systemHealthy, debugActive bool, wsClients int, packetCount, errorCount, uptime int32, cpuUsage, memUsage, diskUsage float32) *models.PLCSystemStatus {
	timestamp := time.Now().UnixNano() / int64(time.Millisecond)
	timestampHigh, timestampLow := models.ConvertTimestampToPLC(timestamp)

	return &models.PLCSystemStatus{
		LiveBit:           liveBit,
		RadarConnected:    radarConnected,
		PLCConnected:      plcConnected,
		NATSConnected:     natsConnected,
		WebSocketRunning:  wsRunning,
		CollectionActive:  collectionActive,
		SystemHealthy:     systemHealthy,
		DebugModeActive:   debugActive,
		WebSocketClients:  int32(wsClients),
		RadarPacketsTotal: packetCount,
		ErrorCount:        errorCount,
		UptimeSeconds:     uptime,
		CPUUsage:          cpuUsage,
		MemoryUsage:       memUsage,
		DiskUsage:         diskUsage,
		TimestampHigh:     timestampHigh,
		TimestampLow:      timestampLow,
	}
}
