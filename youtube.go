package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Cliente mínimo de la YouTube Data API v3 usando solo una API key
// (lectura de datos públicos, no necesita acceso de administrador).
type YouTube struct {
	Key     string
	BaseURL string
	HTTP    *http.Client
	Stream  *http.Client // sin timeout: la conexión del chat en tiempo real queda abierta
	Units   int          // unidades de cuota estimadas que se gastaron
}

func NewYouTube(key string) *YouTube {
	return &YouTube{
		Key:     key,
		BaseURL: "https://www.googleapis.com/youtube/v3",
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Stream:  &http.Client{},
	}
}

func (y *YouTube) get(path string, params url.Values, cost int, out any) error {
	params.Set("key", y.Key)
	resp, err := y.HTTP.Get(y.BaseURL + "/" + path + "?" + params.Encode())
	if err != nil {
		// El error trae la URL completa: sacamos la clave para que no aparezca en logs ni en el dashboard.
		return fmt.Errorf("%s: %s", path, strings.ReplaceAll(err.Error(), y.Key, "***"))
	}
	defer resp.Body.Close()
	y.Units += cost
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &apiErr)
		return fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, apiErr.Error.Message)
	}
	return json.Unmarshal(body, out)
}

type Video struct {
	ID          string
	Title       string
	Channel     string // nombre del canal
	PublishedAt time.Time
	Views       int64
	Comments    int64
	LiveStatus  string // "live", "upcoming" o "none"
	LiveChatID  string
	Concurrent  int64
}

