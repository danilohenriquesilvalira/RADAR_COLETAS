package websocket

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"

	"backend/pkg/models"

	"github.com/gorilla/websocket"
)

// WebSocketManager gerencia as conexões WebSocket
type WebSocketManager struct {
	clients        map[*websocket.Conn]bool
	broadcast      chan models.RadarData
	multiBroadcast chan models.MultiRadarData
	register       chan *websocket.Conn
	unregister     chan *websocket.Conn
	mutex          sync.Mutex
	connCount      int // Contador de conexões ativas
}

// NewWebSocketManager cria um novo gerenciador de WebSockets
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients:        make(map[*websocket.Conn]bool),
		broadcast:      make(chan models.RadarData),
		multiBroadcast: make(chan models.MultiRadarData),
		register:       make(chan *websocket.Conn),
		unregister:     make(chan *websocket.Conn),
		connCount:      0,
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

		case multiMessage := <-manager.multiBroadcast:
			manager.mutex.Lock()
			for client := range manager.clients {
				err := client.WriteJSON(multiMessage)
				if err != nil {
					log.Printf("Erro ao enviar mensagem multi-radar: %v. Removendo cliente: %p", err, client)
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
func (manager *WebSocketManager) BroadcastData(data models.RadarData) {
	manager.mutex.Lock()
	clientCount := len(manager.clients)
	manager.mutex.Unlock()

	if clientCount > 0 {
		manager.broadcast <- data
	}
}

// BroadcastMultiRadarData envia dados de múltiplos radares para todos os clientes conectados
func (manager *WebSocketManager) BroadcastMultiRadarData(data models.MultiRadarData) {
	manager.mutex.Lock()
	clientCount := len(manager.clients)
	manager.mutex.Unlock()

	if clientCount > 0 {
		manager.multiBroadcast <- data
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

// GetConnectedCount retorna número de clientes conectados
func (manager *WebSocketManager) GetConnectedCount() int {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	return manager.connCount
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

// HandleWebSocket trata conexões WebSocket
func (manager *WebSocketManager) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
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

// ServeHTTP inicia o servidor HTTP e WebSocket
func (manager *WebSocketManager) ServeHTTP(webDir string) {
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
	http.HandleFunc("/ws", manager.HandleWebSocket)

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
