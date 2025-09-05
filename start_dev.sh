#!/bin/bash

echo "🚀 INICIANDO RADAR_COLETAS - DESENVOLVIMENTO + MONITORAMENTO"

# Matar sessão anterior se existir
tmux kill-session -t radar_dev 2>/dev/null

echo "Backend: http://192.168.1.92:8080"
echo "Profiling: http://192.168.1.92:6060"
echo "========================================"

# Criar nova sessão tmux
tmux new-session -d -s radar_dev

# Dividir horizontalmente  
tmux split-window -h -t radar_dev

# Painel esquerdo: Backend
tmux send-keys -t radar_dev:0.0 "cd /home/rls/RADAR_COLETAS/backend" Enter
tmux send-keys -t radar_dev:0.0 "echo '=== BACKEND GO ==='" Enter  
tmux send-keys -t radar_dev:0.0 "go run cmd/main.go" Enter

# Painel direito: Monitoramento CPU/Memória
tmux send-keys -t radar_dev:0.1 "cd /home/rls/RADAR_COLETAS/backend" Enter
tmux send-keys -t radar_dev:0.1 "echo '=== MONITORAMENTO CPU/MEMORIA ==='" Enter
tmux send-keys -t radar_dev:0.1 "sleep 3" Enter
tmux send-keys -t radar_dev:0.1 "watch -n 2 'echo \"=== PROCESSO GO ===\" && ps aux | grep \"go run cmd/main.go\" | grep -v grep && echo \"\" && echo \"=== MEMORIA SISTEMA ===\" && free -h && echo \"\" && echo \"=== CPU SISTEMA ===\" && top -bn1 | head -5'" Enter

# Conectar na sessão
tmux attach -t radar_dev