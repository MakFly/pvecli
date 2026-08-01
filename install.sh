#!/bin/sh
# Installe pvecli depuis les releases GitHub.
#
#   curl -fsSL https://raw.githubusercontent.com/MakFly/pvecli/main/install.sh | sh
#
# Variables :
#   PVECLI_VERSION        version précise (défaut : la dernière release)
#   PREFIX                racine d'installation (défaut : ~/.local → ~/.local/bin)
#   PVECLI_NO_AGENT       =1 pour ne pas installer l'agent Claude Code
#   PVECLI_ONLY_IF_NEWER  =1 pour ne rien faire si la version visée est déjà
#                         installée — c'est ce qui rend ce script rejouable
#                         sans frais depuis un timer (voir scripts/autoupdate/).
#
# CE SCRIPT VÉRIFIE LA SOMME SHA-256 AVANT D'INSTALLER, ET S'ARRÊTE SI ELLE NE
# CORRESPOND PAS. Un installeur qu'on canalise dans un shell exécute du code
# arrivé par le réseau ; le minimum qu'il doive à celui qui le lance, c'est de
# prouver que l'octet posé sur son disque est bien celui qui a été publié.
# C'est la même exigence que l'épinglage TLS de la CLI elle-même : vérifier
# coûte peu et ne se rattrape pas après coup.
set -eu

REPO="MakFly/pvecli"
BINARY="pvecli"
PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"

say() { printf '%s\n' "$*"; }
die() {
	printf '\n✗ %s\n' "$*" >&2
	exit 1
}

# ── Outils ────────────────────────────────────────────────────────────────────

have() { command -v "$1" >/dev/null 2>&1; }

# Les téléchargements échouent en silence : chaque appel est suivi d'un message
# qui nomme ce qui manque et pourquoi. Laisser en plus passer « curl: (22) The
# requested URL returned error: 404 » ne dit rien de plus et brouille le vrai.
if have curl; then
	fetch() { curl -fsL "$1" -o "$2" 2>/dev/null; }
	fetch_stdout() { curl -fsL "$1" 2>/dev/null; }
elif have wget; then
	fetch() { wget -qO "$2" "$1" 2>/dev/null; }
	fetch_stdout() { wget -qO- "$1" 2>/dev/null; }
else
	die "ni curl ni wget — impossible de télécharger quoi que ce soit."
fi

# sha256sum sous Linux, shasum -a 256 sous macOS. Sans l'un des deux on
# n'installe pas : le contrôle n'est pas une option de confort.
if have sha256sum; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif have shasum; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die "ni sha256sum ni shasum — la somme de contrôle ne peut pas être vérifiée,
  et installer sans la vérifier n'est pas proposé par ce script."
fi

# ── Plateforme ────────────────────────────────────────────────────────────────

os="$(uname -s)"
arch="$(uname -m)"

case "$os/$arch" in
Linux/x86_64 | Linux/amd64) target="linux_amd64" ;;
Darwin/arm64 | Darwin/aarch64) target="darwin_arm64" ;;
*)
	die "plateforme non publiée : $os/$arch

  Les releases couvrent linux/amd64 et darwin/arm64. Pour tout le reste, la
  compilation depuis les sources prend une commande :

    git clone https://github.com/$REPO.git && cd $BINARY && make install"
	;;
esac

# ── Version ───────────────────────────────────────────────────────────────────

version="${PVECLI_VERSION:-}"
if [ -z "$version" ]; then
	say "→ recherche de la dernière release de $REPO"
	# L'API renvoie du JSON ; on n'extrait qu'un champ, sans dépendre de jq.
	version="$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
	[ -n "$version" ] || die "aucune release trouvée pour $REPO.

  Le dépôt n'en publie peut-être pas encore. Depuis les sources :
    git clone https://github.com/$REPO.git && cd $BINARY && make install"
fi

asset="${BINARY}_${version}_${target}"
base="https://github.com/$REPO/releases/download/$version"

