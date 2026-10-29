package main

import (
	"bufio"
	"encoding/json"
	"maps"
	"os"
	"strings"
	"sync"
	"time"
)

// Las políticas de YouTube piden no guardar datos de la API más de 30 días.
const retention = 30 * 24 * time.Hour

var pacific = mustLoc("America/Los_Angeles") // la cuota se reinicia a medianoche del Pacífico

func mustLoc(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Store guarda en memoria lo que junta el worker y lo persiste en un JSONL
// para no perder datos si se reinicia.
type Store struct {
	mu        sync.RWMutex
	path      string
	quota     string // archivo con la cuota gastada en el día, para que sobreviva a reinicios
	msgs      []Message
	seen      map[string]bool
	channel   string
	videos    []Video
	units     int
	unitsDay  string
	lastErr   string
	lastErrT  time.Time
	chatNote  string
	chatNotes map[string]string // por ID de vivo
	chatInfo  map[string]ChatInfo
	updated   time.Time
}

// quotaDay es lo que se guarda en <datos>.cuota.json: el día del Pacífico y lo gastado.
type quotaDay struct {
	Day   string
	Units int
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, quota: strings.TrimSuffix(path, ".jsonl") + ".cuota.json", seen: map[string]bool{}}
	if b, err := os.ReadFile(s.quota); err == nil {
		var q quotaDay
		if json.Unmarshal(b, &q) == nil {
			s.unitsDay, s.units = q.Day, q.Units
		}
	}
	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		cutoff := time.Now().Add(-retention)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			var m Message
			if json.Unmarshal(sc.Bytes(), &m) != nil || m.At.Before(cutoff) || s.seen[m.ID] {
				continue
			}
			s.seen[m.ID] = true
			s.msgs = append(s.msgs, m)
		}
		f.Close()
		// Reescribimos el archivo sin lo vencido.
		if err := s.rewrite(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) rewrite() error {
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, m := range s.msgs {
		enc.Encode(m)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Add agrega mensajes nuevos (ignora repetidos) y devuelve cuántos eran nuevos.
func (s *Store) Add(msgs []Message) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, _ := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	var enc *json.Encoder
	if f != nil {
		defer f.Close()
		enc = json.NewEncoder(f)
	}
	n := 0
	for _, m := range msgs {
		if m.ID == "" || s.seen[m.ID] {
			continue
		}
		s.seen[m.ID] = true
		s.msgs = append(s.msgs, m)
		if enc != nil {
			enc.Encode(m)
		}
		n++
	}
	s.updated = time.Now()
	return n
}

func (s *Store) SetVideos(channel string, v []Video) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channel, s.videos, s.updated = channel, v, time.Now()
}

// AddUnits suma cuota gastada y la reinicia cuando cambia el día en el Pacífico.
func (s *Store) AddUnits(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollDay()
	s.units += n
	s.saveUnits()
}

// saveUnits guarda la cuota del día. Si falla, el contador sigue en memoria.
func (s *Store) saveUnits() {
	b, _ := json.Marshal(quotaDay{s.unitsDay, s.units})
	tmp := s.quota + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, s.quota)
	}
}

func (s *Store) Units() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollDay()
	return s.units
}

func (s *Store) rollDay() {
	day := time.Now().In(pacific).Format("2006-01-02")
	if day != s.unitsDay {
		s.unitsDay, s.units = day, 0
	}
}

func (s *Store) SetError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.lastErr = ""
		return
	}
	s.lastErr, s.lastErrT = err.Error(), time.Now()
}

// SetChatNotes guarda el estado del chat en general y el de cada vivo.
func (s *Store) SetChatNotes(note string, byLive map[string]string, info map[string]ChatInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatNote, s.chatNotes, s.chatInfo = note, maps.Clone(byLive), maps.Clone(info)
}

// Snapshot es una copia para leer sin bloquear al worker.
type Snapshot struct {
	Channel   string
	Videos    []Video
	Msgs      []Message
	Units     int
	LastErr   string
	LastErrT  time.Time
	ChatNote  string
	ChatNotes map[string]string
	ChatInfo  map[string]ChatInfo
	Updated   time.Time
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.status()
	snap.Msgs = append([]Message(nil), s.msgs...)
	return snap
}

// Status es un Snapshot sin los mensajes: el chat del vivo se pide cada 2 s y
// no hace falta copiar todo el histórico para eso.
func (s *Store) Status() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status()
}

func (s *Store) status() Snapshot {
	s.rollDay()
	return Snapshot{
		Channel: s.channel, Videos: append([]Video(nil), s.videos...),
		Units: s.units, LastErr: s.lastErr, LastErrT: s.lastErrT, ChatNote: s.chatNote,
		ChatNotes: maps.Clone(s.chatNotes), ChatInfo: maps.Clone(s.chatInfo), Updated: s.updated,
	}
}

// LastChat devuelve una copia de los últimos n mensajes de chat de un vivo, del más nuevo al más viejo.
func (s *Store) LastChat(videoID string, n int) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Message
	for i := len(s.msgs) - 1; i >= 0 && len(out) < n; i-- {
		if m := s.msgs[i]; m.VideoID == videoID && m.Source == "chat" {
			out = append(out, m)
		}
	}
	return out
}
