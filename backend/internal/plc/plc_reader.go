package plc

import (
	"encoding/binary"
	"fmt"
	"math"

	"backend/pkg/models"
)

// PLCReader lê dados do PLC de forma otimizada
type PLCReader struct {
	client PLCClient
}

// PLCClient interface para operações do PLC
type PLCClient interface {
	AGWriteDB(dbNumber, byteOffset, size int, buffer []byte) error
	AGReadDB(dbNumber, byteOffset, size int, buffer []byte) error
}

// TypeReader define como ler cada tipo de dado
type TypeReader struct {
	Size   int
	Reader func([]byte, ...int) (interface{}, error)
}

// CommandConfig define configuração de leitura de comando
type CommandConfig struct {
	ByteOffset int
	BitOffset  int
	FieldName  string
}

// NewPLCReader cria leitor PLC otimizado
func NewPLCReader(client PLCClient) *PLCReader {
	return &PLCReader{client: client}
}

// ReadTag lê valor do PLC com sistema otimizado
func (r *PLCReader) ReadTag(dbNumber int, byteOffset int, dataType string, bitOffset ...int) (interface{}, error) {
	// Validações de entrada
	if r.client == nil {
		return nil, fmt.Errorf("cliente PLC não inicializado")
	}
	if byteOffset < 0 {
		return nil, fmt.Errorf("offset inválido: %d", byteOffset)
	}

	// Sistema de readers por tipo
	typeReaders := map[string]TypeReader{
		"real":   {4, r.readReal},
		"dint":   {4, r.readDint},
		"int32":  {4, r.readDint},
		"dword":  {4, r.readDword},
		"uint32": {4, r.readDword},
		"int":    {2, r.readInt16},
		"int16":  {2, r.readInt16},
		"word":   {2, r.readWord},
		"uint16": {2, r.readWord},
		"sint":   {1, r.readSint},
		"int8":   {1, r.readSint},
		"usint":  {1, r.readUsint},
		"byte":   {1, r.readUsint},
		"uint8":  {1, r.readUsint},
		"bool":   {1, r.readBool},
	}

	reader, exists := typeReaders[dataType]
	if !exists {
		return nil, fmt.Errorf("tipo não suportado: %s", dataType)
	}

	// Validar limites da DB
	if err := r.validateOffset(dbNumber, byteOffset, reader.Size); err != nil {
		return nil, err
	}

	// Ler bytes do PLC
	buf := make([]byte, reader.Size)
	if err := r.client.AGReadDB(dbNumber, byteOffset, reader.Size, buf); err != nil {
		return nil, fmt.Errorf("erro leitura PLC DB%d.%d: %w", dbNumber, byteOffset, err)
	}

	// Converter usando reader específico
	return reader.Reader(buf, bitOffset...)
}

// ========== READERS ESPECIALIZADOS ==========

func (r *PLCReader) readReal(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("buffer insuficiente para real")
	}
	return math.Float32frombits(binary.BigEndian.Uint32(buf)), nil
}

func (r *PLCReader) readDint(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("buffer insuficiente para dint")
	}
	return int32(binary.BigEndian.Uint32(buf)), nil
}

func (r *PLCReader) readDword(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("buffer insuficiente para dword")
	}
	return binary.BigEndian.Uint32(buf), nil
}

func (r *PLCReader) readInt16(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 2 {
		return nil, fmt.Errorf("buffer insuficiente para int16")
	}
	return int16(binary.BigEndian.Uint16(buf)), nil
}

func (r *PLCReader) readWord(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 2 {
		return nil, fmt.Errorf("buffer insuficiente para word")
	}
	return binary.BigEndian.Uint16(buf), nil
}

func (r *PLCReader) readSint(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 1 {
		return nil, fmt.Errorf("buffer insuficiente para sint")
	}
	return int8(buf[0]), nil
}

func (r *PLCReader) readUsint(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 1 {
		return nil, fmt.Errorf("buffer insuficiente para usint")
	}
	return buf[0], nil
}

