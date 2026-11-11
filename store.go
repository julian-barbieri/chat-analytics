package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
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
	lpath     string // muestras de los vivos en curso por canal (datos.vivos.jsonl), también aparte
	apath     string // análisis de Claude (datos.analisis.jsonl), aparte de los mensajes
	cpath     string // gasto del día en Claude (datos.claude.json)
	bpath     string // tandas de Claude con las etiquetas por mensaje (datos.etiquetas.jsonl)
	vdpath    string // último dato de cada video (datos.videos.jsonl), para la pantalla Comentarios
	lkpath    string // serie de likes por video (datos.likes.jsonl)
	msgs      []Message
	analyses  []ClaudeAnalysis
	batches   []ClaudeBatch
	labels    map[string]MessageLabel // por Message.ID
	sent      map[sentKey]*SentSlot   // sentimiento por canal, franja y programa (F4)
	groups    map[string]BatchGroup   // grupos de preguntas por ID
	spend     claudeDay
	claude    ClaudeStatus
	viewers   []ViewerSample
	likes     []LikeSample
	lastLike  map[string]LikeSample  // la última muestra guardada de cada video
	archive   map[string]VideoRecord // por ID: el último dato visto
	saved     map[string]time.Time   // cuándo se escribió por última vez cada video en vdpath
	lives     []LiveSample
	channels  []ChannelStatus
	pending   []ChannelStatus // de la configuración, sin resolver todavía
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

// LiveSample es un vivo en curso según una revisión de videos. Se guarda el
// título y no el programa, así una mejora de ProgramName vale también para lo
// guardado (como con programOf).
type LiveSample struct {
	At                                 time.Time
	VideoID, ChannelID, Channel, Title string
}

// ChannelStatus es cómo está cada canal de -canal, para la vista. Va solo en
// memoria: el nombre viene de la API y no hace falta guardarlo.
type ChannelStatus struct {
	ID, Name  string
	Input     string        // lo que se pasó en -canal (el ID o el @handle), para mostrarlo en Canales
	Live      bool          // tiene al menos un vivo en curso
	WaitSince time.Time     // desde cuándo espera el próximo vivo (cero si nunca tuvo uno desde que arrancó)
	FastUntil time.Time     // hasta cuándo se busca el próximo vivo cada fastEvery
	Every     time.Duration // cada cuánto se revisa con el resto (la revisión de videos)
	Paused    bool          // chat en pausa desde Canales (solo el chat: sus vivos se siguen revisando)
	Err       string        // en Pending: por qué no se pudo resolver
}

// ClaudeAnalysis es un análisis de Claude de un tramo (un programa de un
// vivo). Se guarda en datos.analisis.jsonl, nunca en datos.jsonl. Los
// ejemplos van por Message.ID: lo único escrito por el modelo son los textos.
type ClaudeAnalysis struct {
	At                                 time.Time // cuándo se analizó
	Key                                string    // canal|vivo|programa|inicio del tramo (segmentKey)
	ChannelID, VideoID, Program, Model string    // Model: el que figura en la respuesta
	From, To                           time.Time // el tramo; los mensajes empiezan hasta 5 min antes (el título cambia tarde)
	Partial                            bool      // pedido con el botón, con el programa al aire
	Sent, Total                        int       // mensajes mandados y del tramo
	InputTokens, OutputTokens          int
	CostMicros                         int64
	StopReason, RefusalCategory        string // para medir refusals y cortes
	Result                             ClaudeResult
	Error                              string // ya sin la clave
}

// ClaudeStatus es el estado del análisis con Claude para la vista. Lo
// escribe solo RunAnalyst; va solo en memoria.
type ClaudeStatus struct {
	On          bool
	Off         string // por qué se apagó (401, 403, 404): hasta reiniciar
	Model, Busy string // Busy: clave del tramo que se está analizando
	Queued      []string
	LastErr     string
	Capped      bool  // el último pedido no entraba en el tope del día
	Tope, Spent int64 // micro-dólares
	// Tandas (tiempo real).
	RealtimeModel  string
	BatchEvery     time.Duration
	RealtimeCapped bool      // la última tanda no entraba en la parte del tope para tiempo real
	LastBatch      time.Time // cuándo salió la última tanda
	LastBatchErr   string
	Pending        int // mensajes para la próxima tanda (a lo sumo claudeBatchMax)
}

