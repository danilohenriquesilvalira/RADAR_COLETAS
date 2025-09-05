package radar

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"backend/pkg/models"
	"backend/pkg/utils"
)

// SICKRadar representa um radar individual com processamento integrado
type SICKRadar struct {
	IP        string
	Port      int
	conn      net.Conn
	Connected bool
	DebugMode bool

	// Estabilização do objeto principal
	objetoPrincipalInfo       *models.ObjetoPrincipalInfo
	thresholdMudanca          float64
	ciclosMinimosEstabilidade int
	mutex                     sync.Mutex

	// Detecção de desconexão
	lastSuccessfulRead   time.Time
	consecutiveErrors    int
	maxConsecutiveErrors int

	// TCP management
	connectionID   string
	createdAt      time.Time
	forceReconnect bool
}

func NewSICKRadar(ip string, port int) *SICKRadar {
	if port == 0 {
		port = 2111
	}

	now := time.Now()
	connectionID := fmt.Sprintf("%s_%d_%d", ip, port, now.Unix())

	return &SICKRadar{
		IP:                        ip,
		Port:                      port,
		Connected:                 false,
		DebugMode:                 false,
		objetoPrincipalInfo:       nil,
		thresholdMudanca:          15.0,
		ciclosMinimosEstabilidade: 3,
		lastSuccessfulRead:        now,
		consecutiveErrors:         0,
		maxConsecutiveErrors:      5,
		connectionID:              connectionID,
		createdAt:                 now,
		forceReconnect:            false,
	}
}

func (r *SICKRadar) Connect() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.forceCloseConnection()
	time.Sleep(100 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 10*time.Second)
	if err != nil {
		r.Connected = false
		return err
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
		tcpConn.SetLinger(0)
	}

	r.conn = conn
	r.Connected = true
	r.lastSuccessfulRead = time.Now()
	r.consecutiveErrors = 0
	r.forceReconnect = false

	return nil
}

func (r *SICKRadar) Disconnect() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.forceCloseConnection()
}

func (r *SICKRadar) forceCloseConnection() {
	if r.conn != nil {
		if tcpConn, ok := r.conn.(*net.TCPConn); ok {
			tcpConn.SetLinger(0)
		}
		r.conn.SetDeadline(time.Now())
		r.conn.Close()
		r.conn = nil
	}
	r.Connected = false
}

func (r *SICKRadar) IsConnected() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if !r.Connected || r.conn == nil || r.forceReconnect {
		return false
	}

	if r.consecutiveErrors >= r.maxConsecutiveErrors {
		r.Connected = false
		return false
	}

	if time.Since(r.lastSuccessfulRead) > 30*time.Second {
		r.Connected = false
		return false
	}

	return true
}

func (r *SICKRadar) StartMeasurement() error {
	_, err := r.SendCommand("sEN LMDradardata 1")
	if err != nil {
		return fmt.Errorf("erro ao iniciar medição: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (r *SICKRadar) SendCommand(command string) ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	telegram := append([]byte{0x02}, []byte(command)...)
	telegram = append(telegram, 0x03)

	err := r.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	_, err = r.conn.Write(telegram)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	buffer := make([]byte, 4096)
	n, err := r.conn.Read(buffer)
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	r.conn.SetDeadline(time.Time{})
	r.markSuccessfulOperation()
	return buffer[:n], nil
}

// ReadAndProcessData lê dados do radar e os processa em uma operação
func (r *SICKRadar) ReadAndProcessData() (positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal, err error) {
	data, err := r.readRawData()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	if len(data) == 0 {
		return []float64{}, []float64{}, []float64{}, []float64{}, nil, nil
	}

	return r.processRadarData(data)
}

func (r *SICKRadar) readRawData() ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	buffer := make([]byte, 8192)
	err := r.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	if err != nil {
		r.markConnectionError(err)
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	n, err := r.conn.Read(buffer)
	r.conn.SetReadDeadline(time.Time{})

	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// CORREÇÃO: Usar mutex para incrementar consecutiveErrors
			r.mutex.Lock()
			r.consecutiveErrors++
			if r.consecutiveErrors >= r.maxConsecutiveErrors {
				r.Connected = false
			}
			r.mutex.Unlock()
			return nil, nil
		}

		r.markConnectionError(err)
		return nil, fmt.Errorf("erro de leitura: %v", err)
	}

	if n > 0 {
		if n > 8192 {
			n = 8192
		}
		r.markSuccessfulOperation()
		return buffer[:n], nil
	}

	return nil, nil
}

