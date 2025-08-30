package plc

import (
	"encoding/binary"
	"fmt"
	"math"

	"backend/pkg/models"
)

// PLCWriter escreve dados no PLC de forma otimizada
type PLCWriter struct {
	client PLCClient
}

// TypeConverter define conversores por tipo de dado
type TypeConverter struct {
	Size      int
	Converter func(interface{}) ([]byte, error)
}

// NewPLCWriter cria escritor PLC otimizado
func NewPLCWriter(client PLCClient) *PLCWriter {
	return &PLCWriter{client: client}
}

// WriteTag escreve valor no PLC com validação robusta
func (w *PLCWriter) WriteTag(dbNumber int, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	if value == nil {
		return fmt.Errorf("valor não pode ser nil")
	}

	converters := map[string]TypeConverter{
		"real":   {4, w.convertReal},
		"dint":   {4, w.convertDint},
		"int32":  {4, w.convertDint},
		"dword":  {4, w.convertDword},
		"uint32": {4, w.convertDword},
		"int":    {2, w.convertInt16},
		"int16":  {2, w.convertInt16},
		"word":   {2, w.convertWord},
		"uint16": {2, w.convertWord},
		"sint":   {1, w.convertSint},
		"int8":   {1, w.convertSint},
		"usint":  {1, w.convertUsint},
		"byte":   {1, w.convertUsint},
		"uint8":  {1, w.convertUsint},
		"bool":   {1, func(v interface{}) ([]byte, error) { return w.convertBool(v, dbNumber, byteOffset, bitOffset...) }},
	}

	converter, exists := converters[dataType]
	if !exists {
		return fmt.Errorf("tipo não suportado: %s", dataType)
	}

	buf, err := converter.Converter(value)
	if err != nil {
		return fmt.Errorf("erro na conversão %s: %v", dataType, err)
	}

	return w.client.AGWriteDB(dbNumber, byteOffset, len(buf), buf)
}

// ========== CONVERSORES OTIMIZADOS ==========

func (w *PLCWriter) convertReal(value interface{}) ([]byte, error) {
	var val float32

	switch v := value.(type) {
	case float32:
		val = v
	case float64:
		val = float32(v)
	case int, int32, int64:
		val = float32(v.(int))
	default:
		return nil, fmt.Errorf("tipo incompatível com real: %T", value)
	}

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, math.Float32bits(val))
	return buf, nil
}

func (w *PLCWriter) convertDint(value interface{}) ([]byte, error) {
	var val int32

	switch v := value.(type) {
	case int32:
		val = v
	case int:
		val = int32(v)
	case int64:
		val = int32(v)
	case float32, float64:
		val = int32(v.(float64))
	default:
		return nil, fmt.Errorf("tipo incompatível com dint: %T", value)
	}

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(val))
	return buf, nil
}

func (w *PLCWriter) convertDword(value interface{}) ([]byte, error) {
	var val uint32

	switch v := value.(type) {
	case uint32:
		val = v
	case uint, int:
		if v.(int) < 0 {
			return nil, fmt.Errorf("valor negativo para uint32")
		}
		val = uint32(v.(int))
	case float64:
		if v < 0 {
			return nil, fmt.Errorf("valor negativo para uint32")
		}
		val = uint32(v)
	default:
		return nil, fmt.Errorf("tipo incompatível com dword: %T", value)
	}

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, val)
	return buf, nil
}

func (w *PLCWriter) convertInt16(value interface{}) ([]byte, error) {
	var val int16

	switch v := value.(type) {
	case int16:
		val = v
	case int:
		val = int16(v)
	case float32, float64:
		val = int16(v.(float64))
	default:
		return nil, fmt.Errorf("tipo incompatível com int16: %T", value)
	}

	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, uint16(val))
	return buf, nil
}

func (w *PLCWriter) convertWord(value interface{}) ([]byte, error) {
	var val uint16

	switch v := value.(type) {
	case uint16:
		val = v
	case int:
		if v < 0 {
			return nil, fmt.Errorf("valor negativo para word")
		}
		val = uint16(v)
	case float64:
		if v < 0 {
			return nil, fmt.Errorf("valor negativo para word")
		}
		val = uint16(v)
	default:
		return nil, fmt.Errorf("tipo incompatível com word: %T", value)
	}

	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, val)
	return buf, nil
}

func (w *PLCWriter) convertSint(value interface{}) ([]byte, error) {
	var val int8

	switch v := value.(type) {
	case int8:
		val = v
	case int:
		val = int8(v)
	case float64:
		val = int8(v)
	default:
		return nil, fmt.Errorf("tipo incompatível com sint: %T", value)
	}

	return []byte{byte(val)}, nil
}

func (w *PLCWriter) convertUsint(value interface{}) ([]byte, error) {
	var val uint8

	switch v := value.(type) {
	case uint8:
		val = v
	case int:
		if v < 0 {
			return nil, fmt.Errorf("valor negativo para usint")
		}
		val = uint8(v)
	case float64:
		if v < 0 {
			return nil, fmt.Errorf("valor negativo para usint")
		}
		val = uint8(v)
	default:
		return nil, fmt.Errorf("tipo incompatível com usint: %T", value)
	}

	return []byte{val}, nil
}

