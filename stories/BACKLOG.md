# Backlog — post-M7

> Registre des stories nées après la fin de lot M7, là où `stories/M0…M7` couvrent
> le périmètre du PRD. Certaines sont livrées et rattachées à un lot ; celles qui
> ne le sont pas attendent encore une décision d'architecture.
>
> | Story | Lot | Statut |
> | --- | --- | --- |
> | PVX-074 `lxc exec` | M13 Exploitation | ✅ livré |
> | PVX-075 firewall guest + IPSet | M13 Exploitation | ✅ livré |
> | PVX-076 jobs de sauvegarde planifiés | M13 Exploitation | ✅ livré — écritures murées par `Sys.Modify` |
> | PVX-077 rôles sur mesure (`access role add`) | M13 Exploitation | ✅ livré — mais `403 (/access, Sys.Modify)` le 03-08 : la commande bute sur le mur qu'elle doit franchir |
> | PVX-078 `vm agent exec` | M12 Amorçage & secret | ✅ livré |
> | PVX-079 `pvecli login` | M12 Amorçage & secret | ✅ livré |
> | PVX-080 les trois sources du secret | M12 Amorçage & secret | ✅ livré |
> | PVX-081 timer d'auto-update | M12 Amorçage & secret | ✅ livré |
> | PVX-082 `caddy` au catalogue | — | ✅ livré |
> | PVX-083 définitions de stockage (`/storage`) | — | ✅ livré |
> | PVX-084 `node reboot` | — | ✅ livré |
> | PVX-085 couverture d'API-MAP par (méthode, chemin) | — | ✅ livré |
> | PVX-086 preuve live de M13 | M13 Exploitation | 🔴 **RAF** — bloqué : exige une identité `Administrator` |
> | PVX-087 preuve live de M11 | M11 Accès délégué | 🔴 **RAF** — bloqué : `403 (/access/acl, Permissions.Modify)` |
> | PVX-088 capturer les fixtures de job de sauvegarde | M13 Exploitation | 🔴 **RAF** — dépend de PVX-086 |
> | PVX-089 secret en clair sur le poste Linux | M12 Amorçage & secret | 🔴 **RAF** — réglé sur le Mac le 03-08 |

---

## Reste à faire, au 2026-08-03

Quatre stories, et **trois d'entre elles butent sur la même chose** : déléguer un
droit exige un droit que le compte délégué n'a pas. Mesuré, pas supposé.

### PVX-086 — preuve live de M13 : du rôle sur mesure au job qui survit

**Taille** S · **Type** ⚙ · **Lot** M13 · **Dépend de** PVX-077 · **Statut** 🔴 RAF

C'est le **seul** reste de M13 : le code est livré depuis le 02-08, la séquence
n'est jouée nulle part. Le blocage, mesuré le 03-08 : `access role add` répond
`403 (/access, Sys.Modify)`, `backup job create` répond `403 (/, Sys.Modify)`,
et **`PVEAdmin` — que `pvecli login` attache par défaut — ne porte pas
`Sys.Modify`**. Il faut donc une identité `Administrator`, que l'outil ne
fabrique pas.

```sh
pvecli login --user root@pam --role Administrator --token-name pvectl-adm
pvecli access role add ops-backup-job \
    --privs Sys.Audit,Sys.Modify,VM.Backup,Datastore.Audit,Datastore.AllocateSpace
pvecli access acl set --path / --role ops-backup-job --token automation@pve!pvectl-cc
pvecli backup job create --all --storage local --schedule '02:30' --keep-last 3
```

Attention au **chemin** : les jobs exigent `Sys.Modify` sur `/`, pas sur
`/nodes/pve`. Le rôle `node-sysmodify` existe déjà sur le nœud, mais posé sur
`/nodes/pve` — bon rôle, mauvais chemin, et rien ne le signale.

### PVX-087 — preuve live de M11 : quelqu'un d'autre pilote ses propres VM

**Taille** M · **Type** ⚙ · **Lot** M11 · **Statut** 🔴 RAF

Séquence inchangée depuis M11 : `access user create` → `pool create` → trois ACL
→ `cf access app/policy/token` → `cf route add`. Le mur est de la même famille
que PVX-086, et il est désormais mesuré : `access acl set` répond
`403 (/access/acl, Permissions.Modify)` pour le token `pvectl`.

### PVX-088 — capturer les fixtures de job de sauvegarde

**Taille** S · **Type** 🧪 · **Lot** M13 · **Dépend de** PVX-086 · **Statut** 🔴 RAF

`testdata/backup-job{,s}.json` sont **dérivés du schéma**, pas capturés : ils
prouvent ce qu'on a compris du schéma, pas ce que le nœud rend. PVX-078 a montré
que l'écart existe — PVE 9.2 rend `out-truncated` en *nombre* là où le schéma
annonce un booléen. La capture exige au moins un job existant, donc PVX-086
d'abord.

### PVX-089 — le poste Linux lit encore le secret dans un fichier en clair

**Taille** S · **Type** ⚙ · **Lot** M12 · **Statut** 🔴 RAF

D1 dit « Keychain, jamais de fichier en clair ». Tant que la source est
`~/.config/pvecli/secret`, le câblage par `secret_command` **contourne** la
décision au lieu de l'appliquer. Réglé sur le Mac le 03-08 en pointant
`secret_command` sur le trousseau — `doctor` y est vert sans aucune variable
d'environnement. Reste le poste Linux, où libsecret doit remplacer le fichier.

*(Détail : l'entrée du trousseau du Mac porte encore l'ancien nom
`pvectl-token`/`pvectl`, resté du renommage M8. À normaliser en interactif :
`security add-generic-password -U -s pvecli -a lab -w`.)*

---

### PVX-085 — la couverture d'API-MAP appariait sur le motif seul

**Taille** S · **Type** 🧪 · **Statut** ✅ livré — 2026-08-03

