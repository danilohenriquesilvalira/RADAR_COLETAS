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

// SelecionarObjetoPrincipalEstabilizado seleciona o objeto principal com estabilização
func (r *SICKRadar) SelecionarObjetoPrincipalEstabilizado(positions, velocities, azimuths, amplitudes []float64) *models.ObjPrincipal {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if len(amplitudes) == 0 {
		r.objetoPrincipalInfo = nil
		return nil
	}

	// Encontrar objeto com maior amplitude atual
	maxAmpIndex := 0
	maxAmp := amplitudes[0]
	for i, amp := range amplitudes {
		if amp > maxAmp {
			maxAmp = amp
			maxAmpIndex = i
		}
	}

	// Criar novo objeto candidato
	novoObjeto := &models.ObjPrincipal{
		Amplitude: amplitudes[maxAmpIndex],
	}

	// Atribuir valores se disponíveis
	if maxAmpIndex < len(positions) {
		dist := positions[maxAmpIndex]
		novoObjeto.Distancia = &dist
	}
	if maxAmpIndex < len(velocities) {
		vel := velocities[maxAmpIndex]
		novoObjeto.Velocidade = &vel
	}
	if maxAmpIndex < len(azimuths) {
		ang := azimuths[maxAmpIndex]
		novoObjeto.Angulo = &ang
	}

	agora := time.Now()

	// Se não há objeto principal anterior, aceitar o novo
	if r.objetoPrincipalInfo == nil {
		r.objetoPrincipalInfo = &models.ObjetoPrincipalInfo{
			Objeto:               novoObjeto,
			ContadorEstabilidade: 1,
			UltimaAtualizacao:    agora,
			Indice:               maxAmpIndex,
		}
		return novoObjeto
	}

	objetoAtual := r.objetoPrincipalInfo.Objeto

	// Verificar se é o mesmo objeto (mesmo índice ou amplitudes muito próximas)
	mesmoObjeto := (maxAmpIndex == r.objetoPrincipalInfo.Indice) ||
		(math.Abs(novoObjeto.Amplitude-objetoAtual.Amplitude) < (objetoAtual.Amplitude * 0.05))

	if mesmoObjeto {
		// Mesmo objeto, atualizar dados e incrementar estabilidade
		r.objetoPrincipalInfo.Objeto = novoObjeto
		r.objetoPrincipalInfo.ContadorEstabilidade++
		r.objetoPrincipalInfo.UltimaAtualizacao = agora
		r.objetoPrincipalInfo.Indice = maxAmpIndex
		return novoObjeto
	}

	// Objeto diferente detectado
	// Calcular diferença percentual de amplitude
	diferencaPercentual := ((novoObjeto.Amplitude - objetoAtual.Amplitude) / objetoAtual.Amplitude) * 100

	// Verificar se a diferença é significativa E se o objeto atual já está estável
	deveTrocar := (diferencaPercentual > r.thresholdMudanca) &&
		(r.objetoPrincipalInfo.ContadorEstabilidade >= r.ciclosMinimosEstabilidade)

	// Ou se passou muito tempo sem atualização (objeto pode ter desaparecido)
	tempoSemAtualizacao := agora.Sub(r.objetoPrincipalInfo.UltimaAtualizacao)
	if tempoSemAtualizacao > 2*time.Second {
		deveTrocar = true
	}

	if deveTrocar {
		// Trocar para o novo objeto
		r.objetoPrincipalInfo = &models.ObjetoPrincipalInfo{
			Objeto:               novoObjeto,
			ContadorEstabilidade: 1,
			UltimaAtualizacao:    agora,
			Indice:               maxAmpIndex,
		}
		return novoObjeto
	} else {
		// Manter objeto atual, mas resetar contador se diferença for negativa significativa
		if diferencaPercentual < -r.thresholdMudanca {
			r.objetoPrincipalInfo.ContadorEstabilidade = 1
		}
		return objetoAtual
	}
}

