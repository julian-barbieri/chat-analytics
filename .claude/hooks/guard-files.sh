#!/usr/bin/env bash
# PreToolUse(Read|Edit|Write|...): impide leer o escribir .env (tiene la API key).
set -u
p=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // .tool_input.path // empty')
[ -z "$p" ] && exit 0
case "$(basename "$p")" in
.env | .env.*)
	echo "Bloqueado por .claude/hooks/guard-files.sh: $p tiene secretos; no se lee ni se edita (CLAUDE.md)." >&2
	exit 2
	;;
esac
exit 0
