package radar

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"backend/pkg/models"
	"backend/pkg/utils"
)

// BlockProcessor define como processar cada tipo de bloco
type BlockProcessor struct {
	Name      string
	Converter func(string, float64, int, bool) float64
	Target    *[]float64
}

// ObjectStabilityConfig configuração de estabilização
type ObjectStabilityConfig struct {
	ThresholdPercent   float64
	MinStabilityCycles int
	TimeoutSeconds     float64
	AmplitudeThreshold float64
}

// SelecionarObjetoPrincipalEstabilizado algoritmo otimizado de estabilização
func (r *SICKRadar) SelecionarObjetoPrincipalEstabilizado(positions, velocities, azimuths, amplitudes []float64) *models.ObjPrincipal {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if len(amplitudes) == 0 {
		r.objetoPrincipalInfo = nil
		return nil
	}

	// Encontrar objeto com maior amplitude
	maxAmpIndex := r.findMaxAmplitudeIndex(amplitudes)
	novoObjeto := r.buildObjectFromArrays(maxAmpIndex, positions, velocities, azimuths, amplitudes)

	now := time.Now()

	// Primeira detecção
	if r.objetoPrincipalInfo == nil {
		r.objetoPrincipalInfo = &models.ObjetoPrincipalInfo{
			Objeto:               novoObjeto,
			ContadorEstabilidade: 1,
			UltimaAtualizacao:    now,
			Indice:               maxAmpIndex,
		}
		return novoObjeto
	}

	// Verificar estabilidade do objeto
	return r.evaluateObjectStability(novoObjeto, maxAmpIndex, now)
}

// findMaxAmplitudeIndex encontra índice da maior amplitude
func (r *SICKRadar) findMaxAmplitudeIndex(amplitudes []float64) int {
	maxIndex := 0
	maxValue := amplitudes[0]

	for i, amp := range amplitudes[1:] {
		if amp > maxValue {
			maxValue = amp
			maxIndex = i + 1
		}
	}

	return maxIndex
}

// buildObjectFromArrays constrói objeto a partir dos arrays
func (r *SICKRadar) buildObjectFromArrays(index int, positions, velocities, azimuths, amplitudes []float64) *models.ObjPrincipal {
	obj := &models.ObjPrincipal{
		Amplitude: amplitudes[index],
	}

	// Helper para atribuir ponteiros de forma segura
	assignIfValid := func(target **float64, source []float64, idx int) {
		if idx < len(source) {
			val := source[idx]
			*target = &val
		}
	}

	assignIfValid(&obj.Distancia, positions, index)
	assignIfValid(&obj.Velocidade, velocities, index)
	assignIfValid(&obj.Angulo, azimuths, index)

	return obj
}

// evaluateObjectStability avalia se deve trocar objeto principal
func (r *SICKRadar) evaluateObjectStability(novoObjeto *models.ObjPrincipal, maxAmpIndex int, now time.Time) *models.ObjPrincipal {
	currentObj := r.objetoPrincipalInfo.Objeto

	// Verificar se é mesmo objeto
	isSameObject := (maxAmpIndex == r.objetoPrincipalInfo.Indice) ||
		r.isAmplitudeSimilar(novoObjeto.Amplitude, currentObj.Amplitude)

	if isSameObject {
		return r.updateCurrentObject(novoObjeto, maxAmpIndex, now)
	}

	// Avaliar se deve trocar objeto
	if r.shouldSwitchObject(novoObjeto, currentObj, now) {
		return r.switchToNewObject(novoObjeto, maxAmpIndex, now)
	}

	return r.maintainCurrentObject(novoObjeto, currentObj)
}

// isAmplitudeSimilar verifica se amplitudes são similares
func (r *SICKRadar) isAmplitudeSimilar(newAmp, currentAmp float64) bool {
	return math.Abs(newAmp-currentAmp) < (currentAmp * 0.05)
}

// updateCurrentObject atualiza objeto atual
func (r *SICKRadar) updateCurrentObject(novoObjeto *models.ObjPrincipal, maxAmpIndex int, now time.Time) *models.ObjPrincipal {
	r.objetoPrincipalInfo.Objeto = novoObjeto
	r.objetoPrincipalInfo.ContadorEstabilidade++
	r.objetoPrincipalInfo.UltimaAtualizacao = now
	r.objetoPrincipalInfo.Indice = maxAmpIndex
	return novoObjeto
}

