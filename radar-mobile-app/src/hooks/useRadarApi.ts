import { useState, useCallback } from 'react';

interface UseRadarApiOptions {
  onStartSuccess?: () => void;
  onStartError?: (error: string) => void;
  onStopSuccess?: () => void;
  onStopError?: (error: string) => void;
}

export const useRadarApi = (serverUrl: string, options?: UseRadarApiOptions) => {
  const [isCollecting, setIsCollecting] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  
  // Extrair a URL base do WebSocket
  const getBaseUrl = useCallback(() => {
    // Remover 'ws://' ou 'wss://' e '/ws' do final
    let baseUrl = serverUrl;
    if (baseUrl.startsWith('ws://')) {
      baseUrl = baseUrl.substring(5);
    } else if (baseUrl.startsWith('wss://')) {
      baseUrl = baseUrl.substring(6);
    }
    
    if (baseUrl.endsWith('/ws')) {
      baseUrl = baseUrl.substring(0, baseUrl.length - 3);
    }
    
    return `http://${baseUrl}`;
  }, [serverUrl]);
  
  // Iniciar coleta de dados
  const startCollection = useCallback(async () => {
    try {
      setIsLoading(true);
      setError(null);
      
      const baseUrl = getBaseUrl();
      const response = await fetch(`${baseUrl}/api/start-collection`, {
        method: 'POST',
      });
      
      if (!response.ok) {
        throw new Error(`Erro HTTP: ${response.status}`);
      }
      
      setIsCollecting(true);
      if (options?.onStartSuccess) options.onStartSuccess();
    } catch (err) {
      console.error('Erro ao iniciar coleta:', err);
      const errorMsg = err instanceof Error ? err.message : 'Erro desconhecido';
      setError(`Falha ao iniciar coleta: ${errorMsg}`);
      if (options?.onStartError) options.onStartError(errorMsg);
    } finally {
      setIsLoading(false);
    }
  }, [getBaseUrl, options]);
  
  // Parar coleta de dados
  const stopCollection = useCallback(async () => {
    try {
      setIsLoading(true);
      setError(null);
      
      const baseUrl = getBaseUrl();
      const response = await fetch(`${baseUrl}/api/stop-collection`, {
        method: 'POST',
      });
      
      if (!response.ok) {
        throw new Error(`Erro HTTP: ${response.status}`);
      }
      
      setIsCollecting(false);
      if (options?.onStopSuccess) options.onStopSuccess();
    } catch (err) {
      console.error('Erro ao parar coleta:', err);
      const errorMsg = err instanceof Error ? err.message : 'Erro desconhecido';
      setError(`Falha ao parar coleta: ${errorMsg}`);
      if (options?.onStopError) options.onStopError(errorMsg);
    } finally {
      setIsLoading(false);
    }
  }, [getBaseUrl, options]);
  
  return {
    isCollecting,
    isLoading,
    error,
    startCollection,
    stopCollection,
    setIsCollecting
  };
};