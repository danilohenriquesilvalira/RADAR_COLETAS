package utils

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Constantes para conversões
const (
	// Limites para complemento de 2 (16-bit)
	MaxInt16  = 32767
	MaxUint16 = 65536

	// Limites de ângulo
	MaxAngleDegrees = 180.0
	AngleDivisor    = 32.0

	// Prefixos hexadecimais suportados
	HexPrefix = "0x"
)

// ConversionError representa erro de conversão
type ConversionError struct {
	Input     string
	Operation string
	Err       error
}

func (e *ConversionError) Error() string {
	return fmt.Sprintf("erro na conversão %s com entrada '%s': %v",
		e.Operation, e.Input, e.Err)
}

// HexConverter fornece métodos robustos de conversão hexadecimal
type HexConverter struct {
	logger    *slog.Logger
	debugMode bool
}

// NewHexConverter cria novo conversor com configurações
func NewHexConverter(debugMode bool, logger *slog.Logger) *HexConverter {
	if logger == nil {
		logger = slog.Default()
	}

	return &HexConverter{
		logger:    logger.With("component", "hex_converter"),
		debugMode: debugMode,
	}
}

// HexToFloat converte string hexadecimal IEEE-754 para float64
func (hc *HexConverter) HexToFloat(hexStr string) (float64, error) {
	// Validar e limpar entrada
	cleanHex, err := hc.validateAndCleanHex(hexStr)
	if err != nil {
		return 0.0, &ConversionError{hexStr, "HexToFloat", err}
	}

	// Converter hex para uint32
	value, err := strconv.ParseUint(cleanHex, 16, 32)
	if err != nil {
		if hc.debugMode {
			hc.logger.Error("Conversão hex para uint32 falhou",
				"input", hexStr, "error", err)
		}
		return 0.0, &ConversionError{hexStr, "HexToFloat", err}
	}

	// Converter para IEEE-754 float
	bits := uint32(value)
	float32Val := math.Float32frombits(bits)
	result := float64(float32Val)

	if hc.debugMode {
		hc.logger.Debug("Conversão HexToFloat",
			"input", hexStr,
			"hex_clean", cleanHex,
			"uint32", value,
			"float64", result,
		)
	}

	return result, nil
}

// HexToInt converte string hexadecimal para inteiro com complemento de 2
func (hc *HexConverter) HexToInt(hexStr string) (int, error) {
	// Validar e limpar entrada
	cleanHex, err := hc.validateAndCleanHex(hexStr)
	if err != nil {
		return 0, &ConversionError{hexStr, "HexToInt", err}
	}

	// Converter para int64
	value, err := strconv.ParseInt(cleanHex, 16, 64)
	if err != nil {
		if hc.debugMode {
			hc.logger.Error("Conversão hex para int falhou",
				"input", hexStr, "error", err)
		}
		return 0, &ConversionError{hexStr, "HexToInt", err}
	}

	// Aplicar complemento de 2 para 16-bit
	result := hc.applyTwoComplement(value)

	if hc.debugMode {
		hc.logger.Debug("Conversão HexToInt",
			"input", hexStr,
			"hex_clean", cleanHex,
			"raw_value", value,
			"complement_result", result,
		)
	}

	return result, nil
}

// validateAndCleanHex valida e limpa string hexadecimal
func (hc *HexConverter) validateAndCleanHex(hexStr string) (string, error) {
	if hexStr == "" {
		return "", fmt.Errorf("string hexadecimal vazia")
	}

	// Remover prefixo e normalizar
	cleaned := strings.TrimSpace(strings.ToLower(hexStr))
	cleaned = strings.TrimPrefix(cleaned, HexPrefix)

	// Validar caracteres hexadecimais
	for _, char := range cleaned {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return "", fmt.Errorf("caractere inválido '%c' em string hex", char)
		}
	}

	return cleaned, nil
}

// applyTwoComplement aplica complemento de 2 para valores 16-bit
func (hc *HexConverter) applyTwoComplement(value int64) int {
	if value > MaxInt16 {
		return int(value - MaxUint16)
	}
	return int(value)
}

// AngleDecoder decodifica dados de ângulo do radar
type AngleDecoder struct {
	converter *HexConverter
	logger    *slog.Logger
}

// NewAngleDecoder cria novo decodificador de ângulos
func NewAngleDecoder(debugMode bool, logger *slog.Logger) *AngleDecoder {
	if logger == nil {
		logger = slog.Default()
	}

	return &AngleDecoder{
		converter: NewHexConverter(debugMode, logger),
		logger:    logger.With("component", "angle_decoder"),
	}
}

