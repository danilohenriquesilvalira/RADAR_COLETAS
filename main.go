package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/robinson/gos7"
)

// RadarData representa a estrutura de dados do radar enviada via WebSocket
type RadarData struct {
	Positions  []float64     `json:"positions,omitempty"`
	Velocities []float64     `json:"velocities,omitempty"`
	Azimuths   []float64     `json:"azimuths,omitempty"`
	Amplitudes []float64     `json:"amplitudes,omitempty"`
	MainObject *ObjPrincipal `json:"mainObject,omitempty"`
	PLCStatus  *PLCStatus    `json:"plcStatus,omitempty"`
	Timestamp  int64         `json:"timestamp"`
}

// PLCStatus representa o status da conexão com o PLC
type PLCStatus struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// ObjPrincipal representa o objeto com maior amplitude
type ObjPrincipal struct {
	Amplitude  float64  `json:"amplitude"`
	Distancia  *float64 `json:"distancia,omitempty"`
	Velocidade *float64 `json:"velocidade,omitempty"`
	Angulo     *float64 `json:"angulo,omitempty"`
}

// ObjetoPrincipalInfo armazena informações para estabilização
type ObjetoPrincipalInfo struct {
	Objeto               *ObjPrincipal
	ContadorEstabilidade int
	UltimaAtualizacao    time.Time
	Indice               int
}

// WebSocketManager gerencia as conexões WebSocket
type WebSocketManager struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan RadarData
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mutex      sync.Mutex
	connCount  int // Contador de conexões ativas
}

// NewWebSocketManager cria um novo gerenciador de WebSockets
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan RadarData),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		connCount:  0,
	}
}

// Run inicia o gerenciador de WebSockets
func (manager *WebSocketManager) Run() {
	for {
		select {
		case client := <-manager.register:
			manager.mutex.Lock()
			// Verificar se o cliente já está registrado
			if _, exists := manager.clients[client]; !exists {
				manager.clients[client] = true
				manager.connCount++
				log.Printf("Novo cliente conectado. ID: %p, Total: %d", client, manager.connCount)
			}
			manager.mutex.Unlock()

		case client := <-manager.unregister:
			manager.mutex.Lock()
			if _, ok := manager.clients[client]; ok {
				delete(manager.clients, client)
				manager.connCount--
				log.Printf("Cliente desconectado. ID: %p, Total: %d", client, manager.connCount)
				client.Close()
			}
			manager.mutex.Unlock()

		case message := <-manager.broadcast:
			manager.mutex.Lock()
			for client := range manager.clients {
				err := client.WriteJSON(message)
				if err != nil {
					log.Printf("Erro ao enviar mensagem: %v. Removendo cliente: %p", err, client)
					client.Close()
					delete(manager.clients, client)
					manager.connCount--
				}
			}
			manager.mutex.Unlock()
		}
	}
}

// BroadcastData envia dados para todos os clientes conectados
func (manager *WebSocketManager) BroadcastData(data RadarData) {
	manager.mutex.Lock()
	clientCount := len(manager.clients)
	manager.mutex.Unlock()

	if clientCount > 0 {
		manager.broadcast <- data
	}
}

// LimparConexoesAnteriores limpa conexões inativas
func (manager *WebSocketManager) LimparConexoesAnteriores() {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	// Limpar mapa de clientes no início
	for client := range manager.clients {
		client.Close()
		delete(manager.clients, client)
	}

	manager.connCount = 0
	log.Println("Todas as conexões anteriores foram limpas.")
}

// SiemensPLC representa a conexão com o PLC Siemens
type SiemensPLC struct {
	IP        string
	Rack      int
	Slot      int
	Connected bool
	Client    gos7.Client
	Handler   *gos7.TCPClientHandler
	mutex     sync.Mutex
}

// NewSiemensPLC cria uma nova instância de conexão com o PLC
func NewSiemensPLC(ip string) *SiemensPLC {
	return &SiemensPLC{
		IP:        ip,
		Rack:      0, // Valores padrão para S7-1200/1500
		Slot:      1,
		Connected: false,
	}
}

// Connect estabelece conexão com o PLC
func (p *SiemensPLC) Connect() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Criar novo handler para conexão TCP
	p.Handler = gos7.NewTCPClientHandler(p.IP, p.Rack, p.Slot)
	p.Handler.Timeout = 5 * time.Second
	p.Handler.IdleTimeout = 10 * time.Second

	// Conectar ao PLC
	err := p.Handler.Connect()
	if err != nil {
		p.Connected = false
		return fmt.Errorf("erro ao conectar ao PLC: %v", err)
	}

	// Criar cliente S7
	p.Client = gos7.NewClient(p.Handler)
	p.Connected = true

	fmt.Printf("Conectado ao PLC Siemens em %s (Rack: %d, Slot: %d)\n", p.IP, p.Rack, p.Slot)
	return nil
}

