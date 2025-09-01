package radar

import (
	"fmt"
	"log"
	"time"
)

// ReconnectionManager gerencia reconexão automática do radar
type ReconnectionManager struct {
	radar             *SICKRadar
	maxRetries        int
	retryInterval     time.Duration
	isReconnecting    bool
	connectionLost    bool
	consecutiveErrors int
	lastError         error
}

// NewReconnectionManager cria um novo gerenciador de reconexão
func NewReconnectionManager(radar *SICKRadar) *ReconnectionManager {
	return &ReconnectionManager{
		radar:             radar,
		maxRetries:        10,              // Máximo 10 tentativas
		retryInterval:     3 * time.Second, // Tentar a cada 3 segundos
		isReconnecting:    false,
		connectionLost:    false,
		consecutiveErrors: 0,
	}
}

// CheckConnectionHealth verifica se a conexão está saudável
func (rm *ReconnectionManager) CheckConnectionHealth(err error) bool {
	if err != nil {
		rm.consecutiveErrors++
		rm.lastError = err

		// Se muitos erros consecutivos, considerar conexão perdida
		if rm.consecutiveErrors >= 5 {
			if !rm.connectionLost {
				rm.connectionLost = true
				log.Printf("🔴 RADAR: Conexão perdida detectada após %d erros consecutivos", rm.consecutiveErrors)
				log.Printf("🔴 RADAR: Último erro: %v", err)
			}
			return false
		}
	} else {
		// Reset contador se leitura bem sucedida
		if rm.consecutiveErrors > 0 {
			log.Printf("✅ RADAR: Conexão estável - resetando contador de erros")
		}
		rm.consecutiveErrors = 0
		rm.connectionLost = false
	}

	return true
}

// StartReconnection inicia processo de reconexão automática
func (rm *ReconnectionManager) StartReconnection() error {
	if rm.isReconnecting {
		return nil // Já está tentando reconectar
	}

	rm.isReconnecting = true

	log.Printf("🔄 RADAR: Iniciando reconexão automática...")

	for attempt := 1; attempt <= rm.maxRetries; attempt++ {
		log.Printf("🔄 RADAR: Tentativa de reconexão %d/%d", attempt, rm.maxRetries)

		// Desconectar conexão atual (se existir)
		rm.radar.Disconnect()

		// Aguardar antes de tentar reconectar
		time.Sleep(rm.retryInterval)

		// Tentar reconectar
		err := rm.radar.Connect()
		if err != nil {
			log.Printf("❌ RADAR: Tentativa %d falhou: %v", attempt, err)
			continue
		}

		// Tentar reiniciar medição
		err = rm.radar.StartMeasurement()
		if err != nil {
			log.Printf("❌ RADAR: Falha ao reiniciar medição na tentativa %d: %v", attempt, err)
			rm.radar.Disconnect()
			continue
		}

		// Sucesso!
		log.Printf("✅ RADAR: Reconectado com sucesso na tentativa %d", attempt)
		rm.isReconnecting = false
		rm.connectionLost = false
		rm.consecutiveErrors = 0

		return nil
	}

	// Todas as tentativas falharam
	rm.isReconnecting = false
	return fmt.Errorf("falha ao reconectar após %d tentativas", rm.maxRetries)
}

// IsReconnecting verifica se está em processo de reconexão
func (rm *ReconnectionManager) IsReconnecting() bool {
	return rm.isReconnecting
}

// IsConnectionLost verifica se a conexão foi perdida
func (rm *ReconnectionManager) IsConnectionLost() bool {
	return rm.connectionLost
}

// GetConsecutiveErrors retorna número de erros consecutivos
func (rm *ReconnectionManager) GetConsecutiveErrors() int {
	return rm.consecutiveErrors
}

// GetLastError retorna o último erro
func (rm *ReconnectionManager) GetLastError() error {
	return rm.lastError
}

// ResetErrorCount reseta contador de erros
func (rm *ReconnectionManager) ResetErrorCount() {
	rm.consecutiveErrors = 0
	rm.connectionLost = false
	rm.lastError = nil
}
