package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	fakeKey       = "vista-previa"        // la única clave de YouTube que recibe el dashboard de la vista previa
	fakeClaudeKey = "vista-previa-claude" // y la única de Claude (ADR 0006)
	uploadsID     = "UUvistaprevia"
	livesID       = "UULVvistaprevia" // la lista de vivos que ChannelVideos arma con UU → UULV
	retention     = 30 * 24 * time.Hour
)

// fakeVideo es lo que el falso sabe de un video, sacado de los mensajes copiados.
type fakeVideo struct {
	ID          string
	Live        bool // tiene chat: se muestra como vivo en curso
	First, Last time.Time
	Program     string    // el del último mensaje de chat que lo tenga
	ProgramAt   time.Time // cuándo llegó ese mensaje
}

// fakeAPI imita lo mínimo de la YouTube Data API para que el dashboard muestre los datos
// copiados: un canal, sus listas y los videos. Nada de chat ni comentarios: sin
// activeLiveChatId y con commentCount 0 el worker no los pide.
type fakeAPI struct {
	videos   map[string]fakeVideo
	lives    []string // más reciente primero: el vivo por defecto es el más activo
	uploads  []string
	msgs     int
	requests atomic.Int64
	badKeys  atomic.Int64 // pedidos con otra clave: no tendría que haber nunca
}

