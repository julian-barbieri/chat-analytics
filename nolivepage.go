package main

import (
	"net/http"
	"net/url"
	"time"
)

// Handlers de la pantalla Sin vivo. Leen solo copias del Store (Status y
// Lives) y nunca llaman a YouTube: no gastan cuota.

// noLivePeriods son los períodos de la pantalla: lista cerrada.
var noLivePeriods = []option{{"7d", "Últimos 7 días"}, {"30d", "Últimos 30 días"}}

// noLiveShown es cuántos cortes se listan como mucho: 30 días de un canal
// con muchos cortes harían una tabla enorme en cada refresco.
const noLiveShown = 50

// noLiveQuery es el canal y el período elegidos, ya validados.
type noLiveQuery struct{ Canal, Periodo string }

func (q noLiveQuery) values() string {
	return url.Values{"canal": {q.Canal}, "periodo": {q.Periodo}}.Encode()
}

// Page es la URL de la pantalla; Partial, la del parcial que la reemplaza.
func (q noLiveQuery) Page() string    { return "/sin-vivo?" + q.values() }
func (q noLiveQuery) Partial() string { return "/parcial/sin-vivo?" + q.values() }

func (s *Server) noLivePage(w http.ResponseWriter, r *http.Request) {
	data := s.noLiveData(r.URL.Query())
	data["Tab"], data["Channel"] = "sinvivo", s.st.Status().Channel
	s.render(w, "sinvivo.html", data)
}

// noLivePartial reemplaza la pantalla entera (los filtros llevan su
// hx-push-url, como en Comentarios).
func (s *Server) noLivePartial(w http.ResponseWriter, r *http.Request) {
	s.render(w, "sinvivo-cuerpo", s.noLiveData(r.URL.Query()))
}

func (s *Server) noLiveData(v url.Values) map[string]any {
	now := s.clock()
	snap := s.st.Status()
	q := noLiveQuery{Periodo: pickOption(v.Get("periodo"), noLivePeriods)}
	var ch ChannelStatus
	for i, c := range snap.Channels {
		if c.ID != "" && (c.ID == v.Get("canal") || i == 0) {
			ch = c
		}
	}
	q.Canal = ch.ID
	data := map[string]any{"Q": q, "NoChannels": ch.ID == ""}
	var chans []Choice
	for _, c := range snap.Channels {
		if c.ID == "" {
			continue
		}
		n := q
		n.Canal = c.ID
		chans = append(chans, Choice{Name: channelName(c), Page: n.Page(), Partial: n.Partial(), On: c.ID == q.Canal})
	}
	var periods []Choice
	for _, o := range noLivePeriods {
		n := q
		n.Periodo = o.ID
		periods = append(periods, Choice{Name: o.Name, Page: n.Page(), Partial: n.Partial(), On: o.ID == q.Periodo})
	}
	data["Channels"], data["Periods"] = chans, periods
	if ch.ID == "" {
		return data
	}
	_, days := dayRange(q.Periodo, now)
	live := map[string]bool{}
	for _, vid := range snap.Videos {
		if vid.ChannelID == ch.ID && vid.LiveStatus == "live" {
			live[vid.ID] = true
		}
	}
	view := NoLiveCuts(s.st.Lives(days[len(days)-1]), ch.ID, live, days, now)
	if len(view.Cuts) > noLiveShown {
		data["More"] = len(view.Cuts) - noLiveShown
		view.Cuts = view.Cuts[:noLiveShown]
	}
	in := "en los últimos " + map[string]string{"7d": "7 días", "30d": "30 días"}[q.Periodo]
	longest := "—"
	if view.Longest > 0 {
		longest = durText(view.Longest)
	}
	onAir := "—"
	if view.HasData {
		onAir = pct(view.OnAirPct)
	}
	data["View"], data["Name"], data["In"] = view, channelName(ch), in
	data["Tiles"] = []Tile{
		{"Cortes hoy", miles(view.CutsToday), "ratos sin vivo de hasta 2 h", ""},
		{"Sin vivo hoy", durText(time.Duration(view.MinToday) * time.Minute), "sumando los cortes de hoy", ""},
		{"Al aire", onAir, in + ", sin los ratos de más de 2 h", ""},
		{"Corte más largo", longest, in, ""},
	}
	return data
}
