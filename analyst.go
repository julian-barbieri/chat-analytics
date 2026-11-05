package main

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"
)

// Son var para que los tests las achiquen, igual que workerTick.
var (
	analystTick       = 30 * time.Second                                   // cada cuánto se buscan programas terminados
	claudeRetryWaits  = []time.Duration{5 * time.Minute, 15 * time.Minute} // 429, 5xx o red: dos reintentos y no más
	claudeManualEvery = 10 * time.Minute                                   // el botón, a lo sumo una vez por canal en este rato
)

// claudeQueue es cuántos pedidos del botón pueden esperar a la vez.
const claudeQueue = 4

// segmentLead: los mensajes de un tramo empiezan 5 min antes de su primera
// muestra, porque el cambio de título se ve hasta 3 min tarde.
const segmentLead = 5 * time.Minute

type analysisReq struct{ Channel, Video string }

// Analyst es la cola del botón "Analizar ahora". Es seguro para uso
// concurrente: el handler solo encola y RunAnalyst es el que llama a Claude,
// así un doble clic no gasta dos veces y el tope se aplica en un solo lugar.
type Analyst struct {
	queue  chan analysisReq
	mu     sync.Mutex
	last   map[string]time.Time // último pedido de cada canal
	queued map[string]bool
}

func NewAnalyst() *Analyst {
	return &Analyst{queue: make(chan analysisReq, claudeQueue), last: map[string]time.Time{}, queued: map[string]bool{}}
}

// Request encola un análisis del programa en curso de un canal sin bloquear.
// Si no entra, dice por qué.
func (a *Analyst) Request(channel, video string, now time.Time) (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.queued[channel] {
		return false, "Ya hay un análisis pedido para este canal"
	}
	if t, ok := a.last[channel]; ok && now.Sub(t) < claudeManualEvery {
		return false, "Ya hay un análisis pedido para este canal: se puede pedir otro a las " + t.Add(claudeManualEvery).Local().Format("15:04")
	}
	select {
	case a.queue <- analysisReq{channel, video}:
		a.queued[channel], a.last[channel] = true, now
		return true, ""
	default:
		return false, "Hay muchos análisis pedidos: probá de nuevo en unos minutos"
	}
}

// Queued dice si el canal tiene un pedido del botón esperando.
func (a *Analyst) Queued(channel string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.queued[channel]
}

func (a *Analyst) queuedList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for c := range a.queued {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// done marca que el pedido del canal salió de la cola.
func (a *Analyst) done(channel string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.queued, channel)
}

// AnalystConfig: Budget es el tope diario en micro-dólares (-claude-usd).
// Now y Tick son para los tests (nil: el reloj y un ticker de analystTick):
// así no cambian time.Local, que es global, mientras corre el analista, y
// no quedan timers de canal disparando (leen time.Local) al terminar.
type AnalystConfig struct {
	Budget int64
	Now    func() time.Time
	Tick   <-chan time.Time
}

type retryState struct {
	tries int
	next  time.Time
}

type analystRun struct {
	ctx     context.Context
	cl      *Claude
	st      *Store
	an      *Analyst
	cfg     AnalystConfig
	status  ClaudeStatus
	retries map[string]*retryState // por clave de tramo
}

// RunAnalyst es la única goroutine que usa el cliente Claude. Va aparte del
// worker porque un pedido tarda decenas de segundos y frenaría el tick y el
// chat en tiempo real. Lee el Store con copias y escribe solo los análisis,
// el gasto y su estado. Vuelve al cancelar ctx (también con un pedido en
// curso) o cuando Claude rechaza la clave o el modelo.
func RunAnalyst(ctx context.Context, cl *Claude, st *Store, an *Analyst, cfg AnalystConfig) {
	r := &analystRun{ctx: ctx, cl: cl, st: st, an: an, cfg: cfg, retries: map[string]*retryState{},
		status: ClaudeStatus{On: true, Model: cl.Model, Tope: cfg.Budget}}
	if r.cfg.Now == nil {
		r.cfg.Now = time.Now
	}
	tick := cfg.Tick
	if tick == nil {
		t := time.NewTicker(analystTick)
		defer t.Stop()
		tick = t.C
	}
	ok := r.step(r.cfg.Now())
	for ok {
		select {
		case <-ctx.Done():
			return
		case req := <-an.queue:
			an.done(req.Channel)
			ok = r.manual(req, r.cfg.Now())
		case <-tick:
			ok = r.step(r.cfg.Now())
		}
	}
}

// step analiza, como mucho, un programa terminado: el más nuevo que no tenga
// análisis final ni esté esperando un reintento.
func (r *analystRun) step(now time.Time) bool {
	r.status.Capped = false
	snap := r.st.Status()
	from := claudeFrom(now)
	done := map[string]bool{}
	for _, ch := range snap.Channels {
		for _, a := range r.st.Analyses(ch.ID, from) {
			if !a.Partial {
				done[a.Key] = true
			}
		}
	}
	for _, p := range pendingSegments(snap.Channels, r.st.Lives(from), snap.Videos, done, now) {
		if rt := r.retries[segmentKey(p.Channel.ID, p.Seg)]; rt != nil && now.Before(rt.next) {
			continue
		}
		return r.analyze(p.Channel, p.Seg, snap, false, now)
	}
	r.publish()
	return true
}

