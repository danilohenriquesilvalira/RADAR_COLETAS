import useRadarStore from '../stores/radarStore';

export const ConnectionStatus = () => {
  const { connectionStatus } = useRadarStore();

  let statusInfo = {
    color: 'bg-gray-500',
    text: 'Inicializando...',
    bgColor: 'bg-gray-100',
    textColor: 'text-gray-800'
  };

  switch (connectionStatus) {
    case 'connected':
      statusInfo = {
        color: 'bg-green-500',
        text: 'Conectado',
        bgColor: 'bg-green-100',
        textColor: 'text-green-800'
      };
      break;
    case 'disconnected':
      statusInfo = {
        color: 'bg-yellow-500',
        text: 'Desconectado',
        bgColor: 'bg-yellow-100',
        textColor: 'text-yellow-800'
      };
      break;
    case 'error':
      statusInfo = {
        color: 'bg-red-500',
        text: 'Erro de Conexão',
        bgColor: 'bg-red-100',
        textColor: 'text-red-800'
      };
      break;
  }

  return (
    <div className={`flex items-center ${statusInfo.bgColor} px-3 py-1 rounded-full`}>
      <div className={`${statusInfo.color} h-3 w-3 rounded-full animate-pulse mr-2`}></div>
      <span className={`text-sm font-medium ${statusInfo.textColor}`}>
        {statusInfo.text}
      </span>
    </div>
  );
};

export default ConnectionStatus;