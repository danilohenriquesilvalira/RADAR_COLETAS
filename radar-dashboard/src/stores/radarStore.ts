import { create } from 'zustand';
import { RadarData, RadarState, VelocityChange } from '../types';

// Criar a store com Zustand
const useRadarStore = create<RadarState>((set) => ({
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
  updateData: (data: RadarData) => set((state) => {
    // Atualizar histórico de mudanças
    let updatedHistory = [...state.velocityChangesHistory];
    
    if (data.changes && data.changes.length > 0) {
      // Adicionar novas mudanças de velocidade ao início do array
      updatedHistory = [
        ...data.changes,
        ...state.velocityChangesHistory
      ].slice(0, state.maxHistorySize);
    }
    
    // Atualizar dados históricos
    const timestamps = [
      ...state.historicalData.timestamps,
      data.timestamp
    ].slice(-60); // Manter 60 pontos no máximo
    
    const velocityData = { ...state.historicalData.velocityData };
    
    // Adicionar novos pontos de dados para cada sensor
    data.velocities.forEach((value, idx) => {
      if (!velocityData[idx]) {
        velocityData[idx] = [];
      }
      velocityData[idx] = [...velocityData[idx], value].slice(-60); // Manter 60 pontos
    });

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
  }),

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