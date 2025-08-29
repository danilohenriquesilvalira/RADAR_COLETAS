import React from 'react';
import { Radar, Wifi, WifiOff, Target, Activity, TrendingUp, Signal, Clock, Zap, Dot } from 'lucide-react';
import { RadarData, MultiRadarData } from '../types/radarTypes';

interface ModernRadarDisplayProps {
  multiRadarData: MultiRadarData | null;
}

const RadarCard: React.FC<{ radar: RadarData }> = ({ radar }) => {
  const formatValue = (value: number | undefined, digits = 2, unit = ""): string => {
    if (value === undefined || value === null || isNaN(value)) return "--";
    return `${value.toFixed(digits)}${unit}`;
  };

  const getRadarConfig = (radarId: string) => {
    const configs = {
      caldeira: {
        name: "Caldeira",
        accent: "bg-blue-500",
        light: "bg-blue-50",
        border: "border-blue-200",
        text: "text-blue-700"
      },
      porta_jusante: {
        name: "Porta Jusante", 
        accent: "bg-blue-500",
        light: "bg-blue-50",
        border: "border-blue-200", 
        text: "text-blue-700"
      },
      porta_montante: {
        name: "Porta Montante",
        accent: "bg-blue-500",
        light: "bg-blue-50", 
        border: "border-blue-200",
        text: "text-blue-700"
      }
    };
    return configs[radarId as keyof typeof configs] || configs.caldeira;
  };

  const config = getRadarConfig(radar.radarId);
  const isConnected = radar.connected;
  const hasDetection = radar.mainObject && radar.mainObject.amplitude > 0;

  return (
    <div className="group relative h-full">
      {/* Card principal - altura uniforme */}
      <div className="relative bg-white rounded-3xl p-4 sm:p-6 border border-gray-200/60 shadow-sm hover:shadow-xl hover:border-gray-300/60 transition-all duration-500 overflow-hidden h-full flex flex-col">
        
        {/* Accent bar no topo - mais elegante */}
        <div className={`absolute top-0 left-0 right-0 h-2 ${isConnected ? config.accent : 'bg-gray-300'} rounded-t-3xl`} />
        
        {/* Header compacto para mobile */}
        <div className="flex items-center justify-between mb-4 sm:mb-6">
          <div className="flex items-center gap-3">
            <div className={`relative w-10 h-10 sm:w-12 sm:h-12 rounded-xl sm:rounded-2xl flex items-center justify-center shadow-md ${isConnected ? config.accent : 'bg-gray-300'}`}>
              <Radar className="w-5 h-5 sm:w-6 sm:h-6 text-white" />
              {isConnected && (
                <div className="absolute -top-0.5 -right-0.5 w-3 h-3 sm:w-4 sm:h-4 bg-green-400 rounded-full border-2 border-white">
                  <div className="w-full h-full bg-green-500 rounded-full animate-pulse" />
                </div>
              )}
            </div>
            <div className="min-w-0 flex-1">
              <h3 className="text-base sm:text-lg font-bold text-gray-900 tracking-tight truncate">{config.name}</h3>
              <p className="text-xs sm:text-sm text-gray-500 font-medium truncate">{radar.radarId}</p>
            </div>
          </div>
          
          <div className={`px-2.5 py-1.5 sm:px-3 sm:py-2 rounded-xl text-xs sm:text-sm font-semibold ${
            isConnected 
              ? 'bg-green-50 text-green-700 border border-green-200' 
              : 'bg-red-50 text-red-700 border border-red-200'
          }`}>
            <div className="flex items-center gap-1.5">
              {isConnected ? <Wifi className="w-3 h-3 sm:w-4 sm:h-4" /> : <WifiOff className="w-3 h-3 sm:w-4 sm:h-4" />}
              <span className="hidden sm:inline">{isConnected ? 'ATIVO' : 'INATIVO'}</span>
              <span className="sm:hidden">{isConnected ? 'ON' : 'OFF'}</span>
            </div>
          </div>
        </div>

        {/* Conteúdo do card - flex-1 para ocupar espaço restante */}
        <div className="flex-1 flex flex-col">
          {isConnected ? (
            hasDetection ? (
              <div className="space-y-3 sm:space-y-4 flex-1">
              {/* Métrica principal - Mobile compacta */}
              <div className={`${config.light} rounded-2xl p-3 sm:p-4 border ${config.border}`}>
                <div className="flex items-center justify-between">
                  <div className="min-w-0 flex-1">
                    <p className={`text-xs sm:text-sm font-semibold ${config.text} uppercase tracking-wide`}>
                      Amplitude Principal
                    </p>
                    <div className="flex items-baseline gap-1 sm:gap-2 mt-1">
                      <span className="text-xl sm:text-2xl lg:text-3xl font-black text-gray-900">
                        {formatValue(radar.mainObject?.amplitude, 0)}
                      </span>
                      <span className="text-sm sm:text-base font-medium text-gray-600">dB</span>
                    </div>
                  </div>
                  <div className={`w-8 h-8 sm:w-10 sm:h-10 ${config.accent} rounded-xl flex items-center justify-center flex-shrink-0`}>
                    <Activity className="w-4 h-4 sm:w-5 sm:h-5 text-white" />
                  </div>
                </div>
              </div>

              {/* Grid de métricas - Padrão uniforme */}
              <div className="grid grid-cols-2 gap-2 sm:gap-3">
                <div className="bg-gray-50 rounded-xl p-3 sm:p-4 border border-gray-100">
                  <div className="flex items-center gap-2 mb-2">
                    <div className="w-6 h-6 sm:w-7 sm:h-7 bg-blue-500 rounded-lg flex items-center justify-center">
                      <Target className="w-3 h-3 sm:w-4 sm:h-4 text-white" />
                    </div>
                    <span className="text-xs sm:text-sm font-semibold text-gray-700 truncate">Distância</span>
                  </div>
                  <p className="text-lg sm:text-xl font-bold text-gray-900">
                    {formatValue(radar.mainObject?.distancia, 1, "m")}
                  </p>
                </div>

                <div className="bg-gray-50 rounded-xl p-3 sm:p-4 border border-gray-100">
                  <div className="flex items-center gap-2 mb-2">
                    <div className="w-6 h-6 sm:w-7 sm:h-7 bg-blue-500 rounded-lg flex items-center justify-center">
                      <TrendingUp className="w-3 h-3 sm:w-4 sm:h-4 text-white" />
                    </div>
                    <span className="text-xs sm:text-sm font-semibold text-gray-700 truncate">Velocidade</span>
                  </div>
                  <p className="text-lg sm:text-xl font-bold text-gray-900">
                    {formatValue(radar.mainObject?.velocidade, 1, "m/s")}
                  </p>
                </div>
              </div>

              {/* Contador total */}
              <div className="bg-blue-50 rounded-xl p-3 sm:p-4 border border-blue-200">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 min-w-0 flex-1">
                    <div className="w-6 h-6 sm:w-7 sm:h-7 bg-blue-500 rounded-lg flex items-center justify-center flex-shrink-0">
                      <Signal className="w-3 h-3 sm:w-4 sm:h-4 text-white" />
                    </div>
                    <span className="text-xs sm:text-sm font-semibold text-blue-700 truncate">Objetos</span>
                  </div>
                  <span className="text-lg sm:text-xl font-bold text-blue-900 flex-shrink-0">
                    {radar.positions?.length || 0}
                  </span>
                </div>
              </div>
            </div>
            ) : (
              // Estado monitorando - altura uniforme
              <div className="text-center py-6 sm:py-8 flex-1 flex flex-col justify-center">
                <div className="relative inline-flex items-center justify-center w-16 h-16 sm:w-20 sm:h-20 mb-4">
                  <div className={`absolute inset-0 ${config.light} rounded-full animate-ping opacity-75`} />
                  <div className={`relative w-16 h-16 sm:w-20 sm:h-20 ${config.light} rounded-full border ${config.border} flex items-center justify-center`}>
                    <Target className={`w-6 h-6 sm:w-8 sm:h-8 ${config.text}`} />
                  </div>
                </div>
                <h4 className="text-sm sm:text-base font-bold text-gray-800 mb-1">Monitorando</h4>
                <p className="text-xs sm:text-sm text-gray-500">Aguardando detecção</p>
              </div>
            )
          ) : (
            // Estado desconectado - altura uniforme
            <div className="text-center py-6 sm:py-8 flex-1 flex flex-col justify-center">
              <div className="relative inline-flex items-center justify-center w-16 h-16 sm:w-20 sm:h-20 mb-4">
                <div className="absolute inset-0 bg-red-100 rounded-full animate-pulse opacity-75" />
                <div className="relative w-16 h-16 sm:w-20 sm:h-20 bg-red-50 rounded-full border border-red-200 flex items-center justify-center">
                  <WifiOff className="w-6 h-6 sm:w-8 sm:h-8 text-red-500" />
                </div>
              </div>
              <h4 className="text-sm sm:text-base font-bold text-red-700 mb-1">Desconectado</h4>
              <p className="text-xs sm:text-sm text-red-500">Reconectando...</p>
            </div>
          )
        }
        </div>
      </div>
    </div>
  );
};

