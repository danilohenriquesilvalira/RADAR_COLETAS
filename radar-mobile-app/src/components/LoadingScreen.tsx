import { useState, useEffect } from 'react';
import LogoRLS from '../assets/LogoRLS.svg';
import '../styles/loading-screen.css';

interface LoadingScreenProps {
  onComplete: () => void;
}

// Steps fora do componente para evitar re-criação
const LOADING_STEPS = [
  'Inicializando sistema...',
  'Conectando ao servidor...',
  'Carregando configurações...',
  'Preparando interface...',
  'Finalizando...'
];

const LoadingScreen: React.FC<LoadingScreenProps> = ({ onComplete }) => {
  const [progress, setProgress] = useState(0);
  const [currentStep, setCurrentStep] = useState(LOADING_STEPS[0]);

  useEffect(() => {
    console.log('LoadingScreen montado');
    let progressValue = 0;
    let stepIndex = 0;
    
    const interval = setInterval(() => {
      // Incrementar progresso
      progressValue += Math.random() * 12 + 8; // 8-20% por vez
      
      if (progressValue >= 100) {
        progressValue = 100;
      }
      
      // Atualizar step
      const newStepIndex = Math.floor((progressValue / 100) * LOADING_STEPS.length);
      if (newStepIndex !== stepIndex && newStepIndex < LOADING_STEPS.length) {
        stepIndex = newStepIndex;
        setCurrentStep(LOADING_STEPS[stepIndex]);
      }
      
      setProgress(progressValue);
      console.log(`Progress: ${progressValue}%, Step: ${LOADING_STEPS[stepIndex]}`);
      
      // Completar quando chegar a 100%
      if (progressValue >= 100) {
        clearInterval(interval);
        console.log('Loading concluído, chamando onComplete em 1s');
        setTimeout(() => {
          onComplete();
        }, 1000);
      }
    }, 250);

    // Cleanup
    return () => {
      console.log('LoadingScreen desmontado');
      clearInterval(interval);
    };
  }, []); // Array vazio - executa apenas uma vez

  return (
    <div className="loading-screen">
      <div className="loading-content">
        <div className="loading-logo">
          <img src={LogoRLS} alt="RLS Logo" className="logo" />
        </div>
        
        <div className="loading-info">
          <h1>Radar Monitor System</h1>
          <h2>RMS1000 - Sistema de Monitoramento</h2>
        </div>
        
        <div className="loading-progress">
          <div className="progress-container">
            <div 
              className="progress-bar"
              style={{ width: `${progress}%` }}
            ></div>
          </div>
          <div className="progress-text">
            <span className="progress-step">{currentStep}</span>
            <span className="progress-percent">{Math.round(progress)}%</span>
          </div>
        </div>
      </div>
      
      <div className="loading-background">
        <div className="radar-sweep"></div>
        <div className="grid-pattern"></div>
      </div>
    </div>
  );
};

export default LoadingScreen;