func (r *PLCReader) readBool(buf []byte, bitOffset ...int) (interface{}, error) {
	if len(buf) < 1 {
		return nil, fmt.Errorf("buffer insuficiente para bool")
	}

	bit := 0
	if len(bitOffset) > 0 && bitOffset[0] >= 0 && bitOffset[0] <= 7 {
		bit = bitOffset[0]
	}

	return ((buf[0] >> uint(bit)) & 0x01) == 1, nil
}

// validateOffset valida se offset está dentro dos limites
func (r *PLCReader) validateOffset(dbNumber, offset, size int) error {
	maxSizes := map[int]int{
		100: 304, // DB100
		300: 100, // DBs radar
		400: 100,
		500: 100,
	}

	maxSize, exists := maxSizes[dbNumber]
	if !exists {
		return fmt.Errorf("DB%d não configurada", dbNumber)
	}

	if offset < 0 || offset+size > maxSize {
		return fmt.Errorf("offset %d+%d excede DB%d (%d bytes)", offset, size, dbNumber, maxSize)
	}

	return nil
}

// ========== MÉTODOS PÚBLICOS OTIMIZADOS ==========

// ReadCommands lê comandos otimizado - SEM REPETIÇÃO
func (r *PLCReader) ReadCommands() (*models.PLCCommands, error) {
	commands := &models.PLCCommands{}

	// Configuração de comandos - ZERO repetição
	commandConfigs := []struct {
		field      *bool
		byteOffset int
		bitOffset  int
		name       string
	}{
		{&commands.StartCollection, 0, 0, "StartCollection"},
		{&commands.StopCollection, 0, 1, "StopCollection"},
		{&commands.Emergency, 0, 2, "Emergency"},
		{&commands.ResetErrors, 0, 3, "ResetErrors"},
		{&commands.EnableRadarCaldeira, 0, 4, "EnableRadarCaldeira"},
		{&commands.EnableRadarPortaJusante, 0, 5, "EnableRadarPortaJusante"},
		{&commands.EnableRadarPortaMontante, 0, 6, "EnableRadarPortaMontante"},
		{&commands.RestartRadarCaldeira, 0, 7, "RestartRadarCaldeira"},
		{&commands.RestartRadarPortaJusante, 1, 0, "RestartRadarPortaJusante"},
		{&commands.RestartRadarPortaMontante, 1, 1, "RestartRadarPortaMontante"},
	}

	// Ler todos comandos em loop elegante
	for _, config := range commandConfigs {
		value, err := r.readBoolSafe(100, config.byteOffset, config.bitOffset)
		if err != nil {
			return nil, fmt.Errorf("erro ao ler %s: %v", config.name, err)
		}
		*config.field = value
	}

	return commands, nil
}

// ReadSystemStatus lê status otimizado
func (r *PLCReader) ReadSystemStatus() (*models.PLCSystemStatus, error) {
	status := &models.PLCSystemStatus{}

	// Configuração de status
	statusConfigs := []struct {
		field *bool
		bit   int
		name  string
	}{
		{&status.LiveBit, 0, "LiveBit"},
		{&status.CollectionActive, 1, "CollectionActive"},
		{&status.SystemHealthy, 2, "SystemHealthy"},
		{&status.EmergencyActive, 3, "EmergencyActive"},
		{&status.RadarCaldeiraConnected, 4, "RadarCaldeiraConnected"},
		{&status.RadarPortaJusanteConnected, 5, "RadarPortaJusanteConnected"},
		{&status.RadarPortaMontanteConnected, 6, "RadarPortaMontanteConnected"},
	}

	// Ler todos status em loop
	for _, config := range statusConfigs {
		value, err := r.readBoolSafe(100, 4, config.bit)
		if err != nil {
			return nil, fmt.Errorf("erro ao ler %s: %v", config.name, err)
		}
		*config.field = value
	}

	return status, nil
}

// ========== MÉTODOS AUXILIARES SEGUROS ==========

// readBoolSafe lê bool com validação completa
func (r *PLCReader) readBoolSafe(dbNumber, byteOffset, bitOffset int) (bool, error) {
	value, err := r.ReadTag(dbNumber, byteOffset, "bool", bitOffset)
	if err != nil {
		return false, err
	}

	boolValue, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("valor esperado bool, recebido %T", value)
	}

	return boolValue, nil
}

