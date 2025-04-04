import { RadarData } from '../types'

type MessageCallback = (data: RadarData) => void
type StatusCallback = (status: 'connected' | 'disconnected' | 'error') => void

class WebSocketService {
  private socket: WebSocket | null = null
  private messageCallbacks: MessageCallback[] = []
  private statusCallbacks: StatusCallback[] = []
  private reconnectTimeout: number | null = null
  private url: string
  private messageQueue: RadarData[] = []
  private processingQueue: boolean = false
  private lastProcessedTime: number = 0
  private reconnectAttempts: number = 0
  private maxReconnectAttempts: number = 10
  private baseReconnectDelay: number = 1000

  constructor(url: string = `ws://${window.location.hostname}:8080/ws`) {
    this.url = url
    this.startQueueProcessor()
  }

  // Conectar ao WebSocket
  connect(): void {
    if (this.socket) {
      this.socket.close()
    }

    try {
      console.log('Tentando conectar ao WebSocket...')
      this.socket = new WebSocket(this.url)

      this.socket.onopen = () => {
        console.log('WebSocket conectado')
        this.reconnectAttempts = 0
        this.notifyStatusChange('connected')
        if (this.reconnectTimeout) {
          clearTimeout(this.reconnectTimeout)
          this.reconnectTimeout = null
        }
      }

      this.socket.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data) as RadarData
          this.messageQueue.push(data)
        } catch (error) {
          console.error('Erro ao processar mensagem:', error)
        }
      }

      this.socket.onerror = (error) => {
        console.error('Erro de WebSocket:', error)
        this.notifyStatusChange('error')
      }

      this.socket.onclose = () => {
        console.log('WebSocket desconectado')
        this.notifyStatusChange('disconnected')
        this.scheduleReconnect()
      }
    } catch (error) {
      console.error('Erro ao criar WebSocket:', error)
      this.scheduleReconnect()
    }
  }

  // Processa a fila de mensagens usando throttling para não sobrecarregar
  private startQueueProcessor(): void {
    const processQueue = () => {
      // Limitar a frequência de processamento para no máximo uma vez a cada 100ms
      const now = Date.now()
      const timeSinceLastProcess = now - this.lastProcessedTime
      
      if (this.messageQueue.length > 0 && !this.processingQueue && timeSinceLastProcess >= 100) {
        this.processingQueue = true
        this.lastProcessedTime = now
        
        // Usar requestAnimationFrame para sincronizar com a renderização do navegador
        requestAnimationFrame(() => {
          // Se houver mais de uma mensagem na fila, pegar apenas a mais recente
          if (this.messageQueue.length > 1) {
            const latestMessage = this.messageQueue.pop()
            this.messageQueue = [] // Limpar fila antiga
            if (latestMessage) {
              this.notifyMessageCallbacks(latestMessage)
            }
          } else if (this.messageQueue.length === 1) {
            const message = this.messageQueue.pop()
            if (message) {
              this.notifyMessageCallbacks(message)
            }
          }
          
          this.processingQueue = false
        })
      }
      
      // Agendar próxima verificação
      setTimeout(processQueue, 50)
    }
    
    // Iniciar o processador
    processQueue()
  }

  // Agendar reconexão com backoff exponencial
  private scheduleReconnect(): void {
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
    }
    
    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      // Aplicar backoff exponencial com jitter
      const delay = Math.min(
        30000, // Limitar a no máximo 30 segundos
        this.baseReconnectDelay * Math.pow(1.5, this.reconnectAttempts) * 
          (0.9 + Math.random() * 0.2) // Jitter de ±10%
      )
      
      this.reconnectAttempts++
      
      console.log(`Tentando reconectar em ${Math.round(delay)}ms (tentativa ${this.reconnectAttempts})`)
      
      this.reconnectTimeout = window.setTimeout(() => {
        console.log('Tentando reconectar...')
        this.connect()
      }, delay)
    } else {
      console.error('Número máximo de tentativas de reconexão atingido')
    }
  }

  // Registrar callback para mensagens
  onMessage(callback: MessageCallback): () => void {
    this.messageCallbacks.push(callback)
    return () => {
      this.messageCallbacks = this.messageCallbacks.filter(cb => cb !== callback)
    }
  }

  // Registrar callback para mudanças de status
  onStatusChange(callback: StatusCallback): () => void {
    this.statusCallbacks.push(callback)
    return () => {
      this.statusCallbacks = this.statusCallbacks.filter(cb => cb !== callback)
    }
  }

  // Notificar todos os callbacks de mensagem
  private notifyMessageCallbacks(data: RadarData): void {
    this.messageCallbacks.forEach(callback => {
      try {
        callback(data)
      } catch (error) {
        console.error('Erro ao processar callback de mensagem:', error)
      }
    })
  }

  // Notificar todos os callbacks de status
  private notifyStatusChange(status: 'connected' | 'disconnected' | 'error'): void {
    this.statusCallbacks.forEach(callback => {
      try {
        callback(status)
      } catch (error) {
        console.error('Erro ao processar callback de status:', error)
      }
    })
  }

  // Desconectar WebSocket
  disconnect(): void {
    if (this.socket) {
      this.socket.close()
      this.socket = null
    }

    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout)
      this.reconnectTimeout = null
    }
  }

  // Verificar se está conectado
  isConnected(): boolean {
    return this.socket !== null && this.socket.readyState === WebSocket.OPEN
  }
}

// Singleton instance
const websocketService = new WebSocketService()
export default websocketService