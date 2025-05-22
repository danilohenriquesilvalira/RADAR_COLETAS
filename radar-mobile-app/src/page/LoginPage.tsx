import React, { useState, useEffect } from 'react';
import { Lock, User, Eye, EyeOff, AlertCircle } from 'lucide-react';
import LogoRLS from '../assets/LogoRLS.svg'; // Ensure this path is correct
import '../styles/login.css';

interface LoginPageProps {
  onLogin: (username: string, password: string) => Promise<boolean>;
  isLoading?: boolean;
  error?: string | null;
}

const LoginPage: React.FC<LoginPageProps> = ({ onLogin, isLoading = false, error }) => {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [loginError, setLoginError] = useState<string | null>(null);
  const [mounted, setMounted] = useState(false);

  // Combine external error with internal validation error
  const displayError = error || loginError;

  // Effect for initial mount animation
  useEffect(() => {
    setMounted(true); // Trigger card mount animation
  }, []);

  // Clear internal login error when external error or loading state changes
  useEffect(() => {
    if (isLoading || error) {
      setLoginError(null);
    }
  }, [isLoading, error]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoginError(null); // Clear previous internal error

    if (!username.trim() || !password.trim()) {
      setLoginError('Por favor, preencha todos os campos.');
      return;
    }

    try {
      const success = await onLogin(username.trim(), password);
      if (!success) {
        setLoginError('Usuário ou senha incorretos.');
      }
    } catch (err) {
      console.error("Login attempt failed:", err);
      setLoginError('Erro ao fazer login. Tente novamente.');
    }
  };

  const togglePasswordVisibility = () => {
    setShowPassword(prev => !prev);
  };

  return (
    <div className="login-container">
      {/* Extremely subtle background lines/dots */}
      <div className="background-grid-subtle"></div>
      <div className="background-dots-subtle"></div>

      {/* Main Login Card */}
      <div className={`login-card ${mounted ? 'login-card-mounted' : ''}`}>
        <div className="login-header">
          <div className="company-logo">
            <img src={LogoRLS} alt="RLS Logo" className="logo-image" />
          </div>

          <div className="system-info">
            <h1>Radar Monitor System</h1>
            <h2>Sistema de Monitoramento RMS1000</h2>
            <div className="system-status">
              <span className="status-dot"></span>
              <p>Sistema operacional &bull; Conexão Segura</p>
            </div>
          </div>
        </div>

        <form onSubmit={handleSubmit} className="login-form">
          <div className="form-group">
            <label htmlFor="username">Usuário</label>
            <div className="input-wrapper">
              <User className="input-icon" size={20} />
              <input
                id="username"
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="Digite seu usuário"
                disabled={isLoading}
                className="form-input"
                autoComplete="username"
                aria-required="true"
              />
            </div>
          </div>

          <div className="form-group">
            <label htmlFor="password">Senha</label>
            <div className="input-wrapper">
              <Lock className="input-icon" size={20} />
              <input
                id="password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Digite sua senha"
                disabled={isLoading}
                className="form-input"
                autoComplete="current-password"
                aria-required="true"
              />
              <button
                type="button"
                onClick={togglePasswordVisibility}
                className="password-toggle"
                disabled={isLoading}
                aria-label={showPassword ? "Esconder senha" : "Mostrar senha"}
                tabIndex={-1}
              >
                {showPassword ? <EyeOff size={20} /> : <Eye size={20} />}
              </button>
            </div>
          </div>

          {displayError && (
            <div className="error-alert" role="alert">
              <AlertCircle size={20} />
              <span>{displayError}</span>
            </div>
          )}

          <button
            type="submit"
            disabled={isLoading}
            className="submit-button"
          >
            {isLoading ? (
              <>
                <div className="loading-scanner"></div>
                <span>Autenticando...</span>
              </>
            ) : (
              <>
                <Lock size={18} />
                <span>Acessar Sistema</span>
              </>
            )}
          </button>

          <div className="login-footer">
            <div className="version-info">v2.1.4</div>
          </div>
        </form>
      </div>
    </div>
  );
};

export default LoginPage;