#!/bin/sh
# Câble (ou décâble) la notification de mise à jour dans le fichier de
# démarrage du shell de l'utilisateur.
#
#   scripts/shell/install-hook.sh              installe
#   scripts/shell/install-hook.sh --uninstall  retire
#
# POURQUOI CE SCRIPT TOUCHE À UN FICHIER QUI N'EST PAS À NOUS. Un installeur
# qui modifie le ~/.zshrc de quelqu'un doit trois choses à celui qui le lance :
# être REJOUABLE sans jamais dupliquer, être RÉVERSIBLE par une commande, et
# DIRE ce qu'il a écrit et où. Un bloc ajouté en silence, impossible à retirer
# autrement qu'à la main, est la raison pour laquelle beaucoup de gens refusent
# par principe les installeurs qui « configurent le shell ». Les trois
# garanties sont tenues ici par les deux marqueurs ci-dessous, jamais par une
# recherche floue sur le contenu : c'est le seul moyen de retirer EXACTEMENT ce
# qu'on a mis, même si l'utilisateur a réindenté ou déplacé le bloc.
#
# Ne rien faire est toujours possible : PVECLI_NO_SHELL_HOOK=1.
set -eu

BEGIN='# >>> pvecli update notification >>>'
END='# <<< pvecli update notification <<<'

HERE="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
SNIPPET="$HERE/update-notify.sh"

say() { printf '%s\n' "$*"; }
die() {
	printf '\n✗ %s\n' "$*" >&2
	exit 1
}

# ── Quel fichier de démarrage ? ───────────────────────────────────────────────
#
# On suit le shell de CONNEXION ($SHELL), pas le shell qui exécute ce script :
# `curl … | sh` s'exécute sous /bin/sh chez tout le monde, y compris chez les
# gens dont le shell interactif est zsh. Se fier à l'interpréteur courant
# câblerait donc systématiquement le mauvais fichier.
rcfile() {
	case "${PVECLI_SHELL_RC:-}" in
	?*)
		# Échappatoire explicite, et le seul cas où l'on écrit dans un fichier
		# que l'on n'a pas déduit soi-même.
		printf '%s\n' "$PVECLI_SHELL_RC"
		return 0
		;;
	esac

	case "$(basename -- "${SHELL:-}")" in
	zsh) printf '%s\n' "${ZDOTDIR:-$HOME}/.zshrc" ;;
	bash)
		# macOS lance les terminaux en shell de CONNEXION, et bash ne lit
		# alors pas ~/.bashrc mais ~/.bash_profile. Sous Linux c'est
		# l'inverse. On vise donc ~/.bashrc quand il existe — le cas de très
		# loin le plus courant — et ~/.bash_profile sinon.
		if [ -f "$HOME/.bashrc" ]; then
			printf '%s\n' "$HOME/.bashrc"
		elif [ -f "$HOME/.bash_profile" ]; then
			printf '%s\n' "$HOME/.bash_profile"
		else
			printf '%s\n' "$HOME/.bashrc"
		fi
		;;
	*) printf '%s\n' "" ;;
	esac
}

RC="$(rcfile)"

# ── Retrait ───────────────────────────────────────────────────────────────────

if [ "${1:-}" = "--uninstall" ]; then
	[ -n "$RC" ] && [ -f "$RC" ] || {
		say "rien à retirer (aucun fichier de démarrage connu)"
		exit 0
	}
	grep -qF "$BEGIN" "$RC" || {
		say "rien à retirer dans $RC"
		exit 0
	}
	tmp="$(mktemp)"
	# On écrit à côté puis on remplace : une coupure au mauvais moment ne doit
	# jamais laisser un ~/.zshrc tronqué, qui casserait TOUS les shells.
	awk -v b="$BEGIN" -v e="$END" '
		index($0, b) { skip = 1 }
		!skip        { print }
		index($0, e) { skip = 0 }
	' "$RC" >"$tmp"
	cat "$tmp" >"$RC"
	rm -f "$tmp"
	say "✓ bloc pvecli retiré de $RC"
	exit 0
fi

# ── Pose ──────────────────────────────────────────────────────────────────────

if [ "${PVECLI_NO_SHELL_HOOK:-}" = "1" ]; then
	say "· notification non câblée (PVECLI_NO_SHELL_HOOK=1)"
	exit 0
fi

[ -f "$SNIPPET" ] || die "snippet introuvable : $SNIPPET"

if [ -z "$RC" ]; then
	say "· shell non reconnu (\$SHELL=${SHELL:-vide}) — notification non câblée."
	say "  À ajouter à la main dans ton fichier de démarrage :"
	say ""
	say "    [ -f $SNIPPET ] && . $SNIPPET"
	exit 0
fi

# Rejouable : si le marqueur est là, on ne touche à rien. Une réinstallation
# quotidienne par un timer ne doit pas empiler des blocs identiques.
if [ -f "$RC" ] && grep -qF "$BEGIN" "$RC"; then
	say "· notification déjà câblée dans $RC"
	exit 0
fi

# Un fichier de démarrage qui n'existe pas encore est créé ; un fichier
# existant n'est jamais réécrit, seulement complété.
mkdir -p "$(dirname -- "$RC")"
{
	printf '\n%s\n' "$BEGIN"
	printf '%s\n' "# Prévient à l'ouverture d'un terminal qu'une release plus récente existe."
	printf '%s\n' "# Ne fait AUCUN appel réseau en premier plan. Retrait :"
	printf '%s\n' "#   $HERE/install-hook.sh --uninstall"
	printf '%s\n' "[ -f $SNIPPET ] && . $SNIPPET"
	printf '%s\n' "$END"
} >>"$RC"

say "✓ notification câblée dans $RC"
say "  effet au prochain terminal — retrait : $0 --uninstall"