// UploadsPlaylist devuelve el nombre del canal y la lista de "subidas".
// Acepta un ID de canal ("UC...") o un @handle.
func (y *YouTube) UploadsPlaylist(channel string) (title, playlist string, err error) {
	var r struct {
		Items []struct {
			Snippet        struct{ Title string } `json:"snippet"`
			ContentDetails struct {
				RelatedPlaylists struct {
					Uploads string `json:"uploads"`
				} `json:"relatedPlaylists"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	params := url.Values{"part": {"snippet,contentDetails"}, "id": {channel}}
	if strings.HasPrefix(channel, "@") {
		params = url.Values{"part": {"snippet,contentDetails"}, "forHandle": {channel}}
	}
	err = y.get("channels", params, 1, &r)
	if err != nil {
		return "", "", err
	}
	if len(r.Items) == 0 {
		return "", "", fmt.Errorf("no se encontró el canal %s", channel)
	}
	return r.Items[0].Snippet.Title, r.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

// RecentVideoIDs devuelve los últimos n videos subidos (incluye vivos).
func (y *YouTube) RecentVideoIDs(playlist string, n int) ([]string, error) {
	var r struct {
		Items []struct {
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	err := y.get("playlistItems", url.Values{
		"part": {"contentDetails"}, "playlistId": {playlist}, "maxResults": {fmt.Sprint(min(n, 50))},
	}, 1, &r)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		ids = append(ids, it.ContentDetails.VideoID)
	}
	return ids, nil
}

// ChannelVideos devuelve los vivos de los canales, los videos pedidos a mano
// (extra) y los últimos n subidos del primer canal, sin repetir y en ese orden.
// Los vivos que transmiten las 24 horas se publicaron hace días y quedan fuera de
// los últimos n subidos: por eso se buscan también en la lista automática de
// vivos de cada canal. De los otros canales solo quedan los vivos en curso o
// programados, así no se gasta cuota en comentarios de videos viejos.
func (y *YouTube) ChannelVideos(uploads []string, n int, extra []string) ([]Video, error) {
	ups, err := y.RecentVideoIDs(uploads[0], n)
	if err != nil {
		return nil, err
	}
	var ids []string
	seen := map[string]bool{}
	only := map[string]bool{} // videos que aparecen solo en la lista de vivos de otro canal
	add := func(list []string, other bool) {
		for _, id := range list {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
				only[id] = other
			} else if !other {
				only[id] = false
			}
		}
	}
	for i, u := range uploads {
		// "UULV..." es la lista de vivos que YouTube arma sola (1 unidad por canal).
		// No está documentada: si falla, seguimos con las subidas y los videos extra.
		// De los otros canales alcanza con los últimos 5: solo interesan los vivos en curso.
		count := 10
		if i > 0 {
			count = 5
		}
		lives, _ := y.RecentVideoIDs("UULV"+strings.TrimPrefix(u, "UU"), count)
		add(lives, i > 0)
	}
	add(extra, false)
	add(ups, false)
	vids, err := y.Videos(ids)
	out := vids[:0]
	for _, v := range vids {
		if !only[v.ID] || v.LiveStatus != "none" {
			out = append(out, v)
		}
	}
	return out, err
}

// Videos trae datos y estadísticas de los videos, de a 50 por llamada.
func (y *YouTube) Videos(ids []string) ([]Video, error) {
	var out []Video
	for len(ids) > 0 {
		n := min(len(ids), 50)
		vs, err := y.videos(ids[:n])
		if err != nil {
			return out, err
		}
		out = append(out, vs...)
		ids = ids[n:]
	}
	return out, nil
}

func (y *YouTube) videos(ids []string) ([]Video, error) {
	var r struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title                string    `json:"title"`
				ChannelTitle         string    `json:"channelTitle"`
				PublishedAt          time.Time `json:"publishedAt"`
				LiveBroadcastContent string    `json:"liveBroadcastContent"`
			} `json:"snippet"`
			Statistics struct {
				ViewCount    string `json:"viewCount"`
				CommentCount string `json:"commentCount"`
			} `json:"statistics"`
			LiveStreamingDetails struct {
				ActiveLiveChatID  string `json:"activeLiveChatId"`
				ConcurrentViewers string `json:"concurrentViewers"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	params := url.Values{"part": {"snippet,statistics,liveStreamingDetails"}, "id": {joinComma(ids)}}
	if err := y.get("videos", params, 1, &r); err != nil {
		return nil, err
	}
	out := make([]Video, 0, len(r.Items))
	for _, it := range r.Items {
		out = append(out, Video{
			ID:          it.ID,
			Title:       it.Snippet.Title,
			Channel:     it.Snippet.ChannelTitle,
			PublishedAt: it.Snippet.PublishedAt,
			Views:       atoi(it.Statistics.ViewCount),
			Comments:    atoi(it.Statistics.CommentCount),
			LiveStatus:  it.Snippet.LiveBroadcastContent,
			LiveChatID:  it.LiveStreamingDetails.ActiveLiveChatID,
			Concurrent:  atoi(it.LiveStreamingDetails.ConcurrentViewers),
		})
	}
	return out, nil
}

type Message struct {
	ID      string
	Source  string // "comentario" o "chat"
	VideoID string
	Author  string
	Text    string
	At      time.Time
	Likes   int64
	Replies int64
	Program string // en el chat, programa al aire cuando llegó el mensaje
}

// TagProgram anota en cada mensaje de chat el programa según el título del vivo.
// En un vivo de 24 horas el título cambia con cada programa, así que hay que
// guardarlo al recibir el mensaje y no buscarlo después.
func TagProgram(msgs []Message, title, channel string) {
	p := ProgramName(title, channel)
	for i := range msgs {
		msgs[i].Program = p
	}
}

// ProgramName saca del título del vivo lo que no es el programa. Cada canal
// lo arma distinto: Radio Rivadavia pone el canal adelante
// ("🔴RADIO RIVADAVIA en VIVO | CRISTINA SIN VUELTAS"), Rock & Pop, atrás
// ("🔴 [EN VIVO] QUIÉN PAGA LA FIESTA | ROCK & POP") y Luzu TV pone solo la
// marca de vivo atrás ("SE FUE LARGA con FERBO | EN VIVO").
func ProgramName(title, channel string) string {
	p, _ := splitTitle(title, channel)
	return p
}

// LiveLabel es el nombre corto del vivo para los botones del dashboard:
// "RADIO RIVADAVIA en VIVO", "ROCK & POP en VIVO".
func LiveLabel(title, channel string) string {
	if _, ch := splitTitle(title, channel); ch != "" {
		if !strings.Contains(strings.ToLower(ch), "vivo") {
			ch += " en VIVO"
		}
		return ch
	}
	if channel != "" {
		return channel + " en VIVO"
	}
	return ProgramName(title, channel)
}

// splitTitle separa el título del vivo en programa y la parte con el nombre del canal.
func splitTitle(title, channel string) (program, chanPart string) {
	parts := strings.Split(title, "|")
	if n := len(parts); n > 1 && strings.EqualFold(trimLiveMarks(parts[n-1]), "en vivo") {
		parts = parts[:n-1]
	}
	if n := len(parts); n > 1 && sameName(parts[n-1], channel) {
		chanPart, parts = parts[n-1], parts[:n-1]
	} else if len(parts) > 1 && strings.Contains(strings.ToLower(parts[0]), "vivo") {
		chanPart, parts = parts[0], parts[1:]
	}
	program = trimLiveMarks(strings.Join(parts, "|"))
	if program == "" {
		program = strings.TrimSpace(title)
	}
	return program, trimLiveMarks(chanPart)
}

// trimLiveMarks saca marcas como "🔴" o "[EN VIVO]" del principio.
func trimLiveMarks(s string) string {
	s = strings.TrimSpace(s)
	for _, mark := range []string{"🔴", "[en vivo]", "(en vivo)"} {
		if len(s) >= len(mark) && strings.EqualFold(s[:len(mark)], mark) {
			s = strings.TrimSpace(s[len(mark):])
		}
	}
	return s
}

// sameName compara nombres sin mayúsculas, espacios ni signos ("ROCK & POP" = "Rock And Pop").
func sameName(a, b string) bool {
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, strings.ReplaceAll(strings.ToLower(s), "&", "and"))
	}
	a, b = norm(a), norm(b)
	return a != "" && b != "" && (strings.Contains(a, b) || strings.Contains(b, a))
}

// ParseChannel acepta un ID de canal, un @handle o un link de canal
// (/channel/UC..., /@handle) y devuelve el ID o el @handle, o "" si no lo reconoce.
func ParseChannel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "UC") && !strings.Contains(s, "/") || strings.HasPrefix(s, "@") && !strings.Contains(s, "/") {
		return s
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || !strings.Contains(u.Host, "youtube.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.HasPrefix(parts[0], "@"):
		return parts[0]
	case parts[0] == "channel" && len(parts) > 1 && strings.HasPrefix(parts[1], "UC"):
		return parts[1]
	}
	return ""
}

