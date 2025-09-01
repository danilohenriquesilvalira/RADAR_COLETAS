#!/bin/bash

echo "🚀 INICIANDO RADAR_COLETAS - DESENVOLVIMENTO"
echo "Backend: http://localhost:8080"
echo "Frontend: http://localhost:5173" 
echo "========================================"

# Criar sessão tmux com 2 painéis
tmux new-session -d -s radar_dev

# Dividir horizontalmente  
tmux split-window -h -t radar_dev

# Painel esquerdo: Backend
tmux send-keys -t radar_dev:0.0 "cd /home/rls/RADAR_COLETAS/backend" Enter
tmux send-keys -t radar_dev:0.0 "echo '=== BACKEND GO ==='" Enter  
tmux send-keys -t radar_dev:0.0 "go run cmd/main.go" Enter

# Painel direito: Frontend
tmux send-keys -t radar_dev:0.1 "cd /home/rls/RADAR_COLETAS/radar-mobile-app" Enter
tmux send-keys -t radar_dev:0.1 "echo '=== FRONTEND REACT ==='" Enter
tmux send-keys -t radar_dev:0.1 "npm run dev" Enter

# Conectar na sessão (mostra os 2 terminais)
tmux attach -t radar_dev