**Le trou constaté** — relevé par le commit de PVX-084, non corrigé par lui.
`TestAPIMapCoverage` cherchait le *motif* d'endpoint comme une sous-chaîne du
fichier entier : un chemin déjà documenté pour **une** méthode se portait donc
garant de **toutes** les autres. Or la règle du PRD §6.3 — « aucun endpoint
écrit de mémoire » — porte sur un *appel*, et un appel est une méthode **et** un
chemin. Le schéma d'un `POST` n'est pas celui du `GET` de même chemin ; c'est
précisément le schéma qu'on est censé avoir vérifié.

**Livré** : `documentedMethods` analyse la **table** de `docs/API-MAP.md` au lieu
de traiter le fichier comme une chaîne, et rend, par motif, l'ensemble des
méthodes réellement documentées — en tenant compte des cellules combinées
`GET · POST`, qui documentent légitimement deux méthodes en une ligne.
`TestAPIMapCoverage` apparie désormais le couple `(méthode, chemin)`.

**Le résultat contredit le soupçon, et c'est le point.** Le commit de PVX-084
annonçait 8 endpoints passant par ce trou, 7 antérieurs. Le test resserré en
trouve **zéro** : les 7 sont documentés, par les cellules `GET · POST`. Le trou
était réel dans la mécanique du test, pas dans le contenu du fichier — et rien
d'autre qu'un test resserré ne pouvait faire la différence entre les deux.

**Un second test, sans lequel le premier ne vaut rien.**
`TestAPIMapCoverageDistinguishesMethods` épingle la discrimination elle-même :
trois méthodes absentes de la table (`POST /version`, `DELETE
/nodes/{node}/status`, `PUT /cluster/resources`) doivent être vues comme
manquantes, dont une sur un chemin qui porte légitimement deux autres méthodes.
Sans lui, un resserrement qui passe du premier coup est indiscernable d'un
resserrement qui ne teste rien.

**Ce que ça doit t'apprendre** — un test de couverture qui ne peut pas échouer
documente une intention, pas un fait. Avant de croire qu'un resserrement a
servi, il faut l'avoir vu refuser quelque chose.

---

### PVX-084 — `node reboot` : redémarrer l'hyperviseur, et prouver qu'il est revenu

**Taille** M · **Type** ⚙ · **Statut** ✅ livré — 2026-08-03

**Le trou constaté** — pvecli savait redémarrer un invité, pas la machine qui
les porte. Redémarrer le nœud après un `apt dist-upgrade` obligeait donc à
sortir vers le shell — la même classe de dette que PVX-077.

**Livré** : `pvecli node reboot [node]`, `--wait` (10 min par défaut) et
`--no-wait`. `RebootNode` dans `internal/pve/nodes.go`, la commande et sa sonde
dans `cmd/node.go`, 1 endpoint en plus dans `endpoints.go` et `docs/API-MAP.md`.

**Le piège central : ce qui compte comme preuve.** L'endpoint **ne rend aucun
UPID** — un nœud ne peut pas rapporter sur une tâche dont l'objet est qu'il
cesse de répondre. Le HTTP 200 est une *acceptation*, pas un succès, donc le
post-read du pipeline de mutation doit venir de l'extérieur. Et la version
évidente de ce post-read est **fausse** : le nœud continue de répondre pendant
plusieurs secondes après avoir accepté la commande, le temps que systemd
descende ses units. Une sonde qui s'arrête au premier GET réussi annonce donc
« revenu » d'une machine qui n'est même pas encore tombée — la réponse la plus
trompeuse que cette commande puisse donner.

D'où la preuve retenue : **un uptime qui REDESCEND**. L'uptime croît de façon
monotone et ne peut chuter qu'à travers un boot ; une valeur plus basse est la
seule observation qu'un nœud n'ayant pas redémarré est incapable de produire.

**Testabilité assumée dans la signature** — `nodeReturnProbe` prend une
*fonction* de statut et son intervalle, pas un client et une constante, pour
que cette garantie puisse être **contredite par un test**. Avec un client et un
`sleep` en dur, `wait` ne serait exerçable que contre un vrai nœud en train de
redémarrer, c'est-à-dire jamais. Deux tests l'épinglent : un nœud qui répond
avec un uptime *croissant* doit expirer plutôt que réussir, et une coupure au
milieu ne doit pas interrompre l'attente. Les deux ont été vérifiés par mutation
(`st.Uptime < before` affaibli en `err == nil`) et vus échouer.

**Rayon de souffle** — le plus large de la CLI, d'où `Destructive` (retaper le
nom du nœud) et deux tests qui épinglent le plan lui-même : il doit nommer que
**tous** les invités sont arrêtés, et que c'est `onboot=1` qui décide lesquels
repartent. Le champ `Rollback` dit qu'il n'y en a pas, plutôt que d'offrir une
commande inverse qui n'existe pas.

**Privilège** : `Sys.PowerMgmt` sur `/nodes/{node}`, **pas** `Sys.Modify` — un
token qui peut réécrire les dépôts APT du nœud ne peut pas pour autant le
power-cycler. Aucun rôle intégré ne le porte hors `Administrator` : il faut un
rôle sur mesure, donc PVX-077.

**Dette relevée en passant, non corrigée** — `TestAPIMapCoverage` apparie sur le
*motif* d'endpoint seul, donc un motif déjà documenté pour une méthode couvre en
silence toutes les autres. **8 endpoints** passent aujourd'hui par ce trou, 7
antérieurs à ce commit. Celui-ci est documenté avec sa méthode ; resserrer le
test et combler les 7 autres est un commit à part — **c'est du RAF**.

---

### PVX-082 — `caddy` au catalogue : le reverse proxy partagé cesse d'être posé à la main

**Taille** S · **Type** ⚙ · **Statut** ✅ livré — 2026-08-02

