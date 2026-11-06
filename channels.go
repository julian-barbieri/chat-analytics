package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Configuración de canales editable desde la pantalla Canales (ADR 0005).
// Settings es lo único de la configuración que escriben los handlers: tiene
// su propio mutex, fuera del Store, y el worker la lee en cada tick
// comparando la versión. El archivo guarda solo lo que escribió la persona,
// nada que venga de la API.

const maxChannels = 5

// Intervalos que ofrecen los selects de Canales, en segundos. Se acepta
// cualquier múltiplo de 30 s entre 30 s y 10 min: al migrar desde los flags
// (por ejemplo, -chat-seg 180 × 2) el valor puede no estar en la lista.
var (
	peakChoices = []int{30, 60, 90, 120, 180, 300}
	offChoices  = []int{60, 120, 180, 300, 600}
)

const minChatSec, maxChatSec = 30, 600

// Para agregar un canal: lo que salga de ParseChannel tiene que ser un ID o un
// @handle sin comillas ni otros símbolos (va a atributos y al archivo).
var reChannelInput = regexp.MustCompile(`^(UC[A-Za-z0-9_-]+|@[\p{L}\p{N}._·-]{1,100})$`)

// ChannelSetting es un canal de la configuración. Input es lo que escribió
// la persona, ya normalizado con ParseChannel (ID UC… o @handle).
type ChannelSetting struct {
	Input                   string
	ChatPeakSec, ChatOffSec int
	Paused                  bool // pausa solo el chat: sus vivos se siguen revisando
	// NoClaude apaga las tandas de Claude de este canal. Va al revés (el
	// check "Analizar el chat con Claude" viene prendido) para que un
	// archivo de antes, sin el campo, quede con el check activado.
	NoClaude bool `json:",omitempty"`
}

func (c ChannelSetting) peak() time.Duration { return time.Duration(c.ChatPeakSec) * time.Second }
func (c ChannelSetting) off() time.Duration  { return time.Duration(c.ChatOffSec) * time.Second }

// ClaudeSettings es la tarjeta "Análisis con Claude" de Canales: el modelo de
// las tandas (tiempo real), cada cuánto se manda una y el tope diario. El tope
// va en micro-dólares (int64), como el contador de gasto, nunca en float.
type ClaudeSettings struct {
	RealtimeModel string
	BatchSec      int
	BudgetMicros  int64
}

func (c ClaudeSettings) every() time.Duration { return time.Duration(c.BatchSec) * time.Second }

// batchChoices son las tandas que ofrece Canales, en segundos; maxClaudeUSD
// es el tope más alto que se acepta desde la pantalla.
var batchChoices = []int{30, 60, 120, 300}

const maxClaudeUSD = 50

type settingsFile struct {
	Channels []ChannelSetting
	Claude   *ClaudeSettings `json:",omitempty"` // sin la sección valen los flags
}

// Settings es la configuración vigente. Get devuelve copias.
type Settings struct {
	mu       sync.Mutex
	path     string
	channels []ChannelSetting
	version  int
	fromFile bool
	warn     string
	claude   *ClaudeSettings // lo guardado desde Canales; nil: valen los flags
	claudeDf ClaudeSettings  // los flags (-claude-modelo-vivo, -claude-tanda-seg, -claude-usd)
	cwarn    string          // flags de Claude pasados a mano que no se usan
}

// defaultClaude es lo que vale sin flags ni archivo: Haiku, tandas cada 30 s
// y tope 0 (apagado).
var defaultClaude = ClaudeSettings{RealtimeModel: defaultRealtimeModel, BatchSec: 30}

// settingsPath es datos.canales.json al lado de datos.jsonl (y de los demás).
func settingsPath(dataPath string) string {
	return strings.TrimSuffix(dataPath, ".jsonl") + ".canales.json"
}

// flagSettings arma la configuración inicial desde -canal: con N canales cada
// uno lee cada -chat-seg* × N, así el gasto del chat es el de antes, cuando
// todos los vivos se turnaban con un solo intervalo.
func flagSettings(channels []string, peakSec, offSec int) []ChannelSetting {
	n := len(channels)
	out := make([]ChannelSetting, 0, n)
	for _, c := range channels {
		out = append(out, ChannelSetting{Input: c, ChatPeakSec: peakSec * n, ChatOffSec: offSec * n})
	}
	return out
}

// LoadSettings lee datos.canales.json. Sin archivo arranca con fromFlags (no
// escribe nada hasta el primer cambio). Con archivo, manda el archivo; manual
// son los flags de canales pasados a mano (flag.Visit), que se avisan. Un
// archivo que no se entiende frena el arranque y no se pisa.
func LoadSettings(path string, fromFlags []ChannelSetting, manual []string) (*Settings, error) {
	s := &Settings{path: path, claudeDf: defaultClaude}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.channels = append([]ChannelSetting(nil), fromFlags...)
		return s, nil
	}
	name := filepath.Base(path)
	fail := func(err error) (*Settings, error) {
		return nil, fmt.Errorf("no puedo usar %s (%v): corregilo o borralo para arrancar con -canal, -chat-seg-pico y -chat-seg (se pierde lo guardado desde Canales)", name, err)
	}
	if err != nil {
		return fail(err)
	}
	var f settingsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fail(err)
	}
	// Lo guardado se acepta como está: puede tener intervalos heredados de los
	// flags (por ejemplo, -chat-seg 180 × 4 = 720) que Update conservó. La
	// regla estricta vale solo para lo que se cambia desde Canales.
	if f.Channels, err = validSettings(f.Channels, f.Channels); err != nil {
		return fail(err)
	}
	s.channels, s.fromFile = f.Channels, true
	if f.Claude != nil {
		c := *f.Claude
		if err := validClaudeSettings(c); err != nil {
			return fail(err)
		}
		s.claude = &c
	}
	if len(manual) > 0 {
		flags := make([]string, len(manual))
		for i, m := range manual {
			flags[i] = "-" + m
		}
		s.warn = fmt.Sprintf("%s no se usa: valen los canales guardados desde Canales en %s", strings.Join(flags, ", "), name)
	}
	return s, nil
}

