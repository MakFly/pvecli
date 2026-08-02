# Backlog — post-M7

> Idées et dettes identifiées après la fin de lot M7. Non planifiées : chaque
> story y attend une décision d'architecture avant d'entrer dans un lot.

---

### PVX-076 — Jobs de sauvegarde PLANIFIÉS (`/cluster/backup`)

**Taille** M · **Type** ⚙ · **Statut** ✅ livré — 2026-08-02

En tant qu'opérateur, je veux gérer les sauvegardes **récurrentes** depuis
pvecli, parce que `backup run` (PVX-037) ne prouve qu'une chose : qu'on était là
pour la lancer. La sauvegarde qui existera le jour de la panne est la planifiée.

**Le trou constaté** — post-mortem infra : aucune sauvegarde planifiée sur le
nœud. `pvecli backup` n'exposait que `run|ls|restore` ; `/cluster/backup` n'était
couvert par aucun endpoint, donc aucune planification n'était pilotable.

**Livré** : `internal/pve/backupjob.go` + `cmd/backup_job.go`.
- `pvecli backup job ls|show|create|set|rm` (`update` alias de `set`).
- 5 endpoints ajoutés à `endpoints.go` et à `docs/API-MAP.md`.

**Nommage** — `backup job <verbe>` : `backup` est la famille existante, « job »
est le mot de PVE lui-même (« vzdump backup job »), et `ls|show|create|set|rm`
sont les verbes déjà en place ailleurs (`access token`, `vm snapshot`,
`fw ipset`). `set` plutôt qu'`update` par cohérence avec `vm set` et
`access acl set` — c'est la même opération, écrire des champs sur un objet
existant ; `update` reste accepté en alias.

**Six pièges du schéma, gérés — chacun couvert par un test :**
1. **`prune-backups` vaut `keep-all=1` par défaut**, c'est-à-dire *rien ne
   purge*. Un job planifié sans rétention remplit le stockage jusqu'à la panne
   de disque que la sauvegarde existait pour absorber. `create` **exige** donc
   au moins un `--keep-*`, contrairement à l'API.
2. **`prune-backups` est UNE valeur, pas six champs.** C'est le piège le plus
   coûteux, et il a failli passer : un `set --keep-last 5` qui n'envoie que ce
   compteur **efface `keep-daily=7`**, et la prochaine exécution supprime des
   archives que personne n'avait demandé de supprimer. `set` relit donc la
   rétention du nœud et ne surcharge que les `--keep-*` reçus (read-merge-write),
   et le plan affiche la politique complète.
3. **`remove` est l'interrupteur de la rétention, et il est RENDU par le GET.**
   Une politique écrite mais désarmée par `remove=0` ne purge rien tout en
   rassurant. `BackupJob.Remove` est donc décodé, `ls`/`show` affichent
   « keep-last=3 (INERTE : remove=0) », et `set` **refuse** de modifier une
   rétention inerte sans un `--prune` explicite — le rallumer en douce
   supprimerait des archives sans que rien ne l'annonce.
4. **`enabled` absent veut dire ACTIF** (défaut 1). Le décoder en `int` nu
   afficherait « désactivé » sur un job qui tourne toutes les nuits — d'où le
   `*flexInt` et `IsEnabled()`. Même traitement pour `remove`.
5. **`id` est optionnel et le POST ne le rend pas.** Savoir lequel vient d'être
   créé impose de relire `/cluster/backup` : c'est ce que fait le post-read.
6. **Vider un champ ≠ envoyer une valeur vide.** `Values()` omet les clés à leur
   valeur nulle, donc `--all=false` aurait envoyé `all=` — que le nœud refuse
   (« type check ('boolean') failed »). Les effacements passent par le paramètre
   `delete` du PUT. `--schedule ''` et `--storage ''` sont en revanche refusés :
   ils produiraient un job d'apparence normale et parfaitement inutile.