func (w *PLCWriter) convertBool(value interface{}, dbNumber, byteOffset int, bitOffset ...int) ([]byte, error) {
	buf := make([]byte, 1)

	// Ler byte atual para preservar outros bits
	if err := w.client.AGReadDB(dbNumber, byteOffset, 1, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler byte atual: %w", err)
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
		return nil, fmt.Errorf("tipo incompatível com bool: %T", value)
	}

	bit := 0
	if len(bitOffset) > 0 && bitOffset[0] >= 0 && bitOffset[0] <= 7 {
		bit = bitOffset[0]
	}

	mask := uint8(1 << bit)
	if val {
		buf[0] |= mask
	} else {
		buf[0] &^= mask
	}

	return buf, nil
}

// ResetCommand reseta comando específico
func (w *PLCWriter) ResetCommand(byteOffset int, bitOffset int) error {
	return w.WriteTag(100, byteOffset, "bool", false, bitOffset)
}

// WriteSystemStatus escreve status com batch otimizado
func (w *PLCWriter) WriteSystemStatus(status *models.PLCSystemStatus) error {
	// Definir operações de escrita
	statusWrites := []struct {
		field string
		bit   int
		value bool
	}{
		{"LiveBit", 0, status.LiveBit},
		{"CollectionActive", 1, status.CollectionActive},
		{"SystemHealthy", 2, status.SystemHealthy},
		{"EmergencyActive", 3, status.EmergencyActive},
		{"RadarCaldeiraConnected", 4, status.RadarCaldeiraConnected},
		{"RadarPortaJusanteConnected", 5, status.RadarPortaJusanteConnected},
		{"RadarPortaMontanteConnected", 6, status.RadarPortaMontanteConnected},
	}

	// Executar escritas com error handling robusto
	for _, sw := range statusWrites {
		if err := w.WriteTag(100, 4, "bool", sw.value, sw.bit); err != nil {
			return fmt.Errorf("erro ao escrever %s: %v", sw.field, err)
		}
	}

	return nil
}

// WriteRadarDataToDB100 escreve dados do radar otimizado
func (w *PLCWriter) WriteRadarDataToDB100(data *models.PLCRadarData, baseOffset int) error {
	if data == nil {
		return fmt.Errorf("dados do radar não podem ser nil")
	}

	// Definir operações de escrita
	writes := []struct {
		offset   int
		dataType string
		value    interface{}
		bit      int
	}{
		{baseOffset + 0, "bool", data.MainObjectDetected, 0},
		{baseOffset + 2, "real", data.MainObjectAmplitude, -1},
		{baseOffset + 6, "real", data.MainObjectDistance, -1},
		{baseOffset + 10, "real", data.MainObjectVelocity, -1},
		{baseOffset + 14, "int", data.ObjectsDetected, -1},
	}

	// Executar escritas básicas
	for _, write := range writes {
		var err error
		if write.bit >= 0 {
			err = w.WriteTag(100, write.offset, write.dataType, write.value, write.bit)
		} else {
			err = w.WriteTag(100, write.offset, write.dataType, write.value)
		}
		if err != nil {
			return fmt.Errorf("erro na escrita offset %d: %v", write.offset, err)
		}
	}

	// Escrever arrays de forma otimizada
	if err := w.writeFloatArray(100, baseOffset+16, data.Positions[:], 10); err != nil {
		return fmt.Errorf("erro ao escrever positions: %v", err)
	}

	if err := w.writeFloatArray(100, baseOffset+56, data.Velocities[:], 10); err != nil {
		return fmt.Errorf("erro ao escrever velocities: %v", err)
	}

	return nil
}

// writeFloatArray escreve array de floats otimizado
func (w *PLCWriter) writeFloatArray(dbNumber, startOffset int, values []float32, maxSize int) error {
	for i := 0; i < maxSize; i++ {
		val := float32(0)
		if i < len(values) {
			val = values[i]
		}

		offset := startOffset + (i * 4)
		if err := w.WriteTag(dbNumber, offset, "real", val); err != nil {
			return fmt.Errorf("erro no índice %d: %v", i, err)
		}
	}
	return nil
}

// BuildPLCRadarData converte dados com validação robusta
func (w *PLCWriter) BuildPLCRadarData(data models.RadarData) *models.PLCRadarData {
	plcData := &models.PLCRadarData{
		MainObjectDetected: data.MainObject != nil,
		ObjectsDetected:    int16(len(data.Amplitudes)),
	}

	// Converter timestamp de forma segura
	plcData.DataTimestampHigh, plcData.DataTimestampLow = models.ConvertTimestampToPLC(data.Timestamp)

	// Processar objeto principal se existir
	if data.MainObject != nil {
		w.fillMainObjectData(plcData, data.MainObject)
	}

	// Calcular estatísticas
	w.calculateStatistics(plcData, data)

	// Preencher arrays limitados
	w.fillArrayData(plcData, data)

	return plcData
}