# Rejoué chaque jour par un timer, ce script téléchargerait 13 Mo pour réécrire
# le même octet. `pvecli --version` imprime « pvecli v0.1.0 (commit abc1234) » ;
# le premier champ suffit à trancher. On compare des chaînes, pas des numéros :
# on ne cherche pas « plus récent », seulement « autre chose que ce qui tourne ».
if [ "${PVECLI_ONLY_IF_NEWER:-}" = "1" ] && [ -x "$BINDIR/$BINARY" ]; then
	installed="$("$BINDIR/$BINARY" --version 2>/dev/null | awk '{print $2}')"
	if [ "$installed" = "$version" ]; then
		say "→ pvecli $version déjà installé — rien à faire"
		exit 0
	fi
	say "→ pvecli ${installed:-absent} → $version"
fi

say "→ pvecli $version ($target)"

# ── Téléchargement et vérification ────────────────────────────────────────────

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "→ téléchargement"
fetch "$base/$asset" "$tmp/$asset" ||
	die "téléchargement impossible : $base/$asset

  Cette version publie-t-elle bien un binaire pour $target ?"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" ||
	die "SHA256SUMS introuvable pour $version — installation refusée.

  Sans lui, rien ne distingue le binaire publié d'un fichier substitué en route."

expected="$(grep -F " $asset" "$tmp/SHA256SUMS" | cut -d' ' -f1 | head -1)"
[ -n "$expected" ] || die "$asset n'apparaît pas dans SHA256SUMS — installation refusée."

actual="$(sha256 "$tmp/$asset")"
if [ "$expected" != "$actual" ]; then
	die "SOMME DE CONTRÔLE INVALIDE — rien n'a été installé.

  attendue  $expected
  obtenue   $actual

  Soit le téléchargement a été corrompu, soit le fichier n'est pas celui qui a
  été publié. Recommence ; si l'écart persiste, ne l'installe pas."
fi
say "  ✓ SHA-256 vérifiée"

# ── Installation ──────────────────────────────────────────────────────────────

mkdir -p "$BINDIR"
chmod 0755 "$tmp/$asset"
# On écrit à côté puis on déplace : un binaire remplacé pendant qu'il tourne ne
# doit jamais être un fichier à moitié écrit.
mv "$tmp/$asset" "$BINDIR/$BINARY.new"
mv "$BINDIR/$BINARY.new" "$BINDIR/$BINARY"
say "  ✓ $BINDIR/$BINARY"

# La preuve est l'exécution, pas la copie.
got="$("$BINDIR/$BINARY" --version)" || die "le binaire installé ne s'exécute pas."
say "  ✓ $got"

# ── Agent Claude Code ─────────────────────────────────────────────────────────

if [ "${PVECLI_NO_AGENT:-}" != "1" ]; then
	# On rend le chemin que la commande a réellement écrit, pas celui qu'on
	# suppose : CLAUDE_CONFIG_DIR peut déplacer la cible.
	if agent_out="$("$BINDIR/$BINARY" ai install 2>/dev/null)"; then
		say "  ✓ $(printf '%s' "$agent_out" | head -1)"
	else
		# Un agent déjà personnalisé n'est pas un échec d'installation.
		say "  · agent non installé (déjà présent et modifié ?) — « pvecli ai status »"
	fi
fi

# ── Suite ─────────────────────────────────────────────────────────────────────

case ":$PATH:" in
*":$BINDIR:"*) ;;
*)
	say ""
	say "⚠ $BINDIR n'est pas dans ton PATH. Ajoute :"
	say ""
	say "    export PATH=\"\$PATH:$BINDIR\""
	;;
esac

cat <<EOF

Installé. Ensuite :

  pvecli config init --endpoint https://pve.example:8006 \\
      --token-id 'automation@pve!pvectl' --node pve
  pvecli config trust     # épingle le certificat — plus fort que --insecure
  pvecli doctor           # réseau → TLS → auth → nœud → privilèges

L'agent s'invoque depuis Claude Code : « crée-moi une VM 4 vCPU 16 Go ».
EOF