// DecodeAngleData interpreta dados de ângulo com validação robusta
func (ad *AngleDecoder) DecodeAngleData(hexValue string, scale float64) (float64, error) {
	// Validar parâmetros
	if err := ad.validateAngleInputs(hexValue, scale); err != nil {
		return 0.0, err
	}

	// Converter valor hexadecimal
	decimalValue, err := ad.converter.HexToInt(hexValue)
	if err != nil {
		return 0.0, fmt.Errorf("erro decodificando ângulo: %w", err)
	}

	// Aplicar escala
	angleValue := float64(decimalValue) * scale

	// Aplicar correção de codificação se necessário
	correctedAngle := ad.applyCodingCorrection(angleValue)

	// Validar resultado
	if err := ad.validateAngleResult(correctedAngle); err != nil {
		ad.logger.Warn("Ângulo fora dos limites esperados",
			"input", hexValue,
			"result", correctedAngle,
			"error", err,
		)
	}

	return correctedAngle, nil
}

// validateAngleInputs valida parâmetros de entrada
func (ad *AngleDecoder) validateAngleInputs(hexValue string, scale float64) error {
	if hexValue == "" {
		return fmt.Errorf("valor hexadecimal vazio")
	}

	if scale == 0 {
		return fmt.Errorf("escala não pode ser zero")
	}

	if math.IsNaN(scale) || math.IsInf(scale, 0) {
		return fmt.Errorf("escala inválida: %f", scale)
	}

	return nil
}

// applyCodingCorrection aplica correção de codificação para ângulos
func (ad *AngleDecoder) applyCodingCorrection(angle float64) float64 {
	// Correção específica: alguns modelos usam 1/32 graus
	if math.Abs(angle) > MaxAngleDegrees {
		return angle / AngleDivisor
	}
	return angle
}

// validateAngleResult valida resultado final do ângulo
func (ad *AngleDecoder) validateAngleResult(angle float64) error {
	if math.IsNaN(angle) {
		return fmt.Errorf("resultado é NaN")
	}

	if math.IsInf(angle, 0) {
		return fmt.Errorf("resultado é infinito")
	}

	// Aviso para ângulos muito grandes (mas não erro)
	if math.Abs(angle) > 360 {
		return fmt.Errorf("ângulo muito grande: %.2f graus", angle)
	}

	return nil
}

// TerminalCleaner gerencia limpeza de terminal
type TerminalCleaner struct {
	logger *slog.Logger
}

// NewTerminalCleaner cria novo limpador de terminal
func NewTerminalCleaner(logger *slog.Logger) *TerminalCleaner {
	if logger == nil {
		logger = slog.Default()
	}

	return &TerminalCleaner{
		logger: logger.With("component", "terminal_cleaner"),
	}
}

// Clear limpa terminal de forma robusta
func (tc *TerminalCleaner) Clear() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "cls")
	case "linux", "darwin":
		cmd = exec.Command("clear")
	default:
		// Fallback usando escape sequences ANSI
		fmt.Print("\033[H\033[2J")
		return nil
	}

	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		tc.logger.Warn("Falha limpando terminal via comando",
			"os", runtime.GOOS, "error", err)

		// Fallback para escape sequences
		fmt.Print("\033[H\033[2J")
		return err
	}

	return nil
}

// === FUNÇÕES DE COMPATIBILIDADE (mantêm interface original) ===

// HexToFloat função global para compatibilidade
func HexToFloat(hexStr string, debugMode bool) float64 {
	converter := NewHexConverter(debugMode, nil)
	result, err := converter.HexToFloat(hexStr)
	if err != nil {
		if debugMode {
			fmt.Printf("Erro ao converter %s para float: %v\n", hexStr, err)
		}
		return 0.0
	}
	return result
}

// HexToInt função global para compatibilidade
func HexToInt(hexStr string, debugMode bool) int {
	converter := NewHexConverter(debugMode, nil)
	result, err := converter.HexToInt(hexStr)
	if err != nil {
		if debugMode {
			fmt.Printf("Erro ao converter %s para int: %v\n", hexStr, err)
		}
		return 0
	}
	return result
}

// DecodeAngleData função global para compatibilidade
func DecodeAngleData(hexValue string, scale float64, debugMode bool) float64 {
	decoder := NewAngleDecoder(debugMode, nil)
	result, err := decoder.DecodeAngleData(hexValue, scale)
	if err != nil {
		if debugMode {
			fmt.Printf("Erro decodificando ângulo %s: %v\n", hexValue, err)
		}
		return 0.0
	}
	return result
}

// LimparTela função global para compatibilidade
func LimparTela() {
	cleaner := NewTerminalCleaner(nil)
	cleaner.Clear()
}

// === UTILITÁRIOS ADICIONAIS ===

// ConversionUtils agrupa operações de conversão
type ConversionUtils struct {
	hexConverter *HexConverter
	angleDecoder *AngleDecoder
	termCleaner  *TerminalCleaner
	logger       *slog.Logger
}

// NewConversionUtils cria utilitários completos
func NewConversionUtils(debugMode bool, logger *slog.Logger) *ConversionUtils {
	if logger == nil {
		logger = slog.Default()
	}

	return &ConversionUtils{
		hexConverter: NewHexConverter(debugMode, logger),
		angleDecoder: NewAngleDecoder(debugMode, logger),
		termCleaner:  NewTerminalCleaner(logger),
		logger:       logger.With("component", "conversion_utils"),
	}
}

