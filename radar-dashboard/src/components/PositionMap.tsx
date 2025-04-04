import { useEffect, useRef } from 'react';
import useRadarStore from '../stores/radarStore';

const PositionMap = () => {
  const { positions, velocities } = useRadarStore();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  
  // Desenhar o radar no canvas
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    
    // Limpar o canvas
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    
    // Configurações
    const centerX = canvas.width / 2;
    const centerY = canvas.height / 2;
    const maxRadius = Math.min(centerX, centerY) - 20;
    
    // Desenhar círculos concêntricos (escala)
    const numCircles = 5;
    for (let i = 1; i <= numCircles; i++) {
      const radius = (maxRadius / numCircles) * i;
      ctx.beginPath();
      ctx.arc(centerX, centerY, radius, 0, Math.PI * 2);
      ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
      ctx.stroke();
      
      // Adicionar marcação de distância
      const distance = (25 * i).toString();
      ctx.fillStyle = 'rgba(0, 0, 0, 0.5)';
      ctx.font = '10px Arial';
      ctx.fillText(distance + 'm', centerX + 5, centerY - radius + 15);
    }
    
    // Desenhar eixos
    ctx.beginPath();
    ctx.moveTo(centerX, 0);
    ctx.lineTo(centerX, canvas.height);
    ctx.moveTo(0, centerY);
    ctx.lineTo(canvas.width, centerY);
    ctx.strokeStyle = 'rgba(0, 0, 0, 0.2)';
    ctx.stroke();
    
    // Desenhar pontos para cada sensor (posições)
    positions.forEach((position, index) => {
      const velocity = velocities[index];
      const angle = (index * Math.PI * 2) / positions.length;
      
      // Calcular coordenadas do ponto
      const scaledPosition = (position / 25) * (maxRadius / numCircles);
      const x = centerX + scaledPosition * Math.cos(angle);
      const y = centerY - scaledPosition * Math.sin(angle);
      
      // Desenhar linha do centro até o ponto
      ctx.beginPath();
      ctx.moveTo(centerX, centerY);
      ctx.lineTo(x, y);
      ctx.strokeStyle = 'rgba(0, 0, 0, 0.1)';
      ctx.stroke();
      
      // Determinar cor com base na velocidade
      const absVelocity = Math.abs(velocity);
      let pointColor = 'rgb(75, 85, 99)'; // Cinza (parado)
      if (absVelocity > 0.1) {
        if (absVelocity > 5) {
          pointColor = 'rgb(220, 38, 38)'; // Vermelho
        } else if (absVelocity > 2) {
          pointColor = 'rgb(249, 115, 22)'; // Laranja
        } else if (absVelocity > 1) {
          pointColor = 'rgb(234, 179, 8)'; // Amarelo
        } else {
          pointColor = 'rgb(34, 197, 94)'; // Verde
        }
      }
      
      // Desenhar círculo representando o sensor
      ctx.beginPath();
      ctx.arc(x, y, 8, 0, Math.PI * 2);
      ctx.fillStyle = pointColor;
      ctx.fill();
      ctx.strokeStyle = 'white';
      ctx.lineWidth = 1.5;
      ctx.stroke();
      ctx.lineWidth = 1;
      
      // Adicionar número do sensor
      ctx.fillStyle = 'white';
      ctx.font = 'bold 10px Arial';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText((index + 1).toString(), x, y);
    });
    
    // Desenhar radar no centro
    ctx.beginPath();
    ctx.arc(centerX, centerY, 10, 0, Math.PI * 2);
    ctx.fillStyle = 'rgb(37, 99, 235)'; // Azul
    ctx.fill();
    ctx.strokeStyle = 'white';
    ctx.lineWidth = 2;
    ctx.stroke();
    ctx.lineWidth = 1;
    
    // Adicionar texto no centro
    ctx.fillStyle = 'white';
    ctx.font = 'bold 10px Arial';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText('R', centerX, centerY);
    
  }, [positions, velocities]);
  
  return (
    <div className="flex justify-center items-center bg-gray-50 rounded-lg p-2">
      <canvas 
        ref={canvasRef} 
        width={500} 
        height={400}
        className="w-full max-w-lg h-auto"
      />
    </div>
  );
};

export default PositionMap;