package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"backend/internal/nats"
	"backend/internal/plc"
	"backend/internal/radar"
	"backend/internal/websocket"
	"backend/pkg/models"
)

func main() {
	// Configurações
	radarIP := "192.168.1.84"          // IP do radar
	plcIP := "192.168.1.33"            // IP do PLC
	natsURL := "nats://localhost:4222" // URL do NATS (opcional)
	webDir := "./web"

	fmt.Println("\n===== RADAR SICK RMS1000 com AUTO-RECOVERY =====")
	fmt.Println("Servidor Web/WebSocket iniciado em http://localhost:8080")
	fmt.Println("Sistema com reconexão automática e controle PLC bidirecional")

	// Criar instâncias dos componentes
	radarSick := radar.NewSICKRadar(radarIP, 2111)
	plcSiemens := plc.NewSiemensPLC(plcIP)
	wsManager := websocket.NewWebSocketManager()
	natsPublisher := nats.NewPublisher("radar.data")

	// Criar diretório para arquivos web se não existir
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		os.Mkdir(webDir, 0755)
	}

	// Iniciar gerenciador de WebSockets em uma goroutine
	go wsManager.Run()

	// Iniciar servidor HTTP/WebSocket em uma goroutine
	go wsManager.ServeHTTP(webDir)

	// Tentar conectar ao NATS (opcional - não crítico)
	fmt.Println("Tentando conectar ao NATS...")
	err := natsPublisher.Connect(natsURL)
	if err != nil {
		fmt.Printf("Aviso: %v - Continuando sem NATS\n", err)
	}

	// ========== CONEXÃO INICIAL COM RETRY ==========
	fmt.Println("Conectando ao radar com retry automático...")
	err = connectRadarWithRetry(radarSick, 3)
	if err != nil {
		fmt.Printf("❌ Não foi possível conectar ao radar após tentativas: %v\n", err)
		fmt.Println("Sistema continuará tentando reconectar automaticamente...")
	}
	// ===============================================

	// Tentar conectar ao PLC
	fmt.Println("Conectando ao PLC Siemens...")
	err = plcSiemens.Connect()
	if err != nil {
		fmt.Printf("Aviso: %v - Continuando sem conexão com o PLC\n", err)
	}

	// ========== INICIALIZAR CONTROLADOR PLC ==========
	var plcController *plc.PLCController

	if plcSiemens.IsConnected() {
		fmt.Println("Inicializando controlador PLC bidirecional...")

		// Criar controlador PLC
		plcController = plc.NewPLCController(plcSiemens.Client)

		// Iniciar controlador PLC em goroutine
		go plcController.Start()

		fmt.Println("✅ Controlador PLC iniciado - Sistema pode ser controlado via supervisório")
	}
	// ================================================

	// Limpar conexões WebSocket anteriores
	wsManager.LimparConexoesAnteriores()

	fmt.Println("\n🚀 Sistema iniciado com AUTO-RECOVERY")
	fmt.Println("📡 Monitoramento contínuo ativo")
	fmt.Println("🔄 Reconexão automática habilitada")
	fmt.Println("🎛️ Controle via PLC operacional")
	fmt.Println("\nPressione Ctrl+C para parar.")

	// ========== LOOP PRINCIPAL COM AUTO-RECOVERY ==========
	radarSick.SetDebugMode(false)
	consecutiveErrors := 0
	isReconnecting := false
	plcConsecutiveErrors := 0
	isPLCReconnecting := false

	for {
		// ========== VERIFICAR E RECONECTAR PLC SE NECESSÁRIO ==========
		if plcSiemens != nil && !plcSiemens.IsConnected() && !isPLCReconnecting {
			fmt.Println("🔄 PLC desconectado - iniciando reconexão...")
			isPLCReconnecting = true

			// Tentar reconectar PLC em goroutine
			go func() {
				defer func() { isPLCReconnecting = false }()

				for attempt := 1; attempt <= 5; attempt++ {
					fmt.Printf("🔄 PLC: Tentativa de reconexão %d/5\n", attempt)

					// Desconectar conexão atual
					plcSiemens.Disconnect()
					time.Sleep(2 * time.Second)

					// Tentar reconectar
					err := plcSiemens.Connect()
					if err != nil {
						fmt.Printf("❌ PLC: Tentativa %d falhou: %v\n", attempt, err)
						continue
					}

					// Sucesso!
					fmt.Printf("✅ PLC reconectado com sucesso na tentativa %d\n", attempt)
					plcConsecutiveErrors = 0

					// Recriar controlador PLC
					if plcController != nil {
						plcController.Stop()
					}
					plcController = plc.NewPLCController(plcSiemens.Client)
					go plcController.Start()
					fmt.Println("✅ Controlador PLC reiniciado")

					return
				}

				fmt.Println("❌ PLC: Falha ao reconectar após 5 tentativas - continuando sem PLC")
			}()
		}
		// ===============================================================

		// Verificar comandos do PLC primeiro (só se conectado)
		collectionActive := true

		if plcController != nil && plcSiemens.IsConnected() {
			// Verificar se coleta está ativa
			collectionActive = plcController.IsCollectionActive()

			// Verificar parada de emergência
			if plcController.IsEmergencyStop() {
				fmt.Println("🚨 PARADA DE EMERGÊNCIA ATIVADA VIA PLC")
				time.Sleep(2 * time.Second)
				continue
			}

			// Atualizar modo debug
			radarSick.SetDebugMode(plcController.IsDebugMode())
		}

		// Se coleta não está ativa, aguardar
		if !collectionActive {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// ========== VERIFICAR CONEXÃO E RECONECTAR RADAR SE NECESSÁRIO ==========
		if !radarSick.IsConnected() {
			if !isReconnecting {
				fmt.Println("🔄 Radar desconectado - iniciando reconexão...")
				isReconnecting = true

				// Tentar reconectar em goroutine
				go func() {
					defer func() { isReconnecting = false }()

					for attempt := 1; attempt <= 10; attempt++ {
						fmt.Printf("🔄 RADAR: Tentativa de reconexão %d/10\n", attempt)

						// Desconectar conexão atual
						radarSick.Disconnect()
						time.Sleep(3 * time.Second)

						// Tentar reconectar
						err := radarSick.Connect()
						if err != nil {
							fmt.Printf("❌ RADAR: Tentativa %d falhou: %v\n", attempt, err)
							continue
						}

						// Tentar reiniciar medição
						err = radarSick.StartMeasurement()
						if err != nil {
							fmt.Printf("❌ RADAR: Falha ao reiniciar medição: %v\n", err)
							radarSick.Disconnect()
							continue
						}

						// Sucesso!
						fmt.Printf("✅ RADAR: Reconectado com sucesso na tentativa %d\n", attempt)
						consecutiveErrors = 0
						return
					}

					fmt.Println("❌ RADAR: Falha ao reconectar após 10 tentativas")
				}()
			}
			time.Sleep(1 * time.Second)
			continue
		}

		// ========== LEITURA COM DETECÇÃO DE ERRO ==========
		data, err := radarSick.ReadData()

		if err != nil {
			consecutiveErrors++

			// Verificar se é erro de conexão perdida
			if isConnectionError(err) {
				if consecutiveErrors >= 5 {
					fmt.Printf("🔴 RADAR: Conexão perdida detectada após %d erros consecutivos\n", consecutiveErrors)
					fmt.Printf("🔴 RADAR: Último erro: %v\n", err)

					// Marcar radar como desconectado para trigger reconexão
					radarSick.Connected = false
					consecutiveErrors = 0
				}
			}
			continue
		}

		// Reset contador de erros se leitura bem sucedida
		if consecutiveErrors > 0 {
			fmt.Println("✅ RADAR: Conexão estável - resetando contador de erros")
			consecutiveErrors = 0
		}
		// ===============================================

		if data != nil && len(data) > 0 {
			// Incrementar contador no PLC (só se conectado)
			if plcController != nil && plcSiemens.IsConnected() {
				plcController.IncrementPacketCount()
			}

			// Processar dados recebidos
			positions, velocities, azimuths, amplitudes, objPrincipal := radarSick.ProcessData(data)

			// Exibir dados no terminal
			radarSick.DisplayData(
				positions, velocities, azimuths, amplitudes, objPrincipal,
				plcSiemens.IsConnected(), plcIP,
				wsManager.GetConnectedCount(),
				natsPublisher.IsConnected(),
			)

			// Criar estrutura de dados
			radarData := models.RadarData{
				Positions:  positions,
				Velocities: velocities,
				Azimuths:   azimuths,
				Amplitudes: amplitudes,
				MainObject: objPrincipal,
				PLCStatus:  plcSiemens.GetConnectionStatus(),
				Timestamp:  time.Now().UnixNano() / int64(time.Millisecond),
			}

			// Enviar dados via WebSocket (SEMPRE funciona)
			wsManager.BroadcastData(radarData)

			// Enviar dados via NATS (se conectado)
			if natsPublisher.IsEnabled() {
				err := natsPublisher.Publish(radarData)
				if err != nil {
					log.Printf("Erro ao publicar no NATS: %v", err)
				}
			}

			// ========== ATUALIZAR PLC (COM PROTEÇÃO) ==========
			if plcController != nil && plcSiemens.IsConnected() {
				// Escrever dados do radar no PLC
				err := plcController.WriteRadarData(radarData)
				if err != nil {
					plcConsecutiveErrors++
					log.Printf("Erro ao escrever dados do radar no PLC: %v", err)

					// Se muitos erros consecutivos, marcar PLC como desconectado
					if plcConsecutiveErrors >= 3 {
						fmt.Printf("🔴 PLC: Conexão perdida detectada após %d erros consecutivos\n", plcConsecutiveErrors)
						plcSiemens.Connected = false // Trigger reconexão PLC
						plcConsecutiveErrors = 0
					}
				} else {
					// Reset contador de erros PLC se escrita bem sucedida
					if plcConsecutiveErrors > 0 {
						fmt.Println("✅ PLC: Conexão estável - resetando contador de erros")
						plcConsecutiveErrors = 0
					}

					// Atualizar status dos componentes
					plcController.SetRadarConnected(radarSick.IsConnected())
					plcController.SetNATSConnected(natsPublisher.IsConnected())
					plcController.SetWebSocketRunning(true)
					plcController.UpdateWebSocketClients(wsManager.GetConnectedCount())
				}
			} else if plcSiemens != nil && !plcSiemens.IsConnected() {
				// PLC desconectado, mas radar funcionando
				if plcConsecutiveErrors == 0 {
					fmt.Println("⚠️ PLC desconectado - dados do radar continuam sendo coletados")
					plcConsecutiveErrors = 1 // Marcar que já avisou
				}
			}
			// ================================================
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Cleanup
	fmt.Println("Encerrando programa...")

	if plcController != nil {
		plcController.Stop()
	}

	if radarSick.IsConnected() {
		radarSick.Disconnect()
	}
	if plcSiemens.IsConnected() {
		plcSiemens.Disconnect()
	}
	if natsPublisher.IsConnected() {
		natsPublisher.Disconnect()
	}
}

// connectRadarWithRetry tenta conectar ao radar com retry
func connectRadarWithRetry(radar *radar.SICKRadar, maxRetries int) error {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("🔄 Tentativa de conexão %d/%d com o radar...\n", attempt, maxRetries)

		err := radar.Connect()
		if err == nil {
			// Sucesso - tentar iniciar medição
			err = radar.StartMeasurement()
			if err == nil {
				fmt.Println("✅ Radar conectado e medição iniciada com sucesso")
				return nil
			} else {
				fmt.Printf("❌ Falha ao iniciar medição: %v\n", err)
				radar.Disconnect()
			}
		} else {
			fmt.Printf("❌ Falha na conexão: %v\n", err)
		}

		if attempt < maxRetries {
			fmt.Printf("⏳ Aguardando 3 segundos antes da próxima tentativa...\n")
			time.Sleep(3 * time.Second)
		}
	}

	return fmt.Errorf("falha ao conectar após %d tentativas", maxRetries)
}

// isConnectionError verifica se o erro é de conexão perdida
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Verificar tipos comuns de erro de conexão
	connectionErrors := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"wsarecv",
		"host remoto",
		"cancelamento",
		"forcibly closed",
		"network unreachable",
		"no route to host",
	}

	for _, connErr := range connectionErrors {
		if strings.Contains(errStr, connErr) {
			return true
		}
	}

	// Verificar se é erro de rede
	if netErr, ok := err.(net.Error); ok {
		return netErr.Timeout() || !netErr.Temporary()
	}

	return false
}
