package main

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// sameOrigin dice si un POST viene de una página del mismo dashboard, pedido
// por htmx: sin esto, cualquier página abierta en el navegador podría mandar
// pedidos que gastan plata (CSRF). Mismo criterio que el tablero (ADR 0004).
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("HX-Request") != "true" || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return false
	}
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.Host == r.Host
}

// localRequest dice si el pedido llegó desde la misma PC del monitor: Host
// localhost y conexión de loopback. El dashboard no tiene autenticación y
// pedir un análisis gasta plata.
func localRequest(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}
