import { motion } from 'framer-motion';

interface FilterPanelProps {
  selectedSensors: number[];
  onSensorToggle: (sensorIndex: number) => void;
  onSelectAll: () => void;
  onClearAll: () => void;
}

const FilterPanel = ({ 
  selectedSensors, 
  onSensorToggle, 
  onSelectAll, 
  onClearAll 
}: FilterPanelProps) => {
  // Sensores disponíveis (0-6)
  const allSensors = [0, 1, 2, 3, 4, 5, 6];
  
  // Cores para cada sensor (para consistência visual)
  const getSensorColor = (index: number): string => {
    const colors = [
      'bg-blue-500',
      'bg-emerald-500',
      'bg-amber-500',
      'bg-red-500',
      'bg-purple-500',
      'bg-cyan-500',
      'bg-pink-500',
    ];
    return colors[index % colors.length];
  };

  // Variantes para animação
  const variants = {
    hidden: { opacity: 0, height: 0 },
    visible: { opacity: 1, height: 'auto' }
  };

  return (
    <motion.div 
      className="bg-white rounded-lg shadow-md p-4 mb-6"
      initial="hidden"
      animate="visible"
      exit="hidden"
      variants={variants}
      transition={{ duration: 0.3 }}
    >
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold flex items-center">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5 mr-2 text-blue-600" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M3 3a1 1 0 011-1h12a1 1 0 011 1v3a1 1 0 01-.293.707L12 11.414V15a1 1 0 01-.293.707l-2 2A1 1 0 018 17v-5.586L3.293 6.707A1 1 0 013 6V3z" clipRule="evenodd" />
          </svg>
          Filtros
        </h2>
        <div className="flex space-x-2">
          <button 
            onClick={onSelectAll}
            className="px-3 py-1 bg-blue-50 hover:bg-blue-100 text-blue-600 text-sm rounded-md transition-colors"
          >
            Selecionar Todos
          </button>
          <button 
            onClick={onClearAll}
            className="px-3 py-1 bg-gray-50 hover:bg-gray-100 text-gray-600 text-sm rounded-md transition-colors"
          >
            Limpar
          </button>
        </div>
      </div>
      
      <div className="border-t border-gray-100 pt-3">
        <h3 className="text-sm font-medium text-gray-700 mb-2">Sensores:</h3>
        <div className="flex flex-wrap gap-2">
          {allSensors.map(sensorIndex => (
            <button
              key={sensorIndex}
              onClick={() => onSensorToggle(sensorIndex)}
              className={`
                flex items-center justify-center rounded-full w-10 h-10
                transition-all duration-200 
                ${selectedSensors.includes(sensorIndex) 
                  ? `${getSensorColor(sensorIndex)} text-white ring-2 ring-offset-2 ring-${getSensorColor(sensorIndex).replace('bg-', '')}`
                  : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
                }
              `}
            >
              {sensorIndex + 1}
            </button>
          ))}
        </div>
      </div>

      <div className="mt-4 pt-3 border-t border-gray-100">
        <div className="flex items-center text-sm text-gray-500">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 mr-1 text-blue-500" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a1 1 0 000 2v3a1 1 0 001 1h1a1 1 0 100-2v-3a1 1 0 00-1-1H9z" clipRule="evenodd" />
          </svg>
          <span>Selecione os sensores que deseja visualizar nos gráficos e tabelas.</span>
        </div>
      </div>
    </motion.div>
  );
};

export default FilterPanel;