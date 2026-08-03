# pvecli — notification de mise à jour (PVX-090)
#
# Complémentaire du timer systemd de scripts/autoupdate/ (PVX-081) : ce timer
# INSTALLE silencieusement la dernière release une fois par jour ; ce fichier
# ne fait que NOTIFIER, à chaque ouverture de terminal, et rien d'autre. Les
# deux coexistent sans se marcher dessus — l'un peut être absent sans casser
# l'autre.
#
# Installation : ajoute cette ligne à ~/.zshrc (ce fichier n'est jamais sourcé
# tout seul) :
#
#   [[ -f /chemin/vers/pvecli/scripts/shell/update-notify.zsh ]] && \
#     source /chemin/vers/pvecli/scripts/shell/update-notify.zsh
#
# Toute la logique vit côté Go (`pvecli update check --notify`) : ce script se
# contente de la lancer sans ralentir l'ouverture du shell ni polluer le prompt.

command -v pvecli >/dev/null 2>&1 || return

# Piège : un `cmd &` nu dans un zsh interactif imprime tout de suite un
# identifiant de job (« [1] 12345 ») ET une ligne « [1]  done » au prompt
# SUIVANT — les deux atterriraient au milieu de ce que l'utilisateur est en
# train de taper. La parade est d'englober le arrière-plan dans un
# sous-shell : `( … & )`. Le job est créé dans la table de jobs jetable du
# sous-shell, et ce sous-shell se termine lui-même de façon synchrone avant
# que le contrôle de jobs du zsh interactif n'ait quoi que ce soit à
# rapporter — donc silence total, dans les deux sens. La redirection de
# stdout/stderr sur la commande interne est une deuxième ceinture : --notify
# ne dit déjà rien s'il n'y a rien à dire, mais ça garde un futur panic Go
# loin du prompt aussi.
( pvecli update check --notify >/dev/null 2>&1 & ) 2>/dev/null