// shouldSwitchObject determina se deve trocar objeto
func (r *SICKRadar) shouldSwitchObject(novoObjeto, currentObj *models.ObjPrincipal, now time.Time) bool {
	// Diferença percentual
	diffPercent := ((novoObjeto.Amplitude - currentObj.Amplitude) / currentObj.Amplitude) * 100

	// Critérios para trocar
	significantDiff := diffPercent > r.thresholdMudanca
	stable := r.objetoPrincipalInfo.ContadorEstabilidade >= r.ciclosMinimosEstabilidade
	timeout := now.Sub(r.objetoPrincipalInfo.UltimaAtualizacao) > 2*time.Second

	return (significantDiff && stable) || timeout
}

// switchToNewObject troca para novo objeto
func (r *SICKRadar) switchToNewObject(novoObjeto *models.ObjPrincipal, maxAmpIndex int, now time.Time) *models.ObjPrincipal {
	r.objetoPrincipalInfo = &models.ObjetoPrincipalInfo{
		Objeto:               novoObjeto,
		ContadorEstabilidade: 1,
		UltimaAtualizacao:    now,
		Indice:               maxAmpIndex,
	}
	return novoObjeto
}

// maintainCurrentObject mantém objeto atual
func (r *SICKRadar) maintainCurrentObject(novoObjeto, currentObj *models.ObjPrincipal) *models.ObjPrincipal {
	// Reset contador se amplitude caiu muito
	diffPercent := ((novoObjeto.Amplitude - currentObj.Amplitude) / currentObj.Amplitude) * 100
	if diffPercent < -r.thresholdMudanca {
		r.objetoPrincipalInfo.ContadorEstabilidade = 1
	}
	return currentObj
}

// ProcessData processamento otimizado dos dados do radar
func (r *SICKRadar) ProcessData(data []byte) (positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal) {
	if len(data) == 0 {
		return []float64{}, []float64{}, []float64{}, []float64{}, nil
	}

	// Limpar e tokenizar dados
	tokens := r.cleanAndTokenizeData(data)
	if len(tokens) == 0 {
		return []float64{}, []float64{}, []float64{}, []float64{}, nil
	}

	// Inicializar arrays de resultado
	results := map[string][]float64{
		"positions":  {},
		"velocities": {},
		"azimuths":   {},
		"amplitudes": {},
	}

	// Processar blocos de dados
	r.processDataBlocks(tokens, results)

	// Usar algoritmo de estabilização
	objPrincipal = r.SelecionarObjetoPrincipalEstabilizado(
		results["positions"],
		results["velocities"],
		results["azimuths"],
		results["amplitudes"],
	)

	return results["positions"], results["velocities"], results["azimuths"], results["amplitudes"], objPrincipal
}

// cleanAndTokenizeData limpa e tokeniza dados de entrada
func (r *SICKRadar) cleanAndTokenizeData(data []byte) []string {
	var cleanData strings.Builder
	cleanData.Grow(len(data)) // Pre-allocate

	for _, c := range string(data) {
		if c >= 32 && c <= 126 {
			cleanData.WriteRune(c)
		} else {
			cleanData.WriteRune(' ')
		}
	}

	return strings.Fields(cleanData.String())
}

// processDataBlocks processa todos os blocos de dados
func (r *SICKRadar) processDataBlocks(tokens []string, results map[string][]float64) {
	// Configuração de processadores de bloco
	blockProcessors := []struct {
		names     []string
		converter func(string, float64, int, bool) float64
		target    string
	}{
		{[]string{"P3DX1", "DIST1"}, r.convertDistance, "positions"},
		{[]string{"V3DX1", "VRAD1"}, r.convertVelocity, "velocities"},
		{[]string{"AZMT1", "ANG1", "DIR1", "ANGLE1"}, r.convertAngle, "azimuths"},
		{[]string{"AMPL1"}, r.convertAmplitude, "amplitudes"},
	}

	// Processar cada tipo de bloco
	for _, processor := range blockProcessors {
		for _, blockName := range processor.names {
			if values := r.processBlock(tokens, blockName, processor.converter); len(values) > 0 {
				results[processor.target] = values
				break // Usar primeiro bloco encontrado de cada tipo
			}
		}
	}
}

