import { useState, useEffect, useRef, useCallback } from 'react';
import { RadarData, MultiRadarData, ConnectionStatus } from '../types/radarTypes';
import { usePersistentData } from './usePersistentData';

interface UseWebSocketOptions {
  onMessage?: (data: RadarData | MultiRadarData) => void;
  onOpen?: () => void;
  onClose?: () => void;
  onError?: (error: Event) => void;
  autoConnect?: boolean;
  autoReconnect?: boolean; // Nova opção para controlar reconexão
  maxReconnectAttempts?: number; // Limite de tentativas
  reconnectInterval?: number; // Intervalo de reconexão
}

export const useWebSocket = (initialUrl: string, options?: UseWebSocketOptions) => {
  const [url, setUrl] = useState(initialUrl);
  const [status, setStatus] = useState<ConnectionStatus>(ConnectionStatus.DISCONNECTED);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<string>('Nunca');
  
  // Usar o hook de dados persistentes
  const { persistentData, updateData, isInitialized } = usePersistentData();
  
  // Referências para WebSocket e controle de reconexão
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const heartbeatTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const messageReceivedRef = useRef(false);
  const reconnectAttemptsRef = useRef(0);
  const isManualDisconnectRef = useRef(false);
  
  // Configurações padrão
  const autoConnect = options?.autoConnect ?? false;
  const autoReconnect = options?.autoReconnect ?? true;
  const maxReconnectAttempts = options?.maxReconnectAttempts ?? 10; // Máximo 10 tentativas
  const reconnectInterval = options?.reconnectInterval ?? 3000; // 3 segundos
  
  
  // Formatar hora da última atualização
  const updateLastUpdated = useCallback(() => {
    const now = new Date();
    setLastUpdated(
      now.toLocaleTimeString('pt-BR', { 
        hour: '2-digit', 
        minute: '2-digit', 
        second: '2-digit',
        hour12: false 
      })
    );
  }, []);
  
  // Limpar todos os timeouts
  const clearTimeouts = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
    if (heartbeatTimeoutRef.current) {
      clearTimeout(heartbeatTimeoutRef.current);
      heartbeatTimeoutRef.current = null;
    }
  }, []);
  
  // Configurar heartbeat para verificar se a conexão está ativa
  const setupHeartbeat = useCallback(() => {
    clearTimeouts();
    
    heartbeatTimeoutRef.current = setTimeout(() => {
      // Se não recebemos mensagem por 10 segundos, considerar conexão perdida
      if (!messageReceivedRef.current && status === ConnectionStatus.CONNECTED) {
        console.log('Heartbeat: Nenhuma mensagem recebida. Conexão pode estar perdida.');
        
        if (wsRef.current) {
          wsRef.current.close();
        }
      }
      
      // Reiniciar flag e continuar monitoramento apenas se conectado
      messageReceivedRef.current = false;
      if (status === ConnectionStatus.CONNECTED) {
        setupHeartbeat();
      }
    }, 10000); // Verificar a cada 10 segundos
  }, [status, clearTimeouts]);
  
  // Função para processar dados recebidos
  const processData = useCallback((data: any) => {
    try {
      console.log("Dados recebidos do WebSocket:", data);
      
      // Verificar se são dados de múltiplos radares
      if (data.radars && Array.isArray(data.radars)) {
        // Dados de múltiplos radares
        const safeMultiData: MultiRadarData = {
          radars: data.radars.map((radar: any) => ({
            radarId: radar.radarId || '',
            radarName: radar.radarName || '',
            connected: radar.connected || false,
            positions: Array.isArray(radar.positions) ? radar.positions : [],
            velocities: Array.isArray(radar.velocities) ? radar.velocities : [],
            azimuths: Array.isArray(radar.azimuths) ? radar.azimuths : [],
            amplitudes: Array.isArray(radar.amplitudes) ? radar.amplitudes : [],
            mainObject: radar.mainObject,
            plcStatus: radar.plcStatus,
            timestamp: radar.timestamp || Date.now()
          })),
          timestamp: data.timestamp || Date.now()
        };
        
        // Marcar que recebemos uma mensagem
        messageReceivedRef.current = true;
        
        // Atualizar dados persistentes com o primeiro radar (compatibilidade)
        if (safeMultiData.radars.length > 0) {
          updateData(safeMultiData.radars[0]);
        }
        updateLastUpdated();
        
        // Notificar callback com dados multi-radar
        if (options?.onMessage) options.onMessage(safeMultiData);
      } else {
        // Dados de radar único (formato antigo)
        const safeData: RadarData = {
          radarId: data.radarId || 'caldeira',
          radarName: data.radarName || 'Radar Caldeira',
          connected: data.connected !== undefined ? data.connected : true,
          positions: Array.isArray(data.positions) ? data.positions : [],
          velocities: Array.isArray(data.velocities) ? data.velocities : [],
          azimuths: Array.isArray(data.azimuths) ? data.azimuths : [],
          amplitudes: Array.isArray(data.amplitudes) ? data.amplitudes : [],
          mainObject: data.mainObject,
          plcStatus: data.plcStatus,
          timestamp: data.timestamp || Date.now()
        };
        
        // Debug do PLC status
        if (data.plcStatus) {
          console.log("Status do PLC recebido:", data.plcStatus);
        }
        
        // Marcar que recebemos uma mensagem
        messageReceivedRef.current = true;
        
        // Atualizar dados persistentes
        updateData(safeData);
        updateLastUpdated();
        
        // Notificar callback, se existir
        if (options?.onMessage) options.onMessage(safeData);
      }
    } catch (err) {
      console.error('Erro ao processar mensagem WebSocket:', err);
    }
  }, [updateData, updateLastUpdated, options]);
  
  // Função de reconexão com limite de tentativas
  const scheduleReconnect = useCallback(() => {
    // Não reconectar se foi desconexão manual
    if (isManualDisconnectRef.current) {
      console.log('Reconexão cancelada: desconexão manual');
      return;
    }
    
    // Não reconectar se não está habilitado
    if (!autoReconnect) {
      console.log('Reconexão desabilitada');
      return;
    }
    
    // Verificar limite de tentativas
    if (reconnectAttemptsRef.current >= maxReconnectAttempts) {
      console.log(`Limite de ${maxReconnectAttempts} tentativas de reconexão atingido`);
      setError(`Falha na conexão após ${maxReconnectAttempts} tentativas. Verifique se o servidor está online.`);
      return;
    }
    
    reconnectAttemptsRef.current++;
    console.log(`Agendando reconexão (tentativa ${reconnectAttemptsRef.current}/${maxReconnectAttempts}) em ${reconnectInterval}ms`);
    
    reconnectTimeoutRef.current = setTimeout(() => {
      connect();
    }, reconnectInterval);
  }, [autoReconnect, maxReconnectAttempts, reconnectInterval]);
  
  // Função de conexão
  const connect = useCallback(() => {
    // Limpar timeouts anteriores
    clearTimeouts();
    
    // Se já está conectado ou tentando conectar, não fazer nada
    if (wsRef.current?.readyState === WebSocket.OPEN || 
        wsRef.current?.readyState === WebSocket.CONNECTING) {
      console.log('Já conectado/conectando ao WebSocket');
      return;
    }
    
    // Fechar conexão existente se estiver em estado inválido
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    
    // Marcar como não sendo desconexão manual
    isManualDisconnectRef.current = false;
    
    try {
      setStatus(ConnectionStatus.CONNECTING);
      setError(null);
      
      console.log(`Tentando conectar a: ${url} (tentativa ${reconnectAttemptsRef.current + 1})`);
      const ws = new WebSocket(url);
      
      ws.onopen = () => {
        console.log('Conexão WebSocket aberta com sucesso');
        setStatus(ConnectionStatus.CONNECTED);
        setError(null);
        
        // Reset contador de tentativas em caso de sucesso
        reconnectAttemptsRef.current = 0;
        
        if (options?.onOpen) options.onOpen();
        
        // Iniciar heartbeat
        setupHeartbeat();
      };
      
      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          processData(data);
        } catch (err) {
          console.error('Erro ao processar mensagem:', err);
        }
      };
      
      ws.onclose = (event) => {
        console.log('Conexão WebSocket fechada:', event.code, event.reason);
        setStatus(ConnectionStatus.DISCONNECTED);
        wsRef.current = null;
        clearTimeouts();
        
        if (options?.onClose) options.onClose();
        
        // Tentar reconectar apenas se não foi desconexão manual
        if (!isManualDisconnectRef.current) {
          scheduleReconnect();
        }
      };
      
      ws.onerror = (event) => {
        console.error('Erro WebSocket:', event);
        setStatus(ConnectionStatus.ERROR);
        setError('Erro na conexão WebSocket');
        if (options?.onError) options.onError(event);
        
        // Fechar conexão com erro
        if (wsRef.current) {
          wsRef.current.close();
          wsRef.current = null;
        }
        
        clearTimeouts();
      };
      
      wsRef.current = ws;
    } catch (err) {
      console.error('Erro ao criar conexão WebSocket:', err);
      setStatus(ConnectionStatus.ERROR);
      setError(`Erro ao conectar: ${err}`);
      
      // Agendar reconexão em caso de exceção
      scheduleReconnect();
    }
  }, [url, options, processData, setupHeartbeat, clearTimeouts, scheduleReconnect]);
  
  // Efeito para auto-connect sem dependência circular
  useEffect(() => {
    if (autoConnect && wsRef.current?.readyState !== WebSocket.OPEN) {
      connect();
    }
  }, [autoConnect]);
  
  // Função de desconexão
  const disconnect = useCallback(() => {
    console.log('Desconectando WebSocket manualmente');
    
    // Marcar como desconexão manual
    isManualDisconnectRef.current = true;
    
    // Reset contador de tentativas
    reconnectAttemptsRef.current = 0;
    
    // Limpar timeouts
    clearTimeouts();
    
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
      setStatus(ConnectionStatus.DISCONNECTED);
    }
  }, [clearTimeouts]);
  
  // Função para enviar mensagem
  const sendMessage = useCallback((message: string | object) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      const data = typeof message === 'string' ? message : JSON.stringify(message);
      wsRef.current.send(data);
      return true;
    }
    return false;
  }, []);
  
  // Reset do contador de tentativas quando URL mudar
  useEffect(() => {
    reconnectAttemptsRef.current = 0;
  }, [url]);
  
  // Limpar recursos ao desmontar componente
  useEffect(() => {
    return () => {
      isManualDisconnectRef.current = true;
      clearTimeouts();
      
      if (wsRef.current) {
        wsRef.current.close();
      }
    };
  }, [clearTimeouts]);
  
  return {
    status,
    error,
    lastMessage: persistentData,
    lastUpdated,
    connect,
    disconnect,
    sendMessage,
    setUrl,
    hasData: isInitialized(),
    reconnectAttempts: reconnectAttemptsRef.current,
    maxReconnectAttempts
  };
};