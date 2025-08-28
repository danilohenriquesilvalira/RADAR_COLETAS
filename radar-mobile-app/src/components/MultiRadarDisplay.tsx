import React from 'react';
import { Radio, Wifi, WifiOff, Target, TrendingUp, Activity } from 'lucide-react';
import { RadarData, MultiRadarData } from '../types/radarTypes';

interface MultiRadarDisplayProps {
  multiRadarData: MultiRadarData | null;
}

const RadarCard: React.FC<{ radar: RadarData }> = ({ radar }) => {
  const formatValue = (value: number | undefined, digits = 0, unit = ""): string => {
    if (value === undefined || value === null || isNaN(value)) return "N/A";
    return `${value.toFixed(digits)}${unit}`;
  };

  return (
    <div className={`radar-card ${radar.connected ? 'connected' : 'disconnected'}`}>
      <div className="radar-card-header">
        <div className="radar-card-title">
          <div className="radar-card-icon">
            <Radio size={20} />
          </div>
          <div className="radar-card-info">
            <h3>{radar.radarName}</h3>
            <span className="radar-id">{radar.radarId}</span>
          </div>
        </div>
        <div className={`connection-status ${radar.connected ? 'connected' : 'disconnected'}`}>
          {radar.connected ? <Wifi size={18} /> : <WifiOff size={18} />}
          <span>{radar.connected ? 'Conectado' : 'Desconectado'}</span>
        </div>
      </div>

      {radar.connected && radar.mainObject ? (
        <div className="radar-card-content">
          <div className="radar-stats">
            <div className="stat-item">
              <div className="stat-icon primary">
                <Activity size={16} />
              </div>
              <div className="stat-info">
                <span className="stat-label">Amplitude</span>
                <span className="stat-value">{formatValue(radar.mainObject.amplitude)}</span>
              </div>
            </div>
            
            <div className="stat-item">
              <div className="stat-icon success">
                <Target size={16} />
              </div>
              <div className="stat-info">
                <span className="stat-label">Distância</span>
                <span className="stat-value">{formatValue(radar.mainObject.distancia, 2, " m")}</span>
              </div>
            </div>
            
            <div className="stat-item">
              <div className="stat-icon info">
                <TrendingUp size={16} />
              </div>
              <div className="stat-info">
                <span className="stat-label">Velocidade</span>
                <span className="stat-value">{formatValue(radar.mainObject.velocidade, 2, " m/s")}</span>
              </div>
            </div>
          </div>

          <div className="detection-info">
            <span className="detection-count">
              Objetos detectados: {radar.positions?.length || 0}
            </span>
          </div>
        </div>
      ) : (
        <div className="radar-card-empty">
          {radar.connected ? (
            <div className="empty-detection">
              <Target size={24} className="empty-icon" />
              <span>Nenhum objeto detectado</span>
            </div>
          ) : (
            <div className="disconnected-message">
              <WifiOff size={24} className="empty-icon" />
              <span>Radar desconectado</span>
            </div>
          )}
        </div>
      )}
    </div>
  );
};

const MultiRadarDisplay: React.FC<MultiRadarDisplayProps> = ({ multiRadarData }) => {
  if (!multiRadarData || !multiRadarData.radars) {
    return (
      <div className="multi-radar-loading">
        <div className="loading-spinner">
          <Activity size={32} />
        </div>
        <h3>Carregando dados dos radares...</h3>
        <p>Aguardando conexão com o sistema</p>
      </div>
    );
  }

  const connectedCount = multiRadarData.radars.filter(r => r.connected).length;
  const totalDetections = multiRadarData.radars.reduce((sum, r) => sum + (r.positions?.length || 0), 0);

  return (
    <div className="multi-radar-display">
      <div className="multi-radar-header">
        <div className="system-status">
          <h2>Sistema de Radares SICK</h2>
          <div className="system-stats">
            <div className="system-stat">
              <span className="stat-label">Radares Conectados:</span>
              <span className={`stat-value ${connectedCount === 3 ? 'success' : 'warning'}`}>
                {connectedCount}/3
              </span>
            </div>
            <div className="system-stat">
              <span className="stat-label">Total de Objetos:</span>
              <span className="stat-value">{totalDetections}</span>
            </div>
          </div>
        </div>
      </div>

      <div className="radars-grid">
        {multiRadarData.radars.map((radar) => (
          <RadarCard key={radar.radarId} radar={radar} />
        ))}
      </div>

      <div className="timestamp">
        Última atualização: {new Date(multiRadarData.timestamp).toLocaleString('pt-BR')}
      </div>
    </div>
  );
};

export default MultiRadarDisplay;