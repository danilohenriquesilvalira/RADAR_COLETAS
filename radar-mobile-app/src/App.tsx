import { useState, useEffect } from 'react';
import { useWebSocket } from './hooks/useWebSocket';
import { useAuth } from './hooks/useAuth';
import { MultiRadarData } from './types/radarTypes';
import ModernRadarDisplay from './components/ModernRadarDisplay';
import LoginPage from './page/LoginPage';
import LoadingScreen from './components/LoadingScreen';

// Função para obter o URL do WebSocket baseado na localização atual
const getWebSocketUrl = () => {
  return 'ws://localhost:8080/ws';
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
  const [url] = useState(DEFAULT_WS_URL);
  
  // Estado simples para controlar loading após login
  const [justLoggedIn, setJustLoggedIn] = useState(false);
  
  // Estado para dados de múltiplos radares
  const [multiRadarData, setMultiRadarData] = useState<MultiRadarData | null>(null);
  
  // Hook de WebSocket com gestão de dados
  const {
    connect
  } = useWebSocket(url, {
    autoReconnect: true,
    maxReconnectAttempts: 10,
    reconnectInterval: 2000,
    autoConnect: true,
    onMessage: (data) => {
      console.log('Dados recebidos no App:', data);
      // Se recebemos dados de múltiplos radares, atualizar o estado
      if ('radars' in data) {
        setMultiRadarData(data as MultiRadarData);
      } else {
        // Dados de radar único - converter para multi-radar para compatibilidade
        const singleData = data as any;
        const multiData: MultiRadarData = {
          radars: [singleData],
          timestamp: singleData.timestamp
        };
        setMultiRadarData(multiData);
      }
    },
    onOpen: () => {
      console.log('WebSocket conectado com sucesso!');
    },
    onError: (error) => {
      console.error('Erro WebSocket:', error);
    }
  });
  
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
    // Conectar ao WebSocket imediatamente
    connect();
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
    <div className="min-h-screen bg-gray-50">
      <ModernRadarDisplay multiRadarData={multiRadarData} />
      
      {/* Logout Button */}
      <button
        onClick={logout}
        className="fixed top-4 right-4 z-50 bg-white hover:bg-gray-50 text-gray-700 px-4 py-2 rounded-lg shadow-md border border-gray-200 text-sm font-medium transition-colors duration-200"
      >
        Sair ({user?.username})
      </button>
    </div>
  );
}

export default App;