// ReadTagSafe lê tag com type assertion segura
func (r *PLCReader) ReadTagSafe(dbNumber int, byteOffset int, dataType string, bitOffset ...int) (interface{}, error) {
	value, err := r.ReadTag(dbNumber, byteOffset, dataType, bitOffset...)
	if err != nil {
		return nil, err
	}

	if value == nil {
		return nil, fmt.Errorf("valor lido é nil")
	}

	return value, nil
}

// BatchReadBools lê múltiplos bools do mesmo byte
func (r *PLCReader) BatchReadBools(dbNumber, byteOffset int, bitConfigs []struct {
	BitOffset int
	Name      string
}) (map[string]bool, error) {

	// Ler byte uma vez
	buf := make([]byte, 1)
	if err := r.client.AGReadDB(dbNumber, byteOffset, 1, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler byte %d: %w", byteOffset, err)
	}

	// Extrair todos os bits
	results := make(map[string]bool)
	for _, config := range bitConfigs {
		if config.BitOffset < 0 || config.BitOffset > 7 {
			continue
		}

		bitValue := ((buf[0] >> uint(config.BitOffset)) & 0x01) == 1
		results[config.Name] = bitValue
	}

	return results, nil
}

// ReadMultipleReals lê múltiplos REALs sequenciais
func (r *PLCReader) ReadMultipleReals(dbNumber, startOffset, count int) ([]float32, error) {
	if count <= 0 {
		return nil, fmt.Errorf("count deve ser positivo")
	}

	// Ler todos os bytes de uma vez
	totalSize := count * 4
	buf := make([]byte, totalSize)

	if err := r.client.AGReadDB(dbNumber, startOffset, totalSize, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler múltiplos REALs: %w", err)
	}

	// Converter todos os valores
	values := make([]float32, count)
	for i := 0; i < count; i++ {
		offset := i * 4
		if offset+4 <= len(buf) {
			bits := binary.BigEndian.Uint32(buf[offset : offset+4])
			values[i] = math.Float32frombits(bits)
		}
	}

	return values, nil
}

// ReadRadarDataFromDB100 lê dados completos de um radar
func (r *PLCReader) ReadRadarDataFromDB100(baseOffset int) (*models.PLCRadarData, error) {
	data := &models.PLCRadarData{}

	// Configuração de leitura estruturada
	readOps := []struct {
		offset    int
		dataType  string
		target    interface{}
		bitOffset int
	}{
		{baseOffset + 0, "bool", &data.MainObjectDetected, 0},
		{baseOffset + 2, "real", &data.MainObjectAmplitude, -1},
		{baseOffset + 6, "real", &data.MainObjectDistance, -1},
		{baseOffset + 10, "real", &data.MainObjectVelocity, -1},
		{baseOffset + 14, "int16", &data.ObjectsDetected, -1},
	}

	// Executar leituras
	for _, op := range readOps {
		var value interface{}
		var err error

		if op.bitOffset >= 0 {
			value, err = r.ReadTag(100, op.offset, op.dataType, op.bitOffset)
		} else {
			value, err = r.ReadTag(100, op.offset, op.dataType)
		}

		if err != nil {
			return nil, fmt.Errorf("erro na leitura offset %d: %v", op.offset, err)
		}

		// Type assertion segura
		switch target := op.target.(type) {
		case *bool:
			if v, ok := value.(bool); ok {
				*target = v
			}
		case *float32:
			if v, ok := value.(float32); ok {
				*target = v
			}
		case *int16:
			if v, ok := value.(int16); ok {
				*target = v
			}
		}
	}

	// Ler arrays otimizado
	if err := r.readRadarArrays(data, baseOffset); err != nil {
		return nil, fmt.Errorf("erro ao ler arrays: %v", err)
	}

	return data, nil
}

// readRadarArrays lê arrays do radar de forma otimizada
func (r *PLCReader) readRadarArrays(data *models.PLCRadarData, baseOffset int) error {
	// Ler positions (5 REALs)
	positions, err := r.ReadMultipleReals(100, baseOffset+16, 5)
	if err != nil {
		return fmt.Errorf("erro ao ler positions: %v", err)
	}
	copy(data.Positions[:], positions)

	// Ler velocities (5 REALs)
	velocities, err := r.ReadMultipleReals(100, baseOffset+36, 5)
	if err != nil {
		return fmt.Errorf("erro ao ler velocities: %v", err)
	}
	copy(data.Velocities[:], velocities)

	return nil
}