// loadFake lee de la copia solo ID, Source, VideoID, At y Program: el falso nunca
// maneja textos ni autores. Si el archivo no existe, el falso queda vacío.
func loadFake(path string) (*fakeAPI, error) {
	f := &fakeAPI{videos: map[string]fakeVideo{}}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	cutoff := time.Now().Add(-retention) // lo mismo que descarta OpenStore al abrir
	seen := map[string]bool{}
	rd := bufio.NewReader(file)
	for {
		line, err := rd.ReadBytes('\n')
		var m struct {
			ID, Source, VideoID, Program string
			At                           time.Time
		}
		if json.Unmarshal(line, &m) == nil && m.VideoID != "" && !m.At.Before(cutoff) && !seen[m.ID] {
			seen[m.ID] = true
			f.msgs++
			f.add(m.VideoID, m.Source == "chat", m.At, m.Program)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	for id, v := range f.videos {
		if v.Live {
			f.lives = append(f.lives, id)
		} else {
			f.uploads = append(f.uploads, id)
		}
	}
	newest := func(ids []string) {
		sort.Slice(ids, func(i, j int) bool { return f.videos[ids[i]].Last.After(f.videos[ids[j]].Last) })
	}
	newest(f.lives)
	newest(f.uploads)
	return f, nil
}

func (f *fakeAPI) add(id string, chat bool, at time.Time, program string) {
	v, ok := f.videos[id]
	if !ok {
		v = fakeVideo{ID: id, First: at, Last: at}
	}
	if at.Before(v.First) {
		v.First = at
	}
	if !at.Before(v.Last) {
		v.Last = at
	}
	if chat {
		v.Live = true
		if program != "" && !at.Before(v.ProgramAt) {
			v.Program, v.ProgramAt = program, at
		}
	}
	f.videos[id] = v
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	// Claude manda la clave en el header x-api-key, no en la URL: va antes del chequeo de key.
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		f.claude(w, r)
		return
	}
	q := r.URL.Query()
	if q.Get("key") != fakeKey {
		// Si llegara la clave real (por ejemplo, heredada del entorno), no se usa.
		f.badKeys.Add(1)
		apiError(w, http.StatusForbidden, "vista previa: clave inesperada")
		return
	}
	switch r.URL.Path {
	case "/channels":
		writeJSON(w, map[string]any{"items": []any{map[string]any{
			"snippet":        map[string]any{"title": "Vista previa (datos copiados)"},
			"contentDetails": map[string]any{"relatedPlaylists": map[string]any{"uploads": uploadsID}},
		}}})
	case "/playlistItems":
		ids := f.uploads
		switch q.Get("playlistId") {
		case livesID:
			ids = f.lives
		case uploadsID:
		default:
			writeJSON(w, map[string]any{"items": []any{}})
			return
		}
		if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n >= 0 && n < len(ids) {
			ids = ids[:n]
		}
		items := []any{}
		for _, id := range ids {
			items = append(items, map[string]any{"contentDetails": map[string]any{"videoId": id}})
		}
		writeJSON(w, map[string]any{"items": items})
	case "/videos":
		items := []any{}
		for _, id := range strings.Split(q.Get("id"), ",") {
			if v, ok := f.videos[id]; ok {
				items = append(items, v.item())
			}
		}
		writeJSON(w, map[string]any{"items": items})
	default:
		apiError(w, http.StatusNotFound, "vista previa: "+r.URL.Path+" no está simulado")
	}
}

// fakeAnalysis es lo que "responde Claude" en la vista previa: cumple el esquema del
// análisis y cita los mensajes 1 a 3.
const fakeAnalysis = `{"resumen":"Análisis simulado de la vista previa: no lo armó Claude y los mensajes no salieron de esta PC.",` +
	`"sentimiento":{"positivo":50,"neutro":30,"negativo":20,"tono":"simulado"},` +
	`"temas":[{"tema":"Tema simulado","mensajes":[1,2,3]}],` +
	`"preguntas":[{"pregunta":"Pregunta simulada","mensajes":[1,2]}],` +
	`"quejas":[{"queja":"Queja simulada","mensajes":[3]}]}`

// claude imita POST /v1/messages de la API de Claude. Del pedido solo lee el modelo:
// los textos de los mensajes copiados no se guardan ni se loguean.
func (f *fakeAPI) claude(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		apiError(w, http.StatusNotFound, "vista previa: "+r.URL.Path+" no está simulado")
		return
	}
	if r.Header.Get("x-api-key") != fakeClaudeKey {
		// Si llegara la clave real (por ejemplo, heredada del entorno), no se usa.
		f.badKeys.Add(1)
		apiError(w, http.StatusForbidden, "vista previa: clave inesperada")
		return
	}
	var req struct {
		Model    string                     `json:"model"`
		Messages []struct{ Content string } `json:"messages"`
		Output   struct {
			Format struct{ Schema json.RawMessage }
		} `json:"output_config"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&req)
	text := fakeAnalysis
	// Las tandas (ADR 0009) se distinguen por el esquema: se responde una
	// etiqueta por número de mensaje, sin mirar los textos.
	if strings.Contains(string(req.Output.Format.Schema), "grupos_nuevos") && len(req.Messages) > 0 {
		text = fakeBatch(req.Messages[0].Content)
	}
	writeJSON(w, map[string]any{
		"id": "msg_vista_previa", "type": "message", "role": "assistant", "model": req.Model,
		"content":     []any{map[string]any{"type": "text", "text": text}},
		"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1200, "output_tokens": 300},
	})
}

var reBatchLine = regexp.MustCompile(`(?m)^\[(\d+)\] `)

// fakeBatch arma etiquetas simuladas para los números de mensaje de una tanda:
// sentimientos y categorías que rotan, una pregunta simulada y un motivo de
// cada tipo, para que la vista previa tenga algo que mostrar.
func fakeBatch(user string) string {
	sents := []string{"positivo", "neutro", "negativo"}
	cats := []string{"otro", "pregunta", "elogio", "queja"}
	var items []string
	for i, m := range reBatchLine.FindAllStringSubmatch(user, -1) {
		group := ""
		if cats[i%len(cats)] == "pregunta" {
			group = "nuevo1"
		}
		items = append(items, fmt.Sprintf(`{"n":%s,"sentimiento":%q,"categoria":%q,"tecnica":%v,"grupo":%q}`,
			m[1], sents[i%len(sents)], cats[i%len(cats)], i%7 == 3, group))
	}
	return `{"mensajes":[` + strings.Join(items, ",") + `],` +
		`"grupos_nuevos":[{"clave":"nuevo1","pregunta":"Pregunta simulada de la vista previa"}],` +
		`"motivos":[{"tipo":"positivo","motivo":"Motivo positivo simulado","mensajes":[1]},` +
		`{"tipo":"negativo","motivo":"Motivo negativo simulado","mensajes":[1]}]}`
}

// item arma el video como lo devuelve videos.list. Sin activeLiveChatId (el worker no
// abre el chat), commentCount 0 (no pide comentarios) y sin concurrentViewers (no suma
// muestras a la serie de espectadores copiada).
func (v fakeVideo) item() map[string]any {
	title, status := "Video "+v.ID, "none"
	if v.Live {
		status = "live"
		if v.Program != "" {
			title = "VISTA PREVIA en VIVO | " + v.Program
		}
	}
	it := map[string]any{
		"id": v.ID,
		"snippet": map[string]any{
			"title": title, "channelTitle": "Vista previa", "publishedAt": v.First.UTC().Format(time.RFC3339),
			"liveBroadcastContent": status,
		},
		"statistics": map[string]any{"viewCount": "0", "commentCount": "0"},
	}
	if v.Live {
		it["liveStreamingDetails"] = map[string]any{"activeLiveChatId": "", "actualStartTime": v.First.UTC().Format(time.RFC3339Nano)}
	}
	return it
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg}})
}
