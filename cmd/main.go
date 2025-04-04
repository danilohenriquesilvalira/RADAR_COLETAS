package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
	"github.com/robinson/gos7"
)

// Configurações globais
const (
	// Configurações do radar
	RadarHost    = "192.168.1.84"
	RadarPort    = 2111 // Porta AUX para monitoramento (ASCII)
	ProtocolType = "ascii"

	// Configurações do Redis
	RedisAddr     = "localhost:6379"
	RedisPassword = ""
	RedisDB       = 0
	RedisPrefix   = "radar_sick"

	// Configurações do PLC
	PLCHost     = "192.168.1.33"
	PLCRack     = 0
	PLCSlot     = 1
	PLCDB       = 6
	PLCAreaSize = 56 // 14 valores REAL (4 bytes cada)

	// Configurações do WebSocket
	WebSocketPort = 8080
	WebSocketPath = "/ws"

	// Configurações gerais
	SampleRate           = 100 * time.Millisecond // 10 Hz
	MaxConsecutiveErrors = 5
	ReconnectDelay       = 2 * time.Second
	DebuggingEnabled     = true

	// Configurações para detecção de mudanças
	MinVelocityChange      = 0.01 // Mudança mínima de velocidade para ser registrada (m/s)
	MaxVelocityHistorySize = 100  // Número máximo de eventos de mudança a armazenar
)

// RadarMetrics armazena as métricas decodificadas do radar
type RadarMetrics struct {
	Positions       [7]float64
	Velocities      [7]float64
	LastVelocities  [7]float64 // Para rastrear mudanças
	Timestamp       time.Time
	Status          string
	VelocityChanges []VelocityChange // Registra quais velocidades mudaram
}

// VelocityChange representa uma mudança específica em uma velocidade
type VelocityChange struct {
	Index       int       `json:"index"`        // Índice da velocidade (0-6)
	OldValue    float64   `json:"old_value"`    // Valor anterior
	NewValue    float64   `json:"new_value"`    // Valor novo
	ChangeValue float64   `json:"change_value"` // Diferença
	Timestamp   time.Time `json:"timestamp"`    // Momento da mudança
}

// HistoryStats estrutura que armazena estatísticas do Redis
type HistoryStats struct {
	TotalChanges    int                     `json:"total_changes"`
	MaxVelocity     float64                 `json:"max_velocity"`
	MinVelocity     float64                 `json:"min_velocity"`
	AvgVelocity     float64                 `json:"avg_velocity"`
	ChangeFrequency float64                 `json:"change_frequency"`
	LastUpdated     time.Time               `json:"last_updated"`
	VelocityHistory map[int][]VelocityPoint `json:"velocity_history"`
}

// VelocityPoint representa um ponto histórico de velocidade
type VelocityPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// SickRadar gerencia a conexão com o radar
type SickRadar struct {
	conn      net.Conn
	host      string
	port      int
	connected bool
	protocol  string // "ascii" ou "binary"
}

// NewSickRadar cria uma nova instância do radar
func NewSickRadar(host string, port int, protocol string) *SickRadar {
	return &SickRadar{
		host:     host,
		port:     port,
		protocol: strings.ToLower(protocol),
	}
}

// Connect estabelece conexão com o radar
func (r *SickRadar) Connect() error {
	if r.connected {
		return nil
	}

	addr := fmt.Sprintf("%s:%d", r.host, r.port)
	log.Printf("Tentando conectar ao radar em %s...", addr)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("erro ao conectar ao radar: %v", err)
	}

	r.conn = conn
	r.connected = true
	log.Printf("Conectado ao radar em %s", addr)
	return nil
}

// SendCommand envia comando para o radar
func (r *SickRadar) SendCommand(cmd string) (string, error) {
	if !r.connected {
		if err := r.Connect(); err != nil {
			return "", err
		}
	}

	// Adiciona os caracteres STX (0x02) e ETX (0x03) ao comando
	command := fmt.Sprintf("\x02%s\x03", cmd)
	_, err := r.conn.Write([]byte(command))
	if err != nil {
		r.connected = false
		return "", fmt.Errorf("erro ao enviar comando: %v", err)
	}

	// Lê a resposta com timeout
	buffer := make([]byte, 4096)
	r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := r.conn.Read(buffer)
	if err != nil {
		r.connected = false
		return "", fmt.Errorf("erro ao ler resposta: %v", err)
	}

	return string(buffer[:n]), nil
}

// hexStringToFloat32 converte uma string hexadecimal IEEE-754 para float32
func hexStringToFloat32(hexStr string) float32 {
	// Converte a string hexadecimal para um uint32
	val, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		log.Printf("Erro ao converter %s para float: %v", hexStr, err)
		return 0.0
	}

	// Converte o uint32 para float32 usando IEEE-754
	return math.Float32frombits(uint32(val))
}

// smallHexToInt converte um valor hexadecimal pequeno para int
func smallHexToInt(hexStr string) int {
	val, err := strconv.ParseInt(hexStr, 16, 32)
	if err != nil {
		return 0
	}
	return int(val)
}