// claudeDay es lo que se guarda en <datos>.claude.json: el día local (no del
// Pacífico, como la cuota de YouTube) y lo gastado en micro-dólares.
type claudeDay struct {
	Day    string
	Micros int64
}

// quotaDay es lo que se guarda en <datos>.cuota.json: el día del Pacífico y lo gastado.
type quotaDay struct {
	Day   string
	Units int
}

func OpenStore(path string) (*Store, error) {
	base := strings.TrimSuffix(path, ".jsonl")
	s := &Store{path: path, quota: base + ".cuota.json", vpath: base + ".espectadores.jsonl", lpath: base + ".vivos.jsonl",
		apath: base + ".analisis.jsonl", cpath: base + ".claude.json", bpath: base + ".etiquetas.jsonl",
		vdpath: base + ".videos.jsonl", archive: map[string]VideoRecord{}, saved: map[string]time.Time{},
		lkpath: base + ".likes.jsonl", lastLike: map[string]LikeSample{},
		seen: map[string]bool{}, labels: map[string]MessageLabel{},
		sent: map[sentKey]*SentSlot{}, groups: map[string]BatchGroup{}}
	if b, err := os.ReadFile(s.quota); err == nil {
		var q quotaDay
		if json.Unmarshal(b, &q) == nil {
			s.unitsDay, s.units = q.Day, q.Units
		}
	}
	if b, err := os.ReadFile(s.cpath); err == nil {
		json.Unmarshal(b, &s.spend)
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
	lives, err := loadJSONL(s.lpath, func(l LiveSample) bool { return !l.At.Before(cutoff) })
	if err != nil {
		return nil, err
	}
	s.lives = lives
	if err := s.loadVideos(cutoff); err != nil {
		return nil, err
	}
	likes, err := loadJSONL(s.lkpath, func(l LikeSample) bool { return l.VideoID != "" && !l.At.Before(cutoff) })
	if err != nil {
		return nil, err
	}
	sort.SliceStable(likes, func(i, j int) bool { return likes[i].At.Before(likes[j].At) })
	s.likes = likes
	for _, l := range likes {
		s.lastLike[l.VideoID] = l
	}
	// La retención se cuenta desde el mensaje más viejo analizado (los del
	// tramo empiezan segmentLead antes de From), no desde la hora del análisis.
	analyses, err := loadJSONL(s.apath, func(a ClaudeAnalysis) bool { return !a.From.Add(-segmentLead).Before(cutoff) })
	if err != nil {
		return nil, err
	}
	s.analyses = analyses
	// Las etiquetas vencen con la tanda: desde su último mensaje (To).
	batches, err := loadJSONLMax(s.bpath, func(b ClaudeBatch) bool { return !b.To.Before(cutoff) }, maxBatchLine)
	if err != nil {
		return nil, err
	}
	s.batches = batches
	for _, b := range batches {
		s.indexLabels(b)
	}
	return s, nil
}

// loadJSONL carga un archivo de muestras con la retención de los mensajes (son
// datos de la API) y lo reescribe sin lo vencido ni lo ilegible. Si no existe,
// arranca vacío y no lo crea.
func loadJSONL[T any](path string, keep func(T) bool) ([]T, error) {
	return loadJSONLMax(path, keep, maxViewerLine)
}

// loadJSONLMax es loadJSONL con otro largo máximo de línea.
func loadJSONLMax[T any](path string, keep func(T) bool, maxLine int) ([]T, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	items, err := readJSONLMax(f, keep, maxLine)
	f.Close()
	if err != nil {
		// Reescribir con lo leído a medias perdería para siempre el resto.
		return nil, fmt.Errorf("no pude leer %s (si está dañado, movelo a otra carpeta y volvé a arrancar): %w", path, err)
	}
	return items, rewriteJSONL(path, items)
}

// loadViewers carga la serie de espectadores con la misma retención que los
// mensajes (son datos de la API) y reescribe el archivo sin lo vencido ni lo
// ilegible. Si no existe (instalaciones anteriores), se crea con el primer dato.
func (s *Store) loadViewers(cutoff time.Time) error {
	vs, err := loadJSONL(s.vpath, func(v ViewerSample) bool { return !v.At.Before(cutoff) })
	s.viewers = vs
	return err
}

// maxViewerLine: una muestra ocupa menos de 300 bytes; una línea más larga es
// basura (por ejemplo, ceros de un corte de luz) y se descarta sin cargarla.
const maxViewerLine = 64 * 1024

// readViewers lee las muestras de espectadores vigentes (ver readJSONL).
func readViewers(r io.Reader, cutoff time.Time) ([]ViewerSample, error) {
	return readJSONL(r, func(v ViewerSample) bool { return !v.At.Before(cutoff) })
}

// readJSONL lee las líneas que keep acepta salteando las ilegibles o
// demasiado largas. A diferencia de bufio.Scanner, una línea larga no corta
// la lectura: se pierden solo los datos de esa línea.
func readJSONL[T any](r io.Reader, keep func(T) bool) ([]T, error) {
	return readJSONLMax(r, keep, maxViewerLine)
}

// readJSONLMax es readJSONL con otro largo máximo de línea.
func readJSONLMax[T any](r io.Reader, keep func(T) bool, maxLine int) ([]T, error) {
	br := bufio.NewReaderSize(r, maxLine)
	var out []T
	long := false
	for {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			long = true // se descarta hasta el próximo salto de línea
			continue
		}
		var v T
		if !long && json.Unmarshal(line, &v) == nil && keep(v) {
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

// AddLives guarda una muestra por vivo en curso. Sale de la misma revisión de
// videos (o de la búsqueda rápida): no cuesta cuota.
func (s *Store) AddLives(at time.Time, vids []Video) {
	var add []LiveSample
	for _, v := range vids {
		if v.LiveStatus == "live" {
			add = append(add, LiveSample{At: at, VideoID: v.ID, ChannelID: v.ChannelID, Channel: v.Channel, Title: v.Title})
		}
	}
	if len(add) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lives = append(s.lives, add...)
	// Si no se puede abrir el archivo, las muestras quedan igual en memoria.
	f, err := os.OpenFile(s.lpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range add {
		enc.Encode(l)
	}
}

// FirstLive es la hora de la muestra de vivos más vieja guardada (cero si no
// hay): Programas la usa para decir desde cuándo hay datos sin copiar la serie.
func (s *Store) FirstLive() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var first time.Time
	for _, l := range s.lives {
		if first.IsZero() || l.At.Before(first) {
			first = l.At
		}
	}
	return first
}

// Lives devuelve una copia de las muestras de vivos desde since.
func (s *Store) Lives(since time.Time) []LiveSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []LiveSample
	for _, l := range s.lives {
		if !l.At.Before(since) {
			out = append(out, l)
		}
	}
	return out
}

// SetChannels guarda el estado de los canales de -canal, en el orden de -canal.
func (s *Store) SetChannels(ch []ChannelStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channels = append([]ChannelStatus(nil), ch...)
}

// SetPending guarda los canales de la configuración que el worker todavía
// no resolvió (o que YouTube no encontró, con Err), para la pantalla Canales.
func (s *Store) SetPending(p []ChannelStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append([]ChannelStatus(nil), p...)
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

// AddClaudeSpend suma lo gastado en Claude (micro-dólares) y lo guarda.
func (s *Store) AddClaudeSpend(micros int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollClaudeDay()
	s.spend.Micros += micros
	b, _ := json.Marshal(s.spend)
	tmp := s.cpath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, s.cpath)
	}
}

// ClaudeSpend es lo gastado hoy en Claude (micro-dólares).
func (s *Store) ClaudeSpend() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollClaudeDay()
	return s.spend.Micros
}