// processRadarData processa dados recebidos do radar
func (r *SICKRadar) processRadarData(data []byte) (positions, velocities, azimuths, amplitudes []float64, objPrincipal *models.ObjPrincipal, err error) {
	if len(data) == 0 {
		return []float64{}, []float64{}, []float64{}, []float64{}, nil, nil
	}

	if len(data) > 1024*1024 {
		data = data[:1024*1024]
	}

	dataStr := string(data)

	var cleanData strings.Builder
	for _, c := range dataStr {
		if c < 32 || c > 126 {
			cleanData.WriteRune(' ')
		} else {
			cleanData.WriteRune(c)
		}
	}

	tokens := strings.Fields(cleanData.String())
	if len(tokens) > 10000 {
		tokens = tokens[:10000]
	}

	positions = []float64{}
	velocities = []float64{}
	azimuths = []float64{}
	amplitudes = []float64{}

	possibleBlocks := []string{"P3DX1", "V3DX1", "DIST1", "VRAD1", "AZMT1", "AMPL1", "ANG1", "DIR1", "ANGLE1"}

	for _, blockName := range possibleBlocks {
		blockIdx := -1
		for i, token := range tokens {
			if token == blockName {
				blockIdx = i
				break
			}
		}

		if blockIdx == -1 || blockIdx+3 >= len(tokens) {
			continue
		}

		scaleHex := tokens[blockIdx+1]
		if len(scaleHex) > 20 {
			continue
		}

		var scale float64 = 1.0
		scaleFloat := utils.HexToFloat(scaleHex, r.DebugMode)
		if !math.IsNaN(scaleFloat) && !math.IsInf(scaleFloat, 0) && scaleFloat != 0.0 {
			scale = scaleFloat
		}

		numValues := 0
		if blockIdx+3 < len(tokens) {
			if v, err := strconv.Atoi(tokens[blockIdx+3]); err == nil && v >= 0 && v <= 1000 {
				numValues = v
			} else {
				if v, err := strconv.ParseInt(tokens[blockIdx+3], 16, 32); err == nil && v >= 0 && v <= 1000 {
					numValues = int(v)
				}
			}
		}

		if r.DebugMode && numValues > 0 {
			fmt.Printf("Bloco %s: Escala=%.6f, Valores=%d\n", blockName, scale, numValues)
		}

		values := []float64{}
		for i := 0; i < numValues; i++ {
			if blockIdx+i+4 >= len(tokens) {
				break
			}

			valHex := tokens[blockIdx+i+4]
			if len(valHex) > 20 {
				continue
			}

			var finalValue float64

			if blockName == "AZMT1" || blockName == "ANG1" || blockName == "DIR1" || blockName == "ANGLE1" {
				finalValue = utils.DecodeAngleData(valHex, scale, r.DebugMode)
				if math.IsNaN(finalValue) || math.IsInf(finalValue, 0) {
					continue
				}
				if math.Abs(finalValue) > 360 {
					finalValue = math.Mod(finalValue, 360)
				}
			} else if blockName == "P3DX1" || blockName == "DIST1" {
				decimalValue := utils.HexToInt(valHex, r.DebugMode)
				finalValue = float64(decimalValue) * scale / 1000.0
				if math.IsNaN(finalValue) || math.IsInf(finalValue, 0) || finalValue < 0 || finalValue > 1000 {
					continue
				}
			} else {
				decimalValue := utils.HexToInt(valHex, r.DebugMode)
				finalValue = float64(decimalValue) * scale

				if blockName == "V3DX1" || blockName == "VRAD1" {
					if math.IsNaN(finalValue) || math.IsInf(finalValue, 0) || math.Abs(finalValue) > 100 {
						continue
					}
				} else if blockName == "AMPL1" {
					if math.IsNaN(finalValue) || math.IsInf(finalValue, 0) || finalValue < 0 || finalValue > 1000000 {
						continue
					}
				}
			}

			values = append(values, finalValue)

			if r.DebugMode && i < 3 {
				fmt.Printf("  %s_%d: %s -> %.3f\n", blockName, i+1, valHex, finalValue)
			}
		}

		switch blockName {
		case "P3DX1", "DIST1":
			positions = values
		case "V3DX1", "VRAD1":
			velocities = values
		case "AZMT1", "ANG1", "DIR1", "ANGLE1":
			azimuths = values
		case "AMPL1":
			amplitudes = values
		}
	}

	objPrincipal = r.selectStabilizedMainObject(positions, velocities, azimuths, amplitudes)

	return positions, velocities, azimuths, amplitudes, objPrincipal, nil
}

