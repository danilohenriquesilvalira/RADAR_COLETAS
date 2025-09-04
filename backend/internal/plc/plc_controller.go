// ============================================================================
// ARQUIVO: backend/internal/plc/plc_controller.go - FINAL CORRIGIDO
// WRAPPER COMPATIBILIDADE - USA PLCManager CORRIGIDO SEM ERRO FALSO
// ============================================================================
package plc

import (
	"backend/internal/logger"
	"backend/pkg/models"
	"context"
	"time"
)

// PLCController - WRAPPER para manter compatibilidade com main.go
type PLCController struct {
	plcManager *PLCManager // O verdadeiro gerenciador unificado CORRIGIDO
}

// NewPLCController - mantém interface igual para main.go
func NewPLCController(plcClient PLCClient) *PLCController {
	// ✅ USAR PLCManager CORRIGIDO SEM LOG DE ERRO FALSO
	plcManager := NewPLCManager("192.168.1.33")

	return &PLCController{
		plcManager: plcManager,
	}
}

// ============================================================================
// 🛡️ MÉTODOS DE WRAPPER - TODOS DELEGAM PARA PLCManager CORRIGIDO
// ============================================================================

func (pc *PLCController) SetSystemLogger(logger *logger.SystemLogger) {
	pc.plcManager.SetSystemLogger(logger)
}

func (pc *PLCController) SetSiemensPLC(siemens *SiemensPLC) {
	pc.plcManager.SetSiemensPLC(siemens)
}

func (pc *PLCController) StartWithContext(parentCtx context.Context) {
	pc.plcManager.StartWithContext(parentCtx)
}

func (pc *PLCController) Start() {
	pc.plcManager.Start()
}

func (pc *PLCController) Stop() {
	pc.plcManager.Stop()
}

func (pc *PLCController) IsCollectionActive() bool {
	return pc.plcManager.IsCollectionActive()
}

func (pc *PLCController) IsEmergencyStop() bool {
	return pc.plcManager.IsEmergencyStop()
}

func (pc *PLCController) IsPLCConnected() bool {
	return pc.plcManager.IsPLCConnected()
}

func (pc *PLCController) IsRadarEnabled(radarID string) bool {
	return pc.plcManager.IsRadarEnabled(radarID)
}

func (pc *PLCController) GetRadarsEnabled() map[string]bool {
	return pc.plcManager.GetRadarsEnabled()
}

func (pc *PLCController) SetRadarsConnected(status map[string]bool) {
	pc.plcManager.SetRadarsConnected(status)
}

func (pc *PLCController) WriteMultiRadarData(data models.MultiRadarData) error {
	return pc.plcManager.WriteMultiRadarData(data)
}

func (pc *PLCController) IsRadarTimingOut(radarID string) (bool, time.Duration) {
	return pc.plcManager.IsRadarTimingOut(radarID)
}
