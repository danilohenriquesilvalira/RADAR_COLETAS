package plc

import (
	"encoding/binary"
	"fmt"

	"math"
	"strings"

	"backend/pkg/models"
)

type PLCWriter struct {
	client            PLCClient
	consecutiveErrors int
	errorThreshold    int
	isInErrorState    bool
}

func NewPLCWriter(client PLCClient) *PLCWriter {
	return &PLCWriter{
		client:         client,
		errorThreshold: 15,
		isInErrorState: false,
	}
}

// WriteTag - Versao limpa e essencial
func (w *PLCWriter) WriteTag(dbNumber int, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	if w.client == nil {
		return fmt.Errorf("PLC client is nil")
	}

	var buf []byte

	switch dataType {
	case "real":
		buf = make([]byte, 4)
		val := w.convertToFloat32(value)
		binary.BigEndian.PutUint32(buf, math.Float32bits(val))

	case "int", "int16":
		buf = make([]byte, 2)
		val := w.convertToInt16(value)
		binary.BigEndian.PutUint16(buf, uint16(val))

	case "byte", "uint8":
		buf = make([]byte, 1)
		val := w.convertToByte(value)
		buf[0] = val

	case "bool":
		buf = make([]byte, 1)
		val := w.convertToBool(value)
		bit := 0
		if len(bitOffset) > 0 && bitOffset[0] >= 0 && bitOffset[0] <= 7 {
			bit = bitOffset[0]
		}
		if val {
			buf[0] = 1 << uint(bit)
		} else {
			buf[0] = 0
		}

	default:
		return fmt.Errorf("tipo nao suportado: %s", dataType)
	}

	err := w.client.AGWriteDB(dbNumber, byteOffset, len(buf), buf)
	if err != nil {
		w.markError()
		if w.isConnectionError(err) {
			return fmt.Errorf("connection error: %v", err)
		}
		return err
	}

	w.markSuccess()
	return nil
}

// Conversores simples
func (w *PLCWriter) convertToFloat32(value interface{}) float32 {
	switch v := value.(type) {
	case float32:
		return v
	case float64:
		return float32(v)
	case int:
		return float32(v)
	default:
		return 0.0
	}
}

func (w *PLCWriter) convertToInt16(value interface{}) int16 {
	switch v := value.(type) {
	case int16:
		return v
	case int:
		return int16(v)
	case float32:
		return int16(v)
	case float64:
		return int16(v)
	default:
		return 0
	}
}

func (w *PLCWriter) convertToByte(value interface{}) byte {
	switch v := value.(type) {
	case byte:
		return v
	case int:
		if v < 0 || v > 255 {
			return 0
		}
		return byte(v)
	default:
		return 0
	}
}

func (w *PLCWriter) convertToBool(value interface{}) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int:
		return v != 0
	case string:
		return v == "true" || v == "1"
	default:
		return false
	}
}

// Detectar erro de conexao
func (w *PLCWriter) isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "network unreachable")
}

func (w *PLCWriter) markSuccess() {
	if w.consecutiveErrors > 0 {
		w.consecutiveErrors = 0
		w.isInErrorState = false
	}
}

func (w *PLCWriter) markError() {
	w.consecutiveErrors++
	if w.consecutiveErrors >= w.errorThreshold {
		w.isInErrorState = true
	}
}

func (w *PLCWriter) NeedsReset() bool {
	return w.isInErrorState
}

func (w *PLCWriter) ResetErrorState() {
	w.consecutiveErrors = 0
	w.isInErrorState = false
}

// Reset comando no PLC
func (w *PLCWriter) ResetCommand(byteOffset int, bitOffset int) error {
	err := w.WriteTag(100, byteOffset, "bool", false, bitOffset)
	if err != nil && w.isConnectionError(err) {
		return err
	}
	return nil
}

// Escrever status do sistema
func (w *PLCWriter) WriteSystemStatus(status *models.PLCSystemStatus) error {
	var statusByte byte = 0

	if status.LiveBit {
		statusByte |= (1 << 0)
	}
	if status.CollectionActive {
		statusByte |= (1 << 1)
	}
	if status.SystemHealthy {
		statusByte |= (1 << 2)
	}
	if status.EmergencyActive {
		statusByte |= (1 << 3)
	}
	if status.RadarCaldeiraConnected {
		statusByte |= (1 << 4)
	}
	if status.RadarPortaJusanteConnected {
		statusByte |= (1 << 5)
	}
	if status.RadarPortaMontanteConnected {
		statusByte |= (1 << 6)
	}

	err := w.WriteTag(100, 4, "byte", statusByte)
	if err != nil && w.isConnectionError(err) {
		return err
	}
	return nil
}

