package main

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pantalla Comentarios: los comentarios de los videos de los canales, solo
// con reglas (Classify) y funciones puras sobre copias del Store.

// VideoRecord es el último dato visto de un video (datos.videos.jsonl). Sirve
// para mostrar título, canal y vistas de los videos que ya salieron de la
// lista de la revisión, mientras sus comentarios sigan guardados.
type VideoRecord struct {
	Seen               time.Time // la última revisión que lo trajo: la retención se cuenta desde acá
	ID, Title          string
	ChannelID, Channel string
	PublishedAt        time.Time
	Views, Comments    int64
	Live               bool // fue (o es) un vivo
}

// videoRecord es el dato de un video de una revisión. Live: fue o es un vivo
// (está en curso, programado o tiene inicio real).
func videoRecord(v Video, at time.Time) VideoRecord {
	return VideoRecord{Seen: at, ID: v.ID, Title: v.Title, ChannelID: v.ChannelID, Channel: v.Channel, PublishedAt: v.PublishedAt,
		Views: v.Views, Comments: v.Comments, Live: v.LiveStatus != "none" || !v.StartedAt.IsZero()}
}

// commentsPageSize: comentarios por página en el detalle de un video.
const commentsPageSize = 50

// option es un valor de una lista cerrada de parámetros y su texto.
type option struct{ ID, Name string }

// Listas cerradas de la pantalla; el primero de cada una es el valor por defecto.
var (
	commentPeriods = []option{{"30d", "30 días"}, {"7d", "7 días"}, {"pub", "Desde la publicación"}}
	commentTypes   = []option{{"grabados", "Grabados"}, {"vivos", "Vivos"}, {"todos", "Todos"}}
	commentOrders  = []option{{"periodo", "Comentarios en el período"}, {"ratio", "Comentarios cada 1.000 vistas"}, {"quejas", "% de quejas"}, {"fecha", "Publicación"}}
	commentDests   = []option{{"likes", "Más likes"}, {"respuestas", "Más respuestas"}, {"quejas", "Quejas"}}
	videoOrders    = []option{{"likes", "Más likes"}, {"respuestas", "Más respuestas"}, {"recientes", "Más recientes"}, {"viejos", "Más viejos"}}
)

// Los IDs de video de YouTube son de 11 caracteres de este alfabeto; se
// acepta un poco más por las dudas, nunca comillas ni espacios.
var reVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func pickOption(v string, opts []option) string {
	for _, o := range opts {
		if o.ID == v {
			return v
		}
	}
	return opts[0].ID
}

// CommentsQuery son los parámetros de la pantalla, todos de listas cerradas
// salvo buscar (texto de la persona, cortado y escapado por html/template).
// El canal se valida contra los canales configurados en el servidor.
type CommentsQuery struct {
	Canal, Periodo, Tipo, Orden, Dest string
	Video, Buscar, Cat, VOrden        string
	SinResp                           bool
	Pag                               int
}

// commentsQuery lee los parámetros con sus listas cerradas.
func commentsQuery(v url.Values) CommentsQuery {
	q := CommentsQuery{
		Canal:   v.Get("canal"),
		Periodo: pickOption(v.Get("periodo"), commentPeriods),
		Tipo:    pickOption(v.Get("tipo"), commentTypes),
		Orden:   pickOption(v.Get("orden"), commentOrders),
		Dest:    pickOption(v.Get("dest"), commentDests),
		VOrden:  pickOption(v.Get("vorden"), videoOrders),
		SinResp: v.Get("sinresp") == "1",
		Pag:     1,
	}
	if id := v.Get("video"); reVideoID.MatchString(id) {
		q.Video = id
	}
	for _, c := range categories {
		if v.Get("cat") == c {
			q.Cat = c
		}
	}
	if r := []rune(strings.TrimSpace(v.Get("buscar"))); len(r) > 100 {
		q.Buscar = string(r[:100])
	} else {
		q.Buscar = string(r)
	}
	if p, err := strconv.Atoi(v.Get("pag")); err == nil && p > 1 {
		q.Pag = p
	}
	return q
}

