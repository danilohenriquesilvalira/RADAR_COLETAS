import { useState, useCallback, useEffect } from 'react';

interface User {
  username: string;
  role: string;
}

interface AuthState {
  isAuthenticated: boolean;
  user: User | null;
  isLoading: boolean;
  error: string | null;
}

// Credenciais padrão (em produção, isso deveria vir de um backend seguro)
const DEFAULT_CREDENTIALS = {
  username: 'admin',
  password: 'Rls@2024'
};

export const useAuth = () => {
  const [authState, setAuthState] = useState<AuthState>({
    isAuthenticated: false,
    user: null,
    isLoading: true,
    error: null
  });

  // Verificar se já está logado (localStorage)
  useEffect(() => {
    const checkAuth = () => {
      try {
        const savedAuth = localStorage.getItem('radar_auth');
        if (savedAuth) {
          const authData = JSON.parse(savedAuth);
          
          // Verificar se o token não expirou (24 horas)
          const now = new Date().getTime();
          const tokenAge = now - authData.timestamp;
          const maxAge = 24 * 60 * 60 * 1000; // 24 horas
          
          if (tokenAge < maxAge) {
            setAuthState({
              isAuthenticated: true,
              user: authData.user,
              isLoading: false,
              error: null
            });
            return;
          } else {
            // Token expirado, remover
            localStorage.removeItem('radar_auth');
          }
        }
      } catch (error) {
        console.warn('Erro ao verificar autenticação:', error);
        localStorage.removeItem('radar_auth');
      }
      
      setAuthState(prev => ({ ...prev, isLoading: false }));
    };

    checkAuth();
  }, []);

  // Função de login
  const login = useCallback(async (username: string, password: string): Promise<boolean> => {
    setAuthState(prev => ({ ...prev, isLoading: true, error: null }));

    // Simular delay de rede
    await new Promise(resolve => setTimeout(resolve, 1000));

    try {
      // Verificar credenciais
      if (username === DEFAULT_CREDENTIALS.username && password === DEFAULT_CREDENTIALS.password) {
        const user: User = {
          username: username,
          role: 'admin'
        };

        const authData = {
          user,
          timestamp: new Date().getTime()
        };

        // Salvar no localStorage
        localStorage.setItem('radar_auth', JSON.stringify(authData));

        setAuthState({
          isAuthenticated: true,
          user,
          isLoading: false,
          error: null
        });

        console.log('Login realizado com sucesso:', username);
        return true;
      } else {
        setAuthState(prev => ({
          ...prev,
          isLoading: false,
          error: 'Credenciais inválidas'
        }));
        return false;
      }
    } catch (error) {
      console.error('Erro no login:', error);
      setAuthState(prev => ({
        ...prev,
        isLoading: false,
        error: 'Erro interno. Tente novamente.'
      }));
      return false;
    }
  }, []);

  // Função de logout
  const logout = useCallback(() => {
    localStorage.removeItem('radar_auth');
    setAuthState({
      isAuthenticated: false,
      user: null,
      isLoading: false,
      error: null
    });
    console.log('Logout realizado');
  }, []);

  // Limpar erro
  const clearError = useCallback(() => {
    setAuthState(prev => ({ ...prev, error: null }));
  }, []);

  return {
    ...authState,
    login,
    logout,
    clearError
  };
};