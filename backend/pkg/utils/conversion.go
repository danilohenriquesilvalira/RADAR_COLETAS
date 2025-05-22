package utils

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HexToFloat converte string hexadecimal IEEE-754 para float
func HexToFloat(hexStr string, debugMode bool) float64 {
	// Remover prefixo "0x" se existir
	hexStr = strings.TrimPrefix(hexStr, "0x")

	// Converter hex para uint32
	value, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		if debugMode {
			fmt.Printf("Erro ao converter %s para float: %v\n", hexStr, err)
		}
		return 0.0
	}

	// Converter para IEEE-754 float
	bits := uint32(value)
	float := math.Float32frombits(bits)
	return float64(float)
}

// HexToInt converte string hexadecimal para inteiro - SEGUINDO EXATAMENTE O PYTHON
func HexToInt(hexStr string, debugMode bool) int {
	// Remover prefixo "0x" se existir
	hexStr = strings.TrimPrefix(hexStr, "0x")

	// Converter para int
	value, err := strconv.ParseInt(hexStr, 16, 64)
	if err != nil {
		if debugMode {
			fmt.Printf("Erro ao converter %s para int: %v\n", hexStr, err)
		}
		return 0
	}

	// Interpretar como número negativo (complemento de 2)
	if value > 32767 {
		value -= 65536
	}

	return int(value)
}

// DecodeAngleData interpreta dados de ângulo do radar com conversão específica
func DecodeAngleData(hexValue string, scale float64, debugMode bool) float64 {
	// Converter o valor hexadecimal em inteiro decimal
	decimalValue := HexToInt(hexValue, debugMode)

	// Aplicar fator de escala do telegrama
	angleValue := float64(decimalValue) * scale

	// Verificação de codificação possível: alguns modelos usam 1/10 ou 1/32 graus
	if math.Abs(angleValue) > 180 {
		angleValue = angleValue / 32.0
	}

	return angleValue
}

// LimparTela limpa o terminal para melhor visualização
func LimparTela() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = os.Stdout
	cmd.Run()
}
