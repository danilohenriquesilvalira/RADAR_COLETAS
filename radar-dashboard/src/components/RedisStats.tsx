import { motion } from 'framer-motion';
import { HistoryStats } from '../types';
import { formatTimestamp } from '../utils/format';

interface RedisStatsProps {
  stats: HistoryStats;
}

const StatCard = ({ title, value, unit, icon, color }: {
  title: string;
  value: number | string;
  unit?: string;
  icon?: React.ReactNode;
  color: string;
}) => {
  return (
    <div className={`bg-white rounded-lg shadow border-l-4 border-${color}-400 p-4`}>
      <div className="flex justify-between items-start">
        <div>
          <p className="text-sm text-gray-500">{title}</p>
          <div className="flex items-baseline mt-1">
            <p className="text-2xl font-semibold">{value}</p>
            {unit && <p className="ml-1 text-gray-500">{unit}</p>}
          </div>
        </div>
        {icon && (
          <div className={`rounded-md p-2 bg-${color}-100 text-${color}-600`}>
            {icon}
          </div>
        )}
      </div>
    </div>
  );
};

const RedisStats = ({ stats }: RedisStatsProps) => {
  // Verificar se temos dados
  if (!stats) {
    return (
      <div className="text-center p-4 text-gray-500">
        Dados estatísticos não disponíveis.
      </div>
    );
  }

  // Renderização
  return (
    <motion.div 
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.5 }}
      className="space-y-4"
    >
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="Total de Mudanças"
          value={stats.total_changes}
          color="blue"
          icon={
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path d="M13 6a3 3 0 11-6 0 3 3 0 016 0zM18 8a2 2 0 11-4 0 2 2 0 014 0zM14 15a4 4 0 00-8 0v3h8v-3zM6 8a2 2 0 11-4 0 2 2 0 014 0zM16 18v-3a5.972 5.972 0 00-.75-2.906A3.005 3.005 0 0119 15v3h-3zM4.75 12.094A5.973 5.973 0 004 15v3H1v-3a3 3 0 013.75-2.906z" />
            </svg>
          }
        />
        
        <StatCard
          title="Velocidade Máxima"
          value={stats.max_velocity.toFixed(2)}
          unit="m/s"
          color="green"
          icon={
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M12 7a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0V8.414l-4.293 4.293a1 1 0 01-1.414 0L8 10.414l-4.293 4.293a1 1 0 01-1.414-1.414l5-5a1 1 0 011.414 0L11 10.586 14.586 7H12z" clipRule="evenodd" />
            </svg>
          }
        />
        
        <StatCard
          title="Velocidade Mínima"
          value={stats.min_velocity.toFixed(2)}
          unit="m/s"
          color="red"
          icon={
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M12 13a1 1 0 100 2h5a1 1 0 001-1V9a1 1 0 10-2 0v2.586l-4.293-4.293a1 1 0 00-1.414 0L8 9.586 3.707 5.293a1 1 0 00-1.414 1.414l5 5a1 1 0 001.414 0L11 9.414 14.586 13H12z" clipRule="evenodd" />
            </svg>
          }
        />
        
        <StatCard
          title="Velocidade Média"
          value={stats.avg_velocity.toFixed(2)}
          unit="m/s"
          color="yellow"
          icon={
            <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 20 20" fill="currentColor">
              <path d="M2 11a1 1 0 011-1h2a1 1 0 011 1v5a1 1 0 01-1 1H3a1 1 0 01-1-1v-5zM8 7a1 1 0 011-1h2a1 1 0 011 1v9a1 1 0 01-1 1H9a1 1 0 01-1-1V7zM14 4a1 1 0 011-1h2a1 1 0 011 1v12a1 1 0 01-1 1h-2a1 1 0 01-1-1V4z" />
            </svg>
          }
        />
      </div>
      
      <div className="text-sm text-gray-500 text-right italic">
        Última atualização: {formatTimestamp(stats.last_updated)}
      </div>
    </motion.div>
  );
};

export default RedisStats;