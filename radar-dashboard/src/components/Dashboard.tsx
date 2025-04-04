import { useState } from 'react';
import { formatTimestamp } from '../utils/format';
import useRadarStore from '../stores/radarStore';
import Header from './Header';
import MetricGrid from './MetricGrid';
import VelocityTable from './VelocityTable';
import VelocityChart from './VelocityChart';
import RadarVisualization from './RadarVisualization';
import HistoricalChart from './HistoricalChart';
import RedisStats from './RedisStats';
import ChangesHistory from './ChangesHistory';
import { ConnectionStatus } from './ConnectionStatus';
import TimeRangeSelector from './TimeRangeSelector';
import FilterPanel from './FilterPanel';

// Tipos para filtros
export type TimeRange = '1m' | '5m' | '15m' | '30m' | '1h' | '6h' | '12h' | '24h' | 'all';
export type SensorFilter = number[];

const Dashboard = () => {
  const { 
    positions, 
    velocities,
    timestamp,
    historyStats
  } = useRadarStore();

  // Estados para filtros
  const [timeRange, setTimeRange] = useState<TimeRange>('15m');
  const [selectedSensors, setSelectedSensors] = useState<SensorFilter>([0, 1, 2, 3, 4, 5, 6]);
  const [showFilterPanel, setShowFilterPanel] = useState(false);

  // Handler para mudar filtro de tempo
  const handleTimeRangeChange = (range: TimeRange) => {
    setTimeRange(range);
  };

  // Handler para alternar sensores
  const handleSensorToggle = (sensorIndex: number) => {
    if (selectedSensors.includes(sensorIndex)) {
      setSelectedSensors(selectedSensors.filter(idx => idx !== sensorIndex));
    } else {
      setSelectedSensors([...selectedSensors, sensorIndex].sort());
    }
  };

  // Handler para selecionar todos sensores
  const handleSelectAllSensors = () => {
    setSelectedSensors([0, 1, 2, 3, 4, 5, 6]);
  };

  // Handler para limpar seleção
  const handleClearSensors = () => {
    setSelectedSensors([]);
  };

  return (
    <div className="min-h-screen bg-gray-100">
      <Header onToggleFilters={() => setShowFilterPanel(!showFilterPanel)} showFilters={showFilterPanel} />

      <main className="container mx-auto px-4 py-6">
        {/* Painel superior com status e filtros */}
        <div className="flex flex-col lg:flex-row justify-between gap-4 mb-6">
          <div className="bg-white p-4 rounded-lg shadow flex-grow">
            <div className="flex justify-between items-center">
              <ConnectionStatus />
              <div className="text-sm text-gray-500">
                Última atualização: {timestamp ? formatTimestamp(timestamp) : 'N/A'}
              </div>
            </div>
          </div>
          
          <TimeRangeSelector 
            currentRange={timeRange} 
            onRangeChange={handleTimeRangeChange} 
          />
        </div>

        {/* Painel de filtros condicional */}
        {showFilterPanel && (
          <FilterPanel 
            selectedSensors={selectedSensors}
            onSensorToggle={handleSensorToggle}
            onSelectAll={handleSelectAllSensors}
            onClearAll={handleClearSensors}
          />
        )}

        {/* Layout principal com duas colunas em telas grandes */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-6">
          {/* Coluna da esquerda (1/3 da largura) - Visualização de radar */}
          <div className="lg:col-span-1">
            <div className="bg-white rounded-lg shadow-md p-4 h-full">
              <h2 className="text-lg font-semibold mb-4 flex items-center">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                  <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm0-1a7 7 0 110-14 7 7 0 010 14z" clipRule="evenodd" />
                  <path fillRule="evenodd" d="M10 5.172a1 1 0 00-1 1v4a1 1 0 00.293.707l3 3a1 1 0 001.414-1.414L10 8.757V6.172a1 1 0 00-1-1z" clipRule="evenodd" />
                </svg>
                Visualização do Radar
              </h2>
              <RadarVisualization 
                positions={positions} 
                velocities={velocities}
                selectedSensors={selectedSensors}
              />
            </div>
          </div>

          {/* Coluna da direita (2/3 da largura) - Gráficos e métricas */}
          <div className="lg:col-span-2">
            <div className="space-y-6">
              {/* Métricas em tempo real */}
              <div className="bg-white rounded-lg shadow-md p-4">
                <h2 className="text-lg font-semibold mb-4 flex items-center">
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                    <path fillRule="evenodd" d="M5 2a1 1 0 011 1v1h1a1 1 0 010 2H6v1a1 1 0 01-2 0V6H3a1 1 0 010-2h1V3a1 1 0 011-1zm0 10a1 1 0 011 1v1h1a1 1 0 110 2H6v1a1 1 0 11-2 0v-1H3a1 1 0 110-2h1v-1a1 1 0 011-1zm7.5-5A3.5 3.5 0 1118 7.5 3.5 3.5 0 1114.5 11a3.5 3.5 0 110-7z" clipRule="evenodd" />
                  </svg>
                  Métricas em Tempo Real
                </h2>
                <MetricGrid 
                  positions={positions} 
                  velocities={velocities}
                  selectedSensors={selectedSensors}
                />
              </div>

              {/* Gráfico de velocidades */}
              <div className="bg-white rounded-lg shadow-md p-4">
                <h2 className="text-lg font-semibold mb-4 flex items-center">
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                    <path d="M2 11a1 1 0 011-1h2a1 1 0 011 1v5a1 1 0 01-1 1H3a1 1 0 01-1-1v-5zm6-4a1 1 0 011-1h2a1 1 0 011 1v9a1 1 0 01-1 1H9a1 1 0 01-1-1V7zm6-3a1 1 0 011-1h2a1 1 0 011 1v12a1 1 0 01-1 1h-2a1 1 0 01-1-1V4z" />
                  </svg>
                  Velocidades Atuais
                </h2>
                <VelocityChart selectedSensors={selectedSensors} />
              </div>
            </div>
          </div>
        </div>

        {/* Estatísticas do Redis */}
        {historyStats && (
          <div className="mb-6">
            <div className="bg-white rounded-lg shadow-md p-4">
              <h2 className="text-lg font-semibold mb-4 flex items-center">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                  <path fillRule="evenodd" d="M3 3a1 1 0 000 2v8a2 2 0 002 2h2.586l-1.293 1.293a1 1 0 101.414 1.414L10 15.414l2.293 2.293a1 1 0 001.414-1.414L12.414 15H15a2 2 0 002-2V5a1 1 0 100-2H3zm11 4a1 1 0 10-2 0v4a1 1 0 102 0V7zm-3 1a1 1 0 10-2 0v3a1 1 0 102 0V8zM8 9a1 1 0 00-2 0v2a1 1 0 102 0V9z" clipRule="evenodd" />
                </svg>
                Estatísticas Históricas
              </h2>
              <RedisStats stats={historyStats} />
            </div>
          </div>
        )}

        {/* Histórico de Velocidades */}
        <div className="mb-6">
          <div className="bg-white rounded-lg shadow-md p-4">
            <h2 className="text-lg font-semibold mb-4 flex items-center">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                <path fillRule="evenodd" d="M3 12a1 1 0 00-1 1v5a1 1 0 001 1h5a1 1 0 001-1v-5a1 1 0 00-1-1H3zm11-4a1 1 0 10-2 0v9a1 1 0 001 1h5a1 1 0 001-1v-5a1 1 0 00-1-1h-4V8z" clipRule="evenodd" />
              </svg>
              Histórico de Velocidades
            </h2>
            <HistoricalChart 
              timeRange={timeRange}
              selectedSensors={selectedSensors}
            />
          </div>
        </div>

        {/* Dois painéis lado-a-lado em telas grandes */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-6">
          {/* Painel da esquerda */}
          <div className="bg-white rounded-lg shadow-md p-4">
            <h2 className="text-lg font-semibold mb-4 flex items-center">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                <path d="M3 4a1 1 0 011-1h12a1 1 0 011 1v2a1 1 0 01-1 1H4a1 1 0 01-1-1V4zM3 10a1 1 0 011-1h6a1 1 0 011 1v6a1 1 0 01-1 1H4a1 1 0 01-1-1v-6zM14 9a1 1 0 00-1 1v6a1 1 0 001 1h2a1 1 0 001-1v-6a1 1 0 00-1-1h-2z" />
              </svg>
              Valores Atuais
            </h2>
            <VelocityTable 
              positions={positions} 
              velocities={velocities} 
              selectedSensors={selectedSensors}
            />
          </div>

          {/* Painel da direita */}
          <div className="bg-white rounded-lg shadow-md p-4">
            <h2 className="text-lg font-semibold mb-4 flex items-center">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
                <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a1 1 0 000 2v3a1 1 0 001 1h1a1 1 0 100-2v-3a1 1 0 00-1-1H9z" clipRule="evenodd" />
              </svg>
              Histórico de Mudanças
            </h2>
            <ChangesHistory selectedSensors={selectedSensors} />
          </div>
        </div>
      </main>

      <footer className="bg-gray-800 text-white py-6">
        <div className="container mx-auto px-4">
          <div className="flex flex-col md:flex-row justify-between items-center">
            <div>
              <h3 className="text-lg font-semibold">RLS Automação Industrial</h3>
              <p className="text-sm text-gray-300">Monitoramento de Radar em Tempo Real</p>
            </div>
            <div className="text-sm text-gray-300 mt-2 md:mt-0">
              &copy; {new Date().getFullYear()} RLS Automação Industrial. Todos os direitos reservados.
            </div>
          </div>
        </div>
      </footer>
    </div>
  );
};

export default Dashboard;