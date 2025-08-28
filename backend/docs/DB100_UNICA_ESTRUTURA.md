# DB 100 - ESTRUTURA ÚNICA PARA 3 RADARES

## 📋 VISÃO GERAL
**UMA DB ÚNICA** para gerenciar:
- ✅ **3 Radares** (Caldeira, Porta Jusante, Porta Montante)
- ✅ **Controles individuais** por radar
- ✅ **Status de conexão** por radar  
- ✅ **Dados principais** de cada radar
- ✅ **Comandos do sistema**

---

## 🗺️ MAPEAMENTO DB 100

### **BYTES 0-2: COMANDOS GLOBAIS E INDIVIDUAIS**

| Byte.Bit | Campo | Função |
|----------|-------|--------|
| **BYTE 0 - COMANDOS GLOBAIS** |
| DB100.0.0 | StartCollection | ▶️ Iniciar coleta geral |
| DB100.0.1 | StopCollection | ⏹️ Parar coleta geral |
| DB100.0.2 | RestartSystem | 🔄 Reiniciar sistema completo |
| DB100.0.3 | ResetErrors | 🧹 Reset todos os erros |
| DB100.0.4 | EnableDebugMode | 🐛 Ativar modo debug |
| DB100.0.5 | Emergency | 🚨 Parada de emergência |
| DB100.0.6 | RestartNATS | 🔄 Reiniciar NATS |
| DB100.0.7 | RestartWebSocket | 🔄 Reiniciar WebSocket |

| **BYTE 1 - CONTROLE RADARES** |
| DB100.1.0 | EnableRadarCaldeira | 🎯 Habilitar Caldeira |
| DB100.1.1 | EnableRadarPortaJusante | 🎯 Habilitar Porta Jusante |
| DB100.1.2 | EnableRadarPortaMontante | 🎯 Habilitar Porta Montante |
| DB100.1.3 | RestartRadarCaldeira | 🔄 Reconectar Caldeira |
| DB100.1.4 | RestartRadarPortaJusante | 🔄 Reconectar Porta Jusante |
| DB100.1.5 | RestartRadarPortaMontante | 🔄 Reconectar Porta Montante |
| DB100.1.6 | ResetErrorsCaldeira | 🧹 Reset erros Caldeira |
| DB100.1.7 | ResetErrorsPortaJusante | 🧹 Reset erros Porta Jusante |

| **BYTE 2 - STATUS/RESERVA** |
| DB100.2.0 | ResetErrorsPortaMontante | 🧹 Reset erros Porta Montante |
| DB100.2.1-7 | Reservados | 🔒 Para expansão futura |

---

### **BYTES 4-63: STATUS DO SISTEMA** (60 bytes)

| Offset | Tamanho | Campo | Descrição |
|--------|---------|-------|-----------|
| **BYTE 4 - STATUS GERAL** |
| DB100.4.0 | 1 bit | LiveBit | 💓 Bit de vida do sistema |
| DB100.4.1 | 1 bit | PLCConnected | 🔗 PLC conectado |
| DB100.4.2 | 1 bit | NATSConnected | 🔗 NATS conectado |
| DB100.4.3 | 1 bit | WebSocketRunning | 🔗 WebSocket ativo |
| DB100.4.4 | 1 bit | CollectionActive | ▶️ Coleta ativa |
| DB100.4.5 | 1 bit | SystemHealthy | ✅ Sistema saudável |
| DB100.4.6 | 1 bit | DebugModeActive | 🐛 Debug ativo |
| DB100.4.7 | 1 bit | EmergencyActive | 🚨 Emergência ativa |

| **BYTE 5 - STATUS RADARES** |
| DB100.5.0 | 1 bit | RadarCaldeiraConnected | 📡 Caldeira conectado |
| DB100.5.1 | 1 bit | RadarPortaJusanteConnected | 📡 Porta Jusante conectado |
| DB100.5.2 | 1 bit | RadarPortaMontanteConnected | 📡 Porta Montante conectado |
| DB100.5.3-7 | - | Reservados | 🔒 Reserva |