// rollClaudeDay reinicia el gasto de Claude a las 00:00 locales: el tope es
// por día de la radio, no del Pacífico.
func (s *Store) rollClaudeDay() {
	if day := time.Now().Local().Format("2006-01-02"); day != s.spend.Day {
		s.spend = claudeDay{Day: day}
	}
}

// AddAnalysis guarda un análisis de Claude al final de datos.analisis.jsonl.
func (s *Store) AddAnalysis(a ClaudeAnalysis) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.analyses = append(s.analyses, a)
	// Si no se puede abrir el archivo, el análisis queda igual en memoria.
	f, err := os.OpenFile(s.apath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	json.NewEncoder(f).Encode(a)
}

// Analyses devuelve los análisis de un canal con tramos desde since, en el
// orden en que se hicieron. Los análisis no se modifican después de
// guardarlos, así que compartir sus listas internas es seguro.
func (s *Store) Analyses(channelID string, since time.Time) []ClaudeAnalysis {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ClaudeAnalysis
	for _, a := range s.analyses {
		if a.ChannelID == channelID && !a.From.Before(since) {
			out = append(out, a)
		}
	}
	return out
}

// AddBatch guarda una tanda de Claude al final de datos.etiquetas.jsonl y
// suma sus etiquetas al índice. Nunca toca datos.jsonl.
func (s *Store) AddBatch(b ClaudeBatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, b)
	s.indexLabels(b)
	f, err := os.OpenFile(s.bpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	json.NewEncoder(f).Encode(b)
}