// ProcessData processa dados recebidos do radar
func (r *SICKRadar) ProcessData(data []byte) (positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal) {
	// Converter para string para análise
	dataStr := string(data)

	// Limpar caracteres de controle
	var cleanData strings.Builder
	for _, c := range dataStr {
		if c < 32 || c > 126 {
			cleanData.WriteRune(' ')
		} else {
			cleanData.WriteRune(c)
		}
	}

	// Dividir em tokens
	tokens := strings.Fields(cleanData.String())

	// Valores para armazenar os resultados
	positions = []float64{}
	velocities = []float64{}
	azimuths = []float64{}
	amplitudes = []float64{}

	// Extrair todos os blocos disponíveis
	possibleBlocks := []string{"P3DX1", "V3DX1", "DIST1", "VRAD1", "AZMT1", "AMPL1", "ANG1", "DIR1", "ANGLE1"}

	blocksFound := 0

	for _, blockName := range possibleBlocks {
		blockIdx := -1
		for i, token := range tokens {
			if token == blockName {
				blockIdx = i
				blocksFound++
				break
			}
		}

		if blockIdx != -1 && blockIdx+3 < len(tokens) {
			// Extrair escala
			scaleHex := tokens[blockIdx+1]
			var scale float64 = 1.0

			// Tentar converter escala de hexadecimal para float
			scaleFloat := utils.HexToFloat(scaleHex, r.DebugMode)
			if scaleFloat != 0.0 {
				scale = scaleFloat
			}

			// Número de valores
			numValues := 0
			if blockIdx+3 < len(tokens) {
				// Tentar converter como decimal
				if v, err := strconv.Atoi(tokens[blockIdx+3]); err == nil {
					numValues = v
				} else {
					// Tentar ler como hexadecimal
					if v, err := strconv.ParseInt(tokens[blockIdx+3], 16, 32); err == nil {
						numValues = int(v)
					}
				}
			}

			if r.DebugMode {
				fmt.Printf("\nBloco %s encontrado. Escala: %f, Valores: %d\n", blockName, scale, numValues)
			}

			// Processar valores
			values := []float64{}
			for i := 0; i < numValues; i++ {
				if blockIdx+i+4 < len(tokens) {
					valHex := tokens[blockIdx+i+4]

					// Converter conforme o tipo de bloco - EXATAMENTE COMO NO PYTHON
					if blockName == "AZMT1" || blockName == "ANG1" || blockName == "DIR1" || blockName == "ANGLE1" {
						// Ângulos
						finalValue := utils.DecodeAngleData(valHex, scale, r.DebugMode)
						values = append(values, finalValue)
						if r.DebugMode {
							fmt.Printf("  %s_%d: HEX=%s -> %.3f°\n", blockName, i+1, valHex, finalValue)
						}
					} else if blockName == "P3DX1" || blockName == "DIST1" {
						// Posições (metros) - SEGUINDO EXATAMENTE O PYTHON
						decimalValue := utils.HexToInt(valHex, r.DebugMode)
						finalValue := float64(decimalValue) * scale / 1000.0
						values = append(values, finalValue)
						if r.DebugMode {
							fmt.Printf("  %s_%d: HEX=%s -> DEC=%d -> %.3fm\n", blockName, i+1, valHex, decimalValue, finalValue)
						}
					} else {
						// Velocidades e outros
						decimalValue := utils.HexToInt(valHex, r.DebugMode)
						finalValue := float64(decimalValue) * scale
						values = append(values, finalValue)
						if r.DebugMode {
							fmt.Printf("  %s_%d: HEX=%s -> DEC=%d -> %.3f\n", blockName, i+1, valHex, decimalValue, finalValue)
						}
					}
				}
			}

			// Armazenar valores processados
			if blockName == "P3DX1" || blockName == "DIST1" {
				positions = values
			} else if blockName == "V3DX1" || blockName == "VRAD1" {
				velocities = values
			} else if blockName == "AZMT1" || blockName == "ANG1" || blockName == "DIR1" || blockName == "ANGLE1" {
				azimuths = values
			} else if blockName == "AMPL1" {
				amplitudes = values
			}
		}
	}

	// Usar algoritmo de estabilização para selecionar objeto principal
	objPrincipal = r.SelecionarObjetoPrincipalEstabilizado(positions, velocities, azimuths, amplitudes)

	return positions, velocities, azimuths, amplitudes, objPrincipal
}

// DisplayData exibe os dados processados no terminal
func (r *SICKRadar) DisplayData(positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal, plcConnected bool, plcIP string) {
	// Limpar a tela antes de exibir
	utils.LimparTela()

	fmt.Println("==================================================")
	fmt.Println("     DADOS DO RADAR SICK RMS1000 - TEMPO REAL")
	fmt.Println("==================================================")

	// Status do PLC
	fmt.Println("STATUS PLC SIEMENS:")
	fmt.Printf("  Conectado: %v\n", plcConnected)
	fmt.Printf("  Endereço: %s\n", plcIP)

	// RESUMO - TODOS OS OBJETOS
	fmt.Println("\nRESUMO DE TODOS OS OBJETOS DETECTADOS")
	fmt.Println("--------------------------------------------------")

	fmt.Printf("Posições detectadas: %d\n", len(positions))
	for i, pos := range positions {
		fmt.Printf("  Posição %d: %.3fm\n", i+1, pos)
	}

	fmt.Printf("\nVelocidades detectadas: %d\n", len(velocities))
	for i, vel := range velocities {
		fmt.Printf("  Velocidade %d: %.3fm/s\n", i+1, vel)
	}

	fmt.Printf("\nÂngulos detectados: %d\n", len(azimuths))
	for i, ang := range azimuths {
		fmt.Printf("  Ângulo %d: %.3f°\n", i+1, ang)
	}

	fmt.Printf("\nAmplitudes detectadas: %d\n", len(amplitudes))
	for i, amp := range amplitudes {
		fmt.Printf("  Amplitude %d: %.3f\n", i+1, amp)
	}

	// OBJETO PRINCIPAL
	if objPrincipal != nil {
		fmt.Println("\nOBJETO PRINCIPAL (MAIOR AMPLITUDE)")
		fmt.Println("--------------------------------------------------")
		fmt.Printf("  Amplitude: %.3f\n", objPrincipal.Amplitude)
		if objPrincipal.Distancia != nil {
			fmt.Printf("  Distância: %.3fm\n", *objPrincipal.Distancia)
		}
		if objPrincipal.Velocidade != nil {
			fmt.Printf("  Velocidade: %.3fm/s\n", *objPrincipal.Velocidade)
		}
		if objPrincipal.Angulo != nil {
			fmt.Printf("  Ângulo: %.3f°\n", *objPrincipal.Angulo)
		}
	}

	fmt.Println("\n==================================================")
	fmt.Println("Pressione Ctrl+C para parar a monitoração")
}