// Disconnect fecha a conexão com o PLC
func (p *SiemensPLC) Disconnect() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.Connected && p.Handler != nil {
		p.Handler.Close()
		p.Connected = false
		fmt.Println("Desconectado do PLC Siemens")
	}
}

// GetConnectionStatus retorna o status atual da conexão
func (p *SiemensPLC) GetConnectionStatus() *PLCStatus {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	status := &PLCStatus{
		Connected: p.Connected,
	}

	if !p.Connected {
		status.Error = "PLC não conectado"
	}

	return status
}

// SICKRadar representa a conexão e funcionalidade do radar
type SICKRadar struct {
	IP           string
	Port         int
	conn         net.Conn
	Connected    bool
	DebugMode    bool
	WebSocketMgr *WebSocketManager
	PLC          *SiemensPLC
	// Campos para estabilização do objeto principal
	objetoPrincipalInfo       *ObjetoPrincipalInfo
	thresholdMudanca          float64 // Margem mínima para trocar objeto principal (%)
	ciclosMinimosEstabilidade int     // Número mínimo de ciclos para manter objeto
	mutex                     sync.Mutex
}

// NewSICKRadar cria uma nova instância do radar
func NewSICKRadar(ip string, port int) *SICKRadar {
	if port == 0 {
		port = 2111 // Porta padrão
	}
	return &SICKRadar{
		IP:                        ip,
		Port:                      port,
		Connected:                 false,
		DebugMode:                 false,
		WebSocketMgr:              NewWebSocketManager(),
		PLC:                       NewSiemensPLC("192.168.1.33"), // IP fixo do PLC
		objetoPrincipalInfo:       nil,
		thresholdMudanca:          15.0, // 15% de diferença mínima para trocar
		ciclosMinimosEstabilidade: 3,    // Manter por pelo menos 3 ciclos
	}
}

// Connect estabelece conexão TCP com o radar
func (r *SICKRadar) Connect() error {
	var err error
	r.conn, err = net.DialTimeout("tcp", fmt.Sprintf("%s:%d", r.IP, r.Port), 5*time.Second)
	if err != nil {
		fmt.Printf("Erro ao conectar: %v\n", err)
		r.Connected = false
		return err
	}

	r.Connected = true
	fmt.Printf("Conectado ao radar em %s:%d\n", r.IP, r.Port)
	return nil
}

// Disconnect fecha a conexão com o radar
func (r *SICKRadar) Disconnect() {
	if r.conn != nil {
		r.conn.Close()
		r.Connected = false
		fmt.Println("Desconectado do radar")
	}
}

// SendCommand envia um comando para o radar e retorna a resposta
func (r *SICKRadar) SendCommand(command string) ([]byte, error) {
	if !r.Connected || r.conn == nil {
		return nil, fmt.Errorf("não conectado ao radar")
	}

	// Formato CoLa A: <STX>comando<ETX>
	telegram := append([]byte{0x02}, []byte(command)...)
	telegram = append(telegram, 0x03)

	if r.DebugMode {
		fmt.Printf("Enviando comando: %s\n", command)
	}

	// Definir timeout para operações de rede
	err := r.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return nil, fmt.Errorf("erro ao definir deadline: %v", err)
	}

	_, err = r.conn.Write(telegram)
	if err != nil {
		return nil, fmt.Errorf("erro ao enviar comando: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Ler resposta
	buffer := make([]byte, 4096)
	n, err := r.conn.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("erro ao receber resposta: %v", err)
	}

	return buffer[:n], nil
}