func (s *Store) indexLabels(b ClaudeBatch) {
	for _, l := range b.Labels {
		s.labels[l.ID] = l
		// Se suma al guardar: En vivo pide 7 días de franjas cada 30 s y no
		// puede recorrer todas las etiquetas cada vez.
		k := sentKey{b.ChannelID, slotOf(l.At).Unix(), l.Program}
		if s.sent[k] == nil {
			s.sent[k] = &SentSlot{Start: time.Unix(k.start, 0), Program: l.Program}
		}
		s.sent[k].add(l.Sentiment)
	}
	for _, g := range b.NewGroups {
		s.groups[g.ID] = g
	}
}

type sentKey struct {
	channel string
	start   int64
	program string
}

// SentSlots devuelve las franjas de sentimiento de un canal desde since, en
// orden (copias).
func (s *Store) SentSlots(channelID string, since time.Time) []SentSlot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SentSlot
	for k, v := range s.sent {
		if k.channel == channelID && !v.Start.Before(slotOf(since)) {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Program < out[j].Program
	})
	return out
}

// Groups devuelve los grupos de preguntas con esos IDs (los que no están, no).
func (s *Store) Groups(ids []string) map[string]BatchGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]BatchGroup{}
	for _, id := range ids {
		if g, ok := s.groups[id]; ok {
			out[id] = g
		}
	}
	return out
}

// Batches devuelve las tandas de un canal ("" = todos) que terminan desde
// since, en orden. Como con Analyses, las tandas no se modifican después de
// guardarlas: compartir sus listas internas es seguro.
func (s *Store) Batches(channelID string, since time.Time) []ClaudeBatch {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ClaudeBatch
	for _, b := range s.batches {
		if (channelID == "" || b.ChannelID == channelID) && !b.To.Before(since) {
			out = append(out, b)
		}
	}
	return out
}

// Labels devuelve las etiquetas de esos mensajes (los que no tienen, no están).
func (s *Store) Labels(ids []string) map[string]MessageLabel {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]MessageLabel{}
	for _, id := range ids {
		if l, ok := s.labels[id]; ok {
			out[id] = l
		}
	}
	return out
}

