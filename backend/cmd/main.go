package main

import (
	"fmt"
	"log"
	"os"
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

	fmt.Println("\n===== RADAR SICK RMS1000 =====")
	fmt.Println("Servidor Web/WebSocket iniciado em http://localhost:8080")
	fmt.Println("Dados do radar estão disponíveis via WebSocket em ws://localhost:8080/ws")

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

	// Conectar ao radar automaticamente
	fmt.Println("Conectando ao radar automaticamente...")
	if err := radarSick.Connect(); err != nil {
		fmt.Println("Falha ao conectar. Tentando novamente em 5 segundos...")
		time.Sleep(5 * time.Second)
		if err := radarSick.Connect(); err != nil {
			fmt.Println("Não foi possível conectar ao radar. Verifique o IP e a conexão.")
			os.Exit(1)
		}
	}

	// Tentar conectar ao PLC
	fmt.Println("Conectando ao PLC Siemens...")
	err = plcSiemens.Connect()
	if err != nil {
		fmt.Printf("Aviso: %v - Continuando sem conexão com o PLC\n", err)
	}

	// Limpar conexões WebSocket anteriores
	wsManager.LimparConexoesAnteriores()

	// Configurar para leitura contínua
	err = radarSick.SetupContinuousReading()
	if err != nil {
		fmt.Printf("Erro ao configurar leitura contínua: %v\n", err)
		return
	}

	// Iniciar medição
	err = radarSick.StartMeasurement()
	if err != nil {
		fmt.Printf("Erro ao iniciar medição: %v\n", err)
		return
	}

	fmt.Println("Monitorando dados do radar. Pressione Ctrl+C para parar.")

	// Loop principal de coleta de dados
	radarSick.SetDebugMode(false) // Modo visualização limpa

	for {
		// Ler dados do radar
		data, err := radarSick.ReadData()
		if err != nil {
			fmt.Printf("\nErro de leitura: %v\n", err)
			continue
		}

		if data != nil && len(data) > 0 {
			// Processar dados recebidos
			positions, velocities, azimuths, amplitudes, objPrincipal := radarSick.ProcessData(data)

			// Exibir dados no terminal
			radarSick.DisplayData(
				positions, velocities, azimuths, amplitudes, objPrincipal,
				plcSiemens.IsConnected(), plcIP,
				wsManager.GetConnectedCount(),
				natsPublisher.IsConnected(),
			)

			// Obter status do PLC
			plcStatus := plcSiemens.GetConnectionStatus()

			// Criar estrutura de dados para envio
			radarData := models.RadarData{
				Positions:  positions,
				Velocities: velocities,
				Azimuths:   azimuths,
				Amplitudes: amplitudes,
				MainObject: objPrincipal,
				PLCStatus:  plcStatus,
				Timestamp:  time.Now().UnixNano() / int64(time.Millisecond),
			}

			// Enviar dados via WebSocket (mantém funcionamento original)
			wsManager.BroadcastData(radarData)

			// Enviar dados via NATS (nova funcionalidade, opcional)
			if natsPublisher.IsEnabled() {
				err := natsPublisher.Publish(radarData)
				if err != nil {
					log.Printf("Erro ao publicar no NATS: %v", err)
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Cleanup (nunca alcançado devido ao loop infinito)
	fmt.Println("Encerrando programa...")
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
