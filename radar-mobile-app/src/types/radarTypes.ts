// Tipos para os dados do radar
export interface MainObject {
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
  mainObject?: MainObject;
  plcStatus?: PLCStatus;
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