// SetClaudeDefaults fija los valores de los flags de Claude, que valen
// mientras el archivo no tenga la sección Claude. manual son los flags de
// Claude pasados a mano: si manda el archivo, se avisan.
func (s *Settings) SetClaudeDefaults(c ClaudeSettings, manual []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claudeDf = c
	if s.claude != nil && len(manual) > 0 {
		flags := make([]string, len(manual))
		for i, m := range manual {
			flags[i] = "-" + m
		}
		s.cwarn = fmt.Sprintf("%s no se usa: vale lo guardado acá (%s)", strings.Join(flags, ", "), filepath.Base(s.path))
	}
}

// Claude devuelve la configuración de Claude vigente y si salió del archivo.
func (s *Settings) Claude() (ClaudeSettings, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claude != nil {
		return *s.claude, true
	}
	return s.claudeDf, false
}

// ClaudeWarning es el aviso de flags de Claude que no se usan.
func (s *Settings) ClaudeWarning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cwarn
}

// UpdateClaude guarda la tarjeta de Claude: la configuración y qué canales
// se analizan (analyze, por Input). Como Update, valida, guarda y recién
// ahí lo deja vigente.
func (s *Settings) UpdateClaude(c ClaudeSettings, analyze map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validClaudeSettings(c); err != nil {
		return err
	}
	next := append([]ChannelSetting(nil), s.channels...)
	for i := range next {
		next[i].NoClaude = !analyze[next[i].Input]
	}
	if err := s.save(next, &c); err != nil {
		return err
	}
	s.claude, s.cwarn = &c, ""
	return nil
}

func validClaudeSettings(c ClaudeSettings) error {
	if _, ok := claudePrices[c.RealtimeModel]; !ok {
		return fmt.Errorf("no conozco el modelo %q", c.RealtimeModel)
	}
	if !slices.Contains(batchChoices, c.BatchSec) {
		return errors.New("elegí cada cuánto se manda una tanda de la lista")
	}
	if c.BudgetMicros < 0 || c.BudgetMicros > maxClaudeUSD*1_000_000 {
		return fmt.Errorf("el tope tiene que estar entre US$ 0 y US$ %d", maxClaudeUSD)
	}
	return nil
}

// Get devuelve una copia de la configuración y su versión.
func (s *Settings) Get() ([]ChannelSetting, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ChannelSetting(nil), s.channels...), s.version
}

// FromFile dice si la configuración salió de datos.canales.json.
func (s *Settings) FromFile() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fromFile
}

// Warning es el aviso de flags que no se usan porque manda el archivo.
func (s *Settings) Warning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warn
}

// Update cambia la configuración con f, la valida, la guarda (tmp +
// os.Rename) y recién ahí la deja vigente. Si algo falla, queda la anterior.
func (s *Settings) Update(f func([]ChannelSetting) ([]ChannelSetting, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := f(append([]ChannelSetting(nil), s.channels...))
	if err != nil {
		return err
	}
	if next, err = validSettings(next, s.channels); err != nil {
		return err
	}
	return s.save(next, s.claude)
}

// save escribe el archivo (tmp + os.Rename) y deja vigentes los canales.
// Con s.mu tomado.
func (s *Settings) save(channels []ChannelSetting, claude *ClaudeSettings) error {
	b, _ := json.MarshalIndent(settingsFile{Channels: channels, Claude: claude}, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("no se pudo guardar la configuración: %v", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("no se pudo guardar la configuración: %v", err)
	}
	s.channels, s.fromFile = channels, true
	s.version++
	return nil
}

// validSettings normaliza y valida una configuración. Un intervalo que no
// cambió respecto de prev se acepta como está si es positivo (puede venir de
// los flags); uno nuevo tiene que ser de los permitidos.
func validSettings(cs []ChannelSetting, prev []ChannelSetting) ([]ChannelSetting, error) {
	if len(cs) == 0 {
		return nil, errors.New("tiene que quedar al menos un canal")
	}
	if len(cs) > maxChannels {
		return nil, fmt.Errorf("se pueden seguir hasta %d canales", maxChannels)
	}
	old := map[string]ChannelSetting{}
	for _, p := range prev {
		old[p.Input] = p
	}
	seen := map[string]bool{}
	out := make([]ChannelSetting, len(cs))
	for i, c := range cs {
		in := ParseChannel(c.Input)
		if in == "" || !reChannelInput.MatchString(in) {
			return nil, fmt.Errorf("no reconozco el canal %q: usá el link, el @handle o el ID UC…", c.Input)
		}
		if seen[strings.ToLower(in)] {
			return nil, fmt.Errorf("el canal %s ya está", in)
		}
		seen[strings.ToLower(in)] = true
		c.Input = in
		p, had := old[in]
		if c.ChatPeakSec <= 0 || c.ChatOffSec <= 0 ||
			!(had && p.ChatPeakSec == c.ChatPeakSec) && !validChatSec(c.ChatPeakSec) ||
			!(had && p.ChatOffSec == c.ChatOffSec) && !validChatSec(c.ChatOffSec) {
			return nil, fmt.Errorf("intervalo inválido para %s: elegí uno de la lista (múltiplo de 30 s, entre 30 s y 10 min)", in)
		}
		out[i] = c
	}
	return out, nil
}

func validChatSec(n int) bool { return n >= minChatSec && n <= maxChatSec && n%30 == 0 }