// DecodeValues decodifica a resposta do radar em métricas
func (r *SickRadar) DecodeValues(response string) (*RadarMetrics, error) {
	metrics := &RadarMetrics{
		Timestamp: time.Now(),
		Status:    "ok",
	}

	if DebuggingEnabled {
		fmt.Println("\nResposta ASCII do radar:")
		fmt.Println(response)

		// Converte para hexadecimal para depuração
		hexDump := ""
		for i, c := range response {
			if i < 50 { // Limita para os primeiros 50 caracteres
				hexDump += fmt.Sprintf("%02X ", c)
			}
		}
		fmt.Println("Hex dump dos primeiros 50 bytes:")
		fmt.Println(hexDump)
	}

	// Remove caracteres de controle e divide em tokens
	cleanedResponse := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return ' ' // Substitui caracteres de controle por espaço
		}
		return r
	}, response)

	tokens := strings.Fields(cleanedResponse)

	// Processa o bloco de posições (P3DX1)
	posIdx := -1
	for i, token := range tokens {
		if token == "P3DX1" {
			posIdx = i
			break
		}
	}

	if posIdx != -1 && posIdx+3 < len(tokens) {
		// Extrai a escala em formato float
		scaleHex := tokens[posIdx+1]
		scale := hexStringToFloat32(scaleHex)

		// O terceiro token (após o token não utilizado) indica o número de valores que seguem
		numValues := 7 // Padrão para 7 posições
		if posIdx+3 < len(tokens) {
			if valCount, err := strconv.Atoi(tokens[posIdx+3]); err == nil {
				numValues = valCount
				if numValues > 7 {
					numValues = 7 // Limitamos a 7 para manter a compatibilidade
				}
			}
		}

		fmt.Printf("\nBloco de Posição (P3DX1) encontrado. Escala: %f\n", scale)

		// Processa os valores de posição (começando após o contador de valores)
		for i := 0; i < numValues && posIdx+i+4 < len(tokens); i++ {
			valHex := tokens[posIdx+i+4]

			// Converte valor hexadecimal para decimal
			decimalValue := smallHexToInt(valHex)

			// Aplica a escala correta (divide por 1000 para ter metros)
			posMeters := float64(decimalValue) * float64(scale) / 1000.0

			if i < 7 { // Garante que não exceda o array
				metrics.Positions[i] = posMeters
			}

			fmt.Printf("  pos%d: HEX=%s -> DEC=%d -> %.3fm\n", i+1, valHex, decimalValue, posMeters)
		}
	} else {
		fmt.Println("Bloco de Posição (P3DX1) não encontrado ou formato inesperado.")
	}

	// Processa o bloco de velocidades (V3DX1)
	velIdx := -1
	for i, token := range tokens {
		if token == "V3DX1" {
			velIdx = i
			break
		}
	}

	if velIdx != -1 && velIdx+3 < len(tokens) {
		// Extrai a escala em formato float
		scaleHex := tokens[velIdx+1]
		scale := hexStringToFloat32(scaleHex)

		// O terceiro token (após o token não utilizado) indica o número de valores que seguem
		numValues := 7 // Padrão para 7 velocidades
		if velIdx+3 < len(tokens) {
			if valCount, err := strconv.Atoi(tokens[velIdx+3]); err == nil {
				numValues = valCount
				if numValues > 7 {
					numValues = 7 // Limitamos a 7 para manter a compatibilidade
				}
			}
		}

		fmt.Printf("\nBloco de Velocidade (V3DX1) encontrado. Escala: %f\n", scale)

		// Processa os valores de velocidade (começando após o contador de valores)
		for i := 0; i < numValues && velIdx+i+4 < len(tokens); i++ {
			valHex := tokens[velIdx+i+4]

			// Converte valor hexadecimal para decimal
			decimalValue := smallHexToInt(valHex)

			// Para valores de velocidade, pode ser necessário interpretar como valor com sinal
			if decimalValue > 32767 {
				decimalValue -= 65536
			}

			// Aplica a escala (sem divisão por 1000)
			velMS := float64(decimalValue) * float64(scale)

			if i < 7 { // Garante que não exceda o array
				metrics.Velocities[i] = velMS
			}

			fmt.Printf("  vel%d: HEX=%s -> DEC=%d -> %.3fm/s\n", i+1, valHex, decimalValue, velMS)
		}
	} else {
		fmt.Println("Bloco de Velocidade (V3DX1) não encontrado ou formato inesperado.")
	}

	return metrics, nil
}

// DetectVelocityChanges detecta mudanças nas velocidades
func DetectVelocityChanges(metrics *RadarMetrics, lastVelocities [7]float64) {
	// Limpa o array de mudanças
	metrics.VelocityChanges = []VelocityChange{}

	// Verifica cada velocidade individualmente
	for i := 0; i < 7; i++ {
		// Calcula a diferença
		change := metrics.Velocities[i] - lastVelocities[i]

		// Se a mudança for significativa (maior que o limiar), registra
		if math.Abs(change) >= MinVelocityChange {
			metrics.VelocityChanges = append(metrics.VelocityChanges, VelocityChange{
				Index:       i,
				OldValue:    lastVelocities[i],
				NewValue:    metrics.Velocities[i],
				ChangeValue: change,
				Timestamp:   metrics.Timestamp,
			})

			if DebuggingEnabled {
				fmt.Printf("Mudança detectada na velocidade %d: %.3f -> %.3f (Δ%.3f)\n",
					i+1, lastVelocities[i], metrics.Velocities[i], change)
			}
		}
	}

	// Atualiza as velocidades anteriores para a próxima comparação
	copy(metrics.LastVelocities[:], metrics.Velocities[:])
}