const ModernRadarDisplay: React.FC<ModernRadarDisplayProps> = ({ multiRadarData }) => {
  // Configuração fixa dos radares
  const radarConfigs = [
    { radarId: "caldeira", radarName: "Caldeira" },
    { radarId: "porta_jusante", radarName: "Porta Jusante" },
    { radarId: "porta_montante", radarName: "Porta Montante" }
  ];

  const radars = radarConfigs.map(config => {
    if (multiRadarData?.radars) {
      const liveRadar = multiRadarData.radars.find(r => r.radarId === config.radarId);
      return liveRadar || { 
        ...config, 
        connected: false, 
        positions: [], 
        velocities: [], 
        azimuths: [], 
        amplitudes: [], 
        timestamp: Date.now() 
      };
    }
    return { 
      ...config, 
      connected: false, 
      positions: [], 
      velocities: [], 
      azimuths: [], 
      amplitudes: [], 
      timestamp: Date.now() 
    };
  });

  const connectedCount = radars.filter(r => r.connected).length;
  const totalDetections = radars.reduce((sum, r) => sum + (r.positions?.length || 0), 0);

  return (
    <div className="min-h-screen bg-gray-50">
      
      {/* Header elegante e clean */}
      <header className="bg-white border-b border-gray-200 sticky top-0 z-50 backdrop-blur-xl bg-white/95">
        <div className="max-w-7xl mx-auto">
          <div className="flex items-center justify-between px-4 sm:px-6 lg:px-8 h-16">
            
            {/* Brand section */}
            <div className="flex items-center gap-4">
              <div className="w-10 h-10 bg-gradient-to-r from-blue-600 to-indigo-600 rounded-xl flex items-center justify-center shadow-lg">
                <Zap className="w-5 h-5 text-white" />
              </div>
              <div>
                <h1 className="text-xl font-bold text-gray-900 tracking-tight">DH Automação</h1>
                <p className="text-sm text-gray-500 hidden sm:block">Monitoramento de Radares</p>
              </div>
            </div>

            {/* Status global compacto */}
            <div className="flex items-center gap-3">
              <div className="flex items-center gap-2">
                <div className={`w-3 h-3 rounded-full ${
                  connectedCount === 3 ? 'bg-green-500' : 
                  connectedCount > 0 ? 'bg-yellow-500' : 'bg-red-500'
                } animate-pulse`} />
                <span className="text-sm font-semibold text-gray-700">
                  {connectedCount}/3 Online
                </span>
              </div>
              
              <div className="hidden sm:flex items-center gap-2 px-3 py-1.5 bg-blue-50 rounded-lg">
                <Signal className="w-4 h-4 text-blue-600" />
                <span className="text-sm font-semibold text-blue-700">
                  {totalDetections} objetos
                </span>
              </div>
            </div>
          </div>
        </div>
      </header>

      {/* Container principal */}
      <main className="max-w-7xl mx-auto p-4 sm:p-6 lg:p-8">
        
        {/* Grid responsivo de radares - Mobile otimizado */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 sm:gap-6">
          {radars.map((radar) => (
            <RadarCard key={radar.radarId} radar={radar} />
          ))}
        </div>

        {/* Footer responsivo com timestamp */}
        {multiRadarData && (
          <footer className="mt-6 sm:mt-8 text-center">
            <div className="inline-flex items-center gap-2 sm:gap-3 bg-white rounded-xl sm:rounded-2xl px-4 sm:px-6 py-2 sm:py-3 shadow-sm border border-gray-200">
              <Clock className="w-3 h-3 sm:w-4 sm:h-4 text-gray-400" />
              <span className="text-xs sm:text-sm text-gray-600 font-medium">
                {new Date(multiRadarData.timestamp).toLocaleString('pt-BR', {
                  hour: '2-digit',
                  minute: '2-digit',
                  second: '2-digit'
                })}
              </span>
            </div>
          </footer>
        )}
      </main>
    </div>
  );
};

export default ModernRadarDisplay;