package main

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Answers son las preguntas agrupadas que la persona marcó como respondidas
// en En vivo (F4). Es una marca local: no escribe en YouTube. Vive fuera del
// Store, con su mutex, porque la escriben los handlers (detrás de sameOrigin
// y localRequest). Cada marca o "Deshacer" es una línea de
// datos.respondidas.jsonl; al abrir se descarta lo de más de 30 días.
type Answers struct {
	mu   sync.Mutex
	path string
	done map[string]bool // por ID de grupo
}

// AnswerMark es una línea de datos.respondidas.jsonl.
type AnswerMark struct {
	At        time.Time
	ChannelID string
	Group     string
	Undo      bool `json:",omitempty"`
}

// OpenAnswers carga las marcas vigentes y reescribe el archivo sin lo
// vencido (tmp + os.Rename). Si no existe, arranca vacío.
func OpenAnswers(path string, now time.Time) (*Answers, error) {
	cutoff := now.Add(-retention)
	marks, err := loadJSONL(path, func(m AnswerMark) bool { return !m.At.Before(cutoff) && m.Group != "" })
	if err != nil {
		return nil, err
	}
	a := &Answers{path: path, done: map[string]bool{}}
	for _, m := range marks {
		a.done[m.Group] = !m.Undo
	}
	return a, nil
}

// Mark marca (o, con undo, desmarca) un grupo y lo agrega al archivo. Si no
// puede guardar, no cambia nada.
func (a *Answers) Mark(channelID, group string, undo bool, now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(AnswerMark{At: now, ChannelID: channelID, Group: group, Undo: undo}); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	a.done[group] = !undo
	return nil
}

// Answered dice si el grupo está marcado como respondido. Con a en nil
// (sin archivo, los tests de antes), nunca.
func (a *Answers) Answered(group string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.done[group]
}