// Close fecha a conexão com o radar
func (r *SickRadar) Close() {
	if r.conn != nil {
		r.conn.Close()
		r.connected = false
		log.Println("Conexão com o radar fechada")
	}
}

// PLCClient gerencia a conexão com o PLC Siemens S7
type PLCClient struct {
	client    gos7.Client
	handler   *gos7.TCPClientHandler
	connected bool
	dbNumber  int
}

// NewPLCClient cria uma nova instância do cliente PLC
func NewPLCClient(ip string, rack, slot, db int) *PLCClient {
	handler := gos7.NewTCPClientHandler(ip, rack, slot)
	handler.Timeout = 10 * time.Second

	return &PLCClient{
		handler:   handler,
		dbNumber:  db,
		connected: false,
	}
}

// Connect estabelece conexão com o PLC
func (p *PLCClient) Connect() error {
	if p.connected {
		return nil
	}

	log.Printf("Tentando conectar ao PLC em %s...", p.handler.Address)
	if err := p.handler.Connect(); err != nil {
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	p.client = gos7.NewClient(p.handler)
	p.connected = true
	log.Printf("Conectado ao PLC em %s", p.handler.Address)
	return nil
}

// WriteRadarData escreve os dados de posição e velocidade no PLC
func (p *PLCClient) WriteRadarData(positions, velocities [7]float64) error {
	if !p.connected {
		if err := p.Connect(); err != nil {
			return err
		}
	}

	// Cria um buffer para os dados do DB
	// Precisamos de espaço para 14 valores REAL (4 bytes cada)
	buffer := make([]byte, PLCAreaSize)

	// Escreve posições no buffer (offsets 0, 4, 8, 12, 16, 20, 24)
	for i := 0; i < 7; i++ {
		offset := i * 4 // Cada REAL tem 4 bytes
		binary.BigEndian.PutUint32(buffer[offset:offset+4], math.Float32bits(float32(positions[i])))
	}

	// Escreve velocidades no buffer (offsets 28, 32, 36, 40, 44, 48, 52)
	for i := 0; i < 7; i++ {
		offset := 28 + (i * 4) // Começando do offset 28
		binary.BigEndian.PutUint32(buffer[offset:offset+4], math.Float32bits(float32(velocities[i])))
	}

	// Escreve o buffer no PLC
	err := p.client.AGWriteDB(p.dbNumber, 0, PLCAreaSize, buffer)
	if err != nil {
		p.connected = false
		return fmt.Errorf("erro ao escrever dados no PLC: %v", err)
	}

	if DebuggingEnabled {
		log.Printf("Dados escritos com sucesso no PLC DB%d", p.dbNumber)
	}

	return nil
}

// Close fecha a conexão com o PLC
func (p *PLCClient) Close() {
	if p.handler != nil {
		p.handler.Close()
		p.connected = false
		log.Println("Conexão com o PLC fechada")
	}
}

// RedisWriter gerencia a conexão com o Redis
type RedisWriter struct {
	client  *redis.Client
	ctx     context.Context
	prefix  string
	enabled bool
}

// NewRedisWriter cria uma nova instância do escritor Redis
func NewRedisWriter(addr, password string, db int, prefix string) *RedisWriter {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	return &RedisWriter{
		client:  client,
		ctx:     ctx,
		prefix:  prefix,
		enabled: true,
	}
}

// TestConnection testa a conexão com o Redis
func (r *RedisWriter) TestConnection() error {
	if !r.enabled {
		return nil
	}

	_, err := r.client.Ping(r.ctx).Result()
	if err != nil {
		return fmt.Errorf("erro ao conectar ao Redis: %v", err)
	}

	log.Println("Conexão com o Redis estabelecida")
	return nil
}

// WriteMetrics escreve métricas no Redis
func (r *RedisWriter) WriteMetrics(metrics *RadarMetrics) error {
	if !r.enabled {
		return nil
	}

	// Cria uma pipeline para enviar vários comandos de uma vez
	pipe := r.client.Pipeline()
	timestamp := metrics.Timestamp.UnixNano() / int64(time.Millisecond)

	// Armazena o status do radar
	pipe.Set(r.ctx, fmt.Sprintf("%s:status", r.prefix), metrics.Status, 0)
	pipe.Set(r.ctx, fmt.Sprintf("%s:timestamp", r.prefix), timestamp, 0)

	// Adiciona posições ao Redis
	for i := 0; i < 7; i++ {
		key := fmt.Sprintf("%s:pos%d", r.prefix, i+1)

		// Armazenando o valor atual
		pipe.Set(r.ctx, key, metrics.Positions[i], 0)

		// Armazenando no histórico com timestamp
		histKey := fmt.Sprintf("%s:history", key)
		pipe.ZAdd(r.ctx, histKey, &redis.Z{
			Score:  float64(timestamp),
			Member: metrics.Positions[i],
		})

		// Limitando o tamanho do histórico (mantém últimos 1000 pontos)
		pipe.ZRemRangeByRank(r.ctx, histKey, 0, -1001)
	}

	// Adiciona velocidades ao Redis
	for i := 0; i < 7; i++ {
		key := fmt.Sprintf("%s:vel%d", r.prefix, i+1)

		// Armazenando o valor atual
		pipe.Set(r.ctx, key, metrics.Velocities[i], 0)

		// Armazenando no histórico com timestamp
		histKey := fmt.Sprintf("%s:history", key)
		pipe.ZAdd(r.ctx, histKey, &redis.Z{
			Score:  float64(timestamp),
			Member: metrics.Velocities[i],
		})

		// Limitando o tamanho do histórico (mantém últimos 1000 pontos)
		pipe.ZRemRangeByRank(r.ctx, histKey, 0, -1001)
	}

	// Executa a pipeline
	_, err := pipe.Exec(r.ctx)
	if err != nil {
		return fmt.Errorf("erro ao escrever métricas no Redis: %v", err)
	}

	return nil
}

// UpdateVelocityStats atualiza estatísticas de velocidade no Redis
func (r *RedisWriter) UpdateVelocityStats(metrics *RadarMetrics) error {
	if !r.enabled {
		return nil
	}

	pipe := r.client.Pipeline()
	timestamp := metrics.Timestamp.UnixNano() / int64(time.Millisecond)

	// Atualiza contador global de mudanças
	if len(metrics.VelocityChanges) > 0 {
		totalChangesKey := fmt.Sprintf("%s:total_changes", r.prefix)
		pipe.IncrBy(r.ctx, totalChangesKey, int64(len(metrics.VelocityChanges)))
	}

	// Para cada velocidade, atualiza estatísticas
	for i := 0; i < 7; i++ {
		velocity := metrics.Velocities[i]

		// Chaves para estatísticas
		maxKey := fmt.Sprintf("%s:vel%d:max", r.prefix, i+1)
		minKey := fmt.Sprintf("%s:vel%d:min", r.prefix, i+1)

		// Verificar se já existem valores máximos e mínimos
		existsMax := r.client.Exists(r.ctx, maxKey).Val() > 0
		existsMin := r.client.Exists(r.ctx, minKey).Val() > 0

		// Se não existirem, crie-os com os valores atuais
		if !existsMax {
			pipe.Set(r.ctx, maxKey, velocity, 0)
		} else {
			// Se existir, obtenha o valor e compare
			currentMax, err := r.client.Get(r.ctx, maxKey).Float64()
			if err == nil && velocity > currentMax {
				pipe.Set(r.ctx, maxKey, velocity, 0)
			}
		}

		if !existsMin {
			pipe.Set(r.ctx, minKey, velocity, 0)
		} else {
			// Se existir, obtenha o valor e compare
			currentMin, err := r.client.Get(r.ctx, minKey).Float64()
			if err == nil && velocity < currentMin {
				pipe.Set(r.ctx, minKey, velocity, 0)
			}
		}

		// Adiciona ao histórico para cálculo da média
		histKey := fmt.Sprintf("%s:vel%d:history_stats", r.prefix, i+1)
		pipe.ZAdd(r.ctx, histKey, &redis.Z{
			Score:  float64(timestamp),
			Member: fmt.Sprintf("%.6f", velocity), // Garantir formato consistente
		})
		pipe.ZRemRangeByRank(r.ctx, histKey, 0, -101) // Mantém últimos 100 valores

		// Adiciona para o gráfico histórico (com timestamp e valor)
		timeSeriesKey := fmt.Sprintf("%s:vel%d:timeseries", r.prefix, i+1)
		histPoint := VelocityPoint{
			Timestamp: timestamp,
			Value:     velocity,
		}
		histData, _ := json.Marshal(histPoint)
		pipe.ZAdd(r.ctx, timeSeriesKey, &redis.Z{
			Score:  float64(timestamp),
			Member: string(histData),
		})
		pipe.ZRemRangeByRank(r.ctx, timeSeriesKey, 0, -60) // Mantém últimos 60 pontos (1 minuto @ 1Hz)

		// Calcular média dos valores no histórico
		avgKey := fmt.Sprintf("%s:vel%d:avg", r.prefix, i+1)
		// Obter valores do histórico para calcular média
		values, err := r.client.ZRange(r.ctx, histKey, 0, -1).Result()
		if err == nil && len(values) > 0 {
			var sum float64
			var count int

			for _, val := range values {
				if f, err := strconv.ParseFloat(val, 64); err == nil {
					sum += f
					count++
				}
			}

			if count > 0 {
				avg := sum / float64(count)
				pipe.Set(r.ctx, avgKey, avg, 0)
			}
		}
	}

	// Calcula frequência de mudanças (mudanças por minuto)
	changeDateKey := fmt.Sprintf("%s:change_last_minute", r.prefix)
	changeDataCmd := r.client.Get(r.ctx, changeDateKey)

	var changeCount int64 = 0
	var lastChangeTime int64 = 0

	changeData, err := changeDataCmd.Result()
	if err == nil {
		// Temos dados do último minuto
		var lastMinuteChanges map[string]int64
		if err := json.Unmarshal([]byte(changeData), &lastMinuteChanges); err == nil {
			changeCount = lastMinuteChanges["count"]
			lastChangeTime = lastMinuteChanges["timestamp"]
		}
	} else if err != redis.Nil {
		// Um erro diferente de redis.Nil ocorreu
		log.Printf("Erro ao ler dados de mudança: %v", err)
	}

	// Atualiza contagem e horário
	currentTime := time.Now().UnixNano() / int64(time.Millisecond)
	if currentTime-lastChangeTime > 60000 { // Mais de 1 minuto
		// Reinicia a contagem
		changeCount = int64(len(metrics.VelocityChanges))
	} else {
		// Adiciona à contagem existente
		changeCount += int64(len(metrics.VelocityChanges))
	}

	changeFrequencyData := map[string]int64{
		"count":     changeCount,
		"timestamp": currentTime,
	}

	jsonData, err := json.Marshal(changeFrequencyData)
	if err == nil {
		pipe.Set(r.ctx, changeDateKey, string(jsonData), 0)

		// Armazena a frequência de mudanças por minuto
		if currentTime-lastChangeTime > 0 {
			changeFreq := float64(changeCount) * (60000.0 / float64(currentTime-lastChangeTime))
			pipe.Set(r.ctx, fmt.Sprintf("%s:change_frequency", r.prefix), changeFreq, 0)
		}
	}

	// Executa pipeline final
	_, err = pipe.Exec(r.ctx)
	// Ignora erro redis.Nil para não interromper o fluxo
	if err != nil && err != redis.Nil {
		return fmt.Errorf("erro ao atualizar estatísticas no Redis: %v", err)
	}

	return nil
}

// CalculateHistoryStats calcula estatísticas a partir do Redis
func (r *RedisWriter) CalculateHistoryStats() (*HistoryStats, error) {
	if !r.enabled {
		return nil, nil
	}

	stats := &HistoryStats{
		LastUpdated:     time.Now(),
		VelocityHistory: make(map[int][]VelocityPoint),
	}

	// Obtém o contador total de mudanças
	totalChangesKey := fmt.Sprintf("%s:total_changes", r.prefix)
	totalChangesCmd := r.client.Get(r.ctx, totalChangesKey)
	totalChanges, err := totalChangesCmd.Int()
	if err == nil {
		stats.TotalChanges = totalChanges
	} else if err != redis.Nil {
		log.Printf("Erro ao obter total de mudanças: %v", err)
	}

	// Inicia coleta de dados para cada sensor
	for i := 0; i < 7; i++ {
		// Chaves para estatísticas
		maxKey := fmt.Sprintf("%s:vel%d:max", r.prefix, i+1)
		minKey := fmt.Sprintf("%s:vel%d:min", r.prefix, i+1)
		avgKey := fmt.Sprintf("%s:vel%d:avg", r.prefix, i+1)
		timeSeriesKey := fmt.Sprintf("%s:vel%d:timeseries", r.prefix, i+1)

		// Obter valores
		maxValCmd := r.client.Get(r.ctx, maxKey)
		minValCmd := r.client.Get(r.ctx, minKey)
		avgValCmd := r.client.Get(r.ctx, avgKey)
		timeSeriesCmd := r.client.ZRange(r.ctx, timeSeriesKey, 0, -1)

		// Processar valores um a um para lidar com valores nulos
		maxVal, err := maxValCmd.Float64()
		if err == nil {
			if maxVal > stats.MaxVelocity {
				stats.MaxVelocity = maxVal
			}
		} else if err != redis.Nil {
			log.Printf("Erro ao obter valor máximo para sensor %d: %v", i+1, err)
		}

		minVal, err := minValCmd.Float64()
		if err == nil {
			if stats.MinVelocity == 0 || minVal < stats.MinVelocity {
				stats.MinVelocity = minVal
			}
		} else if err != redis.Nil {
			log.Printf("Erro ao obter valor mínimo para sensor %d: %v", i+1, err)
		}

		avgVal, err := avgValCmd.Float64()
		if err == nil {
			// Acumular médias para cálculo de média global
			stats.AvgVelocity += avgVal
		} else if err != redis.Nil {
			log.Printf("Erro ao obter valor médio para sensor %d: %v", i+1, err)
		}

		// Processar série temporal
		timeSeriesValues, err := timeSeriesCmd.Result()
		if err == nil {
			var velocityPoints []VelocityPoint

			for _, jsonData := range timeSeriesValues {
				var point VelocityPoint
				if err := json.Unmarshal([]byte(jsonData), &point); err == nil {
					velocityPoints = append(velocityPoints, point)
				}
			}

			// Adiciona à estrutura de histórico
			if len(velocityPoints) > 0 {
				stats.VelocityHistory[i] = velocityPoints
			}
		} else if err != redis.Nil {
			log.Printf("Erro ao obter série temporal para sensor %d: %v", i+1, err)
		}
	}

	// Calcular média real das médias obtidas
	stats.AvgVelocity = stats.AvgVelocity / 7

	// Obter frequência de mudanças
	changeFreqKey := fmt.Sprintf("%s:change_frequency", r.prefix)
	changeFreqCmd := r.client.Get(r.ctx, changeFreqKey)
	changeFreq, err := changeFreqCmd.Float64()
	if err == nil {
		stats.ChangeFrequency = changeFreq
	} else if err != redis.Nil {
		log.Printf("Erro ao obter frequência de mudanças: %v", err)
	}

	return stats, nil
}

// WriteVelocityChanges escreve as mudanças de velocidade no Redis
func (r *RedisWriter) WriteVelocityChanges(changes []VelocityChange) error {
	if !r.enabled || len(changes) == 0 {
		return nil
	}

	pipe := r.client.Pipeline()

	for _, change := range changes {
		// Criar estrutura para armazenar detalhes da mudança
		changeData := map[string]interface{}{
			"index":        change.Index,
			"old_value":    change.OldValue,
			"new_value":    change.NewValue,
			"change_value": change.ChangeValue,
			"timestamp":    change.Timestamp.UnixNano() / int64(time.Millisecond),
		}

		// Converter para JSON
		jsonData, err := json.Marshal(changeData)
		if err != nil {
			continue
		}

		// Chave única para esta mudança
		changeKey := fmt.Sprintf("%s:velocity_change:%d:%d",
			r.prefix,
			change.Index+1,
			change.Timestamp.UnixNano()/int64(time.Millisecond))

		// Armazena os detalhes da mudança
		pipe.Set(r.ctx, changeKey, string(jsonData), 0)

		// Adiciona à lista de mudanças recentes para cada velocidade
		velocityChangesKey := fmt.Sprintf("%s:vel%d:changes", r.prefix, change.Index+1)
		pipe.ZAdd(r.ctx, velocityChangesKey, &redis.Z{
			Score:  float64(change.Timestamp.UnixNano() / int64(time.Millisecond)),
			Member: changeKey,
		})

		// Limita o tamanho da lista de mudanças
		pipe.ZRemRangeByRank(r.ctx, velocityChangesKey, 0, -MaxVelocityHistorySize-1)

		// Adiciona à lista global de mudanças de velocidade
		allChangesKey := fmt.Sprintf("%s:velocity_changes", r.prefix)
		pipe.ZAdd(r.ctx, allChangesKey, &redis.Z{
			Score:  float64(change.Timestamp.UnixNano() / int64(time.Millisecond)),
			Member: changeKey,
		})

		// Limita o tamanho da lista global
		pipe.ZRemRangeByRank(r.ctx, allChangesKey, 0, -MaxVelocityHistorySize-1)

		// Atualiza o contador de mudanças para esta velocidade
		counterKey := fmt.Sprintf("%s:vel%d:change_count", r.prefix, change.Index+1)
		pipe.Incr(r.ctx, counterKey)
	}

	// Adiciona a última atualização global para o React Native
	latestDataKey := fmt.Sprintf("%s:latest_update", r.prefix)
	latestData := map[string]interface{}{
		"timestamp": time.Now().UnixNano() / int64(time.Millisecond),
		"changes":   changes,
	}
	jsonData, _ := json.Marshal(latestData)
	pipe.Set(r.ctx, latestDataKey, string(jsonData), 0)

	// Executa a pipeline
	_, err := pipe.Exec(r.ctx)
	if err != nil && err != redis.Nil {
		return fmt.Errorf("erro ao escrever mudanças de velocidade no Redis: %v", err)
	}

	if DebuggingEnabled && len(changes) > 0 {
		fmt.Printf("Registradas %d mudanças de velocidade no Redis\n", len(changes))
	}

	return nil
}

// Close fecha a conexão com o Redis
func (r *RedisWriter) Close() {
	if r.client != nil {
		if err := r.client.Close(); err != nil {
			log.Printf("Erro ao fechar conexão com Redis: %v", err)
		} else {
			log.Println("Conexão com o Redis fechada")
		}
	}
}

// WSMessage representa uma mensagem que será enviada via WebSocket
type WSMessage struct {
	Type         string           `json:"type"`
	Timestamp    int64            `json:"timestamp"`
	Positions    []float64        `json:"positions"`
	Velocities   []float64        `json:"velocities"`
	Changes      []VelocityChange `json:"changes,omitempty"`
	HistoryStats *HistoryStats    `json:"history_stats,omitempty"`
}

// WebSocketServer gerencia as conexões WebSocket
type WebSocketServer struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan WSMessage
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mutex      sync.Mutex
	upgrader   websocket.Upgrader
}

