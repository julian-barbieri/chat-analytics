package main

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Handlers de la pantalla Comentarios. Leen solo copias del Store
// (Comments, VideoArchive, Status) y nunca llaman a YouTube.

func (s *Server) commentsPage(w http.ResponseWriter, r *http.Request) {
	data := s.commentsData(r.URL.Query())
	data["Tab"], data["Channel"] = "comentarios", s.st.Status().Channel
	s.render(w, "comentarios.html", data)
}

// commentsPartial reemplaza la pantalla entera. Si lo pidió el formulario del
// detalle, la URL del navegador pasa a la de la pantalla.
func (s *Server) commentsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.commentsData(r.URL.Query())
	if r.Header.Get("HX-Trigger") == "cfiltro" {
		w.Header().Set("HX-Push-Url", data["Q"].(CommentsQuery).Page())
	}
	s.render(w, "comentarios-cuerpo", data)
}

// commentVideos junta el archivo de videos con la lista de la última
// revisión (la más fresca), por si el archivo todavía no existe.
func commentVideos(archive []VideoRecord, current []Video, now time.Time) []VideoRecord {
	byID := map[string]int{}
	out := append([]VideoRecord(nil), archive...)
	for i, v := range out {
		byID[v.ID] = i
	}
	for _, v := range current {
		r := videoRecord(v, now)
		if i, ok := byID[v.ID]; ok {
			out[i] = r
		} else {
			byID[v.ID] = len(out)
			out = append(out, r)
		}
	}
	return out
}

func (s *Server) commentsData(v url.Values) map[string]any {
	now := s.clock()
	snap := s.st.Status()
	q := commentsQuery(v)
	known := false
	for _, c := range snap.Channels {
		known = known || c.ID == q.Canal
	}
	if !known {
		q.Canal = ""
	}
	vids := commentVideos(s.st.VideoArchive(), snap.Videos, now)
	msgs := s.st.Comments(now.Add(-retention))
	data := map[string]any{"Q": q, "Empty": len(msgs) == 0, "IsDetail": q.Video != ""}
	if q.Video != "" {
		d, ok := CommentsVideo(msgs, vids, q, now)
		data["Detail"], data["Found"] = d, ok
		data["Tiles"] = []Tile{
			{"Comentarios", miles(d.Total), "de " + miles(d.Video.Comments) + " según YouTube"},
			{"Personas", miles(d.People), "distintas"},
			{"Preguntas", miles(d.Questions), plural(d.Open, "sin respuesta", "sin respuestas")},
			{"Quejas", pct(d.ComplaintPct), "de los comentarios"},
			{"Likes en comentarios", miles(d.Likes), "sumados"},
		}
		data["Curve"] = curveChart(d.Curve)
		data["CatOpts"], data["VOrders"] = catOptions(), videoOrders
		return data
	}
	view := CommentsSummary(msgs, vids, q, now)
	data["View"] = view
	chans := []Choice{}
	all := q
	all.Canal = ""
	chans = append(chans, Choice{Name: "Todos", Page: all.Page(), Partial: all.Partial(), On: q.Canal == ""})
	for _, c := range snap.Channels {
		n := q
		n.Canal = c.ID
		name := c.Name
		if name == "" {
			name = c.ID
		}
		chans = append(chans, Choice{Name: name, Page: n.Page(), Partial: n.Partial(), On: q.Canal == c.ID})
	}
	data["Channels"] = chans
	data["Periods"] = choices(commentPeriods, q.Periodo, q, func(n *CommentsQuery, v string) { n.Periodo = v })
	data["Types"] = choices(commentTypes, q.Tipo, q, func(n *CommentsQuery, v string) { n.Tipo = v })
	data["Orders"] = choices(commentOrders, q.Orden, q, func(n *CommentsQuery, v string) { n.Orden = v })
	data["Dests"] = choices(commentDests, q.Dest, q, func(n *CommentsQuery, v string) { n.Dest = v })
	period := map[string]string{"7d": "en los últimos 7 días", "30d": "en los últimos 30 días", "pub": "de videos publicados en 30 días"}[q.Periodo]
	data["Period"] = period
	data["Tiles"] = []Tile{
		{"Comentarios", miles(view.Total), period},
		{"Personas distintas", miles(view.People), "que comentaron"},
		{"Videos con comentarios", miles(view.WithComments) + " de " + miles(view.InScope), "en la tabla de abajo"},
		{"Preguntas sin respuesta", miles(view.OpenQuestions), "al momento de guardarlas"},
		{"Quejas", pct(view.ComplaintPct), "de los comentarios"},
	}
	data["Chart"] = dayChart(view.Days)
	return data
}

