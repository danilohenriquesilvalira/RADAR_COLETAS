import { useState, useRef, useCallback, useMemo } from 'react';
import { RadarData } from '../types/radarTypes';

// Hook para armazenar e manter os dados persistentes SEM PISCAR
export const usePersistentData = (initialData: RadarData | null = null) => {
  // Estado para armazenar dados de forma persistente
  const [persistentData, setPersistentData] = useState<RadarData | null>(initialData);
  const [isUpdating, setIsUpdating] = useState(false);
  
  // Referência para rastrear se os dados já foram inicializados
  const initialized = useRef(false);
  const lastValidData = useRef<RadarData | null>(null);
  
  // Função para verificar se os novos dados são válidos
  const isValidData = useCallback((data: RadarData): boolean => {
    // Verificar se pelo menos um array tem dados
    const hasPositions = Array.isArray(data.positions) && data.positions.length > 0;
    const hasVelocities = Array.isArray(data.velocities) && data.velocities.length > 0;
    const hasAzimuths = Array.isArray(data.azimuths) && data.azimuths.length > 0;
    const hasAmplitudes = Array.isArray(data.amplitudes) && data.amplitudes.length > 0;
    
    return hasPositions || hasVelocities || hasAzimuths || hasAmplitudes || !!data.mainObject;
  }, []);
  
  // Função para mesclar dados mantendo sempre algo visível
  const safeMerge = useCallback((prevData: RadarData | null, newData: RadarData): RadarData => {
    // Se não temos dados anteriores, usar os novos (se válidos)
    if (!prevData) {
      return isValidData(newData) ? newData : {
        positions: [],
        velocities: [],
        azimuths: [],
        amplitudes: [],
        mainObject: undefined,
        plcStatus: newData.plcStatus,
        timestamp: newData.timestamp || Date.now()
      };
    }
    
    // Mesclar mantendo dados válidos
    return {
      // Só usar novos arrays se eles tiverem dados, senão manter os anteriores
      positions: (Array.isArray(newData.positions) && newData.positions.length > 0) 
        ? [...newData.positions] 
        : [...(prevData.positions || [])],
      
      velocities: (Array.isArray(newData.velocities) && newData.velocities.length > 0) 
        ? [...newData.velocities] 
        : [...(prevData.velocities || [])],
      
      azimuths: (Array.isArray(newData.azimuths) && newData.azimuths.length > 0) 
        ? [...newData.azimuths] 
        : [...(prevData.azimuths || [])],
      
      amplitudes: (Array.isArray(newData.amplitudes) && newData.amplitudes.length > 0) 
        ? [...newData.amplitudes] 
        : [...(prevData.amplitudes || [])],
      
      // Para mainObject, só substituir se o novo for válido
      mainObject: newData.mainObject || prevData.mainObject,
      
      // Sempre atualizar status do PLC e timestamp
      plcStatus: newData.plcStatus || prevData.plcStatus,
      timestamp: newData.timestamp || Date.now()
    };
  }, [isValidData]);
  
  // Função para atualizar dados de forma persistente SEM PISCAR
  const updateData = useCallback((newData: RadarData | null) => {
    if (!newData) {
      console.warn('Dados nulos recebidos, ignorando atualização');
      return;
    }
    
    setIsUpdating(true);
    
    // Usar setTimeout para simular processamento assíncrono sem bloquear UI
    setTimeout(() => {
      setPersistentData(prevData => {
        const mergedData = safeMerge(prevData, newData);
        
        // Guardar os últimos dados válidos
        lastValidData.current = mergedData;
        
        // Marcar como inicializado
        if (!initialized.current) {
          initialized.current = true;
        }
        
        return mergedData;
      });
      
      setIsUpdating(false);
    }, 0);
  }, [safeMerge]);
  
  // Dados estáveis - sempre retorna dados válidos
  const stableData = useMemo(() => {
    // Durante atualizações, retornar os últimos dados válidos
    if (isUpdating && lastValidData.current) {
      return lastValidData.current;
    }
    
    // Retornar dados atuais ou últimos válidos
    return persistentData || lastValidData.current;
  }, [persistentData, isUpdating]);
  
  // Verificar se os dados estão inicializados
  const isInitialized = useCallback(() => initialized.current, []);
  
  // Limpar dados
  const clearData = useCallback(() => {
    setPersistentData(null);
    lastValidData.current = null;
    initialized.current = false;
    setIsUpdating(false);
  }, []);
  
  return {
    persistentData: stableData, // Dados sempre estáveis
    updateData,
    clearData,
    isInitialized,
    isUpdating, // Para mostrar indicador de carregamento se necessário
    hasValidData: !!stableData
  };
};