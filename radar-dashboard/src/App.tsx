import { useEffect } from 'react'
import { toast } from 'react-toastify'
import Dashboard from './components/Dashboard'
import useWebSocket from './hooks/useWebSocket.ts'
import useRadarStore from './stores/radarStore'

function App() {
  const { connectionStatus } = useRadarStore()
  const { connect } = useWebSocket()

  // Conectar ao WebSocket quando o componente montar
  useEffect(() => {
    connect()
  }, [connect])

  // Exibir notificações para mudanças de estado da conexão
  useEffect(() => {
    if (connectionStatus === 'connected') {
      toast.success('Conectado ao servidor com sucesso!', {
        position: "top-right",
        autoClose: 3000,
      })
    } else if (connectionStatus === 'disconnected') {
      toast.warning('Desconectado do servidor. Tentando reconectar...', {
        position: "top-right",
        autoClose: false,
      })
    } else if (connectionStatus === 'error') {
      toast.error('Erro na conexão com o servidor!', {
        position: "top-right",
        autoClose: false,
      })
    }
  }, [connectionStatus])

  return (
    <Dashboard />
  )
}

export default App