// values arma la URL con lo que no es por defecto, para que los links sean cortos.
func (q CommentsQuery) values() url.Values {
	v := url.Values{}
	set := func(k, val, def string) {
		if val != "" && val != def {
			v.Set(k, val)
		}
	}
	set("canal", q.Canal, "")
	set("periodo", q.Periodo, commentPeriods[0].ID)
	set("tipo", q.Tipo, commentTypes[0].ID)
	set("orden", q.Orden, commentOrders[0].ID)
	set("dest", q.Dest, commentDests[0].ID)
	set("video", q.Video, "")
	if q.Video != "" {
		set("buscar", q.Buscar, "")
		set("cat", q.Cat, "")
		set("vorden", q.VOrden, videoOrders[0].ID)
		if q.SinResp {
			v.Set("sinresp", "1")
		}
		if q.Pag > 1 {
			v.Set("pag", strconv.Itoa(q.Pag))
		}
	}
	return v
}

func (q CommentsQuery) with(path string) string {
	if e := q.values().Encode(); e != "" {
		return path + "?" + e
	}
	return path
}

// Page es la URL de la pantalla; Partial la del parcial que la reemplaza.
func (q CommentsQuery) Page() string    { return q.with("/comentarios") }
func (q CommentsQuery) Partial() string { return q.with("/parcial/comentarios") }

// ToVideo, Back y ToPage son los links del detalle: abrir un video, volver
// al resumen con los mismos filtros e ir a otra página de la lista.
func (q CommentsQuery) ToVideo(id string) CommentsQuery {
	n := q
	n.Video, n.Buscar, n.Cat, n.VOrden, n.SinResp, n.Pag = id, "", "", videoOrders[0].ID, false, 1
	return n
}

func (q CommentsQuery) Back() CommentsQuery { return q.ToVideo("") }

func (q CommentsQuery) ToPage(p int) CommentsQuery {
	q.Pag = p
	return q
}

// Choice es un botón de filtro: un link a la pantalla con ese valor.
type Choice struct {
	Name, Page, Partial string
	On                  bool
}

// choices arma los botones de una lista cerrada cambiando un campo de q.
func choices(opts []option, cur string, q CommentsQuery, set func(*CommentsQuery, string)) []Choice {
	var out []Choice
	for _, o := range opts {
		n := q
		n.Pag = 1
		set(&n, o.ID)
		out = append(out, Choice{Name: o.Name, Page: n.Page(), Partial: n.Partial(), On: o.ID == cur})
	}
	return out
}

// CommentsView es la pantalla de resumen.
type CommentsView struct {
	Q                                    CommentsQuery
	Total, People, WithComments, InScope int
	OpenQuestions                        int
	ComplaintPct                         float64
	Days                                 []CommentDay
	Cats                                 []CatRow
	CatTotal                             int
	Words                                []Count
	Authors                              []AuthorRow
	Rows                                 []CommentVideoRow
	Questions, Featured                  []CommentItem
}

// CommentDay es una barra de "Comentarios por día".
type CommentDay struct {
	Day     time.Time
	N       int
	Pub     []string // títulos de los videos publicados ese día
	Weekend bool
}

// AuthorRow es una persona de "Quiénes más comentan".
type AuthorRow struct {
	Name      string
	N, Videos int
}

// CommentVideoRow es una fila de la tabla de videos.
type CommentVideoRow struct {
	ID, Title, Channel string
	Published          time.Time
	Views, Total       int64 // Total: el contador de YouTube
	Stored, InPeriod   int   // guardados en total y dentro del período
	Questions          int
	Per1000            string
	ratio              float64
	ComplaintPct       float64
}

// CommentItem es un comentario con su categoría y su video, para las listas.
type CommentItem struct {
	Message
	Cat, VideoTitle string
}

// commentScope son los videos de un canal y tipo, con lo que se sabe de cada uno.
type commentScope struct {
	recs map[string]VideoRecord
	q    CommentsQuery
}

func newScope(vids []VideoRecord, q CommentsQuery) commentScope {
	recs := map[string]VideoRecord{}
	for _, v := range vids {
		recs[v.ID] = v
	}
	return commentScope{recs: recs, q: q}
}

// record es el video de un comentario. Si no está en el archivo (por ejemplo,
// de antes de que existiera), se lo trata como grabado de canal desconocido.
func (s commentScope) record(id string) VideoRecord {
	if r, ok := s.recs[id]; ok {
		return r
	}
	return VideoRecord{ID: id, Title: id}
}

