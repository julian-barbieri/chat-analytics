#!/usr/bin/env bash
# Gates locales de yt-monitor (el CI de GitHub está desactivado).
#
#   scripts/gates.sh            rápido: formato, vet y tests (para ir validando)
#   scripts/gates.sh completo   todo lo de "Definición de terminado" (antes del push)
#
# Imprime una tabla en Markdown lista para pegar en el PR y, de cada gate que falla,
# las últimas líneas de su salida. Sale con 1 si falla alguno. govulncheck no hace
# fallar: suele quejarse solo de la stdlib del Go instalado y eso se arregla
# actualizando Go (CLAUDE.md), así que queda en ⚠️ para que lo mire una persona.
set -u
cd "$(dirname "$0")/.."

modo="${1:-rapido}"
case "$modo" in
rapido | completo) ;;
*)
	echo "uso: scripts/gates.sh [rapido|completo]" >&2
	exit 2
	;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
filas=()
fallo=0

# gate <nombre> <solo-aviso:0|1> <comando...>
gate() {
	local nombre="$1" aviso="$2"
	shift 2
	local out="$tmp/${#filas[@]}.txt" ok=1
	printf '… %s\n' "$nombre" >&2
	"$@" >"$out" 2>&1 || ok=0
	# gofmt -l no falla: si lista archivos, está mal.
	if [ "$nombre" = "Formato" ] && [ -s "$out" ]; then ok=0; fi
	local res="✅"
	if [ "$ok" = 0 ]; then
		if [ "$aviso" = 1 ]; then res="⚠️ revisar"; else res="❌"; fallo=1; fi
		{
			echo
			echo "### $nombre ($res)"
			echo '```'
			tail -n 30 "$out"
			echo '```'
		} >>"$tmp/detalle.md"
	fi
	filas+=("| $nombre | \`$*\` | $res |")
}

gate "Formato" 0 gofmt -l .
gate "Vet" 0 go vet ./...
if [ "$modo" = rapido ]; then
	gate "Tests" 0 go test -count=1 ./...
else
	gate "Lint" 0 go run honnef.co/go/tools/cmd/staticcheck@2026.2.2 ./...
	gate "Build Linux" 0 go build ./...
	gate "Build Windows" 0 env GOOS=windows GOARCH=amd64 go build ./...
	gate "Tests con race" 0 go test -race -count=1 ./...
	gate "Intermitencias" 0 go test -count=5 ./...
	gate "Vulnerabilidades" 1 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
fi

echo "**Gates ($modo):**"
echo
echo "| Gate | Comando | Resultado |"
echo "|---|---|---|"
printf '%s\n' "${filas[@]}"
[ -f "$tmp/detalle.md" ] && cat "$tmp/detalle.md"
exit "$fallo"
