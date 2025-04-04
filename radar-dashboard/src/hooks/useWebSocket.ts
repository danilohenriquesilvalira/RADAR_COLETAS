import { useCallback, useEffect } from 'react';
import websocketService from '../services/websocket';
import useRadarStore from '../stores/radarStore';

// Hook personalizado para gerenciar a conexão WebSocket
const useWebSocket = () => {
  const { updateData, setConnectionStatus } = useRadarStore();

  // Função para conectar ao WebSocket
  const connect = useCallback(() => {
    // Registrar callbacks
    const statusUnsubscribe = websocketService.onStatusChange(setConnectionStatus);
    const messageUnsubscribe = websocketService.onMessage(updateData);
    
    // Iniciar conexão
    websocketService.connect();
    
    // Limpar na desmontagem
    return () => {
      statusUnsubscribe();
      messageUnsubscribe();
      websocketService.disconnect();
    };
  }, [updateData, setConnectionStatus]);

  // Efeito para limpar na desmontagem
  useEffect(() => {
    return () => {
      websocketService.disconnect();
    };
  }, []);

  // Retorna as funções e estados relacionados ao WebSocket
  return {
    connect,
    disconnect: websocketService.disconnect,
    isConnected: websocketService.isConnected,
  };
};

export default useWebSocket;