// RealtimeSpend es lo gastado en tandas desde since (micro-dólares): la parte
// del tope para tiempo real se cuenta aparte del gasto total.
func (s *Store) RealtimeSpend(since time.Time) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	for _, b := range s.batches {
		if !b.At.Before(since) {
			n += b.CostMicros
		}
	}
	return n
}

// SetClaudeStatus guarda el estado del análisis con Claude (lo llama RunAnalyst).
func (s *Store) SetClaudeStatus(c ClaudeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Queued = append([]string(nil), c.Queued...)
	s.claude = c
}

// MessagesOf devuelve una copia de los mensajes de esos vivos desde since:
// el analista no necesita copiar todo el histórico en cada vuelta.
func (s *Store) MessagesOf(ids map[string]bool, since time.Time) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Message
	for _, m := range s.msgs {
		if ids[m.VideoID] && !m.At.Before(since) {
			out = append(out, m)
		}
	}
	return out
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
	Channels  []ChannelStatus
	Videos    []Video
	Msgs      []Message
	Units     int
	LastErr   string
	LastErrT  time.Time
	ChatNote  string
	ChatNotes map[string]string
	ChatInfo  map[string]ChatInfo
	Updated   time.Time
	Claude    ClaudeStatus
	Pending   []ChannelStatus // canales de la configuración que el worker todavía no resolvió
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
	s.rollClaudeDay()
	cl := s.claude
	cl.Queued, cl.Spent = append([]string(nil), cl.Queued...), s.spend.Micros
	return Snapshot{
		Claude:  cl,
		Channel: s.channel, Channels: append([]ChannelStatus(nil), s.channels...), Videos: append([]Video(nil), s.videos...),
		Units: s.units, LastErr: s.lastErr, LastErrT: s.lastErrT, ChatNote: s.chatNote,
		ChatNotes: maps.Clone(s.chatNotes), ChatInfo: maps.Clone(s.chatInfo), Updated: s.updated,
		Pending: append([]ChannelStatus(nil), s.pending...),
	}
}

// LastChat devuelve una copia de los últimos n mensajes de chat de un vivo, del más nuevo al más viejo.
func (s *Store) LastChat(videoID string, n int) []Message {
	return s.LastChatOf(map[string]bool{videoID: true}, n)
}

// LastChatOf es LastChat de varios vivos a la vez: los de un canal en el día.
func (s *Store) LastChatOf(ids map[string]bool, n int) []Message {
	if len(ids) == 0 {
		return nil // sin vivos no hace falta recorrer el histórico
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Message
	for i := len(s.msgs) - 1; i >= 0 && len(out) < n; i-- {
		if m := s.msgs[i]; ids[m.VideoID] && m.Source == "chat" {
			out = append(out, m)
		}
	}
	return out
}

// Un video aparece en cada revisión (cada 3 min): en datos.videos.jsonl se
// escribe cuando es nuevo y después a lo sumo una vez por videoSaveEvery. Al
// abrir queda una línea por video.
const videoSaveEvery = time.Hour

// loadVideos carga el último dato de cada video visto en los últimos 30 días
// (son datos de la API) y deja el archivo con una línea por video.
func (s *Store) loadVideos(cutoff time.Time) error {
	recs, err := loadJSONL(s.vdpath, func(v VideoRecord) bool { return v.ID != "" && !v.Seen.Before(cutoff) })
	if err != nil {
		return err
	}
	for _, v := range recs {
		if old, ok := s.archive[v.ID]; !ok || !v.Seen.Before(old.Seen) {
			s.archive[v.ID] = v
		}
	}
	for id, v := range s.archive {
		s.saved[id] = v.Seen
	}
	if len(recs) == len(s.archive) {
		return nil
	}
	return rewriteJSONL(s.vdpath, s.videoArchive())
}

// AddVideos guarda el último dato de cada video de una revisión. Sale del
// mismo pedido de videos: no cuesta cuota.
func (s *Store) AddVideos(at time.Time, vids []Video) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var add []VideoRecord
	for _, v := range vids {
		r := videoRecord(v, at)
		// Un short lo sigue siendo aunque esta revisión no lo sepa (la lista de
		// shorts se pide cada shortsEvery y no se guarda).
		r.Short = r.Short || s.archive[v.ID].Short
		s.archive[v.ID] = r
		if last, ok := s.saved[v.ID]; !ok || !at.Before(last.Add(videoSaveEvery)) {
			s.saved[v.ID] = at
			add = append(add, r)
		}
	}
	if len(add) == 0 {
		return
	}
	// Si no se puede abrir el archivo, el dato queda igual en memoria.
	f, err := os.OpenFile(s.vdpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range add {
		enc.Encode(r)
	}
}

// VideoArchive devuelve una copia del último dato de cada video, del más
// nuevo al más viejo.
func (s *Store) VideoArchive() []VideoRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.videoArchive()
}