// ========== MÉTODOS PÚBLICOS MANTIDOS ==========

// ReadCommandsBatch lê comandos com operação batch
func (r *PLCReader) ReadCommandsBatch() (*models.PLCCommands, error) {
	// Ler bytes 0 e 1 de uma vez
	buf := make([]byte, 2)
	if err := r.client.AGReadDB(100, 0, 2, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler comandos: %w", err)
	}

	commands := &models.PLCCommands{}

	// Extrair bits do byte 0
	byte0Bits := []struct {
		field *bool
		bit   int
	}{
		{&commands.StartCollection, 0},
		{&commands.StopCollection, 1},
		{&commands.Emergency, 2},
		{&commands.ResetErrors, 3},
		{&commands.EnableRadarCaldeira, 4},
		{&commands.EnableRadarPortaJusante, 5},
		{&commands.EnableRadarPortaMontante, 6},
		{&commands.RestartRadarCaldeira, 7},
	}

	for _, config := range byte0Bits {
		*config.field = ((buf[0] >> uint(config.bit)) & 0x01) == 1
	}

	// Extrair bits do byte 1
	commands.RestartRadarPortaJusante = ((buf[1] >> 0) & 0x01) == 1
	commands.RestartRadarPortaMontante = ((buf[1] >> 1) & 0x01) == 1

	return commands, nil
}

// ReadSystemStatusBatch lê status com operação batch
func (r *PLCReader) ReadSystemStatusBatch() (*models.PLCSystemStatus, error) {
	// Ler byte 4 de uma vez
	buf := make([]byte, 1)
	if err := r.client.AGReadDB(100, 4, 1, buf); err != nil {
		return nil, fmt.Errorf("erro ao ler status: %w", err)
	}

	status := &models.PLCSystemStatus{}

	// Extrair todos os bits de uma vez
	statusBits := []struct {
		field *bool
		bit   int
	}{
		{&status.LiveBit, 0},
		{&status.CollectionActive, 1},
		{&status.SystemHealthy, 2},
		{&status.EmergencyActive, 3},
		{&status.RadarCaldeiraConnected, 4},
		{&status.RadarPortaJusanteConnected, 5},
		{&status.RadarPortaMontanteConnected, 6},
	}

	for _, config := range statusBits {
		*config.field = ((buf[0] >> uint(config.bit)) & 0x01) == 1
	}

	return status, nil
}

// ========== UTILITÁRIOS DE DEBUG ==========

// ReadTagWithDebug lê tag com informações de debug
func (r *PLCReader) ReadTagWithDebug(dbNumber int, byteOffset int, dataType string, bitOffset ...int) (interface{}, []byte, error) {
	// Determinar tamanho
	sizes := map[string]int{
		"real": 4, "dint": 4, "int32": 4, "dword": 4, "uint32": 4,
		"int": 2, "int16": 2, "word": 2, "uint16": 2,
		"sint": 1, "int8": 1, "usint": 1, "byte": 1, "uint8": 1, "bool": 1,
	}

	size, exists := sizes[dataType]
	if !exists {
		return nil, nil, fmt.Errorf("tipo não suportado: %s", dataType)
	}

	// Ler bytes raw
	buf := make([]byte, size)
	if err := r.client.AGReadDB(dbNumber, byteOffset, size, buf); err != nil {
		return nil, nil, fmt.Errorf("erro leitura debug: %w", err)
	}

	// Converter valor
	value, err := r.ReadTag(dbNumber, byteOffset, dataType, bitOffset...)
	if err != nil {
		return nil, buf, err
	}

	return value, buf, nil
}

// ValidateConnection verifica se cliente está funcionando
func (r *PLCReader) ValidateConnection() error {
	if r.client == nil {
		return fmt.Errorf("cliente PLC não inicializado")
	}

	// Teste simples - ler 1 byte da DB100
	buf := make([]byte, 1)
	err := r.client.AGReadDB(100, 0, 1, buf)
	if err != nil {
		return fmt.Errorf("falha na validação de conexão: %w", err)
	}

	return nil
}
