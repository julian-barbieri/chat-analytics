#!/usr/bin/env bash
# Hook async de PreToolUse, PostToolUseFailure, SubagentStart, SubagentStop y Stop:
# agrega una línea por evento a factory/events/AAAA-MM-DD.jsonl para el tablero
# (tools/factory-board). Guarda solo un resumen corto, nunca contenidos de archivos,
# y nunca frena al agente: ante cualquier error sale con 0.
set -u
root=${CLAUDE_PROJECT_DIR:-.}
dir="$root/factory/events"
mkdir -p "$dir" 2>/dev/null || exit 0

line=$(jq -c --arg root "$root/" '
	# La API key nunca va a un log (AGENTS.md): como variable, en una URL o suelta.
	def redact: gsub("YT_API_KEY=(\"[^\"]*\"|'"'"'[^'"'"']*'"'"'|[^[:space:]]+)"; "YT_API_KEY=***")
		| gsub("(?<k>[?&]key=)[^&[:space:]\"'"'"']+"; "\(.k)***")
		| gsub("AIza[0-9A-Za-z_-]{35}"; "***");
	def short(n): tostring | gsub("[\\r\\n\\t]+"; " ") | redact | if length > n then .[:n] + "…" else . end;
	def rel: tostring | if startswith($root) then .[($root | length):] else . end;
	(.tool_input // {}) as $in
	| ($in.file_path // $in.notebook_path // "") as $path
	| {
		at: (now | todate),
		evento: (.hook_event_name // ""),
		sesion: (.session_id // ""),
		agent_id: (.agent_id // ""),
		agente: (.agent_type // ""),
		tool: (.tool_name // ""),
		resumen: (
			if .tool_name == "Bash" then ($in.command // "") | short(120)
			elif $path != "" then $path | rel | short(200)
			elif .tool_name == "Agent" or .tool_name == "Task" then
				(($in.subagent_type // "") + ": " + ($in.description // "")) | short(120)
			elif $in.pattern then
				($in.pattern | short(80)) + (if $in.path then " en " + ($in.path | rel | short(120)) else "" end)
			else "" end),
		detalle: (
			if .hook_event_name == "SubagentStop" then
				(.last_assistant_message // "") | split("\n") | map(select(test("\\S"))) | (.[0] // "") | short(160)
			elif .hook_event_name == "PostToolUseFailure" then
				(.error // .tool_response.error // "") | short(160)
			else "" end)
	}
	| with_entries(select(.value != ""))
' 2>/dev/null) || exit 0
[ -z "$line" ] && exit 0

# Un solo printf por línea: los hooks corren en paralelo y así no se intercalan.
printf '%s\n' "$line" >>"$dir/$(date +%F).jsonl"

# No hace falta guardar la actividad más de 30 días.
find "$dir" -name '*.jsonl' -mtime +30 -delete 2>/dev/null
exit 0