// processBlock processa um bloco específico
func (r *SICKRadar) processBlock(tokens []string, blockName string, converter func(string, float64, int, bool) float64) []float64 {
	blockIdx := r.findBlockIndex(tokens, blockName)
	if blockIdx == -1 || blockIdx+3 >= len(tokens) {
		return nil
	}

	// Extrair parâmetros do bloco
	scale := r.extractScale(tokens[blockIdx+1])
	numValues := r.extractNumValues(tokens[blockIdx+3])

	if numValues == 0 {
		return nil
	}

	if r.DebugMode {
		fmt.Printf("Bloco %s: escala=%.3f, valores=%d\n", blockName, scale, numValues)
	}

	// Processar valores
	values := make([]float64, 0, numValues)
	for i := 0; i < numValues; i++ {
		if blockIdx+i+4 < len(tokens) {
			valHex := tokens[blockIdx+i+4]
			finalValue := converter(valHex, scale, i+1, r.DebugMode)
			values = append(values, finalValue)
		}
	}

	return values
}

// findBlockIndex encontra índice do bloco nos tokens
func (r *SICKRadar) findBlockIndex(tokens []string, blockName string) int {
	for i, token := range tokens {
		if token == blockName {
			return i
		}
	}
	return -1
}

// extractScale extrai escala do token hexadecimal
func (r *SICKRadar) extractScale(scaleHex string) float64 {
	scale := utils.HexToFloat(scaleHex, r.DebugMode)
	if scale == 0.0 {
		return 1.0
	}
	return scale
}

// extractNumValues extrai número de valores
func (r *SICKRadar) extractNumValues(valueToken string) int {
	// Tentar decimal primeiro
	if v, err := strconv.Atoi(valueToken); err == nil {
		return v
	}

	// Fallback hexadecimal
	if v, err := strconv.ParseInt(valueToken, 16, 32); err == nil {
		return int(v)
	}

	return 0
}

// ========== CONVERSORES ESPECIALIZADOS ==========

func (r *SICKRadar) convertDistance(valHex string, scale float64, index int, debug bool) float64 {
	decimalValue := utils.HexToInt(valHex, debug)
	finalValue := float64(decimalValue) * scale / 1000.0

	if debug {
		fmt.Printf("  DIST_%d: HEX=%s -> %.3fm\n", index, valHex, finalValue)
	}

	return finalValue
}

func (r *SICKRadar) convertVelocity(valHex string, scale float64, index int, debug bool) float64 {
	decimalValue := utils.HexToInt(valHex, debug)
	finalValue := float64(decimalValue) * scale

	if debug {
		fmt.Printf("  VEL_%d: HEX=%s -> %.3f\n", index, valHex, finalValue)
	}

	return finalValue
}

func (r *SICKRadar) convertAngle(valHex string, scale float64, index int, debug bool) float64 {
	finalValue := utils.DecodeAngleData(valHex, scale, debug)

	if debug {
		fmt.Printf("  ANG_%d: HEX=%s -> %.3f°\n", index, valHex, finalValue)
	}

	return finalValue
}

func (r *SICKRadar) convertAmplitude(valHex string, scale float64, index int, debug bool) float64 {
	decimalValue := utils.HexToInt(valHex, debug)
	finalValue := float64(decimalValue) * scale

	if debug {
		fmt.Printf("  AMP_%d: HEX=%s -> %.3f\n", index, valHex, finalValue)
	}

	return finalValue
}

// DisplayData interface otimizada de exibição
func (r *SICKRadar) DisplayData(positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal, plcConnected bool, plcIP string, wsConnCount int, natsConnected bool) {
	utils.LimparTela()

	r.displayHeader()
	r.displaySystemStatus(plcConnected, plcIP, wsConnCount, natsConnected)
	r.displayObjectsSummary(positions, velocities, azimuths, amplitudes)
	r.displayMainObject(objPrincipal)
	r.displayFooter()
}

// displayHeader exibe cabeçalho
func (r *SICKRadar) displayHeader() {
	fmt.Println("==================================================")
	fmt.Println("     DADOS DO RADAR SICK RMS1000 - TEMPO REAL")
	fmt.Println("==================================================")
}

