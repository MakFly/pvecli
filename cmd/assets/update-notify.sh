# pvecli — notification de mise à jour (PVX-090)
#
# Complémentaire du timer systemd de scripts/autoupdate/ (PVX-081) : ce timer
# INSTALLE silencieusement la dernière release une fois par jour ; ce fichier
# ne fait que NOTIFIER, à chaque ouverture de terminal, et rien d'autre. Les
# deux coexistent sans se marcher dessus — l'un peut être absent sans casser
# l'autre.
#
# CE FICHIER EST UNE COPIE, pas la source. Il est embarqué dans le binaire
# (cmd/assets/update-notify.sh) et déposé ici par « pvecli update install-hook ».
# L'éditer sur place fonctionne jusqu'à la prochaine installation, qui le
# réécrira sans prévenir : passe par le dépôt si tu veux que ça tienne.
#
# Installation et retrait — ce fichier n'est jamais sourcé tout seul, un bloc
# ajouté à ~/.zshrc ou ~/.bashrc s'en charge :
#
#   pvecli update install-hook              câble
#   pvecli update install-hook --uninstall  décâble
#   pvecli update install-hook --print      imprime, n'écrit rien
#
# Toute la logique vit côté Go (`pvecli update check`) : ce script se contente
# de la lancer sans jamais ralentir l'ouverture du shell ni polluer le prompt.
#
# DEUX APPELS, DEUX RÔLES — et c'est délibéré, pas une lubie de style.
#
# Une seule commande ne peut pas à la fois répondre à l'utilisateur
# INSTANTANÉMENT (le prompt ne doit jamais attendre) ET avoir le droit
# d'attendre jusqu'à 2s sur le réseau (`pvecli update check` seul, appelé par
# un humain, en a le droit). Les concilier dans un seul appel forcerait soit
# à bloquer le prompt, soit à imprimer la ligne de façon asynchrone plusieurs
# secondes après — c'est-à-dire au milieu d'une commande que l'utilisateur est
# déjà en train de taper. Les deux sont pires que le compromis retenu ici :
#
#   1. `--notify`  : PREMIER PLAN, synchrone, ne lit QUE le cache disque —
#      jamais de réseau, jamais d'attente. Sa sortie arrive donc avant le
#      prompt, à sa place naturelle.
#   2. `--refresh` : ARRIÈRE-PLAN, détaché, c'est LUI qui a le droit d'aller
#      sur le réseau (timeout 2s côté Go) — et il ne parle jamais à
#      l'utilisateur, en succès comme en échec.
#
# Conséquence assumée : la notification a toujours UN TERMINAL DE RETARD sur
# la vraie dernière release — elle affiche ce que le `--refresh` précédent a
# écrit dans le cache, pas l'état de GitHub à l'instant présent. C'est le bon
# compromis : préférer un shell instantané à une information parfaitement
# fraîche, et laisser le `--refresh` de CETTE ouverture préparer la
# notification de la PROCHAINE.

command -v pvecli >/dev/null 2>&1 || return

# Premier plan : lecture de cache pure, donc rapide par construction. Rien à
# mettre en arrière-plan ici — le faire retarderait inutilement la sortie
# jusqu'après le prompt.
#
# stderr part au trou, stdout NON : c'est toute la différence avec le bug que
# ce fichier a déjà eu une fois. La charge utile de --notify sort sur stdout et
# doit rester visible ; stderr ne peut porter ici qu'un diagnostic destiné à
# personne. Le cas n'est pas théorique : un `pvecli` ANTÉRIEUR à cette story ne
# connaît pas `update check` et répond « Error: unknown flag: --notify » — sans
# cette redirection, ce shell imprimait cette ligne à CHAQUE ouverture de
# terminal (mesuré le 03-08-2026, sur le poste de l'auteur, dans la minute qui
# a suivi l'ajout au ~/.zshrc). Un binaire trop vieux, ou à moitié installé,
# doit se taire, pas s'expliquer.
pvecli update check --notify 2>/dev/null

# Arrière-plan, détaché. Piège classique : un `cmd &` nu dans un zsh
# interactif imprime tout de suite un identifiant de job (« [1] 12345 ») ET
# une ligne « [1]  done » au prompt SUIVANT — les deux atterriraient au
# milieu de ce que l'utilisateur tape. La parade est d'englober le
# arrière-plan dans un sous-shell : `( … & )`. Le job est créé dans la table
# de jobs jetable du sous-shell, et ce sous-shell se termine lui-même de
# façon synchrone avant que le contrôle de jobs du zsh interactif n'ait quoi
# que ce soit à rapporter — donc silence total, dans les deux sens. La
# redirection de stdout/stderr sur la commande interne est une deuxième
# ceinture : `--refresh` ne dit déjà rien, en succès comme en échec, mais ça
# garde un futur panic Go loin du prompt aussi.
( pvecli update check --refresh >/dev/null 2>&1 & ) 2>/dev/null
