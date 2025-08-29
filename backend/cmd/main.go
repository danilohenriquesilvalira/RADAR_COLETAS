package main

import (
	"fmt"
	"log"
	"os"
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

	// ========== INICIALIZAR CONTROLADOR PLC REAL PRIMEIRO ==========
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

		// Aguardar um pouco para o PLC inicializar
		time.Sleep(2 * time.Second)
	}

	// ========== CONEXÃO INTELIGENTE DOS RADARES ==========
	fmt.Println("Verificando enables do PLC antes de conectar radares...")

	var enabledRadars map[string]bool
	if plcController != nil {
		// Obter enables do PLC ANTES de tentar conectar
		enabledRadars = plcController.GetRadarsEnabled()
		fmt.Printf("📋 Status PLC: Caldeira=%t, Porta Jusante=%t, Porta Montante=%t\n",
			enabledRadars["caldeira"], enabledRadars["porta_jusante"], enabledRadars["porta_montante"])
	} else {
		// Se PLC não conectado, considerar todos habilitados
		enabledRadars = map[string]bool{
			"caldeira":       true,
			"porta_jusante":  true,
			"porta_montante": true,
		}
		fmt.Println("⚠️ PLC desconectado - tentando conectar todos os radares")
	}

	// Conectar APENAS radares habilitados
	connectionErrors := make(map[string]error)
	for id, enabled := range enabledRadars {
		if enabled {
			config, _ := radarManager.GetRadarConfig(id)
			radar, _ := radarManager.GetRadar(id)
			fmt.Printf("🔄 Conectando radar HABILITADO: %s...\n", config.Name)

			err := radarManager.ConnectRadarWithRetry(radar, 3)
			if err != nil {
				connectionErrors[id] = err
				fmt.Printf("❌ Falha ao conectar radar %s: %v\n", config.Name, err)
			} else {
				fmt.Printf("✅ Radar %s conectado com sucesso\n", config.Name)
			}
		} else {
			config, _ := radarManager.GetRadarConfig(id)
			fmt.Printf("⚫ Radar %s DESABILITADO - não conectando\n", config.Name)
		}
	}

	if len(connectionErrors) > 0 {
		fmt.Printf("❌ Alguns radares habilitados falharam na conexão:\n")
		for id, err := range connectionErrors {
			config, _ := radarManager.GetRadarConfig(id)
			fmt.Printf("   - %s: %v\n", config.Name, err)
		}
	}
	// ================================================

	// Limpar conexões WebSocket anteriores
	wsManager.LimparConexoesAnteriores()

	fmt.Println("\n🚀 Sistema iniciado com CONTROLE INTELIGENTE")
	fmt.Println("📡 Monitoramento baseado em enables do PLC")
	fmt.Println("⚡ Economia de recursos - só conecta radares habilitados")
	if plcController != nil {
		fmt.Println("🎛️ PLC REAL conectado - controle via DB100")
	} else {
		fmt.Println("⚠️  PLC desconectado - modo manual")
	}
	fmt.Println("\nPressione Ctrl+C para parar.")

	// ========== LOOP PRINCIPAL COM CONTROLE INTELIGENTE ==========

	for {
		// Verificar comandos do PLC
		collectionActive := true
		var enabledRadars map[string]bool

		if plcController != nil {
			// Verificar se coleta está ativa
			collectionActive = plcController.IsCollectionActive()

			// Obter status de habilitação dos radares do PLC
			enabledRadars = plcController.GetRadarsEnabled()

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

		// ========== COLETAR DADOS APENAS DOS RADARES HABILITADOS ==========
		multiRadarData := radarManager.CollectEnabledRadarsData(enabledRadars)

		// Limpar terminal e imprimir dashboard
		fmt.Print("\033[H\033[2J") // Limpa terminal
		fmt.Println("===== STATUS DO SISTEMA RADAR =====")
		connectionStatus := radarManager.GetConnectionStatus()
		connectedCount := 0
		enabledCount := 0

		for id, connected := range connectionStatus {
			config, _ := radarManager.GetRadarConfig(id)
			isEnabled := enabledRadars[id]

			if isEnabled {
				enabledCount++
			}
			if connected && isEnabled {
				connectedCount++
			}

			status := "🔴 DESCONECTADO"
			if !isEnabled {
				status = "⚫ DESABILITADO"
			} else if connected {
				status = "🟢 CONECTADO"
			}
			fmt.Printf("📡 %s: %s\n", config.Name, status)
		}

		fmt.Printf("\n📊 Radares: %d/%d habilitados, %d conectados | WebSocket: %d clientes\n",
			enabledCount, 3, connectedCount, wsManager.GetConnectedCount(),
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

}
