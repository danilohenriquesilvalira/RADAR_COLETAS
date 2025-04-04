import { useState, useMemo, memo, useCallback } from 'react';
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer, AreaChart, Area } from 'recharts';
import { formatTime } from '../utils/format';
import useRadarStore from '../stores/radarStore';
import { TimeRange } from './Dashboard';

interface HistoricalChartProps {
  timeRange: TimeRange;
  selectedSensors: number[];
}

const HistoricalChart = memo(({ timeRange, selectedSensors }: HistoricalChartProps) => {
  const { historicalData } = useRadarStore();
  const [chartType, setChartType] = useState<'line' | 'area'>('line');
  
  // Processar dados do gráfico apenas quando necessário
  const chartData = useMemo(() => {
    if (historicalData.timestamps.length === 0) {
      return [];
    }

    // Filtrar dados conforme o período selecionado
    let filteredData = [...historicalData.timestamps];
    let startIndex = 0;
    
    if (timeRange !== 'all') {
      // Obter timestamp atual
      const now = Date.now();
      let timeWindowMs = 0;
      
      // Converter intervalo selecionado para milissegundos
      switch (timeRange) {
        case '1m': timeWindowMs = 60 * 1000; break;
        case '5m': timeWindowMs = 5 * 60 * 1000; break;
        case '15m': timeWindowMs = 15 * 60 * 1000; break;
        case '30m': timeWindowMs = 30 * 60 * 1000; break;
        case '1h': timeWindowMs = 60 * 60 * 1000; break;
        case '6h': timeWindowMs = 6 * 60 * 60 * 1000; break;
        case '12h': timeWindowMs = 12 * 60 * 60 * 1000; break;
        case '24h': timeWindowMs = 24 * 60 * 60 * 1000; break;
        default: break;
      }
      
      // Encontrar o índice de corte
      const cutoffTime = now - timeWindowMs;
      startIndex = filteredData.findIndex(timestamp => timestamp >= cutoffTime);
      
      if (startIndex === -1) startIndex = 0;
    }
    
    // Cortar o array de timestamps
    filteredData = filteredData.slice(startIndex);
    
    // Preparar dados para o gráfico
    return filteredData.map((timestamp, idx) => {
      const realIdx = idx + startIndex;
      const dataPoint: { [key: string]: any } = {
        time: formatTime(timestamp)
      };
      
      // Adicionar valores apenas para os sensores selecionados
      selectedSensors.forEach(sensorIdx => {
        const velocityData = historicalData.velocityData[sensorIdx];
        if (velocityData && realIdx < velocityData.length) {
          dataPoint[`sensor${sensorIdx + 1}`] = velocityData[realIdx];
        }
      });
      
      return dataPoint;
    });
  }, [historicalData, timeRange, selectedSensors]);

  // Gerar cores para cada linha
  const getLineColor = useCallback((index: number) => {
    const colors = [
      '#2563eb', // azul
      '#16a34a', // verde
      '#d97706', // âmbar
      '#dc2626', // vermelho
      '#7c3aed', // roxo
      '#0891b2', // ciano
      '#db2777', // rosa
    ];
    return colors[index % colors.length];
  }, []);

  // Handler para mudar o tipo de gráfico
  const toggleChartType = useCallback(() => {
    setChartType(prev => prev === 'line' ? 'area' : 'line');
  }, []);

  // Renderizar mensagem se não houver dados
  if (historicalData.timestamps.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Aguardando dados históricos...</p>
      </div>
    );
  }

  if (selectedSensors.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Selecione ao menos um sensor para visualizar dados históricos.</p>
      </div>
    );
  }

  // Renderizar mensagem se não houver dados para o período selecionado
  if (chartData.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Sem dados disponíveis para o período selecionado.</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex justify-between items-center mb-3">
        <div className="text-sm text-gray-500">
          Exibindo {chartData.length} pontos de dados {timeRange === 'all' ? 'de todo o período' : `dos últimos ${timeRange}`}
        </div>
        
        <div className="inline-flex rounded-md shadow-sm">
          <button
            type="button"
            onClick={toggleChartType}
            className={`relative inline-flex items-center px-3 py-1.5 text-xs font-medium rounded-md ${
              chartType === 'line'
                ? 'bg-blue-600 text-white'
                : 'bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5 mr-1" viewBox="0 0 20 20" fill="currentColor">
              <path d="M2 10h16m-2-2l2 2-2 2m-12-4l-2 2 2 2" strokeWidth="2" stroke="currentColor" fill="none" />
            </svg>
            Linha
          </button>
          <button
            type="button"
            onClick={toggleChartType}
            className={`relative inline-flex items-center px-3 py-1.5 text-xs font-medium rounded-md ${
              chartType === 'area'
                ? 'bg-blue-600 text-white'
                : 'bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5 mr-1" viewBox="0 0 20 20" fill="currentColor">
              <path d="M2 10h16m-2-2l2 2-2 2m-12-4l-2 2 2 2M2 15h16M2 5h16" strokeWidth="1" stroke="currentColor" fill="none" />
            </svg>
            Área
          </button>
        </div>
      </div>
      
      <div className="h-80">
        <ResponsiveContainer width="100%" height="100%">
          {chartType === 'line' ? (
            <LineChart 
              data={chartData} 
              margin={{ top: 5, right: 30, left: 20, bottom: 5 }}
            >
              <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
              <XAxis 
                dataKey="time" 
                tick={{ fontSize: 10 }}
                interval="preserveStartEnd"
                axisLine={false}
                tickLine={false}
              />
              <YAxis 
                label={{ 
                  value: 'Velocidade (m/s)', 
                  angle: -90, 
                  position: 'insideLeft', 
                  style: { textAnchor: 'middle', fontSize: 11, fill: '#6b7280' } 
                }} 
                tick={{ fontSize: 10 }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip
                formatter={(value: number) => [`${value.toFixed(2)} m/s`, 'Velocidade']}
                contentStyle={{ 
                  backgroundColor: 'rgba(255, 255, 255, 0.95)',
                  borderRadius: '6px',
                  boxShadow: '0 2px 5px rgba(0,0,0,0.1)',
                  border: '1px solid #e5e7eb',
                  fontSize: '12px'
                }}
              />
              <Legend 
                iconType="circle" 
                iconSize={8}
                wrapperStyle={{ fontSize: '11px' }}
              />
              
              {selectedSensors.map((sensorIdx) => (
                <Line
                  key={`sensor-${sensorIdx}`}
                  type="monotone"
                  dataKey={`sensor${sensorIdx + 1}`}
                  name={`Sensor ${sensorIdx + 1}`}
                  stroke={getLineColor(sensorIdx)}
                  activeDot={{ r: 6 }}
                  dot={false}
                  strokeWidth={1.5}
                  isAnimationActive={false} // Desativar animação para melhor desempenho
                />
              ))}
            </LineChart>
          ) : (
            <AreaChart 
              data={chartData} 
              margin={{ top: 5, right: 30, left: 20, bottom: 5 }}
            >
              <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
              <XAxis 
                dataKey="time" 
                tick={{ fontSize: 10 }}
                interval="preserveStartEnd"
                axisLine={false}
                tickLine={false}
              />
              <YAxis 
                label={{ 
                  value: 'Velocidade (m/s)', 
                  angle: -90, 
                  position: 'insideLeft', 
                  style: { textAnchor: 'middle', fontSize: 11, fill: '#6b7280' } 
                }} 
                tick={{ fontSize: 10 }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip
                formatter={(value: number) => [`${value.toFixed(2)} m/s`, 'Velocidade']}
                contentStyle={{ 
                  backgroundColor: 'rgba(255, 255, 255, 0.95)',
                  borderRadius: '6px',
                  boxShadow: '0 2px 5px rgba(0,0,0,0.1)',
                  border: '1px solid #e5e7eb',
                  fontSize: '12px'
                }}
              />
              <Legend 
                iconType="circle" 
                iconSize={8}
                wrapperStyle={{ fontSize: '11px' }}
              />
              
              {selectedSensors.map((sensorIdx) => (
                <Area
                  key={`sensor-${sensorIdx}`}
                  type="monotone"
                  dataKey={`sensor${sensorIdx + 1}`}
                  name={`Sensor ${sensorIdx + 1}`}
                  stroke={getLineColor(sensorIdx)}
                  fill={getLineColor(sensorIdx)}
                  fillOpacity={0.2}
                  activeDot={{ r: 6 }}
                  dot={false}
                  strokeWidth={1.5}
                  isAnimationActive={false} // Desativar animação para melhor desempenho
                />
              ))}
            </AreaChart>
          )}
        </ResponsiveContainer>
      </div>
    </div>
  );
});

HistoricalChart.displayName = 'HistoricalChart';

export default HistoricalChart;