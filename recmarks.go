package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// RecMarks son las marcas Hecha / No sirve de las recomendaciones
// (recomendaciones-por-rol). Es una marca local de la persona: nada de la API
// ni del modelo. Vive fuera del Store, con su mutex, porque la escribe un
// handler (detrás de sameOrigin y localRequest). Cada marca o "Deshacer" es
// una línea de datos.recmarcas.jsonl; al abrir se descarta lo de más de 30 días.
type RecMarks struct {
	mu    sync.Mutex
	path  string
	state map[string]string // por recID: "hecha" o "nosirve"
}

// RecMark es una línea de datos.recmarcas.jsonl. State vacío es "Deshacer".
type RecMark struct {
	At    time.Time
	Rec   string
	State string `json:",omitempty"`
}

// recStates son los estados de una marca: lista cerrada.
var recStates = map[string]string{"hecha": "Hecha", "nosirve": "No sirve"}

// OpenRecMarks carga las marcas vigentes y reescribe el archivo sin lo
// vencido (tmp + os.Rename). Si no existe, arranca vacío.
func OpenRecMarks(path string, now time.Time) (*RecMarks, error) {
	cutoff := now.Add(-retention)
	marks, err := loadJSONL(path, func(m RecMark) bool { return !m.At.Before(cutoff) && m.Rec != "" })
	if err != nil {
		return nil, err
	}
	rm := &RecMarks{path: path, state: map[string]string{}}
	for _, m := range marks {
		if _, ok := recStates[m.State]; ok {
			rm.state[m.Rec] = m.State
		} else {
			delete(rm.state, m.Rec)
		}
	}
	return rm, nil
}

// Mark guarda una marca (state "" la saca) y la agrega al archivo. Si no
// puede guardar, no cambia nada.
func (m *RecMarks) Mark(id, state string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := os.OpenFile(m.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(RecMark{At: now, Rec: id, State: state}); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if state == "" {
		delete(m.state, id)
	} else {
		m.state[id] = state
	}
	return nil
}

// State es la marca de una recomendación ("" sin marca). Con m en nil (sin
// archivo, los tests de antes), nunca hay marca.
func (m *RecMarks) State(id string) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state[id]
}
