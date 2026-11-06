package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
}

func (c ChannelSetting) peak() time.Duration { return time.Duration(c.ChatPeakSec) * time.Second }
func (c ChannelSetting) off() time.Duration  { return time.Duration(c.ChatOffSec) * time.Second }

type settingsFile struct {
	Channels []ChannelSetting
}

// Settings es la configuración vigente. Get devuelve copias.
type Settings struct {
	mu       sync.Mutex
	path     string
	channels []ChannelSetting
	version  int
	fromFile bool
	warn     string
}

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
	s := &Settings{path: path}
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
	if len(manual) > 0 {
		flags := make([]string, len(manual))
		for i, m := range manual {
			flags[i] = "-" + m
		}
		s.warn = fmt.Sprintf("%s no se usa: valen los canales guardados desde Canales en %s", strings.Join(flags, ", "), name)
	}
	return s, nil
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
	b, _ := json.MarshalIndent(settingsFile{Channels: next}, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("no se pudo guardar la configuración: %v", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("no se pudo guardar la configuración: %v", err)
	}
	s.channels, s.fromFile = next, true
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
