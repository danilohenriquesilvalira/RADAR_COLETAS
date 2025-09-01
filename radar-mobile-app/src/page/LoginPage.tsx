import React, { useState } from 'react';
import { Lock, User, Eye, EyeOff, AlertCircle, Zap, Shield } from 'lucide-react';

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

  const displayError = error || loginError;

  const handleSubmit = async () => {
    setLoginError(null);

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

  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !isLoading) {
      handleSubmit();
    }
  };

  return (
    <div className="min-h-screen bg-gradient-to-br from-slate-900 via-blue-900 to-slate-900 flex items-center justify-center p-4">
      
      {/* Background Pattern */}
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_50%_50%,rgba(59,130,246,0.1),transparent)] pointer-events-none" />
      <div className="absolute inset-0 bg-[linear-gradient(45deg,transparent_49%,rgba(59,130,246,0.03)_50%,transparent_51%)] bg-[length:20px_20px] pointer-events-none" />

      {/* Login Card */}
      <div className="relative w-full max-w-md">
        <div className="bg-white/95 backdrop-blur-sm rounded-3xl p-8 shadow-2xl border border-white/20">
          
          {/* Header */}
          <div className="text-center mb-8">
            <div className="inline-flex items-center justify-center w-16 h-16 bg-gradient-to-r from-blue-600 to-indigo-600 rounded-2xl mb-6 shadow-lg">
              <Zap className="w-8 h-8 text-white" />
            </div>
            <h1 className="text-2xl font-bold text-gray-900 mb-2">DH Automação</h1>
            <p className="text-gray-600 font-medium">Sistema de Monitoramento</p>
            <div className="flex items-center justify-center gap-2 mt-4">
              <div className="w-2 h-2 bg-green-400 rounded-full animate-pulse" />
              <span className="text-sm text-gray-500">Sistema Operacional</span>
            </div>
          </div>

          {/* Form Fields */}
          <div className="space-y-6">
            
            {/* Username */}
            <div className="space-y-2">
              <label className="text-sm font-semibold text-gray-700">Usuário</label>
              <div className="relative">
                <User className="absolute left-3 top-1/2 transform -translate-y-1/2 w-5 h-5 text-gray-400" />
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  onKeyPress={handleKeyPress}
                  placeholder="Digite seu usuário"
                  disabled={isLoading}
                  className="w-full pl-10 pr-4 py-3 bg-gray-50 border border-gray-200 rounded-xl focus:ring-2 focus:ring-blue-500 focus:border-blue-500 transition-all duration-200 placeholder-gray-400 disabled:opacity-50 disabled:cursor-not-allowed"
                  autoComplete="username"
                />
              </div>
            </div>

            {/* Password */}
            <div className="space-y-2">
              <label className="text-sm font-semibold text-gray-700">Senha</label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 transform -translate-y-1/2 w-5 h-5 text-gray-400" />
                <input
                  type={showPassword ? 'text' : 'password'}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  onKeyPress={handleKeyPress}
                  placeholder="Digite sua senha"
                  disabled={isLoading}
                  className="w-full pl-10 pr-12 py-3 bg-gray-50 border border-gray-200 rounded-xl focus:ring-2 focus:ring-blue-500 focus:border-blue-500 transition-all duration-200 placeholder-gray-400 disabled:opacity-50 disabled:cursor-not-allowed"
                  autoComplete="current-password"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-3 top-1/2 transform -translate-y-1/2 p-1 text-gray-400 hover:text-gray-600 transition-colors"
                  disabled={isLoading}
                >
                  {showPassword ? <EyeOff className="w-5 h-5" /> : <Eye className="w-5 h-5" />}
                </button>
              </div>
            </div>

            {/* Error Alert */}
            {displayError && (
              <div className="flex items-start gap-3 p-4 bg-red-50 border border-red-200 rounded-xl text-red-800">
                <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
                <span className="text-sm font-medium">{displayError}</span>
              </div>
            )}

            {/* Submit Button */}
            <button
              onClick={handleSubmit}
              disabled={isLoading}
              className="w-full flex items-center justify-center gap-3 bg-gradient-to-r from-blue-600 to-indigo-600 hover:from-blue-700 hover:to-indigo-700 text-white font-semibold py-3 px-6 rounded-xl shadow-lg hover:shadow-xl transform hover:-translate-y-0.5 transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed disabled:transform-none"
            >
              {isLoading ? (
                <>
                  <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                  <span>Autenticando...</span>
                </>
              ) : (
                <>
                  <Shield className="w-5 h-5" />
                  <span>Acessar Sistema</span>
                </>
              )}
            </button>
          </div>

          {/* Footer */}
          <div className="text-center mt-8 pt-6 border-t border-gray-100">
            <p className="text-xs text-gray-500 font-medium">v2.1.4 • Conexão Segura</p>
          </div>
        </div>
      </div>
    </div>
  );
};

export default LoginPage;