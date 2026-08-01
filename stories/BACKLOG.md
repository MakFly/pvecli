# Backlog — post-M7

> Idées et dettes identifiées après la fin de lot M7. Non planifiées : chaque
> story y attend une décision d'architecture avant d'entrer dans un lot.

---

### PVX-074 — `lxc exec` : lancer une commande DANS un conteneur

**Taille** L · **Type** ⚙ · **Dépend de** PVX-041 (`vm agent exec`) · **Statut** ✅ livré (voie 1, console termproxy) — 2026-08-01

> **Résolu.** Implémenté via la voie 1 (termproxy + vncwebsocket), dans
> `internal/pve/lxc_exec.go` + `cmd/lxc_exec.go`. Trois réalités que seul le nœud
> a révélées, au-delà de ce que cette story anticipait :
>
> 1. **La console d'un LXC est un `getty`, pas un shell.** termproxy tombe sur
>    « infra-01 login: ». Il faut donc s'authentifier : `lxc exec` envoie root +
>    le mot de passe lu dans `PVE_LXC_PASSWORD` (env, comme le secret du token ;
>    `PVE_LXC_USER` pour changer d'identité). Un conteneur créé sans mot de passe
>    n'a pas de console utilisable — d'où l'intérêt de `lxc create --password-stdin`.
> 2. **Le getty ne flushe qu'après une entrée.** Au repos il n'envoie rien ; on
>    pousse un `\n` pour faire réafficher son prompt avant de le lire.
> 3. **C'est un PTY.** Sortie et erreur mêlées, écho de l'entrée. On neutralise :
>    `stty -echo`, script passé en base64, sortie encadrée par des sentinelles
>    fabriquées par `printf` (jamais présentes dans la ligne tapée), code retour
>    imprimé puis relu. Dépendance ajoutée : `github.com/coder/websocket`.
>
> Vérifié contre le conteneur 221 : `hostname`, pipelines, variables, codes
> retour fidèles (0/2/3), et `apt-get update` (sortie verbeuse) passent. Ça
> reste une console, pas un execve : pour du binaire ou du colossal, rediriger
> vers un fichier dans le conteneur.

En tant qu'opérateur, je veux `pvecli lxc exec <vmid> -- <cmd>` (et `--shell`),
comme `vm agent exec` le fait pour les VM QEMU, afin de provisionner et piloter
un conteneur LXC (installer Postgres, régler un service) **sans SSH**.

**Le blocage — à énoncer avant tout critère d'acceptation**

Proxmox VE **n'expose AUCUN endpoint REST d'exec pour LXC**. Là où QEMU offre
`POST /nodes/{node}/qemu/{vmid}/agent/exec` + `.../agent/exec-status` (via le
guest-agent), le côté LXC n'a que `config`, `status`, `snapshot`, `migrate`,
`clone`. `pct exec` entre dans les *namespaces* du conteneur **depuis l'hôte**
(`PVE::LXC::Command`) — c'est une commande hôte, hors `/api2/json`. Les outils
matures (modules Ansible Proxmox) ne proposent donc pas d'`lxc exec` par API :
ils exigent SSH ou `pct`.

**Les deux seules voies réelles — choisir AVANT de coder**

1. **termproxy + websocket (API-native, mais fragile).**
   `POST .../lxc/{vmid}/termproxy` rend un `{ticket, port}` ; on ouvre un
   websocket `wss://…/vncwebsocket?port=&vncticket=`, on s'authentifie, puis on
   pilote un **PTY**. Pas de code de retour propre : il faut envelopper la
   commande dans un sentinelle (`cmd; echo __rc=$?__`) et gratter la sortie du
   terminal (écho de la commande, prompt, séquences ANSI, locale). Ça marche
   pour du one-shot lisible, ça ment dès que la sortie est binaire ou volumineuse.
   Contredit l'esprit « sortie bufferisée, code de retour fidèle » de `vm agent exec`.

2. **exec adossé à SSH (fiable, mais brise l'ADN de l'outil).**
   `lxc create` injecte déjà une clé publique ; `lxc exec` lirait l'IP dans la
   config et ferait un `ssh`. Fiable et testable (~80 lignes), MAIS pvecli se
   définit précisément par « API REST, token, TLS épinglé, **sans SSH** » — le
   SSH rouvre exactement la porte que l'outil veut rendre inutile (voir l'aide de
   `login` : « l'accès SSH que l'API doit rendre inutile »).

**Recommandation** — Ne pas livrer un `lxc exec` fragile par défaut. Deux options
défendables : (a) `termproxy` derrière un flag honnête `--tty` qui documente ses
limites ; (b) ne rien ajouter et assumer que le provisioning LXC passe par SSH
ou cloud-init hors pvecli. **Décision à prendre par le propriétaire du projet
avant d'ouvrir un lot.** En l'état, provisionner un LXC se fait en SSH direct
(clé injectée à la création).

**Critères d'acceptation** *(à figer une fois la voie choisie)*
- `pvecli lxc exec <vmid> -- <cmd>` lance la commande et rend sa sortie.
- `--shell` passe l'argument à `/bin/sh -c`.
- Voie 1 : le code de retour de la commande devient celui de pvecli via sentinelle ;
  la fragilité PTY est documentée dans l'aide.
- Voie 2 : l'aide indique explicitement que la commande ouvre une session SSH,
  et l'IP est résolue depuis la config du conteneur.

**Preuve** *(voie à confirmer)*
```bash
pvecli lxc exec 221 --shell 'apt-get install -y postgresql && pg_lsclusters'
```

**Ce que ça doit t'apprendre** — Que la symétrie « VM / conteneur » s'arrête là où
l'hyperviseur s'arrête : une VM est une boîte noire dotée d'un agent qui parle
l'API ; un conteneur partage le noyau de l'hôte, et son « exec » vit côté hôte,
pas côté API. Vouloir la même commande des deux côtés, c'est se heurter à cette
asymétrie — et la bonne réponse est souvent de la nommer, pas de la masquer.
