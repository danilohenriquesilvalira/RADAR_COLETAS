import { memo, useMemo } from 'react';
import { getVelocityColor } from '../utils/format';

interface MetricCardProps {
  title: string;
  value: number;
  unit: string;
  index: number;
  color: string;
  icon?: React.ReactNode;
  isSelected: boolean;
}

// Componente de cartão individual memoizado
const MetricCard = memo(({ title, value, unit, index, color, icon, isSelected }: MetricCardProps) => {
  // Definir opacidade com base na seleção
  const opacity = isSelected ? 1 : 0.5;

  return (
    <div 
      className={`bg-white rounded-lg shadow-sm p-3 border-l-4 border-${color}-500 transition-all duration-200 ${isSelected ? '' : 'grayscale'}`}
      style={{ opacity }}
    >
      <div className="flex justify-between items-start">
        <div>
          <div className="text-sm text-gray-500">{title}</div>
          <div className="flex items-baseline mt-1">
            <div className={`text-xl font-semibold ${getVelocityColor(value)}`}>
              {value.toFixed(2)}
            </div>
            <div className="ml-1 text-gray-500">{unit}</div>
          </div>
        </div>
        {icon && (
          <div className={`rounded-full p-2 bg-${color}-100 text-${color}-600`}>
            {icon}
          </div>
        )}
      </div>
    </div>
  );
});

MetricCard.displayName = 'MetricCard';

interface MetricGridProps {
  positions: number[];
  velocities: number[];
  selectedSensors: number[];
}

const MetricGrid = memo(({ positions, velocities, selectedSensors }: MetricGridProps) => {
  // Memo para ícones
  const getIcon = useMemo(() => {
    return (velocity: number) => {
      const absVelocity = Math.abs(velocity);
      
      if (absVelocity < 0.1) {
        return (
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM7 9a1 1 0 000 2h6a1 1 0 100-2H7z" clipRule="evenodd" />
          </svg>
        );
      } else if (absVelocity < 1) {
        return (
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z" clipRule="evenodd" />
          </svg>
        );
      } else if (absVelocity < 3) {
        return (
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM9.555 7.168A1 1 0 008 8v4a1 1 0 001.555.832l3-2a1 1 0 000-1.664l-3-2z" clipRule="evenodd" />
          </svg>
        );
      } else {
        return (
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-3a1 1 0 00-.867.5l-2.5 4.33a1 1 0 00-.11.494v.223l.896 1.341a1 1 0 001.66 0l.894-1.341a.5.5 0 00-.224-.795l-.227-.11a1 1 0 01-.582-.844V7.5A1 1 0 0010 7zm-6 8a1 1 0 100-2 1 1 0 000 2zm16 0a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
          </svg>
        );
      }
    };
  }, []);

  // Memo para cor
  const getCardColor = useMemo(() => {
    return (velocity: number) => {
      const absVelocity = Math.abs(velocity);
      return absVelocity > 3 ? "red" : absVelocity > 1 ? "amber" : "blue";
    };
  }, []);

  return (
    <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 xl:grid-cols-7 gap-3">
      {positions.map((position, index) => (
        <MetricCard
          key={`card-${index}`}
          title={`Sensor ${index + 1}`}
          value={velocities[index]}
          unit="m/s"
          index={index}
          color={getCardColor(velocities[index])}
          icon={getIcon(velocities[index])}
          isSelected={selectedSensors.includes(index)}
        />
      ))}
    </div>
  );
});

MetricGrid.displayName = 'MetricGrid';

export default MetricGrid;