// fillMainObjectData preenche dados do objeto principal
func (w *PLCWriter) fillMainObjectData(plcData *models.PLCRadarData, mainObj *models.ObjPrincipal) {
	plcData.MainObjectAmplitude = float32(mainObj.Amplitude)

	if mainObj.Distancia != nil {
		plcData.MainObjectDistance = float32(*mainObj.Distancia)
	}
	if mainObj.Velocidade != nil {
		plcData.MainObjectVelocity = float32(*mainObj.Velocidade)
	}
	if mainObj.Angulo != nil {
		plcData.MainObjectAngle = float32(*mainObj.Angulo)
	}
}

// calculateStatistics calcula estatísticas dos dados
func (w *PLCWriter) calculateStatistics(plcData *models.PLCRadarData, data models.RadarData) {
	// Amplitude máxima
	if len(data.Amplitudes) > 0 {
		maxAmp := data.Amplitudes[0]
		for _, amp := range data.Amplitudes[1:] {
			if amp > maxAmp {
				maxAmp = amp
			}
		}
		plcData.MaxAmplitude = float32(maxAmp)
	}

	// Distâncias mín/máx
	if len(data.Positions) > 0 {
		minDist, maxDist := data.Positions[0], data.Positions[0]
		for _, pos := range data.Positions[1:] {
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
}

// fillArrayData preenche arrays com limite seguro
func (w *PLCWriter) fillArrayData(plcData *models.PLCRadarData, data models.RadarData) {
	// Copiar primeiros 5 elementos com validação
	copyLimit := func(dst []float32, src []float64, maxSize int) {
		for i := 0; i < maxSize && i < len(src); i++ {
			dst[i] = float32(src[i])
		}
	}

	copyLimit(plcData.Positions[:], data.Positions, 5)
	copyLimit(plcData.Velocities[:], data.Velocities, 5)
}

// BuildPLCSystemStatus cria status do sistema
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

// WriteMultiRadarDataToDB100 escreve múltiplos radares otimizado
func (w *PLCWriter) WriteMultiRadarDataToDB100(multiRadarData *models.PLCMultiRadarData) error {
	if multiRadarData == nil {
		return fmt.Errorf("dados multi-radar não podem ser nil")
	}

	// Map de radares com offsets
	radarWrites := map[string]struct {
		data   *models.PLCRadarData
		offset int
		name   string
	}{
		"caldeira":       {&multiRadarData.RadarCaldeira, 6, "Caldeira"},
		"porta_jusante":  {&multiRadarData.RadarPortaJusante, 102, "Porta Jusante"},
		"porta_montante": {&multiRadarData.RadarPortaMontante, 198, "Porta Montante"},
	}

	// Executar escritas com error collection
	var errors []string
	for _, rw := range radarWrites {
		if err := w.WriteRadarDataToDB100(rw.data, rw.offset); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", rw.name, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("erros na escrita: %v", errors)
	}

	return nil
}

// ========== MÉTODOS AUXILIARES ROBUSTOS ==========

// ValidateOffset valida se offset está dentro dos limites da DB
func (w *PLCWriter) ValidateOffset(dbNumber, offset, size int) error {
	maxOffsets := map[int]int{
		100: 304, // DB100 tem 304 bytes
		300: 100, // DBs de radar individuais
		400: 100,
		500: 100,
	}

	maxSize, exists := maxOffsets[dbNumber]
	if !exists {
		return fmt.Errorf("DB%d não configurada", dbNumber)
	}

	if offset < 0 || offset+size > maxSize {
		return fmt.Errorf("offset %d+%d excede limite da DB%d (%d bytes)", offset, size, dbNumber, maxSize)
	}

	return nil
}

// WriteTagSafe escreve com validação completa
func (w *PLCWriter) WriteTagSafe(dbNumber int, byteOffset int, dataType string, value interface{}, bitOffset ...int) error {
	// Validar parâmetros
	if w.client == nil {
		return fmt.Errorf("cliente PLC não inicializado")
	}

	// Determinar tamanho
	sizes := map[string]int{
		"real": 4, "dint": 4, "int32": 4, "dword": 4, "uint32": 4,
		"int": 2, "int16": 2, "word": 2, "uint16": 2,
		"sint": 1, "int8": 1, "usint": 1, "byte": 1, "uint8": 1, "bool": 1,
	}

	size, exists := sizes[dataType]
	if !exists {
		return fmt.Errorf("tipo de dado inválido: %s", dataType)
	}

	// Validar offset
	if err := w.ValidateOffset(dbNumber, byteOffset, size); err != nil {
		return err
	}

	// Chamar WriteTag normal
	return w.WriteTag(dbNumber, byteOffset, dataType, value, bitOffset...)
}

// BatchWriteRadarData escreve dados de radar em uma operação
func (w *PLCWriter) BatchWriteRadarData(radarID string, data *models.PLCRadarData) error {
	baseOffsets := map[string]int{
		"caldeira": 6, "porta_jusante": 102, "porta_montante": 198,
	}

	baseOffset, exists := baseOffsets[radarID]
	if !exists {
		return fmt.Errorf("radar desconhecido: %s", radarID)
	}

	return w.WriteRadarDataToDB100(data, baseOffset)
}