// displaySystemStatus exibe status dos sistemas
func (r *SICKRadar) displaySystemStatus(plcConnected bool, plcIP string, wsConnCount int, natsConnected bool) {
	statusInfo := []struct {
		section string
		items   []struct{ label, value string }
	}{
		{
			"STATUS PLC SIEMENS:",
			[]struct{ label, value string }{
				{"Conectado", fmt.Sprintf("%v", plcConnected)},
				{"Endereço", plcIP},
			},
		},
		{
			"WEBSOCKET:",
			[]struct{ label, value string }{
				{"Clientes conectados", fmt.Sprintf("%d", wsConnCount)},
			},
		},
		{
			"NATS:",
			[]struct{ label, value string }{
				{"Conectado", fmt.Sprintf("%v", natsConnected)},
			},
		},
	}

	for _, section := range statusInfo {
		fmt.Printf("\n%s\n", section.section)
		for _, item := range section.items {
			fmt.Printf("  %s: %s\n", item.label, item.value)
		}
	}
}

// displayObjectsSummary exibe resumo de todos os objetos
func (r *SICKRadar) displayObjectsSummary(positions, velocities, azimuths, amplitudes []float64) {
	fmt.Println("\nRESUMO DE TODOS OS OBJETOS DETECTADOS")
	fmt.Println("--------------------------------------------------")

	// Configuração de exibição de arrays
	arrayDisplays := []struct {
		name   string
		data   []float64
		unit   string
		format string
	}{
		{"Posições", positions, "m", "%.3f"},
		{"Velocidades", velocities, "m/s", "%.3f"},
		{"Ângulos", azimuths, "°", "%.3f"},
		{"Amplitudes", amplitudes, "", "%.3f"},
	}

	for _, display := range arrayDisplays {
		fmt.Printf("\n%s detectadas: %d\n", display.name, len(display.data))
		for i, value := range display.data {
			fmt.Printf("  %s %d: %s%s\n",
				strings.TrimSuffix(display.name, "s"),
				i+1,
				fmt.Sprintf(display.format, value),
				display.unit,
			)
		}
	}
}

// displayMainObject exibe objeto principal
func (r *SICKRadar) displayMainObject(objPrincipal *models.ObjPrincipal) {
	if objPrincipal == nil {
		return
	}

	fmt.Println("\nOBJETO PRINCIPAL (MAIOR AMPLITUDE)")
	fmt.Println("--------------------------------------------------")
	fmt.Printf("  Amplitude: %.3f\n", objPrincipal.Amplitude)

	// Exibir propriedades opcionais
	optionalProps := []struct {
		name  string
		value *float64
		unit  string
	}{
		{"Distância", objPrincipal.Distancia, "m"},
		{"Velocidade", objPrincipal.Velocidade, "m/s"},
		{"Ângulo", objPrincipal.Angulo, "°"},
	}

	for _, prop := range optionalProps {
		if prop.value != nil {
			fmt.Printf("  %s: %.3f%s\n", prop.name, *prop.value, prop.unit)
		}
	}
}

// displayFooter exibe rodapé
func (r *SICKRadar) displayFooter() {
	fmt.Println("\n==================================================")
	fmt.Println("Pressione Ctrl+C para parar a monitoração")
}

// ========== MÉTODOS AUXILIARES ==========

// GetStabilityConfig retorna configuração atual de estabilização
func (r *SICKRadar) GetStabilityConfig() ObjectStabilityConfig {
	return ObjectStabilityConfig{
		ThresholdPercent:   r.thresholdMudanca,
		MinStabilityCycles: r.ciclosMinimosEstabilidade,
		TimeoutSeconds:     2.0,
		AmplitudeThreshold: 0.05,
	}
}

// SetStabilityConfig atualiza configuração de estabilização
func (r *SICKRadar) SetStabilityConfig(config ObjectStabilityConfig) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.thresholdMudanca = config.ThresholdPercent
	r.ciclosMinimosEstabilidade = config.MinStabilityCycles
}

// GetObjectInfo retorna informações do objeto atual
func (r *SICKRadar) GetObjectInfo() *models.ObjetoPrincipalInfo {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.objetoPrincipalInfo == nil {
		return nil
	}

	// Retornar cópia para evitar race conditions
	info := *r.objetoPrincipalInfo
	return &info
}

// ResetObjectStability reseta estabilização do objeto
func (r *SICKRadar) ResetObjectStability() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.objetoPrincipalInfo = nil
}
