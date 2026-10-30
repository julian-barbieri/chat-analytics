package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
	vpath     string // serie de espectadores por vivo, aparte para no tocar el formato de datos.jsonl
	msgs      []Message
	viewers   []ViewerSample
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

// ViewerSample es cuántos miraban un vivo según la revisión de videos.
type ViewerSample struct {
	VideoID string
	At      time.Time
	Viewers int64
}

// quotaDay es lo que se guarda en <datos>.cuota.json: el día del Pacífico y lo gastado.
type quotaDay struct {
	Day   string
	Units int
}

func OpenStore(path string) (*Store, error) {
	base := strings.TrimSuffix(path, ".jsonl")
	s := &Store{path: path, quota: base + ".cuota.json", vpath: base + ".espectadores.jsonl", seen: map[string]bool{}}
	if b, err := os.ReadFile(s.quota); err == nil {
		var q quotaDay
		if json.Unmarshal(b, &q) == nil {
			s.unitsDay, s.units = q.Day, q.Units
		}
	}
	cutoff := time.Now().Add(-retention)
	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
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
		if err := rewriteJSONL(s.path, s.msgs); err != nil {
			return nil, err
		}
	}
	if err := s.loadViewers(cutoff); err != nil {
		return nil, err
	}
	return s, nil
}

// loadViewers carga la serie de espectadores con la misma retención que los
// mensajes (son datos de la API) y reescribe el archivo sin lo vencido ni lo
// ilegible. Si no existe (instalaciones anteriores), se crea con el primer dato.
func (s *Store) loadViewers(cutoff time.Time) error {
	f, err := os.Open(s.vpath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	vs, err := readViewers(f, cutoff)
	f.Close()
	if err != nil {
		// Reescribir con lo leído a medias perdería para siempre el resto.
		return fmt.Errorf("no pude leer %s (si está dañado, movelo a otra carpeta y volvé a arrancar): %w", s.vpath, err)
	}
	s.viewers = vs
	return rewriteJSONL(s.vpath, s.viewers)
}

// maxViewerLine: una muestra ocupa menos de 100 bytes; una línea más larga es
// basura (por ejemplo, ceros de un corte de luz) y se descarta sin cargarla.
const maxViewerLine = 64 * 1024

// readViewers lee las muestras vigentes salteando las líneas ilegibles o
// demasiado largas. A diferencia de bufio.Scanner, una línea larga no corta
// la lectura: se pierden solo los datos de esa línea.
func readViewers(r io.Reader, cutoff time.Time) ([]ViewerSample, error) {
	br := bufio.NewReaderSize(r, maxViewerLine)
	var out []ViewerSample
	long := false
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = true // se descarta hasta el próximo salto de línea
			continue
		}
		var v ViewerSample
		if !long && json.Unmarshal(line, &v) == nil && !v.At.Before(cutoff) {
			out = append(out, v)
		}
		long = false
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// rewriteJSONL reemplaza el archivo con archivo temporal + os.Rename, así un
// corte a la mitad no deja el archivo cortado.
func rewriteJSONL[T any](path string, items []T) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, it := range items {
		enc.Encode(it)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// AddViewers guarda cuántos miran cada vivo en curso. Los que no informan
// espectadores (Concurrent 0: el canal oculta el contador) no se guardan, así
// después se leen como "sin dato" y no como "nadie mirando".
func (s *Store) AddViewers(at time.Time, vids []Video) {
	var add []ViewerSample
	for _, v := range vids {
		if v.LiveStatus == "live" && v.Concurrent > 0 {
			add = append(add, ViewerSample{VideoID: v.ID, At: at, Viewers: v.Concurrent})
		}
	}
	if len(add) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewers = append(s.viewers, add...)
	// Si no se puede abrir el archivo, las muestras quedan igual en memoria.
	f, err := os.OpenFile(s.vpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, v := range add {
		enc.Encode(v)
	}
}

// Viewers devuelve una copia de las muestras de un vivo desde since. No va en
// Snapshot para no copiar la serie entera cada vez que se refresca el chat.
func (s *Store) Viewers(videoID string, since time.Time) []ViewerSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ViewerSample
	for _, v := range s.viewers {
		if v.VideoID == videoID && !v.At.Before(since) {
			out = append(out, v)
		}
	}
	return out
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
