import React from 'react';
import { Radio, Wifi, WifiOff, Target, TrendingUp, Activity, Signal, Zap } from 'lucide-react';
import { RadarData, MultiRadarData } from '../types/radarTypes';

interface ModernRadarDisplayProps {
  multiRadarData: MultiRadarData | null;
}

const RadarCard: React.FC<{ radar: RadarData }> = ({ radar }) => {
  const formatValue = (value: number | undefined, digits = 2, unit = ""): string => {
    if (value === undefined || value === null || isNaN(value)) return "N/A";
    return `${value.toFixed(digits)}${unit}`;
  };

  const getRadarDisplayName = (radarId: string): string => {
    switch (radarId) {
      case "caldeira": return "Caldeira";
      case "porta_jusante": return "Porta Jusante";
      case "porta_montante": return "Porta Montante";
      default: return radarId;
    }
  };

  const isConnected = radar.connected;
  const hasDetection = radar.mainObject && radar.mainObject.amplitude > 0;

  return (
    <div className="w-full bg-white rounded-2xl shadow-lg border border-gray-200 hover:shadow-xl transition-all duration-300 p-6">
      {/* Header do Card */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 mb-6">
        <div className="flex items-center gap-4">
          <div className={`
            w-12 h-12 rounded-xl flex items-center justify-center transition-colors
            ${isConnected ? 'bg-blue-100 text-blue-600' : 'bg-gray-100 text-gray-400'}
          `}>
            <Radio className="w-6 h-6" />
          </div>
          <div className="flex-1">
            <h3 className="text-lg font-bold text-gray-900">
              {getRadarDisplayName(radar.radarId)}
            </h3>
            <p className="text-sm text-gray-500 font-mono">{radar.radarId}</p>
          </div>
        </div>
        
        <div className={`
          inline-flex items-center gap-2 px-4 py-2 rounded-full text-sm font-semibold
          ${isConnected 
            ? 'bg-green-100 text-green-800' 
            : 'bg-red-100 text-red-800'
          }
        `}>
          {isConnected ? <Wifi className="w-4 h-4" /> : <WifiOff className="w-4 h-4" />}
          <span>{isConnected ? 'Conectado' : 'Desconectado'}</span>
        </div>
      </div>

      {/* Conteúdo do Card */}
      {isConnected ? (
        hasDetection ? (
          <div className="space-y-6">
            {/* Métricas Principais - Mobile: 1 col, Tablet: 2 cols, Desktop: 3 cols */}
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
              <div className="bg-gradient-to-br from-blue-50 to-blue-100 rounded-xl p-4 border border-blue-200">
                <div className="flex items-center gap-3 mb-2">
                  <div className="w-8 h-8 bg-blue-600 rounded-lg flex items-center justify-center">
                    <Activity className="w-4 h-4 text-white" />
                  </div>
                  <span className="text-sm font-semibold text-blue-800">Amplitude</span>
                </div>
                <p className="text-2xl font-bold text-blue-900">
                  {formatValue(radar.mainObject?.amplitude, 0)}
                </p>
              </div>

              <div className="bg-gradient-to-br from-green-50 to-green-100 rounded-xl p-4 border border-green-200">
                <div className="flex items-center gap-3 mb-2">
                  <div className="w-8 h-8 bg-green-600 rounded-lg flex items-center justify-center">
                    <Target className="w-4 h-4 text-white" />
                  </div>
                  <span className="text-sm font-semibold text-green-800">Distância</span>
                </div>
                <p className="text-2xl font-bold text-green-900">
                  {formatValue(radar.mainObject?.distancia, 2, "m")}
                </p>
              </div>

              <div className="bg-gradient-to-br from-purple-50 to-purple-100 rounded-xl p-4 border border-purple-200 md:col-span-2 xl:col-span-1">
                <div className="flex items-center gap-3 mb-2">
                  <div className="w-8 h-8 bg-purple-600 rounded-lg flex items-center justify-center">
                    <TrendingUp className="w-4 h-4 text-white" />
                  </div>
                  <span className="text-sm font-semibold text-purple-800">Velocidade</span>
                </div>
                <p className="text-2xl font-bold text-purple-900">
                  {formatValue(radar.mainObject?.velocidade, 2, "m/s")}
                </p>
              </div>
            </div>

            {/* Info Adicional */}
            <div className="bg-gradient-to-r from-gray-50 to-blue-50 rounded-xl p-4 border border-gray-200">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <div className="w-8 h-8 bg-gray-600 rounded-lg flex items-center justify-center">
                    <Signal className="w-4 h-4 text-white" />
                  </div>
                  <span className="text-sm font-semibold text-gray-800">Objetos Detectados</span>
                </div>
                <span className="text-xl font-bold text-gray-900">
                  {radar.positions?.length || 0}
                </span>
              </div>
            </div>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center py-12 text-center">
            <div className="w-16 h-16 bg-gray-100 rounded-full flex items-center justify-center mb-4">
              <Target className="w-8 h-8 text-gray-400" />
            </div>
            <p className="text-gray-600 font-medium">Nenhum objeto detectado</p>
            <p className="text-gray-400 text-sm mt-1">Sistema monitorando...</p>
          </div>
        )
      ) : (
        <div className="flex flex-col items-center justify-center py-12 text-center">
          <div className="w-16 h-16 bg-red-100 rounded-full flex items-center justify-center mb-4">
            <WifiOff className="w-8 h-8 text-red-500" />
          </div>
          <p className="text-red-600 font-semibold">Radar Desconectado</p>
          <p className="text-red-400 text-sm mt-1">Tentando reconectar...</p>
        </div>
      )}
    </div>
  );
};

const ModernRadarDisplay: React.FC<ModernRadarDisplayProps> = ({ multiRadarData }) => {
  if (!multiRadarData || !multiRadarData.radars) {
    return (
      <div className="min-h-screen bg-gradient-to-br from-blue-50 via-white to-blue-50 flex items-center justify-center p-4">
        <div className="w-full max-w-md bg-white rounded-3xl shadow-2xl p-8 text-center border border-blue-100">
          <div className="w-20 h-20 bg-blue-100 rounded-full mx-auto mb-6 flex items-center justify-center">
            <Activity className="w-10 h-10 text-blue-600 animate-pulse" />
          </div>
          <h3 className="text-2xl font-bold text-gray-900 mb-3">Carregando Sistema</h3>
          <p className="text-gray-500">Conectando aos radares SICK...</p>
        </div>
      </div>
    );
  }

  const connectedCount = multiRadarData.radars.filter(r => r.connected).length;
  const totalDetections = multiRadarData.radars.reduce((sum, r) => sum + (r.positions?.length || 0), 0);

  return (
    <div className="min-h-screen bg-gradient-to-br from-blue-50 via-white to-blue-50">
      {/* Header Responsivo */}
      <div className="bg-white shadow-sm border-b border-blue-200">
        <div className="w-full max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
          <div className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-6">
            <div className="text-center lg:text-left">
              <h1 className="text-3xl sm:text-4xl lg:text-5xl font-bold bg-gradient-to-r from-blue-600 to-blue-800 bg-clip-text text-transparent">
                Sistema RADAR SICK
              </h1>
              <p className="text-gray-600 text-base sm:text-lg mt-2">
                Monitoramento em tempo real - 3 radares industriais
              </p>
            </div>
            
            {/* Status Cards Responsivos */}
            <div className="flex flex-col sm:flex-row gap-4 lg:gap-6">
              <div className={`
                flex items-center justify-center gap-3 px-6 py-3 rounded-2xl text-base font-bold shadow-lg
                ${connectedCount === 3 
                  ? 'bg-gradient-to-r from-green-500 to-green-600 text-white' 
                  : connectedCount > 0 
                    ? 'bg-gradient-to-r from-yellow-500 to-orange-500 text-white' 
                    : 'bg-gradient-to-r from-red-500 to-red-600 text-white'
                }
              `}>
                <Signal className="w-5 h-5" />
                <span>{connectedCount}/3 Online</span>
              </div>
              
              <div className="bg-gradient-to-r from-blue-500 to-blue-600 text-white px-6 py-3 rounded-2xl text-base font-bold shadow-lg flex items-center justify-center gap-3">
                <Target className="w-5 h-5" />
                <span>{totalDetections} Objetos</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Grid de Radares - RESPONSIVIDADE COMPLETA */}
      <div className="w-full max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8">
        <div className="grid grid-cols-1 lg:grid-cols-2 xl:grid-cols-3 gap-6 lg:gap-8">
          {multiRadarData.radars.map((radar) => (
            <RadarCard key={radar.radarId} radar={radar} />
          ))}
        </div>

        {/* Footer com Timestamp - Responsivo */}
        <div className="mt-12 text-center">
          <div className="inline-flex items-center gap-3 bg-white rounded-2xl px-6 py-4 shadow-lg border border-gray-200">
            <div className="w-8 h-8 bg-blue-100 rounded-lg flex items-center justify-center">
              <Zap className="w-4 h-4 text-blue-600" />
            </div>
            <div className="text-left">
              <p className="text-xs text-gray-500 font-medium">Última atualização</p>
              <p className="text-sm font-bold text-gray-900">
                {new Date(multiRadarData.timestamp).toLocaleString('pt-BR')}
              </p>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default ModernRadarDisplay;