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
func (p *Publisher) Publish(data interface{}) error {
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

// PublishWithSubject publica em um tópico específico
func (p *Publisher) PublishWithSubject(subject string, data interface{}) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.enabled || p.conn == nil {
		return nil
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("erro ao serializar dados: %v", err)
	}

	err = p.conn.Publish(subject, jsonData)
	if err != nil {
		return fmt.Errorf("erro ao publicar no NATS em %s: %v", subject, err)
	}

	return nil
}

// PublishRaw publica dados brutos (bytes) em um tópico específico
func (p *Publisher) PublishRaw(subject string, data []byte) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.enabled || p.conn == nil {
		return nil
	}

	err := p.conn.Publish(subject, data)
	if err != nil {
		return fmt.Errorf("erro ao publicar no NATS em %s: %v", subject, err)
	}

	return nil
}

// Request envia uma solicitação e aguarda resposta
func (p *Publisher) Request(subject string, data interface{}, timeout time.Duration) ([]byte, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.enabled || p.conn == nil {
		return nil, fmt.Errorf("não conectado ao NATS")
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("erro ao serializar dados: %v", err)
	}

	msg, err := p.conn.Request(subject, jsonData, timeout)
	if err != nil {
		return nil, fmt.Errorf("erro na requisição NATS: %v", err)
	}

	return msg.Data, nil
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

// PublishMultiRadar publica dados de múltiplos radares no NATS
func (p *Publisher) PublishMultiRadar(data models.MultiRadarData) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.enabled || p.conn == nil {
		// Se NATS não está disponível, apenas log mas não falha
		return nil
	}

	// Publicar dados gerais de múltiplos radares
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("erro ao serializar dados multi-radar: %v", err)
	}

	// Publicar no tópico principal
	err = p.conn.Publish(p.subject, jsonData)
	if err != nil {
		return fmt.Errorf("erro ao publicar multi-radar no NATS: %v", err)
	}

	// Publicar dados individuais de cada radar em subtópicos
	for _, radarData := range data.Radars {
		radarSubject := fmt.Sprintf("%s.%s", p.subject, radarData.RadarID)
		radarJsonData, err := json.Marshal(radarData)
		if err != nil {
			log.Printf("Erro ao serializar dados do radar %s: %v", radarData.RadarID, err)
			continue
		}
		
		err = p.conn.Publish(radarSubject, radarJsonData)
		if err != nil {
			log.Printf("Erro ao publicar dados do radar %s: %v", radarData.RadarID, err)
		}
	}

	return nil
}
