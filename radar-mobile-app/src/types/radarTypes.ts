// Tipos para os dados do radar
export interface ObjPrincipal {
  amplitude: number;
  distancia?: number;
  velocidade?: number;
  angulo?: number;
}

// Interface para o status do PLC
export interface PLCStatus {
  connected: boolean;
  error?: string;
}

export interface RadarData {
  radarId: string;
  radarName: string;
  connected: boolean;
  positions: number[];
  velocities: number[];
  azimuths: number[];
  amplitudes: number[];
  mainObject?: ObjPrincipal;
  plcStatus?: PLCStatus; // Status do PLC adicionado aqui
  timestamp: number;
}

export interface MultiRadarData {
  radars: RadarData[];
  timestamp: number;
}

// Estados de conexão
export enum ConnectionStatus {
  DISCONNECTED = 'DISCONNECTED',
  CONNECTING = 'CONNECTING',
  CONNECTED = 'CONNECTED',
  ERROR = 'ERROR'
}