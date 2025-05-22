import { useState, useCallback } from 'react';

interface ErrorInfo {
  message: string;
  timestamp: Date;
  type: 'connection' | 'data' | 'api' | 'general';
  details?: any;
}

export const useErrorHandler = () => {
  const [errors, setErrors] = useState<ErrorInfo[]>([]);
  const [currentError, setCurrentError] = useState<ErrorInfo | null>(null);

  // Adicionar um novo erro
  const addError = useCallback((error: Omit<ErrorInfo, 'timestamp'>) => {
    const errorInfo: ErrorInfo = {
      ...error,
      timestamp: new Date()
    };

    setErrors(prev => [errorInfo, ...prev.slice(0, 9)]); // Manter apenas 10 erros
    setCurrentError(errorInfo);

    // Auto-limpar erro atual após 5 segundos
    setTimeout(() => {
      setCurrentError(prev => prev?.timestamp === errorInfo.timestamp ? null : prev);
    }, 5000);

    // Log do erro
    console.error(`[${error.type.toUpperCase()}] ${error.message}`, error.details);
  }, []);

  // Limpar erro atual
  const clearCurrentError = useCallback(() => {
    setCurrentError(null);
  }, []);

  // Limpar todos os erros
  const clearAllErrors = useCallback(() => {
    setErrors([]);
    setCurrentError(null);
  }, []);

  // Obter erros por tipo
  const getErrorsByType = useCallback((type: ErrorInfo['type']) => {
    return errors.filter(error => error.type === type);
  }, [errors]);

  return {
    errors,
    currentError,
    addError,
    clearCurrentError,
    clearAllErrors,
    getErrorsByType,
    hasErrors: errors.length > 0,
    hasCurrentError: currentError !== null
  };
};