// manual hace el pedido del botón: el programa al aire, hasta ahora.
func (r *analystRun) manual(req analysisReq, now time.Time) bool {
	r.status.Capped = false
	snap := r.st.Status()
	for _, ch := range snap.Channels {
		if ch.ID != req.Channel {
			continue
		}
		from := claudeFrom(now)
		segs := ProgramSegments(r.st.Lives(from), ch.ID, liveOf(snap.Videos, ch.ID), from, now)
		if seg, ok := currentSegment(snap.Videos, segs, ch.ID, req.Video); ok {
			return r.analyze(ch, seg, snap, true, now)
		}
		r.status.LastErr = "No hay un programa al aire en " + channelName(ch) + ": cada programa se analiza solo cuando termina"
	}
	r.publish()
	return true
}

// analyze pide el análisis de un tramo y lo guarda. Devuelve false si hay
// que parar (contexto cancelado o análisis apagado).
func (r *analystRun) analyze(ch ChannelStatus, seg Segment, snap Snapshot, partial bool, now time.Time) bool {
	key := segmentKey(ch.ID, seg)
	msgs := segmentMessages(r.st.MessagesOf(map[string]bool{seg.VideoID: true}, seg.Start.Add(-segmentLead)), seg, videoTitles(snap.Videos))
	to := seg.End
	if partial {
		to = now
	}
	user, sent := claudePrompt(channelName(ch), seg.Program, seg.Start, to, msgs)
	a := ClaudeAnalysis{At: now, Key: key, ChannelID: ch.ID, VideoID: seg.VideoID, Program: seg.Program, From: seg.Start, To: to,
		Partial: partial, Sent: len(sent), Total: len(msgs)}
	if len(sent) < minClaudeMsgs {
		// Se guarda igual (sin pedido ni costo) para no volver a mirarlo.
		a.Error = "Muy pocos mensajes para analizar"
		r.st.AddAnalysis(a)
		r.publish()
		return true
	}
	// El peor caso cuenta la salida completa: así el tope no se pasa aunque la respuesta sea larga.
	if r.st.ClaudeSpend()+claudeWorstCase(r.cl.Model, claudeRequestBytes(user)) > r.cfg.Budget {
		r.status.Capped = true
		r.publish()
		return true
	}
	r.status.Busy = key
	r.publish()
	rep, err := r.cl.AnalyzeChat(r.ctx, claudeSystem, user)
	r.status.Busy = ""
	if r.ctx.Err() != nil {
		return false
	}
	switch {
	case rep.InputTokens > 0 || rep.OutputTokens > 0:
		model := rep.Model
		if model == "" {
			model = r.cl.Model
		}
		// Se cobra también si después la respuesta no sirve: los tokens se gastaron igual.
		a.CostMicros = claudeCost(model, rep.InputTokens, rep.OutputTokens)
		r.st.AddClaudeSpend(a.CostMicros)
	case rep.MaybeBilled:
		// El pedido salió y no se sabe cuánto costó: se cuenta el peor caso,
		// así el contador no queda debajo de lo gastado y el tope no se pasa.
		a.CostMicros = claudeWorstCase(r.cl.Model, claudeRequestBytes(user))
		r.st.AddClaudeSpend(a.CostMicros)
		log.Printf("aviso: no volvió cuánto costó el pedido a Claude: cuento el peor caso (US$ %s)", usd(a.CostMicros))
	}
	a.Model, a.StopReason, a.RefusalCategory, a.InputTokens, a.OutputTokens = rep.Model, rep.StopReason, rep.RefusalCategory, rep.InputTokens, rep.OutputTokens
	if err == nil {
		a.Result, err = validClaudeResult(rep.Raw, sent)
	}
	if err != nil {
		switch claudeKindOf(err) {
		case errFatal:
			log.Printf("aviso: %v. El análisis con Claude queda apagado hasta reiniciar el monitor.", err)
			r.status.On, r.status.Off = false, err.Error()
			r.publish()
			return false
		case errRetry:
			if r.retry(key, err, partial) {
				return true
			}
		}
		log.Printf("aviso: Claude, %s de %s: %v", seg.Program, channelName(ch), err)
		a.Error = err.Error()
	} else {
		r.status.LastErr = ""
		log.Printf("Claude analizó %s de %s: %d de %d mensajes, US$ %s", seg.Program, channelName(ch), a.Sent, a.Total, usd(a.CostMicros))
	}
	delete(r.retries, key)
	r.st.AddAnalysis(a)
	r.publish()
	return true
}

