import { useEffect, useRef, useState, useCallback, memo } from 'react';

interface RadarVisualizationProps {
  positions: number[];
  velocities: number[];
  selectedSensors: number[];
}

const RadarVisualization = memo(({ positions, velocities, selectedSensors }: RadarVisualizationProps) => {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [showDistanceLabels, setShowDistanceLabels] = useState(true);
  const [zoom, setZoom] = useState(1);
  const animationFrameRef = useRef<number | null>(null);
  
  // Desenhar o radar em um callback memoizado
  const drawRadar = useCallback(() => {
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
    
    // Determinar escala baseada no zoom
    const scale = zoom;
    
    // Desenhar fundo meio cinza
    ctx.fillStyle = '#f8f9fa';
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    
    // Desenhar círculos concêntricos (escala)
    const distances = [12.5, 25, 37.5, 50, 62.5, 75, 87.5];
    distances.forEach(distance => {
      const radius = (distance / 87.5) * maxRadius * scale;
      
      // Desenhar círculo
      ctx.beginPath();
      ctx.arc(centerX, centerY, radius, 0, Math.PI * 2);
      ctx.strokeStyle = 'rgba(200, 200, 200, 0.6)';
      ctx.lineWidth = 1;
      ctx.stroke();
      
      // Adicionar marcação de distância
      if (showDistanceLabels) {
        ctx.fillStyle = 'rgba(100, 100, 100, 0.7)';
        ctx.font = '10px Arial';
        ctx.textAlign = 'center';
        
        // Posicionar texto no canto superior
        ctx.fillText(distance.toString(), centerX, centerY - radius + 12);
      }
    });
    
    // Desenhar linhas radiais (ângulos)
    const numAngles = 8; // 8 linhas radiais
    for (let i = 0; i < numAngles; i++) {
      const angle = (i * Math.PI * 2) / numAngles;
      
      ctx.beginPath();
      ctx.moveTo(centerX, centerY);
      ctx.lineTo(
        centerX + Math.cos(angle) * maxRadius * scale,
        centerY + Math.sin(angle) * maxRadius * scale
      );
      ctx.strokeStyle = 'rgba(200, 200, 200, 0.4)';
      ctx.lineWidth = 1;
      ctx.stroke();
    }
    
    // Adicionar eixos principais (horizontal e vertical)
    ctx.beginPath();
    ctx.moveTo(centerX - maxRadius * scale, centerY);
    ctx.lineTo(centerX + maxRadius * scale, centerY);
    ctx.moveTo(centerX, centerY - maxRadius * scale);
    ctx.lineTo(centerX, centerY + maxRadius * scale);
    ctx.strokeStyle = 'rgba(180, 180, 180, 0.7)';
    ctx.lineWidth = 1.5;
    ctx.stroke();

    // Desenhar gráfico lateral de intensidade
    const barWidth = 15;
    const barHeight = canvas.height * 0.8;
    const barX = 30;
    const barY = (canvas.height - barHeight) / 2;
    
    // Fundo do gráfico lateral
    ctx.fillStyle = 'rgba(230, 240, 255, 0.5)';
    ctx.fillRect(barX, barY, barWidth, barHeight);
    
    // Escala vertical
    for (let i = 0; i <= 10; i++) {
      const y = barY + (i / 10) * barHeight;
      ctx.beginPath();
      ctx.moveTo(barX, y);
      ctx.lineTo(barX + barWidth, y);
      ctx.strokeStyle = 'rgba(100, 100, 100, 0.3)';
      ctx.lineWidth = 1;
      ctx.stroke();
      
      // Adicionar valores da escala (de 100 a 0)
      if (i % 2 === 0) { // Mostrar apenas a cada 2 linhas para não ficar poluído
        const value = 100 - (i * 10);
        ctx.fillStyle = 'rgba(80, 80, 80, 0.8)';
        ctx.font = '9px Arial';
        ctx.textAlign = 'right';
        ctx.fillText(value.toString(), barX - 5, y + 3);
      }
    }
    
    // Rótulo da escala vertical
    ctx.save();
    ctx.translate(barX - 20, barY + barHeight / 2);
    ctx.rotate(-Math.PI / 2);
    ctx.fillStyle = 'rgba(80, 80, 80, 0.8)';
    ctx.font = 'bold 10px Arial';
    ctx.textAlign = 'center';
    ctx.fillText('Distance (m)', 0, 0);
    ctx.restore();
    
    // Desenhar valores no gráfico lateral (simulando os valores de posição)
    const filteredPositions = positions.filter((_, i) => selectedSensors.includes(i));
    const maxPosition = Math.max(...filteredPositions, 1);
    positions.forEach((position, index) => {
      if (!selectedSensors.includes(index)) return;
      
      const normalizedHeight = Math.min(position / maxPosition, 1) * 0.3 * barHeight;
      const barPosY = barY + barHeight - normalizedHeight;
      
      // Desenhar barra para cada posição
      ctx.fillStyle = index % 2 === 0 ? 'rgba(100, 180, 255, 0.7)' : 'rgba(30, 144, 255, 0.7)';
      ctx.fillRect(barX, barPosY, barWidth, normalizedHeight);
    });
    
    // Desenhar pontos para cada sensor (posições)
    positions.forEach((position, index) => {
      if (!selectedSensors.includes(index)) return;
      
      const velocity = velocities[index];
      const angle = (index * Math.PI * 2) / positions.length;
      
      // Calcular coordenadas do ponto
      const distance = Math.min(position, 87.5); // Limitar ao raio máximo
      const scaledPosition = (distance / 87.5) * maxRadius * scale;
      const x = centerX + scaledPosition * Math.cos(angle);
      const y = centerY + scaledPosition * Math.sin(angle);
      
      // Desenhar linha do centro até o ponto
      ctx.beginPath();
      ctx.moveTo(centerX, centerY);
      ctx.lineTo(x, y);
      ctx.strokeStyle = 'rgba(100, 100, 100, 0.3)';
      ctx.lineWidth = 1;
      ctx.stroke();
      
      // Determinar cor com base na velocidade
      const absVelocity = Math.abs(velocity);
      let pointColor = 'rgb(100, 116, 139)'; // Cinza (parado)
      
      if (absVelocity > 0.1) {
        if (absVelocity > 3) {
          pointColor = 'rgb(220, 38, 38)'; // Vermelho (muito rápido)
        } else if (absVelocity > 1) {
          pointColor = 'rgb(249, 115, 22)'; // Laranja (rápido)
        } else if (absVelocity > 0.5) {
          pointColor = 'rgb(234, 179, 8)'; // Amarelo (médio)
        } else {
          pointColor = 'rgb(34, 197, 94)'; // Verde (lento)
        }
      }
      
      // Desenhar círculo representando o sensor
      ctx.beginPath();
      ctx.arc(x, y, 6, 0, Math.PI * 2);
      ctx.fillStyle = pointColor;
      ctx.fill();
      ctx.strokeStyle = 'white';
      ctx.lineWidth = 1.5;
      ctx.stroke();
      
      // Adicionar número do sensor
      ctx.fillStyle = 'white';
      ctx.font = 'bold 9px Arial';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText((index + 1).toString(), x, y);
      
      // Adicionar rótulo com velocidade
      ctx.fillStyle = 'rgba(0, 0, 0, 0.7)';
      ctx.font = '10px Arial';
      ctx.textAlign = 'left';
      ctx.fillText(`${position.toFixed(1)}m  ${velocity.toFixed(1)}m/s`, x + 10, y);
    });
    
    // Desenhar legenda na parte inferior
    const legendY = canvas.height - 30;
    const legendX = 60;
    const legendSpacing = 80;
    
    const legends = [
      { color: 'rgb(34, 197, 94)', label: '< 0.5 m/s' },
      { color: 'rgb(234, 179, 8)', label: '0.5-1 m/s' },
      { color: 'rgb(249, 115, 22)', label: '1-3 m/s' },
      { color: 'rgb(220, 38, 38)', label: '> 3 m/s' },
    ];
    
    legends.forEach((legend, i) => {
      const x = legendX + i * legendSpacing;
      
      // Círculo de cor
      ctx.beginPath();
      ctx.arc(x, legendY, 5, 0, Math.PI * 2);
      ctx.fillStyle = legend.color;
      ctx.fill();
      ctx.strokeStyle = 'white';
      ctx.lineWidth = 1;
      ctx.stroke();
      
      // Texto da legenda
      ctx.fillStyle = 'rgba(0, 0, 0, 0.7)';
      ctx.font = '10px Arial';
      ctx.textAlign = 'left';
      ctx.fillText(legend.label, x + 10, legendY + 3);
    });
    
    // Desenhar radar no centro (dispositivo)
    ctx.beginPath();
    ctx.arc(centerX, centerY, 8, 0, Math.PI * 2);
    ctx.fillStyle = 'rgb(14, 165, 233)'; // Azul
    ctx.fill();
    ctx.strokeStyle = 'white';
    ctx.lineWidth = 2;
    ctx.stroke();
    
    // Adicionar texto "R" no centro
    ctx.fillStyle = 'white';
    ctx.font = 'bold 7px Arial';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText('R', centerX, centerY);
    
  }, [positions, velocities, selectedSensors, showDistanceLabels, zoom]);
  
  // Efeito para desenhar o radar quando os dados mudarem
  useEffect(() => {
    if (animationFrameRef.current) {
      cancelAnimationFrame(animationFrameRef.current);
    }
    
    // Usar requestAnimationFrame para sincronizar com a renderização
    animationFrameRef.current = requestAnimationFrame(drawRadar);
    
    // Limpar na desmontagem
    return () => {
      if (animationFrameRef.current) {
        cancelAnimationFrame(animationFrameRef.current);
      }
    };
  }, [drawRadar]);
  
  const handleZoomOut = useCallback(() => {
    setZoom(prev => Math.max(prev - 0.2, 0.5));
  }, []);
  
  const handleZoomIn = useCallback(() => {
    setZoom(prev => Math.min(prev + 0.2, 2));
  }, []);
  
  const toggleDistanceLabels = useCallback(() => {
    setShowDistanceLabels(prev => !prev);
  }, []);
  
  return (
    <div className="flex flex-col h-full">
      <div className="flex justify-between items-center mb-3">
        <div className="flex items-center space-x-2">
          <button
            onClick={handleZoomOut}
            className="p-1 rounded bg-gray-100 hover:bg-gray-200 text-gray-700"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M5 10a1 1 0 011-1h8a1 1 0 110 2H6a1 1 0 01-1-1z" clipRule="evenodd" />
            </svg>
          </button>
          <span className="text-xs font-medium text-gray-600">Zoom: {(zoom * 100).toFixed(0)}%</span>
          <button
            onClick={handleZoomIn}
            className="p-1 rounded bg-gray-100 hover:bg-gray-200 text-gray-700"
          >
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
              <path fillRule="evenodd" d="M10 5a1 1 0 011 1v3h3a1 1 0 110 2h-3v3a1 1 0 11-2 0v-3H6a1 1 0 110-2h3V6a1 1 0 011-1z" clipRule="evenodd" />
            </svg>
          </button>
        </div>
        <div>
          <label className="inline-flex items-center text-xs text-gray-600">
            <input
              type="checkbox"
              checked={showDistanceLabels}
              onChange={toggleDistanceLabels}
              className="form-checkbox h-3 w-3 text-blue-600 rounded"
            />
            <span className="ml-1">Distâncias</span>
          </label>
        </div>
      </div>
      
      <div className="relative flex-grow bg-white rounded-lg overflow-hidden">
        <canvas 
          ref={canvasRef} 
          width={600} 
          height={500}
          className="w-full h-full"
        />
        
        {/* Texto explicativo no canto */}
        <div className="absolute bottom-2 right-2 bg-white bg-opacity-70 p-1 rounded text-xs text-gray-600">
          <div>Monitoramento em tempo real</div>
          <div>Total de sensores: {selectedSensors.length} / 7</div>
        </div>
      </div>
    </div>
  );
});

RadarVisualization.displayName = 'RadarVisualization';

export default RadarVisualization;