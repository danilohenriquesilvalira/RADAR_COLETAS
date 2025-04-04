import { useState } from 'react';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer, LineChart, Line } from 'recharts';
import useRadarStore from '../stores/radarStore';

interface VelocityChartProps {
  selectedSensors: number[];
}

const VelocityChart = ({ selectedSensors }: VelocityChartProps) => {
  const { velocities } = useRadarStore();
  const [chartType, setChartType] = useState<'bar' | 'line'>('bar');
  
  // Preparar dados para o gráfico com apenas os sensores selecionados
  const chartData = velocities
    .filter((_, index) => selectedSensors.includes(index))
    .map((velocity, index) => {
      // Encontrar o índice original no array completo
      const originalIndex = selectedSensors[index];
      return {
        name: `Sensor ${originalIndex + 1}`,
        velocidade: velocity,
        originalIndex
      };
    });

  // Determinar cores dinâmicas baseadas nos valores
  const getBarColor = (value: number) => {
    const absValue = Math.abs(value);
    if (absValue > 3) return '#dc2626'; // vermelho
    if (absValue > 1) return '#f97316'; // laranja
    if (absValue > 0.5) return '#eab308'; // amarelo
    if (absValue > 0.1) return '#22c55e'; // verde
    return '#a3a3a3'; // cinza
  };

  // Verificar se temos dados para exibir
  if (chartData.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Selecione ao menos um sensor para visualizar dados.</p>
      </div>
    );
  }

  return (
    <div className="h-72">
      <div className="flex justify-end mb-2">
        <div className="inline-flex rounded-md shadow-sm">
          <button
            type="button"
            onClick={() => setChartType('bar')}
            className={`relative inline-flex items-center px-3 py-1.5 text-xs font-medium rounded-l-md ${
              chartType === 'bar'
                ? 'bg-blue-600 text-white'
                : 'bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5 mr-1" viewBox="0 0 20 20" fill="currentColor">
              <path d="M2 11a1 1 0 011-1h2a1 1 0 011 1v5a1 1 0 01-1 1H3a1 1 0 01-1-1v-5zM8 7a1 1 0 011-1h2a1 1 0 011 1v9a1 1 0 01-1 1H9a1 1 0 01-1-1V7zM14 4a1 1 0 011-1h2a1 1 0 011 1v12a1 1 0 01-1 1h-2a1 1 0 01-1-1V4z" />
            </svg>
            Barras
          </button>
          <button
            type="button"
            onClick={() => setChartType('line')}
            className={`relative inline-flex items-center px-3 py-1.5 text-xs font-medium rounded-r-md ${
              chartType === 'line'
                ? 'bg-blue-600 text-white'
                : 'bg-white text-gray-700 hover:bg-gray-50'
            }`}
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-3.5 w-3.5 mr-1" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M5 17a1 1 0 01-1-1V9.764a1 1 0 01.553-.894l6-3a1 1 0 01.894 0l6 3A1 1 0 0118 9.764V16a1 1 0 01-1 1H5z" clipRule="evenodd" />
            </svg>
            Linha
          </button>
        </div>
      </div>

      <ResponsiveContainer width="100%" height="100%">
        {chartType === 'bar' ? (
          <BarChart data={chartData} margin={{ top: 20, right: 30, left: 20, bottom: 20 }}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} opacity={0.3} />
            <XAxis 
              dataKey="name" 
              axisLine={false}
              tickLine={false}
              tick={{ fontSize: 11 }}
            />
            <YAxis 
              label={{ 
                value: 'Velocidade (m/s)', 
                angle: -90, 
                position: 'insideLeft',
                style: { fontSize: 11, fill: '#6b7280' }
              }}
              axisLine={false}
              tickLine={false}
              tick={{ fontSize: 10 }}
            />
            <Tooltip 
              formatter={(value) => [`${Number(value).toFixed(2)} m/s`, 'Velocidade']}
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
              wrapperStyle={{ fontSize: '11px', paddingTop: '10px' }}
            />
            {chartData.map((entry) => (
              <Bar
                key={`bar-${entry.originalIndex}`}
                dataKey="velocidade"
                name={`Sensor ${entry.originalIndex + 1}`}
                fill={getBarColor(velocities[entry.originalIndex])}
                radius={[4, 4, 0, 0]}
              />
            ))}
          </BarChart>
        ) : (
          <LineChart data={chartData} margin={{ top: 20, right: 30, left: 20, bottom: 20 }}>
            <CartesianGrid strokeDasharray="3 3" opacity={0.3} />
            <XAxis 
              dataKey="name" 
              axisLine={false}
              tickLine={false}
              tick={{ fontSize: 11 }}
            />
            <YAxis 
              label={{ 
                value: 'Velocidade (m/s)', 
                angle: -90, 
                position: 'insideLeft',
                style: { fontSize: 11, fill: '#6b7280' }
              }}
              axisLine={false}
              tickLine={false}
              tick={{ fontSize: 10 }}
            />
            <Tooltip 
              formatter={(value) => [`${Number(value).toFixed(2)} m/s`, 'Velocidade']}
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
              wrapperStyle={{ fontSize: '11px', paddingTop: '10px' }}
            />
            {chartData.map((entry) => (
              <Line
                key={`line-${entry.originalIndex}`}
                type="monotone"
                dataKey="velocidade"
                name={`Sensor ${entry.originalIndex + 1}`}
                stroke={getBarColor(velocities[entry.originalIndex])}
                strokeWidth={2}
                dot={{ r: 4, fill: getBarColor(velocities[entry.originalIndex]) }}
                activeDot={{ r: 6 }}
              />
            ))}
          </LineChart>
        )}
      </ResponsiveContainer>
    </div>
  );
};

export default VelocityChart;