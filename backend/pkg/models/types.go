package models

import "time"

// RadarData representa a estrutura de dados de um radar específico
type RadarData struct {
	RadarID    string        `json:"radarId"`
	RadarName  string        `json:"radarName"`
	Connected  bool          `json:"connected"`
	Positions  []float64     `json:"positions,omitempty"`
	Velocities []float64     `json:"velocities,omitempty"`
	Azimuths   []float64     `json:"azimuths,omitempty"`
	Amplitudes []float64     `json:"amplitudes,omitempty"`
	MainObject *ObjPrincipal `json:"mainObject,omitempty"`
	PLCStatus  *PLCStatus    `json:"plcStatus,omitempty"`
	Timestamp  int64         `json:"timestamp"`
}

// MultiRadarData representa dados de todos os radares
type MultiRadarData struct {
	Radars    []RadarData `json:"radars"`
	Timestamp int64       `json:"timestamp"`
}

// PLCStatus representa o status da conexão com o PLC
type PLCStatus struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// ObjPrincipal representa o objeto com maior amplitude
type ObjPrincipal struct {
	Amplitude  float64  `json:"amplitude"`
	Distancia  *float64 `json:"distancia,omitempty"`
	Velocidade *float64 `json:"velocidade,omitempty"`
	Angulo     *float64 `json:"angulo,omitempty"`
}

// ObjetoPrincipalInfo armazena informações para estabilização
type ObjetoPrincipalInfo struct {
	Objeto               *ObjPrincipal
	ContadorEstabilidade int
	UltimaAtualizacao    time.Time
	Indice               int
}
