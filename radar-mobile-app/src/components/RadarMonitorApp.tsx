import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Info, Settings, AlertCircle, ChevronDown, ChevronUp, BarChart2, 
  Zap, Wifi, WifiOff, Activity, Layers, Server, TrendingUp, Gauge, 
  Radio, Target, Eye, EyeOff, ChevronLeft, ChevronRight } from 'lucide-react';
import { ConnectionStatus, RadarData } from '../types/radarTypes';
import Header from './Header';

// Tipos estendidos para evitar erros TypeScript
interface ExtendedRadarData extends RadarData {
  systemStatus?: {
    deviceId?: string;
    temperature?: string;
    operatingHours?: string;
    firmwareVersion?: string;
    lastParametrization?: string;
  };
}

interface RadarMonitorAppProps {
  url: string;
  status: ConnectionStatus;
  error: string | null;
  lastUpdated: string;
  onUrlChange: (url: string) => void;
  onConnect: () => void;
  onDisconnect: () => void;
  isCollecting: boolean;
  isLoading: boolean;
  onStartCollection: () => void;
  onStopCollection: () => void;
  radarData: RadarData | null;
  onLogout?: () => void;
  currentUser?: { username: string; role: string };
}

// Componente moderno para status cards
const StatusCard: React.FC<{
  title: string;
  value: string;
  icon: React.ReactNode;
  color: string;
  subtitle?: string;
}> = ({ title, value, icon, color, subtitle }) => (
  <div className="status-card">
    <div className="status-card-header">
      <div className={`status-card-icon ${color}`}>
        {icon}
      </div>
      <div className="status-card-info">
        <h3>{title}</h3>
        {subtitle && <span>{subtitle}</span>}
      </div>
    </div>
    <div className="status-card-value">{value}</div>
  </div>
);

// Componente para objeto principal moderno
const MainObjectDisplay: React.FC<{ 
  mainObject: any, 
  formatValue: (value: number | undefined, digits?: number, unit?: string) => string 
}> = React.memo(({ mainObject, formatValue }) => {
  if (!mainObject) {
    return (
      <div className="empty-state">
        <Target size={48} className="empty-state-icon" />
        <h3>Nenhum Objeto Detectado</h3>
        <p>Aguardando detecção de objetos pelo radar</p>
      </div>
    );
  }

  return (
    <div className="main-object-grid">
      <StatusCard
        title="Amplitude"
        value={formatValue(mainObject.amplitude)}
        icon={<Radio size={24} />}
        color="primary"
        subtitle="Intensidade do sinal"
      />
      <StatusCard
        title="Distância"
        value={formatValue(mainObject.distancia, 2, " m")}
        icon={<Target size={24} />}
        color="success"
        subtitle="Posição relativa"
      />
      <StatusCard
        title="Velocidade"
        value={formatValue(mainObject.velocidade, 2, " m/s")}
        icon={<TrendingUp size={24} />}
        color="info"
        subtitle="Movimento detectado"
      />
      <StatusCard
        title="Ângulo"
        value={formatValue(mainObject.angulo, 2, "°")}
        icon={<Gauge size={24} />}
        color="warning"
        subtitle="Direção angular"
      />
    </div>
  );
});