// HexToFloat converte string hexadecimal IEEE-754 para float
func (r *SICKRadar) HexToFloat(hexStr string) float64 {
	// Remover prefixo "0x" se existir
	hexStr = strings.TrimPrefix(hexStr, "0x")

	// Converter hex para uint32
	value, err := strconv.ParseUint(hexStr, 16, 32)
	if err != nil {
		if r.DebugMode {
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
func (r *SICKRadar) HexToInt(hexStr string) int {
	// Remover prefixo "0x" se existir
	hexStr = strings.TrimPrefix(hexStr, "0x")

	// Converter para int
	value, err := strconv.ParseInt(hexStr, 16, 64)
	if err != nil {
		if r.DebugMode {
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
func (r *SICKRadar) DecodeAngleData(hexValue string, scale float64) float64 {
	// Converter o valor hexadecimal em inteiro decimal
	decimalValue := r.HexToInt(hexValue)

	// Aplicar fator de escala do telegrama
	angleValue := float64(decimalValue) * scale

	// Verificação de codificação possível: alguns modelos usam 1/10 ou 1/32 graus
	if math.Abs(angleValue) > 180 {
		angleValue = angleValue / 32.0
	}

	return angleValue
}

// SelecionarObjetoPrincipalEstabilizado seleciona o objeto principal com estabilização
func (r *SICKRadar) SelecionarObjetoPrincipalEstabilizado(positions, velocities, azimuths, amplitudes []float64) *ObjPrincipal {
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
	novoObjeto := &ObjPrincipal{
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
		r.objetoPrincipalInfo = &ObjetoPrincipalInfo{
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
		r.objetoPrincipalInfo = &ObjetoPrincipalInfo{
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

// LimparTela limpa o terminal para melhor visualização
func (r *SICKRadar) LimparTela() {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "cls")
	} else {
		cmd = exec.Command("clear")
	}
	cmd.Stdout = os.Stdout
	cmd.Run()
}

// CollectData coleta dados do radar e extrai valores de distância, velocidade e ângulo
func (r *SICKRadar) CollectData() {
	// Limpar conexões WebSocket anteriores para evitar duplicações
	r.WebSocketMgr.LimparConexoesAnteriores()

	if !r.Connected {
		fmt.Println("Não conectado ao radar. Conectando...")
		if err := r.Connect(); err != nil {
			return
		}
	}

	// Tentar conectar ao PLC
	fmt.Println("Conectando ao PLC Siemens...")
	err := r.PLC.Connect()
	if err != nil {
		fmt.Printf("Aviso: %v - Continuando sem conexão com o PLC\n", err)
	}

	// Iniciar a medição
	fmt.Println("Iniciando medição...")
	_, err = r.SendCommand("sEN LMDradardata 1")
	if err != nil {
		fmt.Printf("Erro ao iniciar medição: %v\n", err)
		return
	}
	time.Sleep(500 * time.Millisecond)

	fmt.Println("Monitorando dados do radar. Pressione Ctrl+C para parar.")

	// Configurar socket para leitura contínua
	if r.conn != nil {
		err := r.conn.SetReadDeadline(time.Time{}) // Sem deadline
		if err != nil {
			fmt.Printf("Erro ao remover deadline: %v\n", err)
			return
		}
	}

	// Para leitura do socket
	buffer := make([]byte, 8192)

	for {
		// Definir timeout para cada leitura
		err := r.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if err != nil {
			fmt.Printf("\nErro ao definir deadline: %v\n", err)
			continue
		}

		n, err := r.conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Timeout normal, continuar
				continue
			}
			fmt.Printf("\nErro de leitura: %v\n", err)
			continue
		}

		if n > 0 {
			// Processar dados recebidos
			r.ProcessData(buffer[:n])
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// ProcessData processa e exibe os dados recebidos do radar e os envia via WebSocket
func (r *SICKRadar) ProcessData(data []byte) {
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
	positions := []float64{}
	velocities := []float64{}
	azimuths := []float64{}
	amplitudes := []float64{}

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
			scaleFloat := r.HexToFloat(scaleHex)
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
						finalValue := r.DecodeAngleData(valHex, scale)
						values = append(values, finalValue)
						if r.DebugMode {
							fmt.Printf("  %s_%d: HEX=%s -> %.3f°\n", blockName, i+1, valHex, finalValue)
						}
					} else if blockName == "P3DX1" || blockName == "DIST1" {
						// Posições (metros) - SEGUINDO EXATAMENTE O PYTHON
						decimalValue := r.HexToInt(valHex)
						finalValue := float64(decimalValue) * scale / 1000.0
						values = append(values, finalValue)
						if r.DebugMode {
							fmt.Printf("  %s_%d: HEX=%s -> DEC=%d -> %.3fm\n", blockName, i+1, valHex, decimalValue, finalValue)
						}
					} else {
						// Velocidades e outros
						decimalValue := r.HexToInt(valHex)
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

	// Limpar a tela antes de exibir
	r.LimparTela()

	fmt.Println("==================================================")
	fmt.Println("     DADOS DO RADAR SICK RMS1000 - TEMPO REAL")
	fmt.Println("==================================================")

	// Status do PLC
	fmt.Println("STATUS PLC SIEMENS:")
	fmt.Printf("  Conectado: %v\n", r.PLC.Connected)
	fmt.Printf("  Endereço: %s\n", r.PLC.IP)

	// Status do WebSocket
	fmt.Printf("\nWEBSOCKET:")
	fmt.Printf("  Clientes conectados: %d\n", r.WebSocketMgr.connCount)

	// Usar algoritmo de estabilização para selecionar objeto principal
	objPrincipal := r.SelecionarObjetoPrincipalEstabilizado(positions, velocities, azimuths, amplitudes)

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

	// Se debug_mode ativado, exibir dados brutos
	if r.DebugMode {
		fmt.Println("\nDados brutos decodificados (primeiros 300 caracteres):")
		if len(cleanData.String()) > 300 {
			fmt.Println(cleanData.String()[:300] + "...")
		} else {
			fmt.Println(cleanData.String())
		}
	}

	// Obter status do PLC para o WebSocket
	plcStatus := r.PLC.GetConnectionStatus()

	// Enviar dados para WebSocket
	if r.WebSocketMgr != nil {
		radarData := RadarData{
			Positions:  positions,
			Velocities: velocities,
			Azimuths:   azimuths,
			Amplitudes: amplitudes,
			MainObject: objPrincipal,
			PLCStatus:  plcStatus,
			Timestamp:  time.Now().UnixNano() / int64(time.Millisecond),
		}

		r.WebSocketMgr.BroadcastData(radarData)
	}
}

// Configuração de websocket
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Permitir todas as origens
		return true
	},
}

// handleWebSocket trata conexões WebSocket
func handleWebSocket(manager *WebSocketManager, w http.ResponseWriter, r *http.Request) {
	// Verificar se é uma requisição WebSocket legítima
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "Não é uma requisição WebSocket válida", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Erro ao fazer upgrade para WebSocket: %v", err)
		return
	}

	// Adicionar cabeçalho de conexão close
	conn.SetCloseHandler(func(code int, text string) error {
		log.Printf("Conexão WebSocket fechada com código %d: %s", code, text)
		manager.unregister <- conn
		return nil
	})

	// Registrar nova conexão
	manager.register <- conn

	// Monitorar desconexões
	go func() {
		for {
			messageType, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err,
					websocket.CloseGoingAway,
					websocket.CloseAbnormalClosure) {
					log.Printf("Erro WebSocket: %v", err)
				}
				manager.unregister <- conn
				break
			}

			// Responder a ping/pong
			if messageType == websocket.PingMessage {
				if err := conn.WriteMessage(websocket.PongMessage, nil); err != nil {
					log.Printf("Erro ao enviar pong: %v", err)
					manager.unregister <- conn
					break
				}
			}
		}
	}()
}

// serveHTTP inicia o servidor HTTP e WebSocket
func serveHTTP(manager *WebSocketManager, webDir string) {
	// Servir arquivos estáticos
	// Primeiro, tente diretório dist (gerado pelo build)
	distDir := webDir + "/dist"
	if _, err := os.Stat(distDir); err == nil {
		log.Printf("Servindo arquivos estáticos do diretório: %s", distDir)
		fs := http.FileServer(http.Dir(distDir))
		http.Handle("/", fs)
	} else {
		// Fallback para o diretório web
		log.Printf("Diretório dist não encontrado, servindo do diretório: %s", webDir)
		fs := http.FileServer(http.Dir(webDir))
		http.Handle("/", fs)
	}

	// Endpoint WebSocket
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(manager, w, r)
	})

	// API para obter informações do sistema
	http.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		info := struct {
			Version string `json:"version"`
			Name    string `json:"name"`
		}{
			Version: "1.0.0",
			Name:    "SICK RMS1000 Radar Monitor",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})

	log.Println("Servidor Web e WebSocket iniciado na porta 8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Erro ao iniciar servidor HTTP: ", err)
	}
}

