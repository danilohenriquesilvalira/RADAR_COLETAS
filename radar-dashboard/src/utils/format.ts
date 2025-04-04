import { format } from 'date-fns';
import { ptBR } from 'date-fns/locale';

// Formata um timestamp para exibição
export const formatTimestamp = (timestamp: number): string => {
  const date = new Date(timestamp);
  return format(date, 'dd/MM/yyyy HH:mm:ss.SSS', { locale: ptBR });
};

// Formata hora sem data
export const formatTime = (timestamp: number): string => {
  const date = new Date(timestamp);
  return format(date, 'HH:mm:ss.SSS', { locale: ptBR });
};

// Formata um valor numérico com precisão definida
export const formatNumber = (value: number, precision: number = 2): string => {
  return value.toFixed(precision);
};

// Formata uma velocidade (m/s) adicionando unidade
export const formatVelocity = (value: number): string => {
  return `${formatNumber(value)} m/s`;
};

// Formata uma posição (m) adicionando unidade
export const formatPosition = (value: number): string => {
  return `${formatNumber(value)} m`;
};

// Formata uma mudança de velocidade mostrando sinal
export const formatVelocityChange = (value: number): string => {
  const sign = value > 0 ? '+' : '';
  return `${sign}${formatNumber(value)} m/s`;
};

// Helpers para cor baseada no valor
export const getVelocityColor = (value: number): string => {
  const absValue = Math.abs(value);
  if (absValue > 5) return 'text-red-600';
  if (absValue > 2) return 'text-orange-500';
  if (absValue > 1) return 'text-yellow-500';
  return 'text-green-600';
};

export const getChangeColor = (value: number): string => {
  if (value > 0) return 'text-green-600';
  if (value < 0) return 'text-red-600';
  return 'text-gray-600';
};