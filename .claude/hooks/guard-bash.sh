#!/usr/bin/env bash
# PreToolUse(Bash): bloquea comandos destructivos, commits/push a main y todo lo
# que mencione .env. Sale con 2 para que Claude Code cancele el comando y le
# muestre el motivo al agente.
set -u
cmd=$(jq -r '.tool_input.command // empty')
[ -z "$cmd" ] && exit 0

block() {
	echo "Bloqueado por .claude/hooks/guard-bash.sh: $1" >&2
	exit 2
}

# .env tiene la API key: ni leerlo, ni copiarlo, ni commitearlo (CLAUDE.md).
if grep -Eq '(^|[^[:alnum:]_])\.env([^[:alnum:]_]|$)' <<<"$cmd"; then
	block "el comando menciona .env (tiene la API key). Si es un mensaje de commit que lo nombra, usá git commit -F <archivo>."
fi

branch=$(git -C "${CLAUDE_PROJECT_DIR:-.}" branch --show-current 2>/dev/null)

# Se analiza cada comando de la línea por separado (;, &&, ||, |, &, subshells).
while IFS= read -r seg; do
	set -f
	read -ra w <<<"$seg"
	set +f
	[ ${#w[@]} -eq 0 ] && continue

	# rm recursivo y forzado, con las flags en cualquier orden.
	for i in "${!w[@]}"; do
		[[ ${w[$i]} == rm || ${w[$i]} == */rm ]] || continue
		r=0 f=0
		for a in "${w[@]:$((i + 1))}"; do
			case "$a" in
			--recursive) r=1 ;;
			--force) f=1 ;;
			--*) ;;
			-*)
				[[ $a == *[rR]* ]] && r=1
				[[ $a == *f* ]] && f=1
				;;
			esac
		done
		[ $r = 1 ] && [ $f = 1 ] && block "rm recursivo forzado (rm -rf). Borrá archivos puntuales o pedíselo a la persona."
	done

	[[ " ${w[*]} " == *" git "* ]] || continue
	sub=""
	for a in "${w[@]}"; do
		case "$a" in push | commit | reset | clean) sub=$a && break ;; esac
	done
	case "$sub" in
	push)
		for a in "${w[@]}"; do
			case "$a" in
			--force | --force=* | --force-with-lease* | --force-if-includes | --mirror | -f | -[a-zA-Z]*f*) block "git push forzado: reescribe historia remota." ;;
			+*) block "git push con refspec forzada (+)." ;;
			main | HEAD:main | *:main | refs/heads/main | *:refs/heads/main) block "push directo a main: abrí un PR desde una rama." ;;
			esac
		done
		args=0
		for a in "${w[@]}"; do [[ $a != -* && $a != git && $a != push ]] && args=$((args + 1)); done
		[ "$branch" = main ] && [ $args -le 1 ] && block "push estando en main: cambiá a una rama de tarea."
		;;
	commit)
		[ "$branch" = main ] && block "commit en main: creá una rama (git switch -c fix/<slug>) en un comando aparte y commiteá ahí."
		;;
	reset)
		[[ " ${w[*]} " == *" --hard"* ]] && block "git reset --hard descarta trabajo sin vuelta atrás."
		;;
	clean)
		for a in "${w[@]}"; do [[ $a == -*f* && $a != --* ]] && block "git clean -f borra archivos no versionados (por ejemplo datos.jsonl)."; done
		;;
	esac
done < <(sed -E 's/(&&|\|\||;|\||&|\$\(|`|\(|\))/\n/g' <<<"$cmd")
exit 0