// NewWebSocketServer cria um novo servidor WebSocket
func NewWebSocketServer() *WebSocketServer {
	return &WebSocketServer{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan WSMessage),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Permite qualquer origem para facilitar o desenvolvimento
			},
		},
	}
}

// Start inicia o servidor WebSocket
func (server *WebSocketServer) Start() {
	// Configura o handler HTTP para o endpoint WebSocket
	http.HandleFunc(WebSocketPath, func(w http.ResponseWriter, r *http.Request) {
		conn, err := server.upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("Erro ao estabelecer conexão WebSocket: %v", err)
			return
		}

		// Registra o novo cliente
		server.register <- conn

		// Configura um manipulador para quando o cliente se desconectar
		defer func() {
			server.unregister <- conn
			conn.Close()
		}()

		// Loop de leitura (mantém a conexão viva)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	})

	// Configura o servidor HTTP
	addr := fmt.Sprintf(":%d", WebSocketPort)
	httpServer := &http.Server{
		Addr: addr,
	}

	// Inicia o servidor HTTP em uma goroutine
	go func() {
		log.Printf("Servidor WebSocket iniciado na porta %d (endpoint: %s)", WebSocketPort, WebSocketPath)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Erro ao iniciar servidor WebSocket: %v", err)
		}
	}()

	// Inicia o loop de processamento em uma goroutine
	go server.run()
}

