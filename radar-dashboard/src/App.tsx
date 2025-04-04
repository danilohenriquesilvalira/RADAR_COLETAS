import { useEffect, useState, useRef } from 'react';
import { toast } from 'react-toastify';
import Dashboard from './components/Dashboard';
import useWebSocket from './hooks/useWebSocket';
import useRadarStore from './stores/radarStore';

function App() {
  const { connectionStatus } = useRadarStore();
  const { connect } = useWebSocket();
  const [errorShown, setErrorShown] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const toastIdRef = useRef<string | number | null>(null);

  // Conectar ao WebSocket quando o componente montar
  useEffect(() => {
    const cleanup = connect();
    
    // Monitorar erros não tratados para evitar travamentos
    const handleGlobalError = (event: ErrorEvent) => {
      console.error('Erro global capturado:', event.error);
      toast.error('Ocorreu um erro inesperado. Verifique o console para detalhes.', {
        autoClose: 5000,
      });
      event.preventDefault(); // Evita que o erro seja mostrado no console novamente
    };
    
    const handleUnhandledRejection = (event: PromiseRejectionEvent) => {
      console.error('Promessa rejeitada não tratada:', event.reason);
      toast.error('Ocorreu um erro assíncrono não tratado. Verifique o console para detalhes.', {
        autoClose: 5000,
      });
      event.preventDefault(); // Evita que o erro seja mostrado no console novamente
    };
    
    window.addEventListener('error', handleGlobalError);
    window.addEventListener('unhandledrejection', handleUnhandledRejection);
    
    // Limpeza
    return () => {
      cleanup();
      window.removeEventListener('error', handleGlobalError);
      window.removeEventListener('unhandledrejection', handleUnhandledRejection);
      
      // Limpar toasts pendentes
      if (toastIdRef.current !== null) {
        toast.dismiss(toastIdRef.current);
      }
    };
  }, [connect]);

  // Exibir notificações para mudanças de estado da conexão
  useEffect(() => {
    if (connectionStatus === 'connected') {
      // Fechar toast de erro anterior se existir
      if (toastIdRef.current !== null) {
        toast.dismiss(toastIdRef.current);
        toastIdRef.current = null;
      }
      
      if (reconnecting) {
        toast.success('Reconectado ao servidor com sucesso!', {
          position: "top-right",
          autoClose: 2000,
        });
        setReconnecting(false);
      } else {
        toast.success('Conectado ao servidor com sucesso!', {
          position: "top-right",
          autoClose: 2000,
        });
      }
      setErrorShown(false);
    } else if (connectionStatus === 'disconnected') {
      if (!errorShown) {
        // Armazenar o ID do toast para poder fechá-lo depois
        toastIdRef.current = toast.warning('Desconectado do servidor. Tentando reconectar...', {
          position: "top-right",
          autoClose: false,
        });
        setErrorShown(true);
        setReconnecting(true);
      }
    } else if (connectionStatus === 'error') {
      if (!errorShown) {
        // Armazenar o ID do toast para poder fechá-lo depois
        toastIdRef.current = toast.error('Erro na conexão com o servidor!', {
          position: "top-right",
          autoClose: false,
        });
        setErrorShown(true);
      }
    }
  }, [connectionStatus, errorShown, reconnecting]);

  return (
    <Dashboard />
  );
}

export default App;