// catOptions son las categorías para el filtro del detalle.
func catOptions() []option {
	out := []option{{"", "Todas"}}
	for _, c := range categories {
		out = append(out, option{c, catLabels[c]})
	}
	return out
}

// CommentChart es un gráfico de barras armado en Go (SVG estirado y textos
// en HTML, como el de participación).
type CommentChart struct {
	Bars  []Bar
	W     int
	Max   int
	Ticks []Tick
	Pubs  []PubMark
	Aria  string
}

// PubMark marca un día con videos publicados.
type PubMark struct {
	Left  float64
	Title string
}

func barChart(ns []int, labels []string, classes []string) CommentChart {
	c := CommentChart{W: len(ns) * 10, Max: 1}
	for _, n := range ns {
		c.Max = max(c.Max, n)
	}
	for i, n := range ns {
		h := float64(n) * 100 / float64(c.Max)
		c.Bars = append(c.Bars, Bar{X: float64(i*10 + 1), W: 8, Y: 100 - h, H: h, N: n, Class: classes[i],
			Label: labels[i] + ": " + plural(n, "comentario", "comentarios")})
	}
	return c
}

// dayChart es "Comentarios por día", con los fines de semana en gris y una
// marca en los días con videos publicados.
func dayChart(days []CommentDay) CommentChart {
	ns, labels, classes := make([]int, len(days)), make([]string, len(days)), make([]string, len(days))
	for i, d := range days {
		ns[i], labels[i] = d.N, commentDayLabel(d.Day)
		if d.Weekend {
			classes[i] = "finde"
		}
	}
	c := barChart(ns, labels, classes)
	// Lo que se publicó cada día va también en el <title> de su barra: el ▲ de
	// arriba es aria-hidden y su title solo se ve con el mouse.
	for i, d := range days {
		if len(d.Pub) > 0 {
			c.Bars[i].Label += " · publicado: " + strings.Join(d.Pub, " · ")
		}
	}
	step := max(1, len(days)/5)
	for i, d := range days {
		left := (float64(i) + 0.5) * 100 / float64(len(days))
		if len(d.Pub) > 0 {
			c.Pubs = append(c.Pubs, PubMark{Left: left, Title: commentDayLabel(d.Day) + ": " + strings.Join(d.Pub, " · ")})
		}
		if i%step == 0 || i == len(days)-1 {
			label := commentDayLabel(d.Day)
			if i == len(days)-1 {
				label = "Hoy"
			}
			c.Ticks = append(c.Ticks, Tick{Left: left, Label: label})
		}
	}
	// Si el anteúltimo rótulo queda pegado a "Hoy", se saca.
	if n := len(c.Ticks); n > 2 && c.Ticks[n-1].Left-c.Ticks[n-2].Left < 8 {
		c.Ticks = append(c.Ticks[:n-2], c.Ticks[n-1])
	}
	total := 0
	for _, n := range ns {
		total += n
	}
	c.Aria = fmt.Sprintf("Comentarios por día en %d días: %s en total, el día con más %s", len(days), miles(total), miles(c.Max))
	return c
}

// curveChart es la curva desde la publicación; cada barra lleva su tramo abajo.
func curveChart(b []CommentBucket) CommentChart {
	ns, labels, classes := make([]int, len(b)), make([]string, len(b)), make([]string, len(b))
	for i, x := range b {
		ns[i], labels[i] = x.N, x.Label
	}
	c := barChart(ns, labels, classes)
	for i, x := range b {
		c.Ticks = append(c.Ticks, Tick{Left: (float64(i) + 0.5) * 100 / float64(len(b)), Label: x.Label})
	}
	c.Aria = "Comentarios según cuánto después de la publicación llegaron"
	return c
}

// exportVideo es GET /export.csv?video=ID: los comentarios de un video.
func (s *Server) exportVideo(w http.ResponseWriter, id string) {
	if !reVideoID.MatchString(id) {
		http.Error(w, "Video inválido.", http.StatusBadRequest)
		return
	}
	now := s.clock()
	titles := map[string]string{}
	for _, v := range commentVideos(s.st.VideoArchive(), s.st.Status().Videos, now) {
		titles[v.ID] = v.Title
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="comentarios_`+id+`_`+now.Local().Format("2006-01-02_1504")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write(csvHeader)
	for _, m := range s.st.Comments(now.Add(-retention)) {
		if m.VideoID == id {
			cw.Write(csvRow(m, titles))
		}
	}
	cw.Flush()
}
