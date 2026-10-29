// yt-monitor lee comentarios y chat en vivo de canales de YouTube con una
// API key (sin permisos de administrador) y genera un informe en Markdown y
// un CSV para analizar con Claude.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // zona horaria del Pacífico también en Windows
)

func main() {
	channel := flag.String("canal", "UCLAj5TzCrNbqe3LosSKHLcQ", "IDs, @handles o links de canales separados por coma; el primero es el principal (por defecto Radio Rivadavia) y de los demás solo se siguen los vivos")
	videos := flag.Int("videos", 15, "cantidad de videos recientes a revisar")
	hours := flag.Int("horas", 24, "revisar comentarios de las últimas N horas")
	chatMin := flag.Int("chat-min", 0, "si hay un vivo, leer su chat durante N minutos (0 = no leer)")
	outDir := flag.String("salida", "informes", "carpeta de salida")
	dash := flag.String("dashboard", "", "modo continuo: dirección del dashboard, por ejemplo :8080")
	budget := flag.Int("cuota", 9000, "modo continuo: tope de unidades diarias (YouTube da 10.000)")
	chatSeg := flag.Int("chat-seg", 180, "modo continuo: segundos entre lecturas del chat en vivo fuera del horario pico")
	chatPeak := flag.Int("chat-seg-pico", 60, "modo continuo: segundos entre lecturas del chat en vivo en el horario pico")
	peakHours := flag.String("pico", "7-20", "modo continuo: horario pico en hora local, desde-hasta (\"\" = sin horario pico)")
	comMin := flag.Int("comentarios-min", 10, "modo continuo: minutos entre lecturas de comentarios")
	realTime := flag.String("tiempo-real", "", "modo continuo: links o IDs de vivos con tiempo real continuo, separados por coma: se reconectan apenas YouTube corta la conexión (~1.800 unidades por hora cada uno)")
	vivos := flag.String("vivos", "", "links o IDs de videos a revisar siempre, separados por coma (los vivos del canal se detectan solos)")
	flag.Parse()

	// Con un espacio después de la coma ("-vivos a, b") el segundo link queda
	// suelto y flag lo ignora sin avisar (y también todo lo que venga después):
	// si es un link de video se suma a -vivos y se sigue leyendo el resto.
	var loose []string
	for flag.NArg() > 0 {
		s := flag.Arg(0)
		id := ParseVideoID(s)
		if !strings.Contains(s, "youtu") || id == "" {
			log.Fatalf("no entiendo %q: si es otro canal o video de -canal o -vivos, separalos con coma y sin espacios", s)
		}
		log.Printf("aviso: sumo %s a -vivos (la próxima, separá los links con coma y sin espacios)", id)
		loose = append(loose, id)
		check(flag.CommandLine.Parse(flag.Args()[1:]))
	}

	var channels []string
	for _, s := range strings.Split(*channel, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		ch := ParseChannel(s)
		if ch == "" {
			log.Fatalf("-canal: no reconozco el canal %q (usá el ID UC..., el @handle o el link)", s)
		}
		channels = append(channels, ch)
	}
	if len(channels) == 0 {
		log.Fatal("-canal: falta al menos un canal")
	}

	var extra []string
	for _, s := range strings.Split(*vivos, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		id := ParseVideoID(s)
		if id == "" {
			log.Fatalf("-vivos: no reconozco el video %q", s)
		}
		extra = append(extra, id)
	}
	extra = append(extra, loose...)
	rt := map[string]bool{}
	for _, s := range strings.Split(*realTime, ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		id := ParseVideoID(s)
		if id == "" {
			log.Fatalf("-tiempo-real: no reconozco el video %q", s)
		}
		rt[id] = true
		extra = append(extra, id) // ChannelVideos no repite videos
	}

	peakFrom, peakTo, err := ParsePeak(*peakHours)
	if err != nil {
		log.Fatalf("-pico: %v", err)
	}

	loadDotEnv(".env")
	key := os.Getenv("YT_API_KEY")
	if key == "" {
		log.Fatal("falta YT_API_KEY: ponela en un archivo .env (YT_API_KEY=tu_clave) o como variable de entorno (ver README.md)")
	}
	yt := NewYouTube(key)
	if base := os.Getenv("YT_BASE_URL"); base != "" { // solo para pruebas con una API simulada
		yt.BaseURL = base
	}

	if *dash != "" {
		st, err := OpenStore("datos.jsonl")
		check(err)
		go RunWorker(yt, st, WorkerConfig{
			Channels: channels, Videos: *videos, Extra: extra, VideosEvery: 3 * time.Minute,
			CommentsEvery: time.Duration(*comMin) * time.Minute,
			ChatEvery:     time.Duration(*chatSeg) * time.Second, ChatPeak: time.Duration(*chatPeak) * time.Second,
			PeakFrom: peakFrom, PeakTo: peakTo, Budget: *budget, RealTime: rt,
		})
		addr := *dash
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		log.Printf("Dashboard en http://%s (Ctrl+C para cortar)", addr)
		check(http.ListenAndServe(addr, (&Server{st: st, budget: *budget}).Routes()))
		return
	}

	since := time.Now().Add(-time.Duration(*hours) * time.Hour)

	var titles, uploads []string
	for _, ch := range channels {
		title, pl, err := yt.UploadsPlaylist(ch)
		check(err)
		titles, uploads = append(titles, title), append(uploads, pl)
	}
	title := strings.Join(titles, ", ")
	vids, err := yt.ChannelVideos(uploads, *videos, extra)
	check(err)

	var msgs []Message
	for _, v := range vids {
		if v.Comments == 0 {
			continue
		}
		cs, err := yt.Comments(v.ID, since, 5)
		if err != nil {
			// Videos con comentarios desactivados devuelven 403: seguimos con el resto.
			log.Printf("aviso: %v", err)
			continue
		}
		msgs = append(msgs, cs...)
	}

	var chatErr error
	if *chatMin > 0 {
		var lives []Video
		for _, v := range vids {
			if v.LiveStatus == "live" && v.LiveChatID != "" {
				lives = append(lives, v)
			}
		}
		if len(lives) > 0 {
			log.Printf("leyendo el chat de %d vivo(s) durante %d min...", len(lives), *chatMin)
			// Las lecturas se reparten entre los vivos: la cuota es la misma que con uno solo.
			cs, err := yt.LiveChats(lives, time.Duration(*chatMin)*time.Minute, *chatMin*6)
			if err != nil {
				chatErr = err
				log.Printf("aviso chat: %v", err)
			}
			msgs = append(msgs, cs...)
		}
	}

	check(os.MkdirAll(*outDir, 0o755))
	stamp := time.Now().Format("2006-01-02_1504")
	csvPath := filepath.Join(*outDir, "mensajes_"+stamp+".csv")
	mdPath := filepath.Join(*outDir, "informe_"+stamp+".md")
	check(writeCSV(csvPath, msgs, vids))
	check(os.WriteFile(mdPath, []byte(report(title, vids, msgs, since, yt.Units, chatErr, *chatMin)), 0o644))
	fmt.Printf("Listo: %s y %s (cuota usada aprox.: %d de 10.000 unidades)\n", mdPath, csvPath, yt.Units)
}