// ParseVideoID acepta un ID o un link de YouTube (watch?v=, youtu.be/, /live/,
// /shorts/) y devuelve el ID del video, o "" si no lo reconoce.
func ParseVideoID(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") && !strings.Contains(s, "?") {
		return s
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if v := u.Query().Get("v"); v != "" {
		return v
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case strings.HasSuffix(u.Host, "youtu.be") && len(parts) == 1:
		return parts[0]
	case len(parts) == 2 && (parts[0] == "live" || parts[0] == "shorts" || parts[0] == "embed"):
		return parts[1]
	}
	return ""
}

// Comments trae los comentarios principales de un video publicados desde "since".
// Cada página cuesta 1 unidad; se corta en maxPages.
func (y *YouTube) Comments(videoID string, since time.Time, maxPages int) ([]Message, error) {
	var out []Message
	pageToken := ""
	for page := 0; page < maxPages; page++ {
		var r struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				ID      string `json:"id"`
				Snippet struct {
					TotalReplyCount int64 `json:"totalReplyCount"`
					TopLevelComment struct {
						Snippet struct {
							AuthorDisplayName string    `json:"authorDisplayName"`
							TextOriginal      string    `json:"textOriginal"`
							PublishedAt       time.Time `json:"publishedAt"`
							LikeCount         int64     `json:"likeCount"`
						} `json:"snippet"`
					} `json:"topLevelComment"`
				} `json:"snippet"`
			} `json:"items"`
		}
		params := url.Values{
			"part": {"snippet"}, "videoId": {videoID}, "maxResults": {"100"},
			"order": {"time"}, "textFormat": {"plainText"},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		if err := y.get("commentThreads", params, 1, &r); err != nil {
			return out, err
		}
		older := false
		for _, it := range r.Items {
			c := it.Snippet.TopLevelComment.Snippet
			if c.PublishedAt.Before(since) {
				older = true
				continue
			}
			out = append(out, Message{
				ID: it.ID, Source: "comentario", VideoID: videoID, Author: c.AuthorDisplayName,
				Text: c.TextOriginal, At: c.PublishedAt, Likes: c.LikeCount, Replies: it.Snippet.TotalReplyCount,
			})
		}
		// Vienen ordenados del más nuevo al más viejo: si ya aparecieron viejos, cortamos.
		if older || r.NextPageToken == "" {
			break
		}
		pageToken = r.NextPageToken
	}
	return out, nil
}