**Trois garde-fous propres à la CLI** — `set` n'envoie que les drapeaux
explicitement passés hors rétention (un PUT complet remettrait la compression
aux défauts de la CLI sur un job qu'on voulait seulement replanifier) ; `rm` est
marqué `Destructive`, donc la confirmation exige de **retaper l'identifiant**, et
l'aide renvoie vers `set --enabled=false`, réversible, qui est presque toujours
ce qu'on voulait ; un `--dry-run` **n'écrit rien sur stdout**, parce que le
pipeline y rend l'état *avant* écriture et que le rendre comme un résultat
ferait lire une fiction à un `-o json | jq`.

**Changer la CIBLE d'un job (`--vmid` / `--pool` / `--all`) fonctionne** : les
trois clés coexistent dans le fichier de jobs, mais `PVE::API2::Backup::update_job`
efface les deux autres côté nœud avant validation. Il suffit donc d'envoyer
celle qu'on veut. *(Vérifié dans le source du nœud, pas contre un nœud vivant.)*

**Non vérifié en live** — le secret du token n'était pas disponible sur le poste
au moment du développement, et le nœud n'était pas joignable en SSH. Validé par
build, `go vet`, `go test ./...`, seuil de couverture, et l'aide des nouvelles
commandes. Les fixtures `testdata/backup-job{,s}.json` sont **dérivées du
schéma**, pas capturées : à remplacer par une vraie capture (`make capture
ENDPOINT=/cluster/backup`) dès que le token est rétabli.

**Ce que ça doit t'apprendre** — Qu'un défaut d'API peut être un piège de
production. `keep-all=1` est un défaut « sûr » du point de vue de PVE (il ne
supprime rien) et catastrophique du point de vue de l'exploitant (il ne
supprime *jamais* rien). Un bon défaut dépend de ce qu'on protège.

---

### PVX-077 — `access role create` : accorder un privilège sans tout donner

**Taille** S · **Type** ⚙ · **Statut** 🔎 identifié — 2026-08-02

Découvert en documentant PVX-076. Les écritures sur `/cluster/backup` exigent
**`Sys.Modify` sur `/`**. Or une ACL accorde un **rôle**, pas un privilège — et
dans les rôles intégrés du nœud (`testdata/roles.json`, capture réelle), **le
seul qui porte `Sys.Modify` est `Administrator`**. Le donner sur `/`, c'est
`root@pam` sous un autre nom, ce que `access acl set` refuse à juste titre sans
`--i-know-what-im-doing`.

La sortie propre est un **rôle sur mesure** ne portant que ce qu'il faut. Elle
passe par `POST /access/roles`, que pvecli n'expose pas : `access role` est en
lecture seule (`ls|show`). Conséquence pratique : accorder proprement le droit
de gérer les jobs de sauvegarde à un token de moindre privilège **oblige
aujourd'hui à sortir de pvecli** (`pveum role add … -privs "Sys.Audit,Sys.Modify"`
sur le nœud), ce qui contredit l'ADN de l'outil.

**Critères d'acceptation** *(à figer)*
- `pvecli access role create <roleid> --privs Sys.Audit,Sys.Modify`, et le `rm`
  correspondant.
- L'aide dit lesquels des privilèges demandés l'appelant ne détient pas — PVE
  refuse de créer un rôle plus puissant que soi (même règle qu'`ACL.pm`).
- `docs/API-MAP.md` : `POST` et `DELETE /access/roles/{roleid}`.

---

### PVX-075 — Firewall PVE d'un conteneur

**Taille** M · **Type** ⚙ · **Statut** ✅ livré (guest + IPSet) — 2026-08-01

En tant qu'opérateur, je veux piloter le firewall PVE d'un conteneur depuis
pvecli — la best practice Proxmox (filtrage à l'hyperviseur, par-guest, via
l'API) plutôt que du nftables posé à la main dans l'invité.

**Livré** : `internal/pve/firewall.go` + `cmd/firewall.go`.
- `pvecli lxc firewall show|enable|disable|allow|rm <vmid>` — options, règles,
  et pose de `firewall=1` sur net0 (sans ce drapeau, rien ne filtre).
- `pvecli fw ipset ls|create|show|add|del` — IPSets datacenter réutilisables.

**Deux réalités du nœud, gérées :**
1. **Le firewall ne filtre que si le firewall DATACENTER est actif.** L'activer
   sur un nœud qu'on ne joint que par l'API peut couper l'accès (8006/22) sans
   recours console. Donc `enable` pose le guest + la NIC, mais **n'active JAMAIS
   le datacenter** : il se contente d'avertir si celui-ci est éteint. Bascule
   consciente laissée à l'humain.
2. **L'IPSet datacenter exige `Sys.Modify` sur `/`**, hors périmètre d'un token
   PVEAdmin : `fw ipset create` rend alors un 403 explicite. Les règles avec une
   **IP/CIDR directe** en `--source`, elles, ne demandent que le droit firewall
   du guest et fonctionnent. Documenté ; à l'appelant d'élever les droits s'il
   veut des IPSets.

Vérifié en live contre le conteneur 221 : enable (net0 firewall=1, policy_in
DROP), allow 5432/7700 depuis 192.168.1.220, rm, show. Test unitaire
`withFirewallFlag`.

**Reste (hors lot)** : migrer le nftables in-container du LXC infra vers ce
firewall PVE — bloqué tant que le firewall datacenter n'est pas activé (décision
à risque, cf. point 1).

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
