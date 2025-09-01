import { useState } from 'react';
import { useWebSocket } from './hooks/useWebSocket';
import { useAuth } from './hooks/useAuth';
import { MultiRadarData } from './types/radarTypes';
import ModernRadarDisplay from './components/ModernRadarDisplay';
import LoginPage from './page/LoginPage';

const getWebSocketUrl = () => {
  return 'ws://192.168.1.92:8080/ws';
};

function App() {
  const { isAuthenticated, user, login, logout, isLoading: authLoading, error: authError } = useAuth();
  const [multiRadarData, setMultiRadarData] = useState<MultiRadarData | null>(null);
  
  const { connect } = useWebSocket(getWebSocketUrl(), {
    autoReconnect: true,
    maxReconnectAttempts: 10,
    reconnectInterval: 2000,
    autoConnect: isAuthenticated,
    onMessage: (data) => {
      if ('radars' in data) {
        setMultiRadarData(data as MultiRadarData);
      } else {
        const singleData = data as any;
        const multiData: MultiRadarData = {
          radars: [singleData],
          timestamp: singleData.timestamp
        };
        setMultiRadarData(multiData);
      }
    },
    onOpen: () => console.log('WebSocket conectado'),
    onError: (error) => console.error('Erro WebSocket:', error)
  });
  
  const handleLogin = async (username: string, password: string) => {
    const success = await login(username, password);
    if (success) {
      connect();
    }
    return success;
  };

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
        Carregando...
      </div>
    );
  }

  if (!isAuthenticated) {
    return <LoginPage onLogin={handleLogin} isLoading={authLoading} error={authError} />;
  }

  return (
    <div style={{ minHeight: '100vh', backgroundColor: '#f9fafb' }}>
      <ModernRadarDisplay multiRadarData={multiRadarData} />
      <button
        onClick={logout}
        style={{
          position: 'fixed',
          top: '16px',
          right: '16px',
          zIndex: 50,
          backgroundColor: '#ef4444',
          color: 'white',
          padding: '8px 16px',
          borderRadius: '8px',
          border: 'none',
          fontSize: '14px',
          fontWeight: '500',
          cursor: 'pointer',
          boxShadow: '0 4px 6px rgba(0,0,0,0.1)'
        }}
        onMouseOver={(e) => e.currentTarget.style.backgroundColor = '#dc2626'}
        onMouseOut={(e) => e.currentTarget.style.backgroundColor = '#ef4444'}
      >
        Sair ({user?.username})
      </button>
    </div>
  );
}

export default App;