func (s commentScope) in(r VideoRecord) bool {
	if s.q.Canal != "" && r.ChannelID != s.q.Canal {
		return false
	}
	switch s.q.Tipo {
	case "vivos":
		return r.Live
	case "todos":
		return true
	}
	return !r.Live
}

func (s commentScope) item(m Message, cat string) CommentItem {
	return CommentItem{Message: m, Cat: cat, VideoTitle: s.record(m.VideoID).Title}
}

// commentWindow es el primer día de las barras y cuántos días son.
func commentWindow(periodo string, now time.Time) (time.Time, int) {
	n := 30
	if periodo == "7d" {
		n = 7
	}
	return dayStart(now).AddDate(0, 0, 1-n), n
}

// withDefaults completa una consulta armada a mano (los tests) como commentsQuery.
func withDefaults(q CommentsQuery) CommentsQuery {
	q.Periodo = pickOption(q.Periodo, commentPeriods)
	q.Tipo = pickOption(q.Tipo, commentTypes)
	q.Orden = pickOption(q.Orden, commentOrders)
	q.Dest = pickOption(q.Dest, commentDests)
	q.VOrden = pickOption(q.VOrden, videoOrders)
	q.Pag = max(q.Pag, 1)
	return q
}

// CommentsSummary arma la pantalla de resumen. Con "desde la publicación"
// cuentan todos los comentarios guardados de los videos publicados en los
// últimos 30 días; si no, los comentarios del período.
func CommentsSummary(msgs []Message, vids []VideoRecord, q CommentsQuery, now time.Time) CommentsView {
	q = withDefaults(q)
	sc := newScope(vids, q)
	since, n := commentWindow(q.Periodo, now)
	v := CommentsView{Q: q}

	var period []Message
	stored := map[string]int{}
	for _, m := range msgs {
		if m.Source != "comentario" {
			continue
		}
		r := sc.record(m.VideoID)
		if !sc.in(r) {
			continue
		}
		stored[m.VideoID]++
		if q.Periodo == "pub" && !r.PublishedAt.Before(since) || q.Periodo != "pub" && !m.At.Before(since) {
			period = append(period, m)
		}
	}

	people, videos := map[string]bool{}, map[string]bool{}
	byVideo := map[string][]Message{}
	authors := map[string]*AuthorRow{}
	authorVideos := map[string]map[string]bool{}
	complaints := 0
	for _, m := range period {
		cat := Classify(m.Text)
		people[m.Author], videos[m.VideoID] = true, true
		byVideo[m.VideoID] = append(byVideo[m.VideoID], m)
		if cat == CatPregunta && m.Replies == 0 {
			v.OpenQuestions++
			v.Questions = append(v.Questions, sc.item(m, cat))
		}
		if cat == CatQueja {
			complaints++
		}
		if q.Dest != "quejas" || cat == CatQueja {
			v.Featured = append(v.Featured, sc.item(m, cat))
		}
		a := authors[m.Author]
		if a == nil {
			a, authorVideos[m.Author] = &AuthorRow{Name: m.Author}, map[string]bool{}
			authors[m.Author] = a
		}
		a.N++
		authorVideos[m.Author][m.VideoID] = true
	}
	v.Total, v.People, v.WithComments = len(period), len(people), len(videos)
	v.ComplaintPct = share(complaints, len(period))

	// Barras por día y marcas de publicación.
	counts := map[time.Time]int{}
	for _, m := range period {
		counts[dayStart(m.At)]++
	}
	pubs := map[time.Time][]string{}
	for _, r := range vids {
		if sc.in(r) && !r.PublishedAt.IsZero() {
			d := dayStart(r.PublishedAt)
			pubs[d] = append(pubs[d], r.Title)
		}
	}
	for i := range n {
		d := since.AddDate(0, 0, i)
		v.Days = append(v.Days, CommentDay{Day: d, N: counts[d], Pub: pubs[d], Weekend: weekend(d)})
	}

	v.Cats, v.CatTotal = catRows(period), len(period)
	v.Words = TopWords(period, 16)
	for name, a := range authors {
		a.Videos = len(authorVideos[name])
		v.Authors = append(v.Authors, *a)
	}
	sort.Slice(v.Authors, func(i, j int) bool {
		if v.Authors[i].N != v.Authors[j].N {
			return v.Authors[i].N > v.Authors[j].N
		}
		return v.Authors[i].Name < v.Authors[j].Name
	})
	v.Authors = v.Authors[:min(len(v.Authors), 8)]

	v.Rows = commentRows(sc, vids, stored, byVideo, since, q)
	v.InScope = len(v.Rows)

	sortItems(v.Questions, "likes")
	v.Questions = v.Questions[:min(len(v.Questions), 10)]
	sortItems(v.Featured, q.Dest)
	v.Featured = v.Featured[:min(len(v.Featured), 5)]
	return v
}

