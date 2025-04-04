import { RadarData } from '../types'

type MessageCallback = (data: RadarData) => void
type StatusCallback = (status: 'connected' | 'disconnected' | 'error') => void

class WebSocketService {
  private socket: WebSocket | null = null
  private messageCallbacks: MessageCallback[] = []
  private statusCallbacks: StatusCallback[] = []
  private reconnectTimeout: number | null = null
  private url: string

  constructor(url: string = `ws://${window.location.hostname}:8080/ws`) {
    this.url = url
  }

  // Conectar ao WebSocket
  connect(): void {
    if (this.socket) {
      this.socket.close()
    }

    try {
      this.socket = new WebSocket(this.url)

      this.socket.onopen = () => {
        console.log('WebSocket conectado')
        this.notifyStatusChange('connected')
        if (this.reconnectTimeout) {
          clearTimeout(this.reconnectTimeout)
          this.reconnectTimeout = null
        }
      }

      this.socket.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data) as RadarData
          this.notifyMessageCallbacks(data)
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

  // Agendar reconexão automática
  private scheduleReconnect(): void {
    if (!this.reconnectTimeout) {
      this.reconnectTimeout = window.setTimeout(() => {
        console.log('Tentando reconectar...')
        this.connect()
      }, 3000)
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
    this.messageCallbacks.forEach(callback => callback(data))
  }

  // Notificar todos os callbacks de status
  private notifyStatusChange(status: 'connected' | 'disconnected' | 'error'): void {
    this.statusCallbacks.forEach(callback => callback(status))
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