**Le trou constaté** — `edge-01` (LXC VMID 222, `192.168.1.222`, Debian 13
trixie) a reçu Caddy à la main : dépôt Cloudsmith ajouté par un `curl | gpg
--dearmor` suivi d'un `.list` écrit au clavier, service activé au clavier. Le
catalogue de services (`internal/catalog/assets/catalog.yaml`) n'avait pas de
rôle `caddy`, donc la machine n'était pas reproductible — et ne pouvait pas
porter le tag `svc_caddy` sans créer un groupe Ansible sans aucun play
derrière lui, exactement la dette de métadonnée mensongère que ce labo vient
de passer une journée à éliminer. C'est cette classe de dette que
`TestEveryServiceHasAPlay` rend désormais impossible à réintroduire en
silence.

**Livré** : un service `caddy` complet — manifeste, rôle Ansible, play, tests.
- `internal/catalog/assets/catalog.yaml` : entrée `caddy`, sorties `caddy`
  (version installée) et `caddy.conf_dir` (dossier des fragments de route).
- `internal/catalog/assets/ansible/roles/caddy/` : `defaults`, `tasks`,
  `handlers`, `templates/Caddyfile.j2`.
- `internal/catalog/assets/ansible/pvecli.yml` : play « Caddy », inséré entre
  PostgreSQL et Cloudflare Tunnel — l'ingress doit exister avant le connecteur
  qui l'expose.
- `internal/catalog/catalog_test.go` : `TestEveryServiceHasAPlay`,
  `TestEveryReferencedTemplateIsShipped`,
  `TestEveryDeclaredOutputIsPublishedByItsRole`, `TestEmbeddedTagsAreSorted`.

**Pas de `ports:` dans le manifeste.** Ce Caddy ne termine rien publiquement —
Cloudflare termine le TLS, `cloudflared` le rejoint en HTTP simple sur le
loopback — et il n'écoute **rien du tout** tant qu'aucun projet n'a déposé de
fragment dans `conf.d/` (vérifié sur `edge-01` : `ss -lntp` n'y montre aucun
Caddy). Déclarer 80/443 aurait été un mensonge dans un fichier dont le travail
est d'être vrai — même choix que `docker` et `cloudflared`, qui n'en déclarent
pas non plus. Pas de `requires:` non plus : Caddy est utile sans `cloudflared`
et réciproquement.

**`deb822_repository` + suppression du `.list` écrit à la main.** Le rôle
suit l'idiome déjà posé par `roles/docker` : `get_url` la clé armorée dans
`/etc/apt/keyrings/`, puis `deb822_repository` — apt accepte un `.asc` armoré
en `signed_by`, pas besoin de `gpg --dearmor`. Il supprime en plus
`/etc/apt/sources.list.d/caddy-stable.list`, sans quoi une `edge-01` convergée
porterait le dépôt Caddy deux fois et `apt update` avertirait « configured
multiple times » ; c'est cette suppression qui fait converger `edge-01` vers
l'état d'une machine installée par le rôle depuis le début.

**Reload, pas restart — sauf que la première version du rôle ne pouvait pas
recharger du tout.** Le rôle avait été écrit avec `admin off`, copié tel quel
de l'installation à la main d'`edge-01`. Le premier run réel contre
`edge-01` (LXC VMID 222, `192.168.1.222`, Debian 13 trixie, Caddy 2.11.4, sans
trafic) a convergé 4 tâches puis échoué sur le handler de reload —
`PLAY RECAP` : `ok=14 changed=4 failed=1`. `journalctl -u caddy` :

```
Aug 02 16:30:02 edge-01 caddy[2189]: Error: sending configuration to instance:
performing request: Post "http://localhost:2019/load": dial tcp [::1]:2019:
connect: connection refused
```

Caddy v2 n'a pas de rechargement par signal : `ExecReload` du paquet Debian
appelle `caddy reload`, qui est un client de l'API d'admin. `admin off` ne
laisse donc **aucun** moyen de recharger — seul un restart applique un
changement, et un restart sur un proxy partagé coupe les connexions en cours
de TOUS les projets, pas seulement de celui dont le fragment a changé.
Autrement dit, `edge-01` installée à la main était, sans que personne s'en
aperçoive, un proxy sur lequel aucune route ne pouvait jamais être ajoutée
sans couper tout le trafic en cours. Le rôle corrige ce défaut latent en
liant l'API d'admin au loopback IPv4 (`admin 127.0.0.1:2019`) plutôt que de la
couper : injoignable depuis le réseau, mais toujours là pour le reload local
de systemd. Détail rassurant : le reload raté n'a pas fait tomber le proxy —
`systemctl is-active caddy` est resté `active`, la config en service n'a pas
bougé.

**`caddy validate` tourne deux fois, pas une.** La tâche `template` valide le
Caddyfile *candidat* avant de l'écrire — un fichier cassé n'est donc jamais
posé et jamais rechargé. Mais elle ne se déclenche que quand ce template est
rendu, et ce qui pourrit réellement dans le temps, c'est `conf.d/`, rempli par
les déploiements des projets et hors du contrôle de ce rôle. Une seconde tâche
revalide donc la configuration complète, fragments compris, à chaque passage —
et son échec arrête le play **avant** que les handlers ne se déclenchent, donc
un fragment cassé ne peut jamais atteindre un proxy en service.