// commentRows es la tabla de videos: los del alcance publicados en la
// ventana o con comentarios guardados.
func commentRows(sc commentScope, vids []VideoRecord, stored map[string]int, byVideo map[string][]Message, since time.Time, q CommentsQuery) []CommentVideoRow {
	var rows []CommentVideoRow
	seen := map[string]bool{}
	add := func(r VideoRecord) {
		seen[r.ID] = true
		row := CommentVideoRow{ID: r.ID, Title: r.Title, Channel: r.Channel, Published: r.PublishedAt, Views: r.Views, Total: r.Comments,
			Stored: stored[r.ID], InPeriod: len(byVideo[r.ID]), Per1000: "—"}
		complaints := 0
		for _, m := range byVideo[r.ID] {
			switch Classify(m.Text) {
			case CatPregunta:
				row.Questions++
			case CatQueja:
				complaints++
			}
		}
		row.ComplaintPct = share(complaints, row.InPeriod)
		// Con el contador de YouTube, no con los guardados: esos dependen de
		// la retención y de cuándo se empezó a mirar el canal.
		if r.Views > 0 {
			n := r.Comments
			if n == 0 {
				n = int64(row.Stored)
			}
			row.ratio = float64(n) * 1000 / float64(r.Views)
			row.Per1000 = decimal(row.ratio)
		}
		rows = append(rows, row)
	}
	for _, r := range vids {
		if sc.in(r) && (stored[r.ID] > 0 || !r.PublishedAt.Before(since)) {
			add(r)
		}
	}
	var unknown []string
	for id := range stored {
		if !seen[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	for _, id := range unknown {
		add(sc.record(id))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch q.Orden {
		case "ratio":
			if a.ratio != b.ratio {
				return a.ratio > b.ratio
			}
		case "quejas":
			if a.ComplaintPct != b.ComplaintPct {
				return a.ComplaintPct > b.ComplaintPct
			}
		case "periodo":
			if a.InPeriod != b.InPeriod {
				return a.InPeriod > b.InPeriod
			}
		}
		if !a.Published.Equal(b.Published) {
			return a.Published.After(b.Published)
		}
		return a.ID < b.ID
	})
	return rows
}

// sortItems ordena comentarios: más likes, más respuestas, más recientes o
// más viejos; a igual valor, el más reciente primero.
func sortItems(items []CommentItem, by string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch by {
		case "likes", "quejas":
			if a.Likes != b.Likes {
				return a.Likes > b.Likes
			}
		case "respuestas":
			if a.Replies != b.Replies {
				return a.Replies > b.Replies
			}
		case "viejos":
			if !a.At.Equal(b.At) {
				return a.At.Before(b.At)
			}
		}
		if !a.At.Equal(b.At) {
			return a.At.After(b.At)
		}
		return a.ID < b.ID
	})
}

// CommentBucket es una barra de la curva desde la publicación.
type CommentBucket struct {
	Label string
	N     int
}

// Tramos de la curva, en horas desde la publicación: más finos al principio,
// que es cuando llega la mayoría.
var curveEdges = []struct {
	to    float64
	label string
}{
	{1, "0–1 h"}, {2, "1–2 h"}, {3, "2–3 h"}, {6, "3–6 h"}, {12, "6–12 h"}, {24, "12–24 h"},
	{48, "1–2 días"}, {72, "2–3 días"}, {168, "3–7 días"}, {336, "7–14 días"}, {1e9, "más de 14 días"},
}