// chatPage es una respuesta de liveChat/messages: la misma en la lectura
// común y en cada pedazo del streaming.
type chatPage struct {
	NextPageToken         string `json:"nextPageToken"`
	PollingIntervalMillis int    `json:"pollingIntervalMillis"`
	Items                 []struct {
		ID      string `json:"id"`
		Snippet struct {
			DisplayMessage string    `json:"displayMessage"`
			PublishedAt    time.Time `json:"publishedAt"`
		} `json:"snippet"`
		AuthorDetails struct {
			DisplayName string `json:"displayName"`
		} `json:"authorDetails"`
	} `json:"items"`
	Error struct { // en el streaming, los errores llegan dentro de la respuesta
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r chatPage) messages(videoID string) []Message {
	var out []Message
	for _, it := range r.Items {
		if it.Snippet.DisplayMessage == "" {
			continue
		}
		out = append(out, Message{
			ID: it.ID, Source: "chat", VideoID: videoID, Author: it.AuthorDetails.DisplayName,
			Text: it.Snippet.DisplayMessage, At: it.Snippet.PublishedAt,
		})
	}
	return out
}

func chatParams(chatID, pageToken string) url.Values {
	params := url.Values{"liveChatId": {chatID}, "part": {"snippet,authorDetails"}, "maxResults": {"2000"}}
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}
	return params
}

// LiveChatPage hace una lectura del chat en vivo (5 unidades) y devuelve los
// mensajes nuevos, el token para la próxima lectura y cuánto esperar.
func (y *YouTube) LiveChatPage(videoID, chatID, pageToken string) ([]Message, string, time.Duration, error) {
	var r chatPage
	if err := y.get("liveChat/messages", chatParams(chatID, pageToken), 5, &r); err != nil {
		return nil, pageToken, 0, err
	}
	return r.messages(videoID), r.NextPageToken, time.Duration(r.PollingIntervalMillis) * time.Millisecond, nil
}

// ChatBatch es lo que va llegando por el streaming del chat.
type ChatBatch struct {
	Units int // cuota gastada: 5 al abrir la conexión, 0 en el resto
	Msgs  []Message
	Token string // para retomar desde acá si se corta
}

// Si el streaming no manda nada en este tiempo, se corta y se vuelve a abrir
// (por si la conexión quedó colgada sin avisar).
var streamIdle = 5 * time.Minute

