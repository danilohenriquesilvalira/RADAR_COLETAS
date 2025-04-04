// radar-dashboard/src/types/index.ts

// Tipos de dados recebidos via WebSocket
export interface RadarData {
  type: string;
  timestamp: number;
  positions: number[];
  velocities: number[];
  changes?: VelocityChange[];
  history_stats?: HistoryStats;
  // Novos campos
  real_velocity?: number;
  direction?: string;
  boat_size?: string;
}

export interface VelocityChange {
  index: number;
  old_value: number;
  new_value: number;
  change_value: number;
  position: number;  // Nova propriedade: posição onde a mudança foi detectada
  timestamp: number;
}

export interface HistoryStats {
  total_changes: number;
  max_velocity: number;
  min_velocity: number;
  avg_velocity: number;
  change_frequency: number;
  last_updated: number;
  velocity_history: {
    [key: number]: VelocityPoint[];
  };
  // Novos campos
  real_velocity?: number;
  direction?: string;
  boat_size?: string;
}

export interface VelocityPoint {
  timestamp: number;
  value: number;
}

// Estados da conexão WebSocket
export type ConnectionStatus = 'initializing' | 'connected' | 'disconnected' | 'error';

// Interface para o store do radar
export interface RadarState {
  isConnected: boolean;
  connectionStatus: ConnectionStatus;
  latestData: RadarData | null;
  positions: number[];
  velocities: number[];
  timestamp: number | null;
  velocityChangesHistory: VelocityChange[];
  historyStats: HistoryStats | null;
  maxHistorySize: number;
  
  // Novos campos para informações do barco
  realVelocity: number;
  direction: string;
  boatSize: string;
  
  // Dados históricos
  historicalData: {
    timestamps: number[];
    velocityData: {
      [key: number]: number[];
    };
  };
  
  // Actions
  setConnectionStatus: (status: 'connected' | 'disconnected' | 'error') => void;
  updateData: (data: RadarData) => void;
  clearData: () => void;
}