// Escrever dados do radar
func (w *PLCWriter) WriteRadarDataToDB100(data *models.PLCRadarData, offset int) error {
	// ObjectDetected
	if err := w.WriteTag(100, offset, "bool", data.MainObjectDetected, 0); err != nil {
		if w.isConnectionError(err) {
			return err
		}
	}

	// Amplitude
	if err := w.WriteTag(100, offset+2, "real", data.MainObjectAmplitude); err != nil {
		if w.isConnectionError(err) {
			return err
		}
	}

	// Distance
	if err := w.WriteTag(100, offset+6, "real", data.MainObjectDistance); err != nil {
		if w.isConnectionError(err) {
			return err
		}
	}

	// Velocity
	if err := w.WriteTag(100, offset+10, "real", data.MainObjectVelocity); err != nil {
		if w.isConnectionError(err) {
			return err
		}
	}

	// Objects count
	if err := w.WriteTag(100, offset+14, "int", data.ObjectsDetected); err != nil {
		if w.isConnectionError(err) {
			return err
		}
	}

	// Positions array
	for i := 0; i < 10; i++ {
		pos := offset + 16 + (i * 4)
		val := float32(0)
		if i < len(data.Positions) {
			val = data.Positions[i]
		}
		if err := w.WriteTag(100, pos, "real", val); err != nil {
			if w.isConnectionError(err) {
				return err
			}
		}
	}

	// Velocities array
	for i := 0; i < 10; i++ {
		pos := offset + 56 + (i * 4)
		val := float32(0)
		if i < len(data.Velocities) {
			val = data.Velocities[i]
		}
		if err := w.WriteTag(100, pos, "real", val); err != nil {
			if w.isConnectionError(err) {
				return err
			}
		}
	}

	return nil
}

// Limpar dados do radar (enviar zeros)
func (w *PLCWriter) WriteRadarSickCleanDataToDB100(offset int) error {
	// Object detected = false
	w.WriteTag(100, offset, "bool", false, 0)

	// Zeros nos campos principais
	w.WriteTag(100, offset+2, "real", float32(0.0))  // Amplitude
	w.WriteTag(100, offset+6, "real", float32(0.0))  // Distance
	w.WriteTag(100, offset+10, "real", float32(0.0)) // Velocity
	w.WriteTag(100, offset+14, "int", int16(0))      // Count

	// Zeros nos arrays
	for i := 0; i < 10; i++ {
		w.WriteTag(100, offset+16+(i*4), "real", float32(0.0)) // Positions
		w.WriteTag(100, offset+56+(i*4), "real", float32(0.0)) // Velocities
	}

	return nil
}

// Converter dados do radar para PLC
func (w *PLCWriter) BuildPLCRadarData(data models.RadarData) *models.PLCRadarData {
	plcData := &models.PLCRadarData{
		MainObjectDetected: data.MainObject != nil,
		ObjectsDetected:    int16(len(data.Amplitudes)),
	}

	// Timestamp
	plcData.DataTimestampHigh, plcData.DataTimestampLow = models.ConvertTimestampToPLC(data.Timestamp)

	// Objeto principal
	if data.MainObject != nil {
		plcData.MainObjectAmplitude = float32(data.MainObject.Amplitude)
		if data.MainObject.Distancia != nil {
			plcData.MainObjectDistance = float32(*data.MainObject.Distancia)
		}
		if data.MainObject.Velocidade != nil {
			plcData.MainObjectVelocity = float32(*data.MainObject.Velocidade)
		}
	}

	// Arrays (primeiros 5 elementos)
	for i := 0; i < 5 && i < len(data.Positions); i++ {
		plcData.Positions[i] = float32(data.Positions[i])
	}
	for i := 0; i < 5 && i < len(data.Velocities); i++ {
		plcData.Velocities[i] = float32(data.Velocities[i])
	}

	return plcData
}
