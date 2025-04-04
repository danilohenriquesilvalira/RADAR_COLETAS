import { getVelocityColor } from '../utils/format';

interface VelocityTableProps {
  positions: number[];
  velocities: number[];
  selectedSensors: number[];
}

const VelocityTable = ({ positions, velocities, selectedSensors }: VelocityTableProps) => {
  // Se não houver sensores selecionados, exibe mensagem
  if (selectedSensors.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 bg-gray-50 rounded-lg">
        <p className="text-gray-500">Selecione ao menos um sensor para visualizar dados.</p>
      </div>
    );
  }

  // Filtrar apenas os sensores selecionados
  const filteredData = selectedSensors.map(index => ({
    index,
    position: positions[index],
    velocity: velocities[index]
  }));

  return (
    <div className="overflow-auto">
      <table className="min-w-full divide-y divide-gray-200">
        <thead className="bg-gray-50">
          <tr>
            <th scope="col" className="px-3 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Sensor
            </th>
            <th scope="col" className="px-3 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Posição (m)
            </th>
            <th scope="col" className="px-3 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Velocidade (m/s)
            </th>
            <th scope="col" className="px-3 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Status
            </th>
          </tr>
        </thead>
        <tbody className="bg-white divide-y divide-gray-200">
          {filteredData.map((item) => (
            <tr key={item.index} className="hover:bg-gray-50 transition-colors">
              <td className="px-3 py-2.5 whitespace-nowrap">
                <div className="flex items-center">
                  <div className="flex-shrink-0 h-8 w-8 rounded-full bg-blue-100 flex items-center justify-center">
                    <span className="text-xs font-medium text-blue-800">{item.index + 1}</span>
                  </div>
                  <div className="ml-3">
                    <div className="text-sm font-medium text-gray-900">
                      Sensor {item.index + 1}
                    </div>
                  </div>
                </div>
              </td>
              <td className="px-3 py-2.5 whitespace-nowrap">
                <div className="text-sm text-gray-900">{item.position.toFixed(2)} m</div>
                <div className="text-xs text-gray-500">Distância</div>
              </td>
              <td className="px-3 py-2.5 whitespace-nowrap">
                <div className={`text-sm font-medium ${getVelocityColor(item.velocity)}`}>
                  {item.velocity.toFixed(2)} m/s
                </div>
                <div className="text-xs text-gray-500">Velocidade</div>
              </td>
              <td className="px-3 py-2.5 whitespace-nowrap">
                <span className={getStatusBadge(item.velocity)}>
                  {getStatusText(item.velocity)}
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

// Helpers para determinar o status baseado na velocidade
const getStatusBadge = (velocity: number): string => {
  const absVelocity = Math.abs(velocity);
  if (absVelocity < 0.1) {
    return "px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-gray-100 text-gray-800";
  } else if (absVelocity < 1) {
    return "px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-green-100 text-green-800";
  } else if (absVelocity < 3) {
    return "px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-yellow-100 text-yellow-800";
  } else {
    return "px-2 inline-flex text-xs leading-5 font-semibold rounded-full bg-red-100 text-red-800";
  }
};

const getStatusText = (velocity: number): string => {
  const absVelocity = Math.abs(velocity);
  if (absVelocity < 0.1) {
    return "Parado";
  } else if (absVelocity < 1) {
    return "Movimento lento";
  } else if (absVelocity < 3) {
    return "Movimento moderado";
  } else {
    return "Movimento rápido";
  }
};

export default VelocityTable;