**Écrire `admin 127.0.0.1:2019` dans le fichier ne suffit pas — l'instance déjà
en cours l'ignore.** Ce premier correctif a été appliqué et le nouveau
Caddyfile écrit sur `edge-01` ; le run suivant a de nouveau échoué :
`PLAY RECAP` : `ok=14 changed=1 failed=1`, avec `journalctl -u caddy` montrant
le même refus de connexion sur `[::1]:2019`. Raison : le *fichier* a changé,
mais le *processus* tournait encore sous l'ancien `admin off`, donc sans aucun
socket admin à joindre — et `caddy reload` étant lui-même un client de cette
API, il ne peut jamais être ce qui la fait naître. Le rôle ajoute donc une
sonde (`wait_for` sur `127.0.0.1` seul — mesuré sur `edge-01` : l'admin API ne
bind que la loopback IPv4, même quand `::1` existe sur l'hôte), et ne
redémarre Caddy que si cette sonde échoue — un `block`/`rescue`, pas un
`when` sur un résultat bouclé, un restart, une fois, pour payer la transition
`admin off` → `admin 127.0.0.1:2019`, plutôt que de laisser cette manip à la
mémoire de quelqu'un. Ce n'est pas un « reload, et restart si ça échoue » :
un reload échoue aussi quand un fragment de projet est sémantiquement refusé,
et redémarrer dans ce cas couperait tous les projets pour rien — seule
l'injoignabilité de l'instance justifie le restart. Au run suivant, la sonde
trouve le socket et ne change rien, ce qui garde `--idempotence` à `changed=0`.

**Preuve d'idempotence — jouée contre `edge-01`, pas déduite.** L'hôte a
d'abord été remis dans son état d'installation manuelle (Caddyfile écrit à la
main avec `admin off`, `caddy-stable.list` restauré, `caddy.sources` et la clé
`/etc/apt/keyrings/caddy.asc` retirées, `systemctl restart caddy` pour que le
processus tourne bien sous `admin off`), afin que le premier passage parte du
vrai point de départ. Inventaire d'un seul hôte, plus `--limit edge-01` : les
plays `svc_docker` / `svc_postgresql` / `svc_cloudflared` ne trouvent aucun
hôte et sont passés.

```
# 1er passage — convergence depuis l'installation manuelle
edge-01  : ok=16   changed=6    unreachable=0    failed=0    skipped=1    rescued=1    ignored=0

# 2e passage — rien à faire
edge-01  : ok=15   changed=0    unreachable=0    failed=0    skipped=1    rescued=0    ignored=0
```

Les 6 changements du premier passage sont exactement les écarts entre l'install
à la main et le rôle : la clé de dépôt posée sous `/etc/apt/keyrings/`, la
source `.list` manuelle retirée, le dépôt redéclaré en `deb822`, le Caddyfile
regénéré, le restart unique qui fait naître l'admin API, puis le reload du
handler. Le `rescued=1` est la sonde d'admin API qui échoue et déclenche ce
restart — il ne réapparaît pas au second passage.

État final relu sur l'hôte : `caddy` `active`, admin sur `127.0.0.1:2019`,
`/etc/apt/sources.list.d/` ne contient plus que `caddy.sources`, et
`caddy validate` ne journalise plus l'avertissement « Caddyfile input is not
formatted ».

**Le chemin d'échec a été joué, pas seulement raisonné.** Un fragment
volontairement cassé (`conf.d/zz-broken.caddy`) a été déposé sur `edge-01`,
dans les deux états qui comptent :

- *instance déjà convergée* — la tâche « Revalider la configuration complète »
  échoue (`ok=11 changed=0 failed=1`) en nommant le fichier et la ligne fautifs
  (`/etc/caddy/conf.d/zz-broken.caddy:1 import chain [...]`), le play s'arrête
  **avant** les handlers, et le proxy reste `active` sur son ancienne
  configuration valide ;
- *instance encore sous `admin off`, celle où le rôle voudrait redémarrer* —
  c'est le `validate` de la tâche `template` qui échoue le premier
  (`ok=8 changed=0 failed=1`), sur le fichier **candidat**, donc avant même la
  sonde et le restart. Le proxy reste `active`.

C'est ce qui rend l'ordre des tâches sûr : le seul cas où le rôle redémarre
Caddy est un cas où la configuration complète a déjà été validée sur le
candidat. Un fragment cassé ne peut donc ni être rechargé, ni faire tomber au
restart un proxy qui tournait. Après retrait du fragment, l'hôte reconverge
(`changed=3`) puis retombe à `changed=0`.
### PVX-083 — DÉFINITIONS de stockage (`/storage`)

**Taille** M · **Type** ⚙ · **Statut** ✅ livré — 2026-08-02

En tant qu'opérateur, je veux **déclarer** un stockage depuis pvecli, parce que
tant qu'aucun n'accepte le contenu `backup` ailleurs que sur le disque du nœud,
la seule destination possible est `local` — et une sauvegarde qui vit sur le
disque de ce qu'elle protège meurt avec lui.

**Le trou constaté** — suite directe de PVX-076 : `backup job create --storage`
exige une destination, et le lab n'en a aucune de valable (capture réelle
`testdata/storage-defs.json` : `local` en `dir`, `local-lvm` en `lvmthin`).
`pvecli storage` ne pilotait que le **contenu** d'un stockage
(`ls|content|download-url|upload|rm <storage> <volid>`) ; `/storage`, l'endpoint
de **cluster** qui porte les définitions, n'était couvert par aucun endpoint.

**Livré** : `internal/pve/storagedef.go` + `cmd/storage_def.go`.
- `pvecli storage def ls|show|add|set|rm` (`list`, `create`, `update`, `delete`
  en alias).
- 5 endpoints ajoutés à `endpoints.go` et à `docs/API-MAP.md`.

**Nommage** — `storage def`, dans un **sous-nom**, et c'est une décision de
sécurité, pas de style. `pvecli storage rm <storage> <volid>` **existe déjà** et
supprime un VOLUME. Poser à côté un `storage rm <storage>` à un seul argument
fabriquerait le pire piège possible : **oublier le volid ne rendrait pas une
erreur, ça supprimerait la définition du stockage entier au lieu d'une ISO.**
Le précédent du dépôt est `backup job` face à `backup run` — le parent agit sur
le contenu, le sous-nom décrit l'objet. `add` (alias `create`) comme
`access role add`, `set` (alias `update`) comme partout, `rm` (alias `delete`)
de même.

**Sept pièges du schéma, gérés — chacun couvert par un test :**
1. **`export`, `share`, `datastore`, `path` et `type` sont POST-only.** Ils
   n'existent pas dans le schéma du PUT : un stockage ne se **repointe pas**
   ailleurs. `set` refuse ces quatre drapeaux **localement**, avant tout appel,
   en nommant la sortie (supprimer puis recréer) et en rappelant que `rm`
   n'efface aucune donnée. Sans ça, le nœud rend un 400 qui ne dit pas pourquoi.
