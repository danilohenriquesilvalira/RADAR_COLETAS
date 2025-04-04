import { create } from 'zustand';
import { RadarData, RadarState } from '../types';
// Removendo o import não utilizado de VelocityChange e importando corretamente o lodash
import type { VelocityChange } from '../types'; // Importado como type para uso no tipo de estado

// Criar a store com Zustand
const useRadarStore = create<RadarState>((set, get) => ({
  isConnected: false,
  connectionStatus: 'initializing',
  latestData: null,
  positions: Array(7).fill(0),
  velocities: Array(7).fill(0),
  timestamp: null,
  velocityChangesHistory: [],
  historyStats: null,
  maxHistorySize: 100,
  
  // Dados históricos inicializados vazios
  historicalData: {
    timestamps: [],
    velocityData: {
      0: [], 1: [], 2: [], 3: [], 4: [], 5: [], 6: []
    }
  },

  // Atualizar status de conexão
  setConnectionStatus: (status) => set(() => ({ 
    connectionStatus: status,
    isConnected: status === 'connected'
  })),

  // Atualizar dados com as métricas recebidas
  updateData: (data: RadarData) => {
    // Verificar se realmente temos mudanças significativas
    // antes de atualizar o estado (evita renderizações desnecessárias)
    const currentState = get();
    
    // Função para verificar se há alterações significativas no array de números
    const hasSignificantChanges = (current: number[], incoming: number[], threshold = 0.01): boolean => {
      if (!current || !incoming || current.length !== incoming.length) return true;
      return incoming.some((value, idx) => Math.abs(value - (current[idx] || 0)) > threshold);
    };
    
    // Verificar se precisamos atualizar
    const velocitiesChanged = hasSignificantChanges(currentState.velocities, data.velocities);
    const positionsChanged = hasSignificantChanges(currentState.positions, data.positions, 0.05);
    const hasChanges = data.changes && data.changes.length > 0;
    const isNewTimestamp = !currentState.timestamp || 
                          Math.abs(currentState.timestamp - data.timestamp) > 1000;
    
    // Se não houver mudanças significativas, não atualize o estado
    if (!velocitiesChanged && !positionsChanged && !hasChanges && !isNewTimestamp) {
      return;
    }

    // Atualizar apenas quando necessário
    set((state) => {
      // Atualizar histórico de mudanças quando houver mudanças significativas
      let updatedHistory = [...state.velocityChangesHistory];
      
      if (data.changes && data.changes.length > 0) {
        // Adicionar novas mudanças de velocidade ao início do array
        updatedHistory = [
          ...data.changes,
          ...state.velocityChangesHistory
        ].slice(0, state.maxHistorySize);
      }
      
      // Atualizar dados históricos sem criar cópias desnecessárias
      let timestamps = state.historicalData.timestamps;
      let velocityData = { ...state.historicalData.velocityData };
      
      // Adicionar timestamp apenas se for significativamente diferente do último (> 1s)
      const shouldAddTimestamp = timestamps.length === 0 || 
                                 data.timestamp - timestamps[timestamps.length - 1] > 1000;
      
      if (shouldAddTimestamp) {
        // Atualizar histórico
        timestamps = [...timestamps, data.timestamp].slice(-60);
        
        // Adicionar novos pontos de dados para cada sensor
        data.velocities.forEach((value, idx) => {
          if (!velocityData[idx]) {
            velocityData[idx] = [];
          }
          velocityData[idx] = [...velocityData[idx], value].slice(-60);
        });
      }

      return {
        latestData: data,
        positions: data.positions,
        velocities: data.velocities,
        timestamp: data.timestamp,
        velocityChangesHistory: updatedHistory,
        historyStats: data.history_stats || state.historyStats,
        historicalData: {
          timestamps,
          velocityData
        }
      };
    });
  },

  // Limpar todos os dados
  clearData: () => set(() => ({
    latestData: null,
    positions: Array(7).fill(0),
    velocities: Array(7).fill(0),
    timestamp: null,
    velocityChangesHistory: [],
    historyStats: null,
    historicalData: {
      timestamps: [],
      velocityData: {
        0: [], 1: [], 2: [], 3: [], 4: [], 5: [], 6: []
      }
    }
  })),
}));

export default useRadarStore;