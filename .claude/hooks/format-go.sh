#!/usr/bin/env bash
# PostToolUse(Edit|Write): formatea el .go editado con gofmt y corre go vet.
# Si vet falla sale con 2 para que el agente vea el error y lo corrija.
set -u
f=$(jq -r '.tool_input.file_path // empty')
[[ $f == *.go && -f $f ]] || exit 0
if ! out=$(gofmt -w "$f" 2>&1); then
	printf 'gofmt falló en %s:\n%s\n' "$f" "$out" >&2
	exit 2
fi
if ! out=$(cd "${CLAUDE_PROJECT_DIR:-.}" && go vet ./... 2>&1); then
	printf 'go vet ./... falló después de editar %s:\n%s\n' "$f" "$out" >&2
	exit 2
fi
exit 0