func (s *Store) videoArchive() []VideoRecord {
	out := make([]VideoRecord, 0, len(s.archive))
	for _, v := range s.archive {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].PublishedAt.After(out[j].PublishedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Comments devuelve una copia de los comentarios desde since: la pantalla
// Comentarios no copia el chat.
func (s *Store) Comments(since time.Time) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Message
	for _, m := range s.msgs {
		if m.Source == "comentario" && !m.At.Before(since) {
			out = append(out, m)
		}
	}
	return out
}

// LikeSample es el contador de likes de un video en una revisión.
type LikeSample struct {
	VideoID string
	At      time.Time
	Likes   int64
}

// Un vivo guarda una muestra de likes en cada revisión en que cambia (para
// repartirlos por programa); un video o un short, a lo sumo una cada
// likeSaveEvery: suben despacio y la curva no necesita más.
const likeSaveEvery = 30 * time.Minute

// AddLikes guarda el contador de likes de cada video de una revisión. Sale del
// mismo pedido de videos: no cuesta cuota. Sin likes (ocultos o cero), nada.
func (s *Store) AddLikes(at time.Time, vids []Video) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var add []LikeSample
	for _, v := range vids {
		if v.Likes <= 0 {
			continue
		}
		last, ok := s.lastLike[v.ID]
		if ok && (last.Likes == v.Likes || v.LiveStatus != "live" && at.Before(last.At.Add(likeSaveEvery))) {
			continue
		}
		l := LikeSample{VideoID: v.ID, At: at, Likes: v.Likes}
		s.lastLike[v.ID] = l
		add = append(add, l)
	}
	if len(add) == 0 {
		return
	}
	s.likes = append(s.likes, add...)
	// Si no se puede abrir el archivo, las muestras quedan igual en memoria.
	f, err := os.OpenFile(s.lkpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, l := range add {
		enc.Encode(l)
	}
}

// Likes devuelve una copia de las muestras de un video desde since.
func (s *Store) Likes(id string, since time.Time) []LikeSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []LikeSample
	for _, l := range s.likes {
		if l.VideoID == id && !l.At.Before(since) {
			out = append(out, l)
		}
	}
	return out
}

// LikesSince devuelve una copia de las muestras desde since, por video y en
// orden de hora.
func (s *Store) LikesSince(since time.Time) map[string][]LikeSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string][]LikeSample{}
	for _, l := range s.likes {
		if !l.At.Before(since) {
			out[l.VideoID] = append(out[l.VideoID], l)
		}
	}
	return out
}

// MissingVideos son los videos con comentarios guardados que no están en el
// archivo (de antes de que existiera o que salieron de la revisión): el
// worker los pide para que Comentarios no muestre filas sin título.
func (s *Store) MissingVideos() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	var out []string
	for _, m := range s.msgs {
		if m.Source == "comentario" && !seen[m.VideoID] {
			seen[m.VideoID] = true
			if _, ok := s.archive[m.VideoID]; !ok {
				out = append(out, m.VideoID)
			}
		}
	}
	sort.Strings(out)
	return out
}