// run processa registros, cancelamentos e broadcasts
func (server *WebSocketServer) run() {
	for {
		select {
		case client := <-server.register:
			// Registra um novo cliente
			server.mutex.Lock()
			server.clients[client] = true
			server.mutex.Unlock()
			log.Printf("Novo cliente WebSocket conectado. Total: %d", len(server.clients))

		case client := <-server.unregister:
			// Remove um cliente
			server.mutex.Lock()
			if _, ok := server.clients[client]; ok {
				delete(server.clients, client)
			}
			server.mutex.Unlock()
			log.Printf("Cliente WebSocket desconectado. Restantes: %d", len(server.clients))

		case message := <-server.broadcast:
			// Envia uma mensagem para todos os clientes
			server.mutex.Lock()
			clientCount := len(server.clients)
			server.mutex.Unlock()

			if clientCount == 0 {
				continue
			}

			jsonMessage, err := json.Marshal(message)
			if err != nil {
				log.Printf("Erro ao serializar mensagem WebSocket: %v", err)
				continue
			}

			server.mutex.Lock()
			for client := range server.clients {
				err := client.WriteMessage(websocket.TextMessage, jsonMessage)
				if err != nil {
					log.Printf("Erro ao enviar mensagem para cliente: %v", err)
					client.Close()
					delete(server.clients, client)
				}
			}
			server.mutex.Unlock()
		}
	}
}