func main() {
	radarIP := "192.168.1.84" // IP do radar
	radar := NewSICKRadar(radarIP, 2111)

	// Criar diretório para arquivos web se não existir
	webDir := "./web"
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		os.Mkdir(webDir, 0755)
	}

	// Iniciar gerenciador de WebSockets em uma goroutine
	go radar.WebSocketMgr.Run()

	// Iniciar servidor HTTP/WebSocket em uma goroutine
	go serveHTTP(radar.WebSocketMgr, webDir)

	fmt.Println("\n===== RADAR SICK RMS1000 =====")
	fmt.Println("Servidor Web/WebSocket iniciado em http://localhost:8080")
	fmt.Println("Dados do radar estão disponíveis via WebSocket em ws://localhost:8080/ws")
	fmt.Println("Conectando ao radar automaticamente...")

	// Conectar ao radar automaticamente
	if err := radar.Connect(); err != nil {
		fmt.Println("Falha ao conectar. Tentando novamente em 5 segundos...")
		time.Sleep(5 * time.Second)
		if err := radar.Connect(); err != nil {
			fmt.Println("Não foi possível conectar ao radar. Verifique o IP e a conexão.")
			os.Exit(1)
		}
	}

	// Iniciar coleta de dados em modo visualização limpa automaticamente
	radar.DebugMode = false
	fmt.Println("Iniciando coleta de dados automaticamente...")
	radar.CollectData() // Esta função contém um loop infinito

	// Este código nunca será alcançado devido ao loop infinito em CollectData()
	// Mas mantemos como precaução
	fmt.Println("Encerrando programa...")
	if radar.Connected {
		radar.Disconnect()
	}
	if radar.PLC.Connected {
		radar.PLC.Disconnect()
	}
}