// Componente para tabela moderna COM PAGINAÇÃO
const DetectedObjectsTable: React.FC<{ 
  objects: any[], 
  formatValue: (value: number | undefined, digits?: number, unit?: string) => string 
}> = React.memo(({ objects, formatValue }) => {
  const [currentPage, setCurrentPage] = useState(0);
  const itemsPerPage = 5;
  
  // Calcular número total de páginas
  const totalPages = Math.max(1, Math.ceil(objects.length / itemsPerPage));
  
  // Resetar página quando não há objetos ou mudança drástica
  useEffect(() => {
    if (objects.length === 0 || currentPage >= totalPages) {
      setCurrentPage(0);
    }
  }, [objects.length, totalPages, currentPage]);
  
  // Obter objetos da página atual + preencher vazios
  const getCurrentPageObjects = useMemo(() => {
    const startIndex = currentPage * itemsPerPage;
    const pageObjects = objects.slice(startIndex, startIndex + itemsPerPage);
    
    // Preencher com objetos vazios até completar 5
    while (pageObjects.length < itemsPerPage) {
      pageObjects.push(null);
    }
    
    return pageObjects;
  }, [objects, currentPage, itemsPerPage]);
  
  const goToNextPage = useCallback(() => {
    if (currentPage < totalPages - 1) {
      setCurrentPage(prev => prev + 1);
    }
  }, [currentPage, totalPages]);
  
  const goToPrevPage = useCallback(() => {
    if (currentPage > 0) {
      setCurrentPage(prev => prev - 1);
    }
  }, [currentPage]);

  return (
    <div className="modern-table-container">
      <div className="table-header">
        <h4>Objetos Detectados ({objects.length})</h4>
        <div className="table-controls">
          <span className="table-info">Ordenado por amplitude</span>
          
          {/* Controles de Paginação */}
          {objects.length > 0 && (
            <div className="pagination-controls">
              <button 
                className="pagination-btn" 
                onClick={goToPrevPage}
                disabled={currentPage === 0}
                aria-label="Página anterior"
              >
                <ChevronLeft size={16} />
              </button>
              
              <span className="pagination-info">
                {currentPage + 1} / {totalPages}
              </span>
              
              <button 
                className="pagination-btn" 
                onClick={goToNextPage}
                disabled={currentPage >= totalPages - 1}
                aria-label="Próxima página"
              >
                <ChevronRight size={16} />
              </button>
            </div>
          )}
        </div>
      </div>
      
      <div className="table-wrapper">
        <table className="modern-table">
          <thead>
            <tr>
              <th>#</th>
              <th>Distância</th>
              <th>Velocidade</th>
              <th>Ângulo</th>
              <th>Amplitude</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {getCurrentPageObjects.map((obj, index) => {
              const globalIndex = currentPage * itemsPerPage + index + 1;
              
              if (obj === null) {
                // Linha vazia para manter layout fixo
                return (
                  <tr key={`empty-${index}`} className="empty-row">
                    <td className="row-number">{globalIndex}</td>
                    <td className="empty-cell">-</td>
                    <td className="empty-cell">-</td>
                    <td className="empty-cell">-</td>
                    <td className="empty-cell">-</td>
                    <td className="empty-cell">
                      <span className="status-badge empty">Sem detecção</span>
                    </td>
                  </tr>
                );
              }
              
              return (
                <tr key={`${obj.index}-${obj.amplitude}`} className={obj.isMainObject ? 'main-object-row' : ''}>
                  <td className="row-number">{globalIndex}</td>
                  <td className="metric-value">{formatValue(obj.position, 2)} <span className="unit">m</span></td>
                  <td className="metric-value">{formatValue(obj.velocity, 2)} <span className="unit">m/s</span></td>
                  <td className="metric-value">{formatValue(obj.azimuth, 2)} <span className="unit">°</span></td>
                  <td className="amplitude-cell">
                    <div className="amplitude-bar-mini">
                      <div className="amplitude-fill" style={{ width: `${(obj.amplitude / 1000) * 100}%` }}></div>
                    </div>
                    <span className="amplitude-value">{formatValue(obj.amplitude, 1)}</span>
                  </td>
                  <td>
                    {obj.isMainObject && (
                      <span className="status-badge primary">Principal</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      
      {/* Informações da paginação no rodapé */}
      {objects.length > itemsPerPage && (
        <div className="table-footer">
          <span className="table-footer-info">
            Mostrando {Math.min(itemsPerPage, objects.length - currentPage * itemsPerPage)} de {objects.length} objetos
          </span>
        </div>
      )}
    </div>
  );
});

// Componente para painel moderno - CORRIGIDO
const ModernPanel: React.FC<{
  title: string;
  icon: React.ReactNode;
  children: React.ReactNode;
  isExpanded: boolean;
  onToggle: () => void;
  className?: string;
}> = ({ title, icon, children, isExpanded, onToggle, className = '' }) => (
  <div className={`modern-panel ${className}`}>
    <div className="modern-panel-header" onClick={onToggle}>
      <div className="panel-header-content">
        <div className="panel-icon">{icon}</div>
        <h3>{title}</h3>
      </div>
      <button className="panel-toggle" aria-label={isExpanded ? "Recolher" : "Expandir"}>
        {isExpanded ? <ChevronUp size={20} /> : <ChevronDown size={20} />}
      </button>
    </div>
    {/* CORREÇÃO: Usar classes para expansão/recolhimento */}
    <div className={`modern-panel-content ${isExpanded ? 'expanded' : 'collapsed'}`}>
      {children}
    </div>
  </div>
);

const RadarMonitorApp: React.FC<RadarMonitorAppProps> = ({ 
  url,
  status,
  error,
  lastUpdated,
  onUrlChange,
  onConnect,
  onDisconnect,
  isCollecting,
  isLoading,
  onStartCollection,
  onStopCollection,
  radarData,
  onLogout,
  currentUser
}) => {
  const [serverUrl, setServerUrl] = useState(url);
  const [expandedPanels, setExpandedPanels] = useState({
    connection: true,
    parameters: true,
    mainObject: true,
    detectedObjects: true,
    systemInfo: false
  });
  
  const [radarSettings, setRadarSettings] = useState({
    autostart: true,
    amplitudeMin: 0,
    amplitudeMax: 100
  });
  
  const [prevRadarData, setPrevRadarData] = useState<RadarData | null>(null);
  
  // Dados estáveis
  const stableRadarData = useMemo(() => {
    if (radarData && (
      (radarData.positions && radarData.positions.length > 0) ||
      (radarData.velocities && radarData.velocities.length > 0) ||
      (radarData.amplitudes && radarData.amplitudes.length > 0) ||
      radarData.mainObject
    )) {
      setPrevRadarData(radarData);
      return radarData;
    }
    return prevRadarData || radarData;
  }, [radarData, prevRadarData]);
  
  // Arrays do radar
  const radarArrays = useMemo(() => {
    if (!stableRadarData) {
      return {
        positions: [],
        velocities: [],
        azimuths: [],
        amplitudes: [],
        mainObjectAmplitude: undefined
      };
    }
    
    return {
      positions: stableRadarData.positions || [],
      velocities: stableRadarData.velocities || [],
      azimuths: stableRadarData.azimuths || [],
      amplitudes: stableRadarData.amplitudes || [],
      mainObjectAmplitude: stableRadarData.mainObject?.amplitude
    };
  }, [stableRadarData]);
  
  // Formatador de valores
  const formatValue = useCallback((value: number | undefined, digits = 2, unit = ""): string => {
    if (value === undefined || value === null || isNaN(value)) return "N/A";
    return `${value.toFixed(digits)}${unit}`;
  }, []);
  
  // Objetos filtrados
  const filteredObjects = useMemo(() => {
    const objects: Array<{
      index: number;
      position: number;
      velocity?: number;
      azimuth?: number;
      amplitude: number;
      isMainObject: boolean;
    }> = [];
    
    const { positions, velocities, azimuths, amplitudes, mainObjectAmplitude } = radarArrays;
    
    if (positions.length === 0) return [];
    
    for (let i = 0; i < positions.length; i++) {
      const amplitude = i < amplitudes.length ? amplitudes[i] : undefined;
      
      if (amplitude !== undefined && 
          !isNaN(amplitude) &&
          amplitude >= radarSettings.amplitudeMin && 
          amplitude <= radarSettings.amplitudeMax) {
        objects.push({
          index: i,
          position: positions[i] || 0,
          velocity: i < velocities.length ? velocities[i] : undefined,
          azimuth: i < azimuths.length ? azimuths[i] : undefined,
          amplitude: amplitude,
          isMainObject: amplitude === mainObjectAmplitude
        });
      }
    }
    
    return objects.sort((a, b) => b.amplitude - a.amplitude);
  }, [radarArrays, radarSettings.amplitudeMin, radarSettings.amplitudeMax]);
  
  // Handlers
  const handleUrlInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    setServerUrl(e.target.value);
  }, []);
  
  const handleConnect = useCallback(() => {
    onUrlChange(serverUrl);
    onConnect();
  }, [serverUrl, onUrlChange, onConnect]);
  
  // CORREÇÃO: Função de toggle aprimorada
  const togglePanel = useCallback((panel: keyof typeof expandedPanels) => {
    setExpandedPanels(prev => ({
      ...prev,
      [panel]: !prev[panel]
    }));
  }, []);
  
  const handleSettingChange = useCallback((setting: string, value: any) => {
    setRadarSettings(prev => ({
      ...prev,
      [setting]: value
    }));
  }, []);

  // NOVO: Handlers para inputs manuais de amplitude
  const handleAmplitudeMinInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const value = parseInt(e.target.value) || 0;
    const clampedValue = Math.max(0, Math.min(value, radarSettings.amplitudeMax - 1));
    handleSettingChange('amplitudeMin', clampedValue);
  }, [radarSettings.amplitudeMax, handleSettingChange]);

  const handleAmplitudeMaxInputChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const value = parseInt(e.target.value) || 0;
    const clampedValue = Math.max(radarSettings.amplitudeMin + 1, Math.min(value, 1000));
    handleSettingChange('amplitudeMax', clampedValue);
  }, [radarSettings.amplitudeMin, handleSettingChange]);
  
  // Status classes
  const connectionStatusClass = useMemo(() => {
    switch(status) {
      case ConnectionStatus.CONNECTED: return 'connected';
      case ConnectionStatus.CONNECTING: return 'connecting';
      case ConnectionStatus.ERROR: return 'error';
      default: return 'disconnected';
    }
  }, [status]);
  
  const plcStatusClass = useMemo(() => {
    return stableRadarData?.plcStatus?.connected ? 'connected' : 'disconnected';
  }, [stableRadarData?.plcStatus?.connected]);
  
  const systemInfo = useMemo(() => ({
    deviceId: (stableRadarData as ExtendedRadarData)?.systemStatus?.deviceId || 'SN 23490050',
    temperature: (stableRadarData as ExtendedRadarData)?.systemStatus?.temperature || '38.5',
    operatingHours: (stableRadarData as ExtendedRadarData)?.systemStatus?.operatingHours || '2374',
    firmwareVersion: (stableRadarData as ExtendedRadarData)?.systemStatus?.firmwareVersion || '1.2.5',
    lastParametrization: (stableRadarData as ExtendedRadarData)?.systemStatus?.lastParametrization || '21/05/2025 15:25'
  }), [stableRadarData]);
  
  const plcStatus = useMemo(() => ({
    connected: stableRadarData?.plcStatus?.connected || false,
    error: stableRadarData?.plcStatus?.error || null
  }), [stableRadarData?.plcStatus]);

  return (
    <div className="radar-app">
      <Header 
        deviceId={systemInfo.deviceId}
        lastUpdated={lastUpdated}
        connectionStatusClass={connectionStatusClass}
        status={status}
        currentUser={currentUser}
        onLogout={onLogout}
      />
      
      <div className="app-layout">
        {/* Sidebar */}
        <aside className="app-sidebar">
          <ModernPanel
            title="Conexão"
            icon={<Zap size={20} />}
            isExpanded={expandedPanels.connection}
            onToggle={() => togglePanel('connection')}
          >
            <div className="form-section">
              <label className="form-label">Endereço do Servidor</label>
              <div className="input-group">
                <Wifi size={18} className="input-icon" />
                <input
                  type="text"
                  value={serverUrl}
                  onChange={handleUrlInputChange}
                  placeholder="ws://192.168.0.1:8080"
                  disabled={status === ConnectionStatus.CONNECTED || status === ConnectionStatus.CONNECTING}
                  className="modern-input"
                />
              </div>
            </div>
            
            <div className="connection-status-section">
              <div className="connection-item">
                <div className="connection-label">
                  <Server size={16} />
                  <span>PLC Siemens</span>
                </div>
                <div className={`connection-status ${plcStatusClass}`}>
                  {plcStatus.connected ? 'Conectado' : 'Desconectado'}
                </div>
              </div>
            </div>
            
            <div className="action-section">
              {status !== ConnectionStatus.CONNECTED ? (
                <button
                  className="modern-button primary"
                  onClick={handleConnect}
                  disabled={status === ConnectionStatus.CONNECTING || isLoading}
                >
                  <Wifi size={18} />
                  {status === ConnectionStatus.CONNECTING ? 'Conectando...' : 'Conectar'}
                </button>
              ) : (
                <button
                  className="modern-button danger"
                  onClick={onDisconnect}
                  disabled={isLoading}
                >
                  <WifiOff size={18} />
                  Desconectar
                </button>
              )}
            </div>
            
            {error && (
              <div className="alert error">
                <AlertCircle size={18} />
                <span>{error}</span>
              </div>
            )}
          </ModernPanel>
          
          <ModernPanel
            title="Parâmetros"
            icon={<Settings size={20} />}
            isExpanded={expandedPanels.parameters}
            onToggle={() => togglePanel('parameters')}
          >
            <div className="form-section">
              <div className="switch-group">
                <label className="form-label">Medição Automática</label>
                <div className="modern-switch">
                  <input
                    type="checkbox"
                    checked={radarSettings.autostart}
                    onChange={(e) => handleSettingChange('autostart', e.target.checked)}
                    id="autostart-switch"
                  />
                  <label htmlFor="autostart-switch"></label>
                </div>
              </div>
            </div>
            
            {/* NOVO: Amplitude Mínima com Slider e Input */}
            <div className="form-section">
              <div className="amplitude-control">
                <label className="form-label">Amplitude Mínima: {radarSettings.amplitudeMin}</label>
                <div className="control-group">
                  <input 
                    type="range" 
                    min="0"
                    max="500"
                    value={radarSettings.amplitudeMin} 
                    onChange={(e) => handleSettingChange('amplitudeMin', parseInt(e.target.value) || 0)}
                    className="modern-range"
                  />
                  <input
                    type="number"
                    min="0"
                    max="500"
                    value={radarSettings.amplitudeMin}
                    onChange={handleAmplitudeMinInputChange}
                    className="amplitude-input"
                    placeholder="0"
                  />
                </div>
              </div>
            </div>
            
            {/* NOVO: Amplitude Máxima com Slider e Input */}
            <div className="form-section">
              <div className="amplitude-control">
                <label className="form-label">Amplitude Máxima: {radarSettings.amplitudeMax}</label>
                <div className="control-group">
                  <input 
                    type="range" 
                    min="500"
                    max="1000"
                    value={radarSettings.amplitudeMax} 
                    onChange={(e) => handleSettingChange('amplitudeMax', parseInt(e.target.value) || 0)}
                    className="modern-range"
                  />
                  <input
                    type="number"
                    min="500"
                    max="1000"
                    value={radarSettings.amplitudeMax}
                    onChange={handleAmplitudeMaxInputChange}
                    className="amplitude-input"
                    placeholder="1000"
                  />
                </div>
              </div>
            </div>
            
            <div className="range-info">
              <BarChart2 size={16} />
              <span>Filtrando objetos entre {radarSettings.amplitudeMin} - {radarSettings.amplitudeMax}</span>
            </div>
          </ModernPanel>
          
          <ModernPanel
            title="Sistema"
            icon={<Info size={20} />}
            isExpanded={expandedPanels.systemInfo}
            onToggle={() => togglePanel('systemInfo')}
          >
            <div className="system-info-grid">
              <div className="info-item">
                <span className="info-label">Dispositivo</span>
                <span className="info-value">{systemInfo.deviceId}</span>
              </div>
              <div className="info-item">
                <span className="info-label">Temperatura</span>
                <span className="info-value">{systemInfo.temperature}°C</span>
              </div>
              <div className="info-item">
                <span className="info-label">Horas de Operação</span>
                <span className="info-value">{systemInfo.operatingHours}h</span>
              </div>
              <div className="info-item">
                <span className="info-label">Firmware</span>
                <span className="info-value">{systemInfo.firmwareVersion}</span>
              </div>
            </div>
          </ModernPanel>
        </aside>
        
        {/* Main Content */}
        <main className="app-main">
          <ModernPanel
            title="Objeto Principal"
            icon={<Layers size={20} />}
            isExpanded={expandedPanels.mainObject}
            onToggle={() => togglePanel('mainObject')}
            className="dashboard-card full-width"
          >
            <MainObjectDisplay 
              mainObject={stableRadarData?.mainObject} 
              formatValue={formatValue}
            />
          </ModernPanel>
          
          <ModernPanel
            title="Objetos Detectados"
            icon={<Activity size={20} />}
            isExpanded={expandedPanels.detectedObjects}
            onToggle={() => togglePanel('detectedObjects')}
            className="dashboard-card full-width"
          >
            <DetectedObjectsTable 
              objects={filteredObjects} 
              formatValue={formatValue}
            />
          </ModernPanel>
          
          <ModernPanel
            title="Distribuição de Amplitude"
            icon={<BarChart2 size={20} />}
            isExpanded={true}
            onToggle={() => {}}
            className="chart-panel"
          >
            <div className="amplitude-chart">
              <div className="chart-container">
                <div 
                  className="amplitude-filter-region"
                  style={{ 
                    left: `${(radarSettings.amplitudeMin / 1000) * 100}%`, 
                    width: `${((radarSettings.amplitudeMax - radarSettings.amplitudeMin) / 1000) * 100}%` 
                  }}
                />
                
                {radarArrays.amplitudes.map((amplitude, idx) => (
                  <div 
                    key={`amplitude-${idx}-${amplitude}`}
                    className={`amplitude-bar ${
                      amplitude === radarArrays.mainObjectAmplitude ? 'main-object' : ''
                    } ${
                      amplitude >= radarSettings.amplitudeMin && amplitude <= radarSettings.amplitudeMax
                      ? 'in-range' : 'out-range'
                    }`}
                    style={{ 
                      left: `${(amplitude / 1000) * 100}%`, 
                      height: `${Math.min(80, (amplitude / 1000) * 80 + 20)}%`,
                    }}
                    title={`Amplitude: ${amplitude.toFixed(1)}`}
                  />
                ))}
                
                {[0, 200, 400, 600, 800, 1000].map((value) => (
                  <div 
                    key={value}
                    className="chart-grid-line"
                    style={{ left: `${(value / 1000) * 100}%` }}
                  >
                    <span className="grid-label">{value}</span>
                  </div>
                ))}
              </div>
            </div>
          </ModernPanel>
        </main>
      </div>
    </div>
  );
};

export default RadarMonitorApp;