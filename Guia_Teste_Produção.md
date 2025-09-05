# Guia Direto - Testes de Produção (3 Radares)

**ANTES DE PRODUÇÃO - EXECUTAR ESTES 4 TESTES**

---

## TESTE 0: Race Condition (15 min)

```bash
go run -race cmd/main.go > race_3radars_test.log 2>&1
```

**Ações durante teste:**
- Minuto 5: Desconectar cabo PLC por 2 min
- Minuto 10: Desconectar cabo radar caldeira por 1 min  
- Minuto 13: Desconectar cabo radar jusante por 1 min

**Parar após 15 min:** Ctrl+C

**Verificar:**
```bash
grep "WARNING: DATA RACE" race_3radars_test.log
```
**Resultado esperado:** Nenhuma linha encontrada

---

## TESTE 1: CPU Profile (30 min)

```bash
go run cmd/main.go > cpu_3radars_test.log 2>&1
```

**Deixar rodando 30 min sem mexer**

**Parar após 30 min:** Ctrl+C

**Análise CPU:**
```bash
# Durante execução (minuto 15):
curl http://localhost:6060/debug/pprof/profile?seconds=10 > cpu_3radars.prof
go tool pprof -top cpu_3radars.prof
```

**Resultado esperado:** CPU < 30% com 3 radares

---

## TESTE 2: Memory Leak (2 horas)

```bash
go run cmd/main.go > memory_3radars_test.log 2>&1
```

**Snapshots:**
```bash
# Minuto 5:
curl http://localhost:6060/debug/pprof/heap > heap_3radars_inicial.prof

# Minuto 65:
curl http://localhost:6060/debug/pprof/heap > heap_3radars_1h.prof

# Minuto 125:
curl http://localhost:6060/debug/pprof/heap > heap_3radars_final.prof
```

**Parar após 2h:** Ctrl+C

**Análise:**
```bash
go tool pprof -diff_base heap_3radars_inicial.prof heap_3radars_final.prof
```

**Resultado esperado:** < 5MB de crescimento

---

## TESTE 3: Goroutine Leak (1 hora)

```bash
GODEBUG=schedtrace=1000 go run cmd/main.go > goroutine_3radars_test.log 2>&1
```

**Deixar rodando 1h sem mexer**

**Análise final:**
```bash
# Antes de parar:
curl http://localhost:6060/debug/pprof/goroutine > goroutines_3radars_final.prof
go tool pprof -top goroutines_3radars_final.prof

# Verificar threads estáveis:
grep "threads=" goroutine_3radars_test.log | tail -5
```

**Parar após 1h:** Ctrl+C

**Resultado esperado:** 
- Threads: 15-25 (estável)
- Goroutines: < 30

---

## VALIDAÇÃO FINAL

**Verificar erros em todos os testes:**
```bash
grep -i "panic\|fatal\|crash" *_3radars_test.log
```

**Resultado esperado:** Nenhuma linha encontrada

---

## CRONOGRAMA TOTAL: 4h 15min

- **0h00-0h15:** Teste 0 (Race Condition)
- **0h15-0h45:** Teste 1 (CPU Profile) 
- **0h45-2h45:** Teste 2 (Memory Leak)
- **2h45-3h45:** Teste 3 (Goroutine Leak)
- **3h45-4h00:** Validação e análise

---

## CRITÉRIOS DE APROVAÇÃO

- **Race:** 0 race conditions
- **CPU:** < 30% com 3 radares  
- **Memory:** < 5MB crescimento em 2h
- **Goroutines:** < 30 ativas, threads estáveis
- **Logs:** 0 panics/crashes

**SE TODOS PASSAREM: SISTEMA APROVADO PARA PRODUÇÃO**

---

## COMANDOS DE LIMPEZA

**Entre testes (aguardar 30s):**
```bash
pkill -f "go run cmd/main.go"
sleep 30
```

**Limpar logs antigos:**
```bash
rm -f *.log *.prof
```