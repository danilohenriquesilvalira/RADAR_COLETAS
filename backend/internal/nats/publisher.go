package nats

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"backend/pkg/models"

	"github.com/nats-io/nats.go"
)

// Publisher gerencia a publicação de dados no NATS
type Publisher struct {
	conn    *nats.Conn
	subject string
	mutex   sync.Mutex
	enabled bool
}

// NewPublisher cria um novo publisher NATS
func NewPublisher(subject string) *Publisher {
	return &Publisher{
		subject: subject,
		enabled: false,
	}
}

// Connect conecta ao servidor NATS
func (p *Publisher) Connect(natsURL string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Opções de conexão com retry automático
	opts := []nats.Option{
		nats.Name("Radar-Data-Publisher"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1), // Retry infinito
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("NATS desconectado: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconectado: %s", nc.ConnectedUrl())
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			log.Printf("NATS conexão fechada")
		}),
	}

	var err error
	p.conn, err = nats.Connect(natsURL, opts...)
	if err != nil {
		p.enabled = false
		return fmt.Errorf("erro ao conectar ao NATS: %v", err)
	}

	p.enabled = true
	log.Printf("NATS conectado em: %s", natsURL)
	return nil
}

// Publish publica dados do radar no NATS
func (p *Publisher) Publish(data models.RadarData) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.enabled || p.conn == nil {
		// Se NATS não está disponível, apenas log mas não falha
		return nil
	}

	// Serializar dados para JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("erro ao serializar dados: %v", err)
	}

	// Publicar no NATS
	err = p.conn.Publish(p.subject, jsonData)
	if err != nil {
		return fmt.Errorf("erro ao publicar no NATS: %v", err)
	}

	return nil
}

// Disconnect desconecta do NATS
func (p *Publisher) Disconnect() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
		p.enabled = false
		log.Println("NATS desconectado")
	}
}

// IsConnected verifica se está conectado ao NATS
func (p *Publisher) IsConnected() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.enabled && p.conn != nil && p.conn.IsConnected()
}

// IsEnabled verifica se NATS está habilitado
func (p *Publisher) IsEnabled() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.enabled
}
