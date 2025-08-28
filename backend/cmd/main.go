package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"backend/internal/plc"
	"backend/internal/radar"
	"backend/internal/websocket"
)

func main() {
	// Configurações
	webDir := "./web"

	fmt.Println("\n===== SISTEMA RADAR SICK - 3 RADARES com AUTO-RECOVERY =====")
	fmt.Println("Servidor Web/WebSocket iniciado em http://localhost:8080")
	fmt.Println("Sistema com reconexão automática para múltiplos radares")

	// Criar gerenciador de radares
	radarManager := radar.NewRadarManager()
	
	// Adicionar os 3 radares
	radarManager.AddRadar(radar.RadarConfig{
		ID:   "caldeira",
		Name: "Radar Caldeira",
		IP:   "192.168.1.84",
		Port: 2111,
	})
	
	radarManager.AddRadar(radar.RadarConfig{
		ID:   "porta_jusante",
		Name: "Radar Porta Jusante",
		IP:   "192.168.1.85",
		Port: 2111,
	})
	
	radarManager.AddRadar(radar.RadarConfig{
		ID:   "porta_montante",
		Name: "Radar Porta Montante",
		IP:   "192.168.1.86",
		Port: 2111,
	})

	// Criar instâncias dos outros componentes
	wsManager := websocket.NewWebSocketManager()

	// Criar diretório para arquivos web se não existir
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		os.Mkdir(webDir, 0755)
	}

	// Iniciar gerenciador de WebSockets em uma goroutine
	go wsManager.Run()

	// Iniciar servidor HTTP/WebSocket em uma goroutine
	go wsManager.ServeHTTP(webDir)


	// ========== CONEXÃO INICIAL DOS RADARES ==========
	fmt.Println("Conectando aos radares com retry automático...")
	connectionErrors := radarManager.ConnectAll()
	if len(connectionErrors) > 0 {
		fmt.Printf("❌ Alguns radares falharam na conexão inicial:\n")
		for id, err := range connectionErrors {
			config, _ := radarManager.GetRadarConfig(id)
			fmt.Printf("   - %s: %v\n", config.Name, err)
		}
		fmt.Println("Sistema continuará tentando reconectar automaticamente...")
	}
	// ==================================================

	// ========== INICIALIZAR CONTROLADOR PLC REAL ==========
	fmt.Println("Conectando ao PLC Siemens 192.168.1.33...")
	plcSiemens := plc.NewSiemensPLC("192.168.1.33")
	
	err := plcSiemens.Connect()
	if err != nil {
		fmt.Printf("Erro ao conectar PLC: %v - Sistema continuará sem PLC\n", err)
	}
	
	var plcController *plc.PLCController
	if plcSiemens.IsConnected() {
		fmt.Println("Inicializando controlador PLC bidirecional...")
		plcController = plc.NewPLCController(plcSiemens.Client)
		
		// Iniciar controlador PLC em goroutine
		go plcController.Start()
		
		fmt.Println("✅ Controlador PLC REAL iniciado - Sistema controlado via DB100")
	}
	// ================================================

	// Limpar conexões WebSocket anteriores
	wsManager.LimparConexoesAnteriores()

	fmt.Println("\n🚀 Sistema iniciado com AUTO-RECOVERY para 3 radares")
	fmt.Println("📡 Monitoramento contínuo ativo")
	fmt.Println("🔄 Reconexão automática habilitada")
	if plcController != nil {
		fmt.Println("🎛️ PLC REAL conectado - dados sendo escritos na DB100")
	} else {
		fmt.Println("⚠️  PLC desconectado")
	}
	fmt.Println("\nPressione Ctrl+C para parar.")

	// Iniciar monitor de reconexão automática
	radarManager.StartReconnectionMonitor()

	// ========== LOOP PRINCIPAL COM AUTO-RECOVERY ==========
	for {
		// Verificar comandos do PLC
		collectionActive := true
		if plcController != nil {
			// Verificar se coleta está ativa
			collectionActive = plcController.IsCollectionActive()

			// Verificar parada de emergência
			if plcController.IsEmergencyStop() {
				fmt.Println("🚨 PARADA DE EMERGÊNCIA ATIVADA VIA PLC")
				time.Sleep(2 * time.Second)
				continue
			}
		}

		// Se coleta não está ativa, aguardar
		if !collectionActive {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// ========== COLETAR DADOS DE TODOS OS RADARES ==========
		multiRadarData := radarManager.CollectAllData()
		
		// Exibir status de conexão dos radares
		connectionStatus := radarManager.GetConnectionStatus()
		connectedCount := 0
		for id, connected := range connectionStatus {
			config, _ := radarManager.GetRadarConfig(id)
			if connected {
				connectedCount++
			}
			status := "🔴 DESCONECTADO"
			if connected {
				status = "🟢 CONECTADO"
			}
			fmt.Printf("📡 %s: %s\n", config.Name, status)
		}
		
		fmt.Printf("📊 Radares conectados: %d/3 | WebSocket clientes: %d\n",
			connectedCount,
			wsManager.GetConnectedCount(),
		)

		// Enviar dados via WebSocket (SEMPRE funciona)
		wsManager.BroadcastMultiRadarData(multiRadarData)

		// ========== ATUALIZAR PLC DB100 ==========
		if plcController != nil {
			// Escrever dados dos radares na DB100
			err := plcController.WriteMultiRadarData(multiRadarData)
			if err != nil {
				log.Printf("Erro ao escrever dados dos radares na DB100: %v", err)
			}

			// Atualizar status dos componentes
			plcController.SetRadarsConnected(connectionStatus)
			plcController.SetNATSConnected(false)
			plcController.SetWebSocketRunning(true)
			plcController.UpdateWebSocketClients(wsManager.GetConnectedCount())
		}
		// ================================================

		time.Sleep(200 * time.Millisecond) // Aumentei um pouco o delay para 3 radares
	}

	// Cleanup
	fmt.Println("Encerrando programa...")

	if plcController != nil {
		plcController.Stop()
	}
	
	if plcSiemens.IsConnected() {
		plcSiemens.Disconnect()
	}

	// Desconectar todos os radares
	radarManager.DisconnectAll()

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
