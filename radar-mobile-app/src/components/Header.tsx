import React, { useEffect, useState, useRef } from 'react';
import { Clock, Wifi, WifiOff, User, LogOut } from 'lucide-react';
import { ConnectionStatus } from '../types/radarTypes';
import LogoRLS from '../assets/LogoRLS.svg';
import '../styles/header.css';

interface HeaderProps {
  deviceId: string;
  lastUpdated: string;
  connectionStatusClass: string;
  status: ConnectionStatus;
  currentUser?: { username: string; role: string };
  onLogout?: () => void;
}

const Header: React.FC<HeaderProps> = ({
  deviceId,
  lastUpdated,
  connectionStatusClass,
  status,
  currentUser,
  onLogout
}) => {
  const [isVisible, setIsVisible] = useState(true);
  const lastScrollY = useRef(0);

  useEffect(() => {
    const handleScroll = () => {
      const currentScrollY = window.scrollY;

      if (currentScrollY > lastScrollY.current && currentScrollY > 100) {
        // Scrolling down & not at the top
        setIsVisible(false);
      } else {
        // Scrolling up or at the top
        setIsVisible(true);
      }

      lastScrollY.current = currentScrollY;
    };

    window.addEventListener('scroll', handleScroll, { passive: true });
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  return (
    <header className={`sick-header ${isVisible ? 'visible' : 'hidden'}`}>
      <div className="sick-header-left">
        <div className="sick-logo">
          <div className="sick-logo-container">
            <img src={LogoRLS} alt="RLS Logo" className="sick-logo-image" />
          </div>
          <div className="sick-title-container">
            <span className="sick-divider">|</span>
            <span className="sick-title">Radar Monitor RMS1000</span>
          </div>
        </div>
      </div>

      <div className="sick-header-center">
        <div className="sick-device-id">
          {deviceId}
        </div>

        <div className="sick-timestamp">
          <Clock size={16} />
          <span>Atualizado: {lastUpdated || '--:--:--'}</span>
        </div>

        <div className={`sick-connection-status ${connectionStatusClass}`}>
          {status === ConnectionStatus.CONNECTED ? (
            <Wifi size={16} />
          ) : (
            <WifiOff size={16} />
          )}
          <span className="status-text">
            {status === ConnectionStatus.CONNECTED ? 'Conectado' :
              status === ConnectionStatus.CONNECTING ? 'Conectando...' :
              status === ConnectionStatus.ERROR ? 'Erro' :
              'Desconectado'}
          </span>
        </div>
      </div>

      <div className="sick-header-right">
        {currentUser && (
          <div className="sick-user-info">
            <User size={16} />
            <span>
              <strong>{currentUser.username}</strong>
              <small>{currentUser.role}</small>
            </span>
          </div>
        )}

        {onLogout && (
          <button
            onClick={onLogout}
            className="sick-logout-button"
            title="Sair do sistema"
          >
            <LogOut size={18} />
            <span className="logout-text">Sair</span>
          </button>
        )}
      </div>
    </header>
  );
};

export default Header;