// retry agenda otro intento de un tramo terminado si le quedan. Los pedidos
// del botón no se reintentan: se pueden volver a pedir.
func (r *analystRun) retry(key string, err error, partial bool) bool {
	rt := r.retries[key]
	if rt == nil {
		rt = &retryState{}
		r.retries[key] = rt
	}
	rt.tries++
	if partial || rt.tries > len(claudeRetryWaits) {
		return false
	}
	var after time.Duration
	if ce := (*claudeErr)(nil); errors.As(err, &ce) {
		after = ce.RetryAfter
	}
	rt.next = r.cfg.Now().Add(retryDelay(rt.tries, after))
	r.status.LastErr = err.Error() + " (se reintenta a las " + rt.next.Local().Format("15:04") + ")"
	log.Printf("aviso: %s", r.status.LastErr)
	r.publish()
	return true
}

func (r *analystRun) publish() {
	r.status.Queued = r.an.queuedList()
	r.st.SetClaudeStatus(r.status)
}

// retryDelay es la espera antes del intento try + 1: la de claudeRetryWaits
// o lo que pidió la API con retry-after, si es mayor.
func retryDelay(try int, retryAfter time.Duration) time.Duration {
	w := claudeRetryWaits[min(max(try, 1), len(claudeRetryWaits))-1]
	return max(w, retryAfter)
}

// claudeFrom: los tramos se arman desde las 00:00 menos 12 h, así un programa
// que cruza la medianoche entra entero.
func claudeFrom(now time.Time) time.Time { return dayStart(now).Add(-12 * time.Hour) }

// segmentKey identifica un tramo: canal|vivo|programa|inicio.
func segmentKey(channelID string, seg Segment) string {
	return channelID + "|" + seg.VideoID + "|" + seg.Program + "|" + seg.Start.UTC().Format(time.RFC3339)
}

// liveOf son los vivos en curso de un canal según la lista de videos.
func liveOf(videos []Video, channelID string) map[string]bool {
	live := map[string]bool{}
	for _, v := range videos {
		if v.ChannelID == channelID && v.LiveStatus == "live" {
			live[v.ID] = true
		}
	}
	return live
}

// currentSegment es el programa al aire de un canal: el del vivo want si
// sigue en curso o el del primero, con su último tramo.
func currentSegment(videos []Video, segs []Segment, channelID, want string) (Segment, bool) {
	var lives []Video
	for _, v := range videos {
		if v.ChannelID == channelID && v.LiveStatus == "live" {
			lives = append(lives, v)
		}
	}
	sort.SliceStable(lives, func(i, j int) bool { return lives[i].ID == want && lives[j].ID != want })
	if len(lives) == 0 {
		return Segment{}, false
	}
	v := lives[0]
	prog := ProgramName(v.Title, v.Channel)
	var cur Segment
	found := false
	for _, sg := range segs {
		if sg.VideoID == v.ID && sg.Program == prog {
			cur, found = sg, true
		}
	}
	cur.State = SegLive
	return cur, found
}

// segmentMessages son los mensajes de un tramo: del vivo, con su programa y
// desde 5 min antes de la primera muestra. Un tramo terminado llega hasta 5
// min después de la última. Es el mismo filtro que "Programa en curso".
func segmentMessages(msgs []Message, seg Segment, titles map[string]string) []Message {
	from := seg.Start.Add(-segmentLead)
	var to time.Time
	if seg.State != SegLive {
		to = seg.End.Add(segmentLead)
	}
	var out []Message
	for _, m := range msgs {
		if m.VideoID == seg.VideoID && !m.At.Before(from) && (to.IsZero() || !m.At.After(to)) && programOf(m, titles) == seg.Program {
			out = append(out, m)
		}
	}
	return out
}

type pendingSeg struct {
	Channel ChannelStatus
	Seg     Segment
}

// pendingSegments son los programas de hoy que terminaron y no tienen
// análisis final, del que terminó último al primero. El último tramo de un
// vivo que no figura al aire espera segmentGap sin muestras: puede ser el
// programa en curso si la lista de videos todavía no se revisó (por ejemplo,
// al arrancar).
func pendingSegments(channels []ChannelStatus, samples []LiveSample, videos []Video, done map[string]bool, now time.Time) []pendingSeg {
	from, today := claudeFrom(now), dayStart(now)
	var out []pendingSeg
	for _, ch := range channels {
		segs := ProgramSegments(samples, ch.ID, liveOf(videos, ch.ID), from, now)
		last := map[string]int{}
		for i, sg := range segs {
			if sg.State != SegNone {
				last[sg.VideoID] = i
			}
		}
		for i, sg := range segs {
			if sg.State != SegEnded || sg.End.Before(today) || done[segmentKey(ch.ID, sg)] {
				continue
			}
			if last[sg.VideoID] == i && now.Sub(sg.End) <= segmentGap {
				continue
			}
			out = append(out, pendingSeg{ch, sg})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seg.End.After(out[j].Seg.End) })
	return out
}