// CommentsDetail es el detalle de un video.
type CommentsDetail struct {
	Q                  CommentsQuery
	Video              VideoRecord
	Total, People      int
	Questions, Open    int
	ComplaintPct       float64
	Likes              int64
	Curve              []CommentBucket
	HalfHours          string // en cuánto llegó la mitad de los comentarios
	ChannelHalf        string // lo mismo, mediana de los otros videos del canal
	Cats               []CatRow
	List               []CommentItem
	Shown, Page, Pages int
	PrevPage, NextPage int
}

// CommentsVideo arma el detalle de un video; false si no tiene comentarios guardados.
func CommentsVideo(msgs []Message, vids []VideoRecord, q CommentsQuery, now time.Time) (CommentsDetail, bool) {
	q = withDefaults(q)
	sc := newScope(vids, q)
	r := sc.record(q.Video)
	byVideo := map[string][]Message{}
	for _, m := range msgs {
		if m.Source == "comentario" {
			byVideo[m.VideoID] = append(byVideo[m.VideoID], m)
		}
	}
	all := byVideo[q.Video]
	if q.Video == "" || len(all) == 0 {
		return CommentsDetail{Q: q}, false
	}
	d := CommentsDetail{Q: q, Video: r, Total: len(all)}
	people := map[string]bool{}
	complaints := 0
	for _, m := range all {
		people[m.Author] = true
		d.Likes += m.Likes
		switch Classify(m.Text) {
		case CatPregunta:
			d.Questions++
			if m.Replies == 0 {
				d.Open++
			}
		case CatQueja:
			complaints++
		}
	}
	d.People, d.ComplaintPct = len(people), share(complaints, len(all))
	d.Cats = catRows(all)

	if !r.PublishedAt.IsZero() {
		for _, e := range curveEdges {
			d.Curve = append(d.Curve, CommentBucket{Label: e.label})
		}
		for _, m := range all {
			h := m.At.Sub(r.PublishedAt).Hours()
			for i, e := range curveEdges {
				if h < e.to {
					d.Curve[i].N++
					break
				}
			}
		}
		if h, ok := halfTime(all, r.PublishedAt); ok {
			d.HalfHours = humanHours(h)
		}
		var halves []float64
		for _, o := range vids {
			if o.ID != r.ID && r.ChannelID != "" && o.ChannelID == r.ChannelID && !o.PublishedAt.IsZero() && len(byVideo[o.ID]) >= 5 {
				if h, ok := halfTime(byVideo[o.ID], o.PublishedAt); ok {
					halves = append(halves, h)
				}
			}
		}
		if len(halves) > 0 {
			sort.Float64s(halves)
			d.ChannelHalf = humanHours(halves[len(halves)/2])
		}
	}

	needle := foldText(q.Buscar)
	var list []CommentItem
	for _, m := range all {
		cat := Classify(m.Text)
		if q.Cat != "" && cat != q.Cat || q.SinResp && m.Replies > 0 || needle != "" && !strings.Contains(foldText(m.Text), needle) {
			continue
		}
		list = append(list, sc.item(m, cat))
	}
	sortItems(list, q.VOrden)
	d.Shown = len(list)
	d.Pages = max(1, (len(list)+commentsPageSize-1)/commentsPageSize)
	d.Page = min(q.Pag, d.Pages)
	d.PrevPage, d.NextPage = d.Page-1, d.Page+1
	from := (d.Page - 1) * commentsPageSize
	d.List = list[from:min(len(list), from+commentsPageSize)]
	return d, true
}

// foldText es el texto para buscar: minúsculas y sin tildes.
func foldText(s string) string { return unaccent.Replace(strings.ToLower(s)) }

// halfTime son las horas desde la publicación hasta que llegó la mitad de los comentarios.
func halfTime(msgs []Message, pub time.Time) (float64, bool) {
	if len(msgs) == 0 {
		return 0, false
	}
	ages := make([]float64, 0, len(msgs))
	for _, m := range msgs {
		ages = append(ages, max(0, m.At.Sub(pub).Hours()))
	}
	sort.Float64s(ages)
	return ages[(len(ages)+1)/2-1], true
}

func humanHours(h float64) string {
	switch {
	case h < 1:
		return "menos de 1 hora"
	case h < 48:
		return plural(int(h), "hora", "horas")
	}
	return plural(int(h/24), "día", "días")
}

// commentDayLabel es el rótulo corto de un día de las barras.
func commentDayLabel(d time.Time) string { return fmt.Sprintf("%02d/%02d", d.Day(), int(d.Month())) }