2. **`GET /storage/{storage}` répond 500 sur un nom inconnu**, pas 404
   (`storage 'x' does not exist`). Le motif du dépôt « une erreur au pre-read =
   l'identifiant est libre » (`access user create`, `access token create`) reste
   juste, mais l'erreur brute parle d'erreur **interne du nœud** pour un simple
   nom absent — c'est écrit dans l'aide de `show`.
3. **L'ordre de `content` n'est pas stable.** Le même `local` rend
   `backup,import,vztmpl,iso` par l'index et `import,backup,vztmpl,iso` par le
   détail, à une seconde d'intervalle — les deux captures le prouvent. Toute
   comparaison octet à octet conclurait à un changement inexistant :
   `pve.SameContentTypes` compare des **ensembles**, et `Storage.ContentTypes()`
   / `Accepts()` ont été rebranchés dessus plutôt que dupliqués.
4. **Le `digest` du PUT couvre TOUT `/etc/pve/storage.cfg`**, pas l'entrée
   seule : les deux stockages du lab portent le même `921a2c39…`. Il est renvoyé
   depuis le pre-read — un changement sur **n'importe quel** stockage entre la
   lecture et l'écriture fait donc échouer le PUT. L'échec est bruyant et
   rejouable : c'est la garde anti-écrasement concurrent voulue par PVE, pas un
   défaut.
5. **`content` est REMPLACÉ, pas fusionné.** Contrairement à `prune-backups`
   d'un job (PVX-076), l'unité du drapeau CLI est ici **la même** que celle de
   l'API — une chaîne à virgules pour une chaîne à virgules. C'est donc un
   remplacement honnête et non un écrasement silencieux : aucun read-merge-write
   n'est nécessaire, mais l'aide de `set` dit qu'il faut écrire la liste
   complète.
6. **`prune-backups` d'un stockage est une option string**, exposée en UN
   drapeau `--prune-backups "keep-last=3,keep-daily=7"` — donc remplacement de
   la politique entière, et c'est dit. Six drapeaux `--keep-*` auraient rejoué
   la classe d'erreur « option-string clobber » déjà payée sur `backup job`.
7. **`password` existe sur le POST et sur le PUT.** `service.redactValue` le
   masque déjà dans le plan ; c'est **vérifié par un test**, pas supposé — la
   sortie complète (stdout ET stderr) est fouillée pour le secret.

**Le mot de passe — décision D1 appliquée telle quelle.** Ni drapeau, ni fichier
de configuration : `PVE_STORAGE_PASSWORD`, sinon saisie masquée via
`golang.org/x/term` si le terminal le permet, sinon refus explicite. Un drapeau
serait visible dans `ps` par tout utilisateur de la machine et resterait dans
l'historique du shell. Le modèle est recopié de `resolveNewPassword`
(`cmd/access.go`). Il est **exigé** pour `pbs` (sans lui la sauvegarde ne part
jamais, et l'échec est silencieux et différé) et **optionnel** pour `cifs`, où
`--guest` monte le partage en invité — sur le modèle de `--no-password` de
`access user create` : se passer d'un secret doit être un choix énoncé. Sur
`set`, `--password` est un **booléen** qui déclenche la resaisie.

**Deux exigences plus strictes que l'API, pour la même raison :**
- **`--content` est obligatoire** alors que l'API le donne pour optionnel. Sans
  lui PVE choisit un défaut qui peut très bien ne pas contenir `backup`, et on
  obtient un stockage d'apparence normale sur lequel aucune sauvegarde
  n'atterrira. Même logique que la rétention exigée par `backup job create`.
- **Un `pbs` déclaré avec autre chose que `backup` est refusé.** Le nœud
  l'accepterait et n'y écrirait jamais rien : un stockage définitivement vide et
  d'apparence saine.

**Le cœur de `rm`.** L'appel supprime l'**entrée de configuration**, pas les
données : archives NFS/CIFS et snapshots PBS restent en place, redéclarer le
stockage retrouve son contenu. C'est le miroir exact de `backup job rm`. Ce qui
casse, ce sont les **jobs qui écrivaient dessus** : le pre-read lit
`/cluster/backup` et les nomme un par un, parce qu'un job privé de destination
échoue à chaque exécution, en silence. Cette lecture exige `Sys.Audit` sur `/` ;
un 403 **n'échoue pas** la suppression, il annonce que la vérification n'a pas
pu être faite — et que ce n'est pas la preuve qu'aucun job n'en dépend.
L'alternative réversible (`storage def set <id> --disable`) est proposée.

**La même garde sur `set`, ajoutée en relecture.** Elle manquait, et son absence
rendait `rm` contournable par le chemin le plus banal : retirer `backup` de
`--content`, ou passer `--disable`, a **exactement** la conséquence d'une
suppression — les jobs planifiés continuent d'exister, leur prochaine exécution
reste annoncée, et ils échouent à chaque passage. `set` marque donc ces deux
modifications `Destructive` et nomme les jobs concernés, comme `rm`. La
comparaison se fait avec `Accepts()`, jamais sur la chaîne : l'ordre de `content`
n'est pas stable.

**Un défaut trouvé en relecture, et corrigé.** `--password` est un booléen, mais
il était lu comme les autres drapeaux (`Flags().Changed`) : un
`storage def set <id> --password=false` marquait donc la clé comme modifiée et
envoyait `password=` — c'est-à-dire **effaçait** le mot de passe enregistré, pour
avoir demandé le contraire. Le partage aurait cessé de se monter sans que rien
ne dise pourquoi. Seul un `--password` vrai déclenche désormais l'envoi ; un test
fige les deux cas.

**La bonne nouvelle du moindre privilège** — les trois écritures exigent
**`Datastore.Allocate` sur `/storage`**, et **pas** `Sys.Modify`. Le rôle
intégré `PVEDatastoreAdmin` le porte déjà : contrairement à `/cluster/backup`
(PVX-076/077), aucun rôle sur mesure n'est nécessaire. C'est dit dans l'aide.

**Portée volontairement restreinte** — seuls `nfs`, `cifs`, `pbs` et `dir` sont
acceptés par `--type`. Ce sont les quatre dont le schéma a été vérifié champ par
champ ; les autres (`lvm`, `zfspool`, `ceph`…) sont refusés avec un message qui
renvoie à l'interface web. Accepter un type dont on n'a pas vérifié les champs
reviendrait à écrire un payload de mémoire — ce que le PRD §6.3 interdit.

**Non vérifié en live** — aucune écriture n'a été émise contre le nœud : il
héberge une production. Les verbes mutants sont couverts en unitaire et en
`--dry-run` uniquement, avec des assertions sur le **corps réellement émis**
(`storeWriteServer`, `r.PostForm`) et non sur la sortie d'un `--dry-run`.

**Ce que ça doit t'apprendre** — Qu'un nom de commande peut être un dispositif
de sécurité. `storage rm <storage>` et `storage rm <storage> <volid>` sont
distinguables par arity pour un parseur, jamais pour un opérateur fatigué : la
seule façon de rendre l'erreur impossible est de ne pas offrir la forme
ambiguë.

---

### PVX-076 — Jobs de sauvegarde PLANIFIÉS (`/cluster/backup`)

**Taille** M · **Type** ⚙ · **Lot** M13 · **Statut** ✅ livré — 2026-08-02

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

**Ce qui est vérifié en live, et ce qui ne l'est pas** — le secret du token,
introuvable au moment du développement, a été rétabli le 02-08 (PVX-080). Depuis :
`backup job ls` répond contre le nœud réel et **ne liste aucun job planifié** —
c'est le constat qui a motivé la story, désormais mesuré et non plus supposé.
Les **écritures** (`create`, `set`, `rm`), elles, ne sont toujours pas exercées :
elles exigent `Sys.Modify` sur `/`, que le token n'a pas, et renvoient
`403 Permission check failed (/, Sys.Modify)`. Cf. PVX-077.

Les fixtures `testdata/backup-job{,s}.json` restent **dérivées du schéma**, pas
capturées : à remplacer par une vraie capture (`make capture
ENDPOINT=/cluster/backup`) — ce qui exige au moins un job existant, donc
PVX-077 d'abord.

**Ce que ça doit t'apprendre** — Qu'un défaut d'API peut être un piège de
production. `keep-all=1` est un défaut « sûr » du point de vue de PVE (il ne
supprime rien) et catastrophique du point de vue de l'exploitant (il ne
supprime *jamais* rien). Un bon défaut dépend de ce qu'on protège.

