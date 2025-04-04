import { format } from 'date-fns';
import { getChangeColor } from '../utils/format';
import useRadarStore from '../stores/radarStore';

interface ChangesHistoryProps {
  selectedSensors: number[];
}

const ChangesHistory = ({ selectedSensors }: ChangesHistoryProps) => {
  const { velocityChangesHistory } = useRadarStore();

  // Filtrar histórico apenas com os sensores selecionados
  const filteredHistory = velocityChangesHistory.filter(change => 
    selectedSensors.includes(change.index)
  );

  if (selectedSensors.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Selecione ao menos um sensor para visualizar mudanças.</p>
      </div>
    );
  }

  if (filteredHistory.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center text-center p-8 bg-gray-50 rounded-lg">
        <svg 
          xmlns="http://www.w3.org/2000/svg" 
          className="h-12 w-12 text-gray-400 mb-4" 
          fill="none" 
          viewBox="0 0 24 24" 
          stroke="currentColor"
        >
          <path 
            strokeLinecap="round" 
            strokeLinejoin="round" 
            strokeWidth={1.5} 
            d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" 
          />
        </svg>
        <p className="text-lg font-medium text-gray-700">Nenhuma mudança registrada</p>
        <p className="mt-1 text-sm text-gray-500 max-w-sm">
          Mudanças significativas de velocidade nos sensores selecionados aparecerão aqui.
        </p>
      </div>
    );
  }

  return (
    <div className="overflow-auto max-h-96 -mx-4 sm:mx-0">
      <table className="min-w-full divide-y divide-gray-200">
        <thead className="bg-gray-50">
          <tr>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Horário
            </th>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Sensor
            </th>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Posição
            </th>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Anterior
            </th>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Novo
            </th>
            <th scope="col" className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Variação
            </th>
          </tr>
        </thead>
        <tbody className="bg-white divide-y divide-gray-200">
          {filteredHistory.map((change, idx) => (
            <tr key={idx} className={idx % 2 === 0 ? 'bg-white hover:bg-gray-50' : 'bg-gray-50 hover:bg-gray-100'}>
              <td className="px-4 py-3 whitespace-nowrap text-sm text-gray-500">
                <div className="font-medium">{format(new Date(change.timestamp), 'HH:mm:ss')}</div>
                <div className="text-xs">{format(new Date(change.timestamp), 'dd/MM/yyyy')}</div>
              </td>
              <td className="px-4 py-3 whitespace-nowrap">
                <div className="flex items-center">
                  <div className="flex-shrink-0 h-8 w-8 rounded-full bg-blue-100 flex items-center justify-center">
                    <span className="text-xs font-medium text-blue-800">{change.index + 1}</span>
                  </div>
                  <div className="ml-3">
                    <div className="text-sm font-medium text-gray-900">
                      Sensor {change.index + 1}
                    </div>
                  </div>
                </div>
              </td>
              <td className="px-4 py-3 whitespace-nowrap">
                <div className="text-sm font-medium text-gray-900">
                  {change.position !== undefined ? change.position.toFixed(2) + ' m' : 'N/A'}
                </div>
                <div className="text-xs text-gray-500">Distância</div>
              </td>
              <td className="px-4 py-3 whitespace-nowrap text-sm text-gray-500">
                {change.old_value.toFixed(2)} m/s
              </td>
              <td className="px-4 py-3 whitespace-nowrap text-sm text-gray-500">
                {change.new_value.toFixed(2)} m/s
              </td>
              <td className="px-4 py-3 whitespace-nowrap">
                <span className={`px-2 py-1 inline-flex text-xs leading-5 font-semibold rounded-full ${getChangeClass(change.change_value)}`}>
                  {getChangePrefix(change.change_value)}{Math.abs(change.change_value).toFixed(2)} m/s
                </span>
                <div className="text-xs text-gray-400 mt-1">
                  {getChangeDescription(change.change_value)}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

// Helpers para formatação visual
const getChangeClass = (value: number): string => {
  if (value > 0) return 'bg-green-100 text-green-800';
  if (value < 0) return 'bg-red-100 text-red-800';
  return 'bg-gray-100 text-gray-800';
};

const getChangePrefix = (value: number): string => {
  if (value > 0) return '+';
  if (value < 0) return '-';
  return '';
};

const getChangeDescription = (value: number): string => {
  const absValue = Math.abs(value);
  
  if (absValue < 0.1) {
    return "Mudança mínima";
  } else if (absValue < 0.5) {
    return "Mudança pequena";
  } else if (absValue < 1) {
    return "Mudança moderada";
  } else if (absValue < 3) {
    return "Mudança significativa";
  } else {
    return "Mudança crítica";
  }
};

export default ChangesHistory;