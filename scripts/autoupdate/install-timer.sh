#!/bin/sh
# Pose (ou retire) le timer systemd --user qui tient pvecli à jour sur ce poste.
#
#   scripts/autoupdate/install-timer.sh            installe et active
#   scripts/autoupdate/install-timer.sh --uninstall  désactive et retire
#
# Ce que ça fait, en une phrase : une fois par jour, la copie locale de
# install.sh demande à GitHub la dernière release, et ne réinstalle que si le
# binaire présent n'est pas déjà celui-là.
#
# systemd --user et non root : pvecli s'installe dans ~/.local/bin, il n'y a
# rien à faire en root ici. Un timer système écrirait dans le home d'un
# utilisateur depuis un contexte privilégié — c'est exactement le genre de
# détail qu'on paie six mois plus tard.
set -eu

UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
HERE="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
REPO="$(CDPATH='' cd -- "$HERE/../.." && pwd)"

say() { printf '%s\n' "$*"; }
die() {
	printf '\n✗ %s\n' "$*" >&2
	exit 1
}

command -v systemctl >/dev/null 2>&1 ||
	die "systemctl absent — ce poste n'est pas sous systemd.
  Sur macOS, l'équivalent est un launchd agent ; sur les autres, un cron :
    0 4 * * *  PVECLI_ONLY_IF_NEWER=1 sh $REPO/install.sh"

if [ "${1:-}" = "--uninstall" ]; then
	systemctl --user disable --now pvecli-update.timer 2>/dev/null || true
	rm -f "$UNIT_DIR/pvecli-update.timer" "$UNIT_DIR/pvecli-update.service"
	systemctl --user daemon-reload
	say "✓ timer retiré (pvecli lui-même reste installé)"
	exit 0
fi

[ -f "$REPO/install.sh" ] || die "install.sh introuvable dans $REPO"

mkdir -p "$UNIT_DIR"

# Le gabarit ne connaît pas l'endroit où le dépôt a été cloné ; on l'y écrit.
sed "s#@INSTALL_SH@#$REPO/install.sh#" \
	"$HERE/pvecli-update.service" >"$UNIT_DIR/pvecli-update.service"
cp "$HERE/pvecli-update.timer" "$UNIT_DIR/pvecli-update.timer"

systemctl --user daemon-reload
systemctl --user enable --now pvecli-update.timer

say ""
say "✓ pvecli se met à jour tout seul — une vérification par jour."
say ""
say "  état      systemctl --user list-timers pvecli-update.timer"
say "  forcer    systemctl --user start pvecli-update.service"
say "  journal   journalctl --user -u pvecli-update.service -n 20"
say "  retirer   $0 --uninstall"
say ""

# Le poste peut être éteint quand le timer tombe : `Persistent=true` rattrape au
# démarrage suivant, mais encore faut-il que la session utilisateur démarre sans
# ouverture de session. Sans linger, le timer ne tourne que pendant que tu es
# connecté — ce qui est acceptable, mais autant le dire plutôt que le découvrir.
if command -v loginctl >/dev/null 2>&1; then
	if [ "$(loginctl show-user "$(id -un)" --property=Linger --value 2>/dev/null)" != "yes" ]; then
		say "note : le linger est désactivé — le timer ne tourne que session ouverte."
		say "       pour qu'il tourne en permanence : sudo loginctl enable-linger $(id -un)"
		say ""
	fi
fi