---

### PVX-077 — rôles sur mesure : accorder un privilège sans tout donner

**Taille** S · **Type** ⚙ · **Lot** M13 · **Statut** ✅ livré — 2026-08-02

Découvert en documentant PVX-076, puis **confirmé contre le nœud le 02-08** :
`fw ipset create`, l'activation du firewall datacenter et la création d'un job
de sauvegarde renvoient toutes trois
`403 Permission check failed (/, Sys.Modify)`.

Les écritures sur `/cluster/backup` exigent **`Sys.Modify` sur `/`**. Or une ACL
accorde un **rôle**, pas un privilège — et dans les rôles intégrés du nœud
(`testdata/roles-with-custom.json`, capture réelle), **le seul qui porte
`Sys.Modify` est `Administrator`**. Le donner sur `/`, c'est
`root@pam` sous un autre nom, ce que `access acl set` refuse à juste titre sans
`--i-know-what-im-doing`. La sortie propre est un **rôle sur mesure**, et elle
passait par des endpoints que pvecli n'exposait pas : `access role` était en
lecture seule (`ls|show`).

**Livré** : écritures dans `internal/pve/access.go` + `cmd/access.go`.
- `pvecli access role add|set|rm` (`create`, `update`, `delete` en alias).
- 3 endpoints ajoutés à `endpoints.go` et à `docs/API-MAP.md`.

**Mesuré contre le nœud le 03-08, et c'est pire que ce qui était écrit.** La
commande livrée ici pour franchir le mur **bute sur le même mur** :

```
$ pvecli access role add ops-backup-job --privs Sys.Audit,Sys.Modify,…
Error: POST /access/roles : HTTP 403 — Permission check failed (/access, Sys.Modify)
```

Créer le rôle de moindre privilège exige donc soi-même `Sys.Modify` sur
`/access`, que seul `Administrator` porte. Et l'amorçage prévu ne suffit pas
non plus : **`PVEAdmin` ne porte pas `Sys.Modify`** — ses seuls privilèges
`Sys.*` sont `Sys.Audit`, `Sys.Console`, `Sys.Syslog` — alors que `pvecli login`
l'attache par défaut. Le franchissement exige donc `pvecli login --role
Administrator`, ou un mot de passe `root@pam`, à chaque fois.

Relevé au passage : un rôle sur mesure `node-sysmodify` (`Sys.Audit`,
`Sys.Modify`) **existe déjà** sur le nœud, mais posé sur `/nodes/pve`. Les jobs
de sauvegarde exigent `Sys.Modify` sur `/` : une ACL au bon rôle et au mauvais
chemin n'accorde rien, et rien ne le signale.

**Ce qui reste à faire pour clore M13** — une seule séquence, qui exige une
identité `Administrator` que l'outil ne fabrique pas :

```sh
pvecli login --user root@pam --role Administrator --token-name pvectl-adm
pvecli access role add ops-backup-job \
    --privs Sys.Audit,Sys.Modify,VM.Backup,Datastore.Audit,Datastore.AllocateSpace