// BroadcastMetrics envia métricas para todos os clientes conectados
func (server *WebSocketServer) BroadcastMetrics(metrics *RadarMetrics, redis *RedisWriter) {
	if len(server.clients) == 0 {
		return
	}

	// Obtenha as estatísticas do Redis se estiver habilitado
	var historyStats *HistoryStats
	if redis != nil && redis.enabled {
		stats, err := redis.CalculateHistoryStats()
		if err != nil {
			log.Printf("Erro ao calcular estatísticas do Redis: %v", err)
		} else {
			historyStats = stats
		}
	}

	// Cria uma mensagem com as métricas atuais
	message := WSMessage{
		Type:         "metrics",
		Timestamp:    metrics.Timestamp.UnixNano() / int64(time.Millisecond),
		Positions:    metrics.Positions[:],
		Velocities:   metrics.Velocities[:],
		HistoryStats: historyStats,
	}

	// Se houver mudanças, inclui na mensagem
	if len(metrics.VelocityChanges) > 0 {
		message.Changes = metrics.VelocityChanges
	}

	// Envia a mensagem para o canal de broadcast
	server.broadcast <- message
}

func main() {
	// Configuração de log
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("=== Iniciando Coleta do Radar SICK e Comunicação com PLC ===")

	// Configura o radar com a porta apropriada para o protocolo
	radar := NewSickRadar(RadarHost, RadarPort, ProtocolType)

	// Configura o Redis
	redis := NewRedisWriter(RedisAddr, RedisPassword, RedisDB, RedisPrefix)

	// Configura o cliente PLC
	plc := NewPLCClient(PLCHost, PLCRack, PLCSlot, PLCDB)

	// Configura o servidor WebSocket
	wsServer := NewWebSocketServer()
	wsServer.Start()

	// Garante que as conexões sejam fechadas ao final
	defer radar.Close()
	defer redis.Close()
	defer plc.Close()

	// Testa conexão com Redis
	if err := redis.TestConnection(); err != nil {
		log.Printf("Aviso: %v. O Redis será desabilitado.", err)
		redis.enabled = false
	}

	// Testa conexão com PLC
	if err := plc.Connect(); err != nil {
		log.Printf("Aviso: %v. Tentaremos reconectar durante a execução.", err)
	}

	// Instruções para React Native
	fmt.Println("\n=== Informações para integração com React Native ===")
	fmt.Println("Dados armazenados no Redis que podem ser consultados pelo React Native:")
	fmt.Printf("1. Última atualização: %s:latest_update\n", RedisPrefix)
	fmt.Printf("2. Valores atuais: %s:vel1, %s:vel2, ...\n", RedisPrefix, RedisPrefix)
	fmt.Printf("3. Mudanças recentes: %s:velocity_changes (últimas %d mudanças)\n", RedisPrefix, MaxVelocityHistorySize)
	fmt.Printf("4. Mudanças por velocidade: %s:vel1:changes, %s:vel2:changes, ...\n", RedisPrefix, RedisPrefix)
	fmt.Printf("5. Contador de mudanças: %s:vel1:change_count, %s:vel2:change_count, ...\n", RedisPrefix, RedisPrefix)
	fmt.Println("=============================================")

	// Informações do PLC
	fmt.Println("\n=== Informações da comunicação com PLC ===")
	fmt.Printf("Conectado ao PLC: %s (DB%d)\n", PLCHost, PLCDB)
	fmt.Println("Offsets na DB6:")
	fmt.Println("- Posições: 0, 4, 8, 12, 16, 20, 24")
	fmt.Println("- Velocidades: 28, 32, 36, 40, 44, 48, 52")
	fmt.Println("=============================================")

	// Informações do WebSocket
	fmt.Println("\n=== Informações do servidor WebSocket ===")
	fmt.Printf("Servidor WebSocket iniciado: ws://localhost:%d%s\n", WebSocketPort, WebSocketPath)
	fmt.Println("Formato da mensagem JSON com dados do Redis:")
	fmt.Println("  {")
	fmt.Println("    \"type\": \"metrics\",")
	fmt.Println("    \"timestamp\": 1650000000000,")
	fmt.Println("    \"positions\": [valor1, valor2, ...],")
	fmt.Println("    \"velocities\": [valor1, valor2, ...],")
	fmt.Println("    \"changes\": [{ ... }] (opcional),")
	fmt.Println("    \"history_stats\": {")
	fmt.Println("      \"total_changes\": 100,")
	fmt.Println("      \"max_velocity\": 12.5,")
	fmt.Println("      \"min_velocity\": 0.1,")
	fmt.Println("      \"avg_velocity\": 5.3,")
	fmt.Println("      \"change_frequency\": 2.1,")
	fmt.Println("      \"velocity_history\": { ... }")
	fmt.Println("    }")
	fmt.Println("  }")
	fmt.Println("=============================================")

	log.Printf("Iniciando coleta de dados do radar usando protocolo %s. Taxa de amostragem: %v",
		ProtocolType, SampleRate)
	log.Println("Pressione Ctrl+C para interromper.")

	// Configura canal para capturar sinais de interrupção
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Configura ticker para coletar dados periodicamente
	ticker := time.NewTicker(SampleRate)
	defer ticker.Stop()

	// Variáveis para controle de estado
	var consecutiveErrors int = 0
	var lastErrorMsg string
	var radarStatus string = "ok"
	var lastVelocities [7]float64
	var plcErrors int = 0

	// Loop principal
	for {
		select {
		case <-sigChan:
			log.Println("\nProcesso interrompido pelo usuário.")
			return
		case <-ticker.C:
			// Envia comando para o radar
			response, err := radar.SendCommand("sRN LMDradardata")
			if err != nil {
				consecutiveErrors++
				lastErrorMsg = err.Error()

				log.Printf("Erro ao enviar comando: %v. Tentando reconectar... (Tentativa %d)",
					err, consecutiveErrors)
				radar.connected = false

				if consecutiveErrors > MaxConsecutiveErrors {
					radarStatus = "falha_comunicacao"
					log.Printf("ALERTA: Múltiplas falhas de comunicação com o radar. "+
						"Verifique a conexão física! Último erro: %s", lastErrorMsg)

					// Notifica o status no Redis se estiver habilitado
					if redis.enabled {
						pipe := redis.client.Pipeline()
						pipe.Set(redis.ctx, redis.prefix+":status", radarStatus, 0)
						pipe.Set(redis.ctx, redis.prefix+":ultimo_erro", lastErrorMsg, 0)
						pipe.Set(redis.ctx, redis.prefix+":erros_consecutivos", consecutiveErrors, 0)
						pipe.Exec(redis.ctx)
					}

					// Pausa mais longa após muitas falhas consecutivas
					time.Sleep(ReconnectDelay)
				}

				continue
			}

			// Resetar contador de erros se comunicação bem sucedida
			if consecutiveErrors > 0 {
				log.Printf("Comunicação com o radar restaurada após %d tentativas", consecutiveErrors)
				consecutiveErrors = 0
				radarStatus = "ok"

				// Atualiza status no Redis
				if redis.enabled {
					redis.client.Set(redis.ctx, redis.prefix+":status", radarStatus, 0)
				}
			}

			// Decodifica a resposta
			metrics, err := radar.DecodeValues(response)
			if err != nil {
				log.Printf("Erro ao decodificar valores: %v", err)
				continue
			}

			if metrics != nil {
				// Verifica se o radar está obstruído (todas posições zero)
				allZero := true
				for _, pos := range metrics.Positions {
					if pos != 0 {
						allZero = false
						break
					}
				}

				if allZero {
					metrics.Status = "obstruido"
					log.Println("ALERTA: Radar possivelmente obstruído - todas as posições são zero!")
				}

				// Detecta mudanças nas velocidades
				DetectVelocityChanges(metrics, lastVelocities)

				// Atualiza para a próxima comparação
				lastVelocities = metrics.Velocities

				// Envia dados para o Redis
				if redis.enabled {
					// Sempre envia métricas completas
					if err := redis.WriteMetrics(metrics); err != nil {
						log.Printf("Erro ao escrever métricas no Redis: %v", err)
					}

					// Se houver mudanças de velocidade, registra separadamente
					if len(metrics.VelocityChanges) > 0 {
						if err := redis.WriteVelocityChanges(metrics.VelocityChanges); err != nil {
							log.Printf("Erro ao escrever mudanças de velocidade no Redis: %v", err)
						}
					}

					// Atualiza estatísticas no Redis
					if err := redis.UpdateVelocityStats(metrics); err != nil {
						log.Printf("Erro ao atualizar estatísticas no Redis: %v", err)
					}
				}

				// Envia dados para o PLC
				if err := plc.WriteRadarData(metrics.Positions, metrics.Velocities); err != nil {
					plcErrors++
					log.Printf("Erro ao escrever dados no PLC (tentativa %d): %v", plcErrors, err)

					if plcErrors > MaxConsecutiveErrors {
						log.Printf("ALERTA: Múltiplas falhas de comunicação com o PLC. Tentaremos reconectar na próxima iteração.")
						plc.connected = false
						plcErrors = 0
					}
				} else {
					if plcErrors > 0 {
						log.Printf("Comunicação com o PLC restaurada após %d tentativas", plcErrors)
						plcErrors = 0
					}
				}

				// Envia dados via WebSocket para clientes conectados
				wsServer.BroadcastMetrics(metrics, redis)
			} else {
				log.Println("Nenhuma métrica válida extraída da resposta")
			}
		}
	}
}