func writeCSV(path string, msgs []Message, vids []Video) error {
	titles := map[string]string{}
	for _, v := range vids {
		titles[v.ID] = v.Title
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write(csvHeader)
	for _, m := range msgs {
		w.Write(csvRow(m, titles))
	}
	w.Flush()
	return w.Error()
}

// Columnas del CSV para Claude; las usan el informe y el dashboard.
var csvHeader = []string{"fuente", "fecha", "video", "programa", "autor", "categoria", "likes", "respuestas", "texto"}

func csvRow(m Message, titles map[string]string) []string {
	return []string{
		m.Source, m.At.Local().Format("2006-01-02 15:04"), titles[m.VideoID], programOf(m, titles), m.Author,
		Classify(m.Text), fmt.Sprint(m.Likes), fmt.Sprint(m.Replies), m.Text,
	}
}

// programOf devuelve el programa de un mensaje de chat ("" para comentarios).
// Los mensajes guardados antes de que existiera el campo usan el título actual del vivo.
func programOf(m Message, titles map[string]string) string {
	if m.Program != "" || m.Source != "chat" {
		return m.Program
	}
	return ProgramName(titles[m.VideoID], "")
}

func report(channel string, vids []Video, msgs []Message, since time.Time, units int, chatErr error, chatMin int) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("# Monitor de YouTube: %s\n\n", channel)
	p("Generado: %s. Comentarios desde %s. Cuota usada aprox.: %d unidades.\n\n",
		time.Now().Format("2006-01-02 15:04"), since.Local().Format("2006-01-02 15:04"), units)

	byCat := map[string][]Message{}
	comments, chat := 0, 0
	for _, m := range msgs {
		c := Classify(m.Text)
		byCat[c] = append(byCat[c], m)
		if m.Source == "chat" {
			chat++
		} else {
			comments++
		}
	}

	p("## Números\n\n")
	p("- Comentarios nuevos: **%d**\n", comments)
	if chatMin > 0 {
		p("- Mensajes de chat leídos en %d min: **%d**\n", chatMin, chat)
		if chatErr != nil {
			p("- El chat no se pudo leer: `%s`\n", strings.ReplaceAll(chatErr.Error(), "\n", "; "))
		}
	}
	p("\n| Categoría | Cantidad |\n|---|---|\n")
	for _, c := range []string{CatPregunta, CatQueja, CatElogio, CatInsulto, CatSpam, CatOtro} {
		p("| %s | %d |\n", c, len(byCat[c]))
	}

	p("\n## Videos revisados\n\n| Video | Canal | Estado | Vistas | Comentarios |\n|---|---|---|---|---|\n")
	for _, v := range vids {
		estado := "subido"
		if v.LiveStatus == "live" {
			estado = fmt.Sprintf("EN VIVO (%d mirando)", v.Concurrent)
		} else if v.LiveStatus == "upcoming" {
			estado = "programado"
		}
		p("| [%s](https://youtu.be/%s) | %s | %s | %d | %d |\n", esc(v.Title), v.ID, esc(v.Channel), estado, v.Views, v.Comments)
	}

	p("\n## Palabras más repetidas\n\n")
	for _, c := range TopWords(msgs, 15) {
		p("%s (%d) · ", c.Key, c.N)
	}
	p("\n\n## Chat por programa\n\n")
	for _, c := range TopPrograms(msgs, vids, 10) {
		p("- %s: %d\n", esc(c.Key), c.N)
	}
	p("\n## Quiénes más escriben\n\n")
	for _, c := range TopAuthors(msgs, 10) {
		p("- %s: %d\n", c.Key, c.N)
	}

	for _, c := range []string{CatPregunta, CatQueja, CatInsulto, CatSpam} {
		list := byCat[c]
		if len(list) == 0 {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Likes > list[j].Likes })
		p("\n## Ejemplos: %s\n\n", c)
		for i, m := range list {
			if i == 10 {
				p("- ... y %d más (ver CSV)\n", len(list)-10)
				break
			}
			p("- **%s** (%s, %d likes): %s\n", esc(m.Author), m.Source, m.Likes, esc(oneLine(m.Text)))
		}
	}
	return b.String()
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// loadDotEnv carga líneas CLAVE=valor de un archivo .env sin pisar
// variables que ya estén definidas. Si el archivo no existe, no hace nada.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "/") }

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
