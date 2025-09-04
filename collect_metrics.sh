#!/usr/bin/env bash
# collect_metrics_live_smooth.sh
# Live, grouped, in-place server metrics for Ubuntu Server.
# Smooth, non-blinking updates.
# Usage: ./collect_metrics_live_smooth.sh [interval_seconds]

set -euo pipefail

INTERVAL="${1:-2}"
FIRST_RUN=true

# sanitize interval
INTERVAL="${INTERVAL//[^0-9]/}"
if [ -z "$INTERVAL" ] || [ "$INTERVAL" -le 0 ]; then INTERVAL=2; fi

# Nice visual helpers
hide_cursor() { tput civis 2>/dev/null || true; }
show_cursor() { tput cnorm 2>/dev/null || true; }
cleanup() { show_cursor; echo; exit 0; }
trap cleanup INT TERM

# bytes_human() function is not needed for this simplified version
# bytes_human() { ... }

# CPU calculation using /proc/stat
read_cpu_stat() {
  # returns total idle as: total idle
  read -r cpu user nice system idle iowait irq softirq steal guest gguest < /proc/stat
  total=$((user + nice + system + idle + iowait + irq + softirq + steal + guest + gguest))
  echo "$total $idle"
}

# disk usage (root)
get_disk_usage() {
  df -h / | awk 'NR==2 {printf "%s/%s used:%s", $3,$2,$5}'
}

# initial previous values
read -r prev_total prev_idle <<< "$(read_cpu_stat)"

hide_cursor
while true; do
  if [ "$FIRST_RUN" = true ]; then
    clear
    FIRST_RUN=false
  else
    # Moves the cursor to the top-left corner
    tput cuu 100 2>/dev/null || true
  fi

  now="$(date '+%Y-%m-%d %H:%M:%S')"
  echo "==================== SERVER METRICS (${now}) ===================="
  echo

  # UPTIME & LOAD
  label() { printf "%-28s: %s\n" "$1" "$2"; }
  label "Hostname" "$(hostname -f 2>/dev/null || hostname)"
  label "Uptime" "$(uptime -p 2>/dev/null || uptime)"
  load=$(awk '{print $1" "$2" "$3}' /proc/loadavg 2>/dev/null || echo "N/A N/A N/A")
  label "Loadavg (1/5/15)" "$load"

  echo
  # CPU
  read -r total idle <<< "$(read_cpu_stat)"
  dtotal=$((total - prev_total))
  didle=$((idle - prev_idle))
  if [ "$dtotal" -gt 0 ]; then
    cpu_usage=$(( ( (dtotal - didle) * 100 / dtotal ) ))
  else
    cpu_usage=0
  fi
  prev_total=$total; prev_idle=$idle
  label "CPU usage (%)" "${cpu_usage}%"

  echo
  # MEMORY & SWAP
  if command -v free >/dev/null 2>&1; then
    mem_line="$(free -h | awk 'NR==2{printf "%s used / %s total (avail %s)", $3,$2,$7}')"
    label "Memory" "$mem_line"
    swap_line="$(free -h | awk 'NR==3{printf "%s used / %s total", $3,$2}')"
    label "Swap" "$swap_line"
  fi

  echo
  # DISK
  label "Root disk (/) usage" "$(get_disk_usage)"

  echo
  echo "Pressione Ctrl+C para sair — atualizando a cada ${INTERVAL}s"
  
  sleep "$INTERVAL"
done