pvecli access acl set --path / --role ops-backup-job --token automation@pve!pvectl-cc
pvecli backup job create --all --storage local --schedule '02:30' --keep-last 3
```

**Ce que ça doit t'apprendre** — un privilège ne se délègue pas en une étape :
accorder `Sys.Modify` exige `Sys.Modify` sur `/access`. La chaîne d'amorçage ne
se termine jamais dans l'outil, elle se termine sur une identité qu'il n'a pas
fabriquée. Une CLI d'automatisation peut retirer le SSH de l'exploitation
quotidienne ; elle ne peut pas le retirer de sa propre racine de confiance.

**Nommage** — `add` plutôt que `create` comme nom principal, parce que PVE dit
lui-même `pveum role add` ; `create` reste accepté en alias pour rester
cohérent avec `access user create`, `access token create` et `backup job
create`. `set` (alias `update`) comme `vm set`, `access acl set` et `backup job
set` — c'est la même opération, écrire des champs sur un objet existant. `rm`
(alias `delete`) comme partout ailleurs. `--i-know-what-im-doing` réutilise le
mot de `access acl set` plutôt que d'inventer un troisième vocabulaire de
forçage.

**Le piège central : `append`.** Le `PUT /access/roles/{roleid}` **REMPLACE**
toute la liste de privilèges. Le schéma expose bien un `append` booléen qui
ferait l'union — **pvecli ne l'envoie jamais**, et c'est une décision
d'architecture, pas un oubli : avec lui, l'union se ferait *côté nœud*, donc la
liste résultante ne serait connue qu'**après** l'écriture et un `--dry-run` ne
pourrait pas la montrer. Un plan qui n'affiche pas ce qu'il produit est une
fiction polie. pvecli relit donc les privilèges actuels, calcule ici, et envoie
la liste finale complète — c'est aussi la **seule** façon de retirer un
privilège, l'API n'ayant aucune primitive de retrait. Même classe d'erreur que
`prune-backups` en PVX-076 : l'unité de mise à jour côté API est plus grosse que
l'unité de drapeau côté CLI.

**Cinq garde-fous, chacun couvert par un test :**
1. **`--privs` est obligatoire sur `add`**, alors que l'API le donne pour
   optionnel. Un rôle sans privilège est `NoAccess` sous un autre nom : il
   s'attribue, et il n'accorde rien. Même logique que la rétention exigée par
   `backup job create`.
2. **Toute PERTE de privilège est `Destructive`** — donc la confirmation exige
   de retaper le nom du rôle, et le pre-read nomme les privilèges perdus un par
   un. Ce n'est pas une destruction, ça en a la conséquence : les identités qui
   portent le rôle la subissent sans qu'aucune ACL soit relue. Précédent du
   dépôt : `access user create`, `Destructive` sans rien détruire.
3. **Les privilèges sont validés contre le NŒUD, jamais contre une liste codée
   en dur** : l'univers vient de `GET /access/roles/Administrator`, qui les
   porte tous. Une casse fautive (`Sys.modify`) est corrigée dans le refus. Si
   cette lecture échoue, la commande ne tombe pas : la validation est un
   confort, pas une précondition.
4. **Les rôles intégrés sont refusés.** La vérité est `special == 1` dans
   `GET /access/roles` (la capture prouve qu'un rôle non intégré, `URLFetch`,
   coexiste avec eux) ; le motif de nom sert de second filet quand la liste
   n'est pas lisible. `--i-know-what-im-doing` lève le refus sur `rm`.
5. **`rm` liste les ACL qui référencent le rôle** avant de supprimer — en
   disant que `GET /access/acl` est **filtré** par les droits de l'appelant :
   une liste vide veut dire « aucune ACL VISIBLE », pas « aucune ACL ». Un 403
   sur cette lecture n'échoue pas la commande, il annonce que la vérification
   n'a pas pu être faite.

**Non vérifié en live** — aucune écriture n'a été émise contre le nœud : il
héberge une production, et le token courant n'a de toute façon pas `Sys.Modify`
(c'est le problème que la story résout). Validé par build, `go vet`,
`golangci-lint`, `go test ./...`, le seuil de couverture, et des tests qui
assertent sur le **corps réellement émis** — pas sur la sortie d'un `--dry-run`.

**Le critère d'acceptation initial était FAUX, et le source le prouve.** Il
disait « PVE refuse de créer un rôle plus puissant que soi (même règle
qu'`ACL.pm`) ». Lecture de `pve-access-control/src/PVE/API2/Role.pm` : `create_role`
ne fait que trois choses — le contrôle de l'espace de noms, un refus si le rôle
existe déjà, puis `add_role_privs`. **Aucune comparaison avec les privilèges de
l'appelant.** Un compte portant `Sys.Modify` sur `/access` peut donc fabriquer un
rôle qui porte `Permissions.Modify`, qu'il ne détient pas lui-même. La barrière
n'est pas à la création du rôle, elle est à son ATTRIBUTION (`ACL.pm:190`, déjà
documenté). Rien de tel n'est donc affirmé dans l'aide, et la story ne le
réclame plus.

**Deux règles du nœud, découvertes en relecture et transcrites depuis le source
plutôt que devinées :**
- `create_role` fait `raise_param_exc` sur `$role =~ /^PVE/i`. Le préfixe `PVE`
  est un **espace de noms réservé**, comparé **sans tenir compte de la casse** :
  `pveBackup` est refusé comme `PVEBackup`. Conséquence pratique et
  contre-intuitive : **un rôle sur mesure ne peut PAS s'appeler
  `PVEBackupJobAdmin`**. La première version de `IsBuiltinRoleName` comparait
  avec la casse — elle laissait donc passer `pveBackup` jusqu'à l'écriture, pour
  le faire échouer côté nœud *après* la confirmation. Corrigé, et figé par un
  test qui nomme explicitement `PVEBackupJobAdmin`.
- `update_role` et `delete_role` meurent sur `role_is_special()`, dont la table
  contient `NoAccess`, `Administrator` et tous les `PVE…` générés. Le refus
  local n'invente donc rien : il dit la même chose, plus tôt.
- `add_role_privs` meurt sur `invalid privilege '<p>'` — le nœud valide les noms
  de privilèges en correspondance **exacte**. La validation locale contre les
  privilèges d'`Administrator` ne fait que rendre ce refus lisible et le déplacer
  avant la confirmation.

**Ce que ça doit t'apprendre** — Qu'un modèle d'autorisation peut n'avoir aucune
granularité intermédiaire. Entre « ce privilège » et « tout », PVE n'offre rien
tant qu'on n'a pas fabriqué l'objet qui manque. La CLI qui ne sait que lire les
rôles condamne son utilisateur à `Administrator`.

---

### PVX-075 — Firewall PVE d'un conteneur

**Taille** M · **Type** ⚙ · **Lot** M13 · **Statut** ✅ livré (guest + IPSet) — 2026-08-01

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

**Taille** L · **Type** ⚙ · **Lot** M13 · **Dépend de** PVX-078 (`vm agent exec`) · **Statut** ✅ livré (voie 1, console termproxy) — 2026-08-01

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

---

### PVX-078 — `vm agent exec` : une commande dans une VM, sans SSH

**Taille** M · **Type** ⚙ · **Lot** M12 · **Statut** ✅ livré — 2026-08-01

`POST .../qemu/{vmid}/agent/exec` puis `.../agent/exec-status` jusqu'à `exited`.
Deux détails que le schéma seul ne donne pas : `command` est **répété une fois
par argument** — une chaîne unique serait lue comme un exécutable dont le nom
contient des espaces, et il n'y a **pas de shell** derrière ; et les champs de
retour sont en **tirets** (`out-data`, `err-data`, `out-truncated`).

**Correctif payé en live** — PVE 9.2 sérialise `out-truncated` / `err-truncated`
en **nombre** là où le schéma annonce un booléen : tout `vm agent exec` plantait
au décodage, sur un champ dont personne ne lit jamais la valeur. Type tolérant
`flexBool`. Second correctif : le `pid` était perdu quand le délai expirait
pendant la requête de scrutation — on ne pouvait plus aller lire le résultat
d'une commande qui, elle, avait bien tourné.

---

### PVX-079 — `pvecli login` : fabriquer le premier token sans SSH

**Taille** M · **Type** ⚙ · **Lot** M12 · **Statut** ✅ livré — 2026-08-01

Le lot répare une circularité : toutes les commandes s'authentifient par token,
et aucune ne savait en créer un — il fallait un accès SSH au nœud pour lancer
`pveum`, exactement l'accès que cette CLI existe pour rendre inutile.

`POST /access/ticket` échange un mot de passe (saisi sans écho, ou `PVE_PASSWORD`)
contre un ticket ; avec lui, `login` crée l'utilisateur s'il manque, crée le
token, lit son secret — **PVE ne le montre qu'une fois** — attache le rôle sur
son chemin en propagation, et écrit le `token_id` dans la configuration. Le
secret n'est jamais écrit sur le disque : il est imprimé une fois.

**Le ticket ramène le CSRF, que le token avait le droit d'ignorer.** PVX-003
avait établi *pourquoi* un token en est dispensé (il n'est jamais attaché
automatiquement à une requête, donc rien à protéger) ; `login` est le seul
chemin du client qui repasse par un ticket, donc le seul qui doive poser
`CSRFPreventionToken` — et seulement sur les méthodes autres que `GET`.

**Rejouable, sauf sur un point** : utilisateur et ACL sont réappliqués sans
bruit, un token existant est laissé en place — et ne peut pas rendre son secret
une seconde fois. Pour repartir de zéro, il faut le détruire.

Preuve : `automation@pve!pvectl-cc` fabriqué par cette commande le 2026-08-01.

---

### PVX-080 — Les trois sources du secret, et `auth status`

**Taille** M · **Type** ⚙ · **Lot** M12 · **Statut** ✅ livré — 2026-08-02

Le secret n'avait qu'une source : l'environnement. Donc une variable à
réexporter dans chaque shell, et rien à quoi se raccrocher quand elle manque.

Trois sources désormais, la première qui répond gagne :

1. `PVE_API_TOKEN_SECRET` ;
2. une commande dont la **sortie standard EST** le secret — `secret_command`
   dans le contexte (`pass show pve/token`, `cat …`) ;
3. le trousseau du système — libsecret sous Linux, Keychain sous macOS, alimenté
   par `pvecli auth set-secret` (saisie masquée ou `--stdin`).

`secret_source` restreint la recherche à une seule d'entre elles, pour qu'une
erreur se **voie** au lieu d'être rattrapée en silence par une source moins
fraîche. Le secret n'est toujours jamais acceptable en argument (`ps`,
historique du shell) ni dans le fichier de configuration — décision D1, imposée
par le code depuis PVX-002.

**L'incident qui a produit la story.** Le secret de `pvectl-cc` a été déclaré
introuvable sur le poste Linux et cherché comme une perte. Il était sur le
disque depuis sa création, dans `~/.config/pvecli/secret` : aucune des trois
sources n'y pointait. Câblé en une ligne
(`pvecli config set secret_command "cat …/secret"`), `doctor` repasse au vert
sans qu'aucune variable d'environnement ne soit exportée.

D'où la formulation de `auth status`, qui est la vraie livraison : **il répond
« ABSENT » quand le secret n'est pas *atteignable*, jamais quand il n'existe
pas** — il ne peut pas connaître la seconde question. Même famille que
« aucune ACL **VISIBLE** » (PVX-032).

---

### PVX-081 — Le poste tient `pvecli` à jour tout seul

**Taille** S · **Type** ⚙ · **Lot** M12 · **Statut** ✅ livré — 2026-08-01

`scripts/autoupdate/` : un `.timer` systemd `OnCalendar=daily` +
`Persistent=true` — sans quoi un déclenchement tombé pendant que le poste est
éteint est simplement sauté et la mise à jour n'arrive jamais — et
`RandomizedDelaySec=1h`, parce qu'une machine qui frappe GitHub à heure fixe est
une machine de plus dans la pointe. Une fois par jour suffit : `pvecli` n'est pas
un service exposé, et le quota anonyme de l'API GitHub est de 60 appels/h par IP.

**Le garde-fou qui compte** — un binaire issu d'un `make install` porte la
version `dev` et contient presque toujours **plus** que la dernière release. Le
remplacer la nuit ferait disparaître le correctif en cours de test, et la panne
du lendemain se chercherait partout sauf là. `install.sh` refuse donc d'écraser
un binaire `dev` et dit comment repasser volontairement sur la release publiée.
