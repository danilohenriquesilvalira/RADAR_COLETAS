import { useState, useEffect } from 'react';
import { useWebSocket } from './hooks/useWebSocket';
import { useRadarApi } from './hooks/useRadarApi';
import { useAuth } from './hooks/useAuth';
import RadarMonitorApp from './components/RadarMonitorApp';
import LoginPage from './page/LoginPage';
import LoadingScreen from './components/LoadingScreen';

// Função para obter o URL do WebSocket baseado na localização atual
const getWebSocketUrl = () => {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const host = window.location.hostname;
  return `${protocol}//${host}:8080/ws`;
};

// URL padrão para o WebSocket
const DEFAULT_WS_URL = getWebSocketUrl();

function App() {
  // Hook de autenticação
  const { 
    isAuthenticated, 
    user, 
    login, 
    logout, 
    isLoading: authLoading, 
    error: authError 
  } = useAuth();

  // Estado para URL do WebSocket
  const [url, setUrl] = useState(DEFAULT_WS_URL);
  
  // Estado simples para controlar loading após login
  const [justLoggedIn, setJustLoggedIn] = useState(false);
  
  // Hook de WebSocket com gestão de dados
  const {
    status,
    error: wsError,
    lastMessage: radarData,
    lastUpdated,
    connect,
    disconnect
  } = useWebSocket(url, {
    autoReconnect: true,
    maxReconnectAttempts: 5,
    reconnectInterval: 3000
  });
  
  // Hook da API para controle de coleta
  const {
    isCollecting,
    isLoading,
    error: apiError,
    startCollection,
    stopCollection
  } = useRadarApi(url);
  
  // Manipulador para alteração de URL
  const handleUrlChange = (newUrl: string) => {
    setUrl(newUrl);
  };
  
  // Erro combinado (WebSocket ou API)
  const error = wsError || apiError;
  
  // Função de login simplificada
  const handleLogin = async (username: string, password: string) => {
    const success = await login(username, password);
    if (success) {
      setJustLoggedIn(true);
    }
    return success;
  };
  
  // Função quando loading screen termina
  const handleLoadingComplete = () => {
    setJustLoggedIn(false);
    // Conectar ao WebSocket após loading
    setTimeout(() => {
      connect();
    }, 500);
  };
  
  // Reset estado quando faz logout
  useEffect(() => {
    if (!isAuthenticated) {
      setJustLoggedIn(false);
    }
  }, [isAuthenticated]);

  console.log('App State:', { 
    isAuthenticated, 
    authLoading, 
    justLoggedIn, 
    user: user?.username 
  });

  // Loading inicial (verificando sessão)
  if (authLoading) {
    return (
      <div style={{ 
        display: 'flex', 
        alignItems: 'center', 
        justifyContent: 'center', 
        height: '100vh',
        fontSize: '18px',
        color: '#666'
      }}>
        Verificando sessão...
      </div>
    );
  }

  // Não está autenticado - mostrar login
  if (!isAuthenticated) {
    return (
      <LoginPage 
        onLogin={handleLogin}
        isLoading={authLoading}
        error={authError}
      />
    );
  }

  // Acabou de fazer login - mostrar loading screen
  if (justLoggedIn) {
    return <LoadingScreen onComplete={handleLoadingComplete} />;
  }

  // Está autenticado e pronto - mostrar app principal
  return (
    <div className="app-container">
      <RadarMonitorApp
        // Props de conexão
        url={url}
        status={status}
        error={error}
        lastUpdated={lastUpdated}
        onUrlChange={handleUrlChange}
        onConnect={connect}
        onDisconnect={disconnect}
        
        // Props de coleta de dados
        isCollecting={isCollecting}
        isLoading={isLoading}
        onStartCollection={startCollection}
        onStopCollection={stopCollection}
        
        // Dados do radar
        radarData={radarData}
        
        // Props de autenticação
        onLogout={logout}
        currentUser={user || undefined}
      />
    </div>
  );
}

export default App;