// StreamChat abre el streaming del chat (liveChat/messages/stream): una sola
// conexión que cuesta 5 unidades y por la que YouTube manda cada mensaje apenas
// se publica. Llama a got con cada pedazo hasta que la conexión se termina o se
// cancela ctx. No toca y.Units: corre en su propia goroutine y la cuota la
// cuenta quien recibe los ChatBatch.
func (y *YouTube) StreamChat(ctx context.Context, videoID, chatID, pageToken string, got func(ChatBatch)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := time.AfterFunc(streamIdle, cancel)
	defer idle.Stop()

	params := chatParams(chatID, pageToken)
	params.Set("key", y.Key)
	req, err := http.NewRequestWithContext(ctx, "GET", y.BaseURL+"/liveChat/messages/stream?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	client := y.Stream
	if client == nil {
		client = http.DefaultClient
	}
	const path = "liveChat/messages/stream"
	resp, err := client.Do(req)
	if ctx.Err() != nil {
		return nil // la cortamos nosotros: terminó el vivo o quedó colgada
	}
	if err != nil {
		return fmt.Errorf("%s: %s", path, strings.ReplaceAll(err.Error(), y.Key, "***"))
	}
	defer resp.Body.Close()
	got(ChatBatch{Units: 5})
	if resp.StatusCode != http.StatusOK {
		// El error puede venir solo o dentro de un arreglo: [{"error": ...}].
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct{ Error struct{ Message string } }
		var es []struct{ Error struct{ Message string } }
		if json.Unmarshal(b, &es) == nil && len(es) > 0 {
			e = es[0]
		} else {
			_ = json.Unmarshal(b, &e)
		}
		return fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, e.Error.Message)
	}
	// La respuesta es un arreglo JSON que se va mandando de a un elemento.
	dec := json.NewDecoder(resp.Body)
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return fmt.Errorf("%s: respuesta inesperada", path)
	}
	for dec.More() {
		var r chatPage
		if err := dec.Decode(&r); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%s: %s", path, strings.ReplaceAll(err.Error(), y.Key, "***"))
		}
		if r.Error.Message != "" || r.Error.Code != 0 {
			return fmt.Errorf("%s: HTTP %d: %s", path, r.Error.Code, r.Error.Message)
		}
		idle.Reset(streamIdle)
		got(ChatBatch{Msgs: r.messages(videoID), Token: r.NextPageToken})
	}
	return nil
}

// Intervalo mínimo entre lecturas del chat en el modo informe (los tests lo achican).
var minChatGap = 10 * time.Second

// LiveChats lee por turnos el chat de varios vivos durante "duration" y anota el
// programa en cada mensaje. Cada lectura cuesta 5 unidades y entre una y otra
// pasan al menos 10 s en total: con más vivos, cada uno se lee menos seguido y
// el gasto no cambia. Se corta en maxCalls. Si un chat falla, sigue con los demás.
func (y *YouTube) LiveChats(lives []Video, duration time.Duration, maxCalls int) ([]Message, error) {
	var (
		out    []Message
		errs   []error
		tokens = map[string]string{}
		active = append([]Video(nil), lives...)
	)
	deadline := time.Now().Add(duration)
	for i, call := 0, 0; call < maxCalls && len(active) > 0 && time.Now().Before(deadline); call++ {
		i %= len(active)
		v := active[i]
		msgs, next, wait, err := y.LiveChatPage(v.ID, v.LiveChatID, tokens[v.ID])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", v.Title, err))
			active = append(active[:i], active[i+1:]...)
			continue
		}
		TagProgram(msgs, v.Title, v.Channel)
		out = append(out, msgs...)
		tokens[v.ID] = next
		i++
		wait = max(wait, minChatGap)
		if time.Now().Add(wait).After(deadline) {
			break
		}
		time.Sleep(wait)
	}
	return out, errors.Join(errs...)
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func atoi(s string) int64 {
	var n int64
	fmt.Sscan(s, &n)
	return n
}