| **BYTES 6-63 - CONTADORES E MÉTRICAS** |
| DB100.6 | DINT | WebSocketClients | 👥 Clientes conectados |
| DB100.10 | DINT | PacketsTotal | 📦 Total de pacotes |
| DB100.14 | DINT | ErrorsTotal | ❌ Total de erros |
| DB100.18 | DINT | UptimeSeconds | ⏱️ Tempo ativo (segundos) |
| DB100.22 | DINT | PacketsCaldeira | 📦 Pacotes Caldeira |
| DB100.26 | DINT | PacketsPortaJusante | 📦 Pacotes Porta Jusante |
| DB100.30 | DINT | PacketsPortaMontante | 📦 Pacotes Porta Montante |
| DB100.34 | DINT | ErrorsCaldeira | ❌ Erros Caldeira |
| DB100.38 | DINT | ErrorsPortaJusante | ❌ Erros Porta Jusante |
| DB100.42 | DINT | ErrorsPortaMontante | ❌ Erros Porta Montante |
| DB100.46 | REAL | CPUUsage | 💻 Uso CPU (%) |
| DB100.50 | REAL | MemoryUsage | 🧠 Uso Memória (%) |
| DB100.54 | REAL | DiskUsage | 💾 Uso Disco (%) |
| DB100.58 | DINT | TimestampHigh | ⏰ Timestamp (high) |
| DB100.62 | DINT | TimestampLow | ⏰ Timestamp (low) |

---

### **BYTES 64-223: DADOS RADAR CALDEIRA** (80 bytes)

| Offset | Tamanho | Campo | Descrição |
|--------|---------|-------|-----------|
| DB100.64.0 | BOOL | MainObjectDetected | 🎯 Objeto principal detectado |
| DB100.66 | REAL | MainObjectAmplitude | 📊 Amplitude |
| DB100.70 | REAL | MainObjectDistance | 📏 Distância (m) |
| DB100.74 | REAL | MainObjectVelocity | 🏃 Velocidade (m/s) |
| DB100.78 | REAL | MainObjectAngle | 🔄 Ângulo (°) |
| DB100.82 | INT | ObjectsDetected | 🔢 Qtd objetos detectados |
| DB100.84 | REAL | MaxAmplitude | 📈 Amplitude máxima |
| DB100.88 | REAL | MinDistance | 📏 Distância mínima |
| DB100.92 | REAL | MaxDistance | 📏 Distância máxima |
| DB100.96 | REAL[5] | Positions | 📍 Posições (5 objetos) |
| DB100.116 | REAL[5] | Velocities | 🏃 Velocidades (5 objetos) |
| DB100.136 | DINT | DataTimestampHigh | ⏰ Timestamp dados (high) |
| DB100.140 | DINT | DataTimestampLow | ⏰ Timestamp dados (low) |
| DB100.144 | - | Reserva | 🔒 4 bytes reserva |

---

### **BYTES 144-223: DADOS RADAR PORTA JUSANTE** (80 bytes)
*Mesma estrutura do Radar Caldeira*

---

### **BYTES 224-303: DADOS RADAR PORTA MONTANTE** (80 bytes)  
*Mesma estrutura do Radar Caldeira*

---

## 📊 RESUMO FINAL

### **TAMANHO TOTAL: 304 bytes**
- **Comandos**: 3 bytes
- **Status Sistema**: 60 bytes  
- **Dados Caldeira**: 80 bytes
- **Dados Porta Jusante**: 80 bytes
- **Dados Porta Montante**: 80 bytes
- **Reserva**: 1 byte

### **VANTAGENS:**
✅ **Uma DB única** - fácil de gerenciar  
✅ **Controle individual** de cada radar  
✅ **Status completo** do sistema  
✅ **Dados detalhados** de cada radar  
✅ **Contadores separados** por radar  
✅ **Compatível** com estrutura atual  

### **IPs DOS RADARES:**
- **Caldeira**: 192.168.1.84:2111
- **Porta Jusante**: 192.168.1.85:2111  
- **Porta Montante**: 192.168.1.86:2111