// selectStabilizedMainObject seleciona objeto principal com estabilização
func (r *SICKRadar) selectStabilizedMainObject(positions, velocities, azimuths, amplitudes []float64) *models.ObjPrincipal {
	if len(amplitudes) == 0 {
		r.objetoPrincipalInfo = nil
		return nil
	}

	if len(amplitudes) > 1000 {
		amplitudes = amplitudes[:1000]
	}

	maxAmpIndex := 0
	maxAmp := amplitudes[0]
	for i, amp := range amplitudes {
		if math.IsNaN(amp) || math.IsInf(amp, 0) || amp < 0 {
			continue
		}
		if amp > maxAmp {
			maxAmp = amp
			maxAmpIndex = i
		}
	}

	novoObjeto := &models.ObjPrincipal{
		Amplitude: maxAmp,
	}

	if maxAmpIndex < len(positions) && maxAmpIndex >= 0 {
		dist := positions[maxAmpIndex]
		if !math.IsNaN(dist) && !math.IsInf(dist, 0) && dist >= 0 && dist <= 1000 {
			novoObjeto.Distancia = &dist
		}
	}
	if maxAmpIndex < len(velocities) && maxAmpIndex >= 0 {
		vel := velocities[maxAmpIndex]
		if !math.IsNaN(vel) && !math.IsInf(vel, 0) && math.Abs(vel) <= 100 {
			novoObjeto.Velocidade = &vel
		}
	}
	if maxAmpIndex < len(azimuths) && maxAmpIndex >= 0 {
		ang := azimuths[maxAmpIndex]
		if !math.IsNaN(ang) && !math.IsInf(ang, 0) && math.Abs(ang) <= 360 {
			novoObjeto.Angulo = &ang
		}
	}

	agora := time.Now()

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

	mesmoObjeto := (maxAmpIndex == r.objetoPrincipalInfo.Indice)

	if objetoAtual.Amplitude > 0 {
		diff := math.Abs(novoObjeto.Amplitude-objetoAtual.Amplitude) / objetoAtual.Amplitude
		mesmoObjeto = mesmoObjeto || (diff < 0.05)
	}

	if mesmoObjeto {
		r.objetoPrincipalInfo.Objeto = novoObjeto
		if r.objetoPrincipalInfo.ContadorEstabilidade < 1000 {
			r.objetoPrincipalInfo.ContadorEstabilidade++
		}
		r.objetoPrincipalInfo.UltimaAtualizacao = agora
		r.objetoPrincipalInfo.Indice = maxAmpIndex
		return novoObjeto
	}

	var diferencaPercentual float64
	if objetoAtual.Amplitude > 0 {
		diferencaPercentual = ((novoObjeto.Amplitude - objetoAtual.Amplitude) / objetoAtual.Amplitude) * 100
	}

	deveTrocar := (diferencaPercentual > r.thresholdMudanca) &&
		(r.objetoPrincipalInfo.ContadorEstabilidade >= r.ciclosMinimosEstabilidade)

	tempoSemAtualizacao := agora.Sub(r.objetoPrincipalInfo.UltimaAtualizacao)
	if tempoSemAtualizacao > 2*time.Second {
		deveTrocar = true
	}

	if deveTrocar {
		r.objetoPrincipalInfo = &models.ObjetoPrincipalInfo{
			Objeto:               novoObjeto,
			ContadorEstabilidade: 1,
			UltimaAtualizacao:    agora,
			Indice:               maxAmpIndex,
		}
		return novoObjeto
	} else {
		if diferencaPercentual < -r.thresholdMudanca {
			r.objetoPrincipalInfo.ContadorEstabilidade = 1
		}
		return objetoAtual
	}
}

// CORREÇÃO: markConnectionError COM MUTEX
func (r *SICKRadar) markConnectionError(err error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.consecutiveErrors++

	if r.isConnectionError(err) {
		if r.consecutiveErrors >= r.maxConsecutiveErrors {
			r.Connected = false
			r.forceReconnect = true
		}
	}
}

// CORREÇÃO: markSuccessfulOperation COM MUTEX
func (r *SICKRadar) markSuccessfulOperation() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.consecutiveErrors = 0
	r.lastSuccessfulRead = time.Now()
}

func (r *SICKRadar) isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	if netErr, ok := err.(net.Error); ok {
		if !netErr.Temporary() {
			return true
		}
	}

	errStr := err.Error()
	connectionErrors := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"network unreachable",
		"no route to host",
		"connection timed out",
		"forcibly closed",
		"use of closed network connection",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	return false
}

func (r *SICKRadar) ForceReconnect() {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.forceReconnect = true
	r.Connected = false
}

func (r *SICKRadar) SetDebugMode(enabled bool) {
	r.DebugMode = enabled
}

func (r *SICKRadar) GetDebugMode() bool {
	return r.DebugMode
}

func (r *SICKRadar) GetConnectionStats() (int, time.Time) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.consecutiveErrors, r.lastSuccessfulRead
}