// ProcessHexArray processa array de valores hex
func (cu *ConversionUtils) ProcessHexArray(hexArray []string, isFloat bool) ([]float64, error) {
	if len(hexArray) == 0 {
		return []float64{}, nil
	}

	results := make([]float64, 0, len(hexArray))
	var errors []string

	for i, hexStr := range hexArray {
		var value float64
		var err error

		if isFloat {
			value, err = cu.hexConverter.HexToFloat(hexStr)
		} else {
			intVal, intErr := cu.hexConverter.HexToInt(hexStr)
			value = float64(intVal)
			err = intErr
		}

		if err != nil {
			errors = append(errors, fmt.Sprintf("índice %d: %v", i, err))
			continue
		}

		results = append(results, value)
	}

	if len(errors) > 0 {
		cu.logger.Warn("Erros na conversão de array",
			"total_errors", len(errors),
			"total_items", len(hexArray),
		)
	}

	return results, nil
}

// ValidateFloatResult valida resultado de conversão float
func (cu *ConversionUtils) ValidateFloatResult(value float64, context string) error {
	if math.IsNaN(value) {
		return fmt.Errorf("%s: resultado é NaN", context)
	}

	if math.IsInf(value, 0) {
		return fmt.Errorf("%s: resultado é infinito", context)
	}

	return nil
}

// === FUNÇÕES DE COMPATIBILIDADE APRIMORADAS ===

// SafeHexToFloat conversão segura com validação
func SafeHexToFloat(hexStr string, debugMode bool) (float64, error) {
	converter := NewHexConverter(debugMode, nil)
	return converter.HexToFloat(hexStr)
}

// SafeHexToInt conversão segura com validação
func SafeHexToInt(hexStr string, debugMode bool) (int, error) {
	converter := NewHexConverter(debugMode, nil)
	return converter.HexToInt(hexStr)
}

// SafeDecodeAngleData decodificação segura de ângulo
func SafeDecodeAngleData(hexValue string, scale float64, debugMode bool) (float64, error) {
	decoder := NewAngleDecoder(debugMode, nil)
	return decoder.DecodeAngleData(hexValue, scale)
}

// BatchHexConversion converte múltiplos valores em uma operação
type BatchConversionResult struct {
	Floats  []float64
	Ints    []int
	Angles  []float64
	Errors  []error
	Success int
	Failed  int
}

// ConvertHexBatch processa lote de conversões
func ConvertHexBatch(hexValues []string, scales []float64, debugMode bool) *BatchConversionResult {
	converter := NewHexConverter(debugMode, nil)
	decoder := NewAngleDecoder(debugMode, nil)

	result := &BatchConversionResult{
		Floats: make([]float64, len(hexValues)),
		Ints:   make([]int, len(hexValues)),
		Angles: make([]float64, len(hexValues)),
		Errors: make([]error, len(hexValues)),
	}

	for i, hexStr := range hexValues {
		// Conversão para float
		if floatVal, err := converter.HexToFloat(hexStr); err == nil {
			result.Floats[i] = floatVal
			result.Success++
		} else {
			result.Errors[i] = err
			result.Failed++
		}

		// Conversão para int
		if intVal, err := converter.HexToInt(hexStr); err == nil {
			result.Ints[i] = intVal
		}

		// Conversão para ângulo (se escala fornecida)
		if i < len(scales) && scales[i] != 0 {
			if angleVal, err := decoder.DecodeAngleData(hexStr, scales[i]); err == nil {
				result.Angles[i] = angleVal
			}
		}
	}

	return result
}

// === UTILITÁRIOS DE SISTEMA ===

// SystemUtils utilitários de sistema
type SystemUtils struct {
	logger *slog.Logger
}

// NewSystemUtils cria utilitários de sistema
func NewSystemUtils(logger *slog.Logger) *SystemUtils {
	if logger == nil {
		logger = slog.Default()
	}

	return &SystemUtils{
		logger: logger.With("component", "system_utils"),
	}
}

// ClearTerminal limpa terminal com fallback robusto
func (su *SystemUtils) ClearTerminal() error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "cls")
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		cmd = exec.Command("clear")
	default:
		su.logger.Debug("SO não reconhecido, usando ANSI escape", "os", runtime.GOOS)
		fmt.Print("\033[H\033[2J")
		return nil
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		su.logger.Warn("Comando clear falhou, usando ANSI",
			"os", runtime.GOOS, "error", err)
		fmt.Print("\033[H\033[2J")
		return err
	}

	return nil
}

// GetSystemInfo retorna informações do sistema
func (su *SystemUtils) GetSystemInfo() map[string]interface{} {
	return map[string]interface{}{
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"go_version":    runtime.Version(),
		"num_cpu":       runtime.NumCPU(),
		"num_goroutine": runtime.NumGoroutine(),
	}
}
