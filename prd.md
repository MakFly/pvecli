# PRD — `pvecli` : CLI d'administration Proxmox VE

> **Statut** : v0.1 — brouillon fondateur
> **Auteur** : Kevin
> **Date** : 2026-07-31
> **Cible** : nœud Proxmox VE de lab — `https://192.0.2.23:8006`
> **Source pédagogique** : [MakFly/proxmox-practice-lab](https://github.com/MakFly/proxmox-practice-lab)
> **Backlog** : [`stories/README.md`](stories/README.md) — 55 user stories réparties en 8 lots
> **Suivi** : statut et journal vivants hors dépôt ; `docs/LEARNING-LOG.md` en porte la trace publique.
> **Lab** : nœud mono-serveur documenté hors dépôt (accès, ACL, empreinte TLS).

---

## 1. Contexte

Le repo `proxmox-practice-lab` est un parcours d'apprentissage Proxmox VE en 14 chapitres (bilingue FR/EN), accompagné :

- d'artefacts IaC réels — `infra/terraform` (provider `bpg/proxmox`, clone de VM cloud-init) et `infra/ansible` (Nginx natif, Docker/Caddy) ;
- d'un skill agent `.agents/skills/proxmox-api/` qui documente une **discipline d'appel de l'API PVE** (auth par token, séquence d'inspection minimale, contrat de mutation, gestion des UPID, triage d'erreurs) et fournit un client bash `pve-api` ;
- d'un dashboard local (Bun / TanStack Start) pour suivre la progression.

Le manuel enseigne le *raisonnement*. Ce qui manque : un **outil qu'on construit soi-même**, qui force à traverser l'API endpoint par endpoint et à relier ce que fait l'interface web à ce que fait réellement `/api2/json`.

État vérifié du lab :

```
$ curl -sk -o /dev/null -w "%{http_code}" https://192.0.2.23:8006/api2/json/version
401        # API joignable, authentification requise → token à créer
```

## 2. Vision produit

> **`pvecli` est une CLI Go qui pilote un homelab Proxmox de bout en bout — de l'inspection au CRUD complet — et qui sert de pont entre l'API PVE et la chaîne Terraform / Ansible.**

Ce n'est pas un clone de `pvesh` (qui n'existe que *sur* le nœud, en SSH). C'est un client **distant**, **typé**, **scriptable**, avec des garde-fous que l'interface web n'offre pas : `--dry-run`, sortie JSON, suivi de tâches, génération d'inventaire.

## 3. Objectifs d'apprentissage

Objectif global : **apprentissage de A à Z**. Deux axes retenus, dans cet ordre de priorité.

### 3.1 Axe A — Maîtriser l'API Proxmox VE

| Compétence | Preuve attendue |
| --- | --- |
| Modèle d'authentification | Token dédié `automation@pve!pvectl` créé, à privilèges restreints, expirant ; `root@pam` jamais utilisé |
| Structure de l'arbre `/api2/json` | Savoir situer un endpoint (cluster / nodes / access / storage) sans documentation |
| Cycle de vie QEMU & LXC | Créer, cloner, configurer, snapshotter, migrer, détruire — via API uniquement |
| Tâches asynchrones (UPID) | Aucune commande ne déclare « succès » sur un HTTP 200 ; toutes attendent l'`exitstatus` |
| ACL & privilege separation | Reproduire un `403` volontairement, le diagnostiquer, le corriger par ACL et non par élévation |
| Storage & content types | Comprendre pourquoi un `.qcow2` ne se dépose pas sur un storage `content=iso` |
| Backup / restore | Restauration réellement testée, validée par relecture de la ressource |

### 3.2 Axe B — Automatisation IaC

| Compétence | Preuve attendue |
| --- | --- |
| Frontière CLI ↔ Terraform | Savoir dire *qui possède quoi* : ce que `pvecli` crée à la main vs ce que Terraform possède dans son state |
| Détection de dérive | `pvecli iac drift` compare l'état live (API) au state Terraform et liste les écarts |
| Inventaire dynamique | `pvecli iac inventory` génère un inventaire Ansible depuis l'API (tags, agent QEMU, IP réelles) |
| Idempotence | Rejouer une commande `apply` deux fois ne produit aucun changement |
| Chaînage complet | `template → clone (TF) → inventaire (pvecli) → configuration (Ansible) → validation (pvecli)` |

### 3.3 Non-objectifs (v1)

- Pas de TUI interactive (une `pvecli top` pourra arriver plus tard).
- Pas de support cluster multi-nœuds avancé (HA, corosync, replication) — le lab est mono-nœud.
- Pas de PBS / PMG / PDM. **Uniquement PVE.**
- Pas de couche « self-service » multi-utilisateurs.
- Pas de réimplémentation de Terraform : `pvecli` *observe* et *alimente* l'IaC, il ne la remplace pas.

## 4. Utilisateur & environnement

**Utilisateur unique** : l'auteur, en apprentissage, depuis un poste macOS (Darwin arm64), Go 1.26 disponible.

**Cible** :

```
Endpoint   https://192.0.2.23:8006/api2/json
Réalm      pve (token) — jamais pam/root
TLS        certificat auto-signé → gestion explicite (voir §7.3)
Topologie  mono-nœud, homelab, données non critiques
```

**Contrainte forte** : le lab est jetable *par convention*, mais les commandes destructives doivent quand même exiger une confirmation. L'objectif est d'acquérir des réflexes transposables en production.

## 5. Architecture

### 5.1 Vue en couches

```
┌──────────────────────────────────────────────────────────────────────┐
│  cmd/  — couche Cobra (parsing, flags, aide, complétion shell)        │
│  root · node · vm · lxc · storage · task · access · net · backup     │
│  iac · config · completion                                           │
└───────────────┬──────────────────────────────────────────────────────┘
                │  appelle uniquement des interfaces (jamais net/http)
┌───────────────▼──────────────────────────────────────────────────────┐
│  internal/service/  — cas d'usage métier                             │
│  VMService · LXCService · StorageService · TaskService · IaCService   │
│  → orchestre : lecture préalable → validation → mutation → attente    │
│    tâche → relecture de confirmation                                 │
└───────────────┬──────────────────────────────────┬───────────────────┘
                │                                  │
┌───────────────▼──────────────────┐  ┌────────────▼───────────────────┐
│  internal/pve/  — client API     │  │  internal/iac/  — adaptateurs  │
│  client.go   transport, retry    │  │  terraform.go  lecture du state│
│  auth.go     PVEAPIToken header  │  │  ansible.go    génération inv. │
│  tasks.go    UPID parse + poll   │  │  exec.go       wrapper process │
│  models/     structs typés       │  └────────────────────────────────┘
│  errors.go   401/403/400/404/lock│
└───────────────┬──────────────────┘
                │
┌───────────────▼──────────────────────────────────────────────────────┐
│  internal/config/ (layering)   internal/output/ (table|json|yaml)     │
│  internal/log/ (--verbose, trace HTTP redacté)                        │
└──────────────────────────────────────────────────────────────────────┘
```

**Règle d'or** : `cmd/` ne fait *jamais* d'appel HTTP direct. Toute la logique testable vit dans `service/` et `pve/`, derrière des interfaces mockables. C'est ce qui rendra les tests possibles sans nœud Proxmox allumé.

### 5.2 Arborescence cible

```
cli-proxmox/
├── prd.md
├── go.mod
├── main.go
├── cmd/
│   ├── root.go            # flags globaux, chargement config, PersistentPreRun
│   ├── version.go         # GET /version
│   ├── node.go            # node ls|show|status
│   ├── vm.go              # vm ls|show|create|clone|start|stop|...|rm
│   ├── lxc.go             # lxc ls|show|create|start|stop|...|rm
│   ├── storage.go         # storage ls|content|upload|download-url
│   ├── backup.go          # backup run|ls|restore
│   ├── task.go            # task ls|show|log|wait
│   ├── access.go          # access user|token|acl|role
│   ├── net.go             # net ls|show|apply
│   └── iac.go             # iac inventory|drift|plan|apply|adopt
├── internal/
│   ├── config/            # fichier + env + flags
│   ├── pve/               # client HTTP + modèles + tâches + erreurs
│   ├── service/           # cas d'usage
│   ├── iac/               # terraform state, ansible inventory
│   ├── output/            # renderers
│   └── testutil/          # httptest fixtures, golden files
├── testdata/              # réponses API capturées (anonymisées)
├── docs/
│   ├── LEARNING-LOG.md    # journal : ce que chaque endpoint m'a appris
│   └── API-MAP.md         # carte des endpoints implémentés
└── Makefile
```

### 5.3 Flux d'une mutation asynchrone

C'est **le** concept structurant de l'API PVE. Toute commande d'écriture suit ce chemin :

```
  pvecli vm clone 9000 --newid 210 --name lab-app-01 --full
        │
        │ 1. PRE-READ  — l'objet existe-t-il ? est-il verrouillé ?
        ├──► GET /nodes/pve/qemu/9000/config      (template ok ?)
        ├──► GET /nodes/pve/qemu/210/status/current  (404 attendu = libre)
        │
        │ 2. RENDER    — afficher le plan résolu
        │    ┌────────────────────────────────────────────────┐
        │    │ node=pve  method=POST                          │
        │    │ path=/nodes/pve/qemu/9000/clone                │
        │    │ payload={newid:210,name:"lab-app-01",full:1}   │
        │    │ effet=création VM 210   rollback=DELETE 210    │
        │    └────────────────────────────────────────────────┘
        │
        │ 3. GATE      — --dry-run ? → stop ici.  Sinon confirmation (--yes)
        │
        │ 4. WRITE
        ├──► POST /nodes/pve/qemu/9000/clone
        │    ◄── 200 {"data":"UPID:pve:0011A2:...:qmclone:9000:automation@pve:"}
        │                    │
        │                    │ ⚠ 200 ≠ succès. C'est une acceptation.
        │ 5. POLL           ▼
        ├──► GET /nodes/pve/tasks/{upid_urlencoded}/status   (backoff 1s→5s)
        │    ◄── {"status":"running"}  …  {"status":"stopped","exitstatus":"OK"}
        │
        │ 6. ON FAILURE
        ├──► GET /nodes/pve/tasks/{upid}/log      → affiché tel quel
        │
        │ 7. POST-READ — preuve indépendante
        └──► GET /nodes/pve/qemu/210/config       → rendu final à l'écran
```

Une commande qui saute l'étape 5 ou 7 est considérée **non conforme** et sera refusée en revue.

### 5.4 Pipeline IaC

```
                    ┌─────────────────────────────────┐
                    │   Proxmox VE  (192.0.2.23)    │
                    └───▲──────────────▲──────────────┘
                        │              │
              API reads │              │ API writes (provider bpg/proxmox)
                        │              │
   ┌────────────────────┴───┐    ┌─────┴──────────────────┐
   │       pvecli           │    │      Terraform         │
   │  • inspection          │    │  infra/terraform/      │
   │  • CRUD manuel         │    │  → possède les VM      │
   │  • iac drift  ─────────┼───►│    taguées "managed"   │
   │  • iac adopt           │    └─────┬──────────────────┘
   │  • iac inventory ──┐   │          │ terraform output
   └────────────────────┼───┘          │
                        │              ▼
                        │     ┌──────────────────────┐
                        └────►│  inventory.yml       │
                              │  (généré, jamais     │
                              │   édité à la main)   │
                              └─────┬────────────────┘
                                    │
                              ┌─────▼────────────────┐
                              │  Ansible site.yml    │
                              │  nginx / docker /    │
                              │  caddy               │
                              └─────┬────────────────┘
                                    │
                              ┌─────▼────────────────┐
                              │ pvecli vm exec-check │
                              │ (validation finale)  │
                              └──────────────────────┘
```

**Contrat de propriété** : une ressource portant le tag `managed` (convention du repo : `tags = ["lab","terraform","managed"]`) est **interdite en écriture** à `pvecli`, sauf `--force-unmanaged`. `pvecli` refusera `vm rm 210` si la VM est taguée `managed` — c'est Terraform qui doit la détruire. Cette règle est le cœur de l'apprentissage IaC.

## 6. Modèle de commandes (v1 — CRUD complet + IaC)

### 6.1 Convention

```
pvecli <domaine> <verbe> [cible] [flags]
```

Verbes normalisés : `ls`, `show`, `create`, `update`, `rm`, plus des verbes de domaine (`start`, `clone`, `restore`…). Toute commande accepte `--output table|json|yaml`.

### 6.2 Inventaire des commandes

| Commande | Endpoint(s) PVE | Type |
| --- | --- | --- |
| `pvecli version` | `GET /version` | R |
| `pvecli cluster status` | `GET /cluster/status` | R |
| `pvecli cluster resources` | `GET /cluster/resources` | R |
| `pvecli node ls` | `GET /nodes` | R |
| `pvecli node show <node>` | `GET /nodes/{n}/status` | R |
| `pvecli vm ls [--node] [--tag]` | `GET /nodes/{n}/qemu` | R |
| `pvecli vm show <vmid>` | `GET .../qemu/{id}/config` + `/status/current` | R |
| `pvecli vm create` | `POST /nodes/{n}/qemu` | **W** |
| `pvecli vm clone <src> --newid` | `POST .../qemu/{id}/clone` | **W** |
| `pvecli vm set <vmid> --cores --memory …` | `PUT .../qemu/{id}/config` | **W** |
| `pvecli vm start\|stop\|shutdown\|reboot\|reset <vmid>` | `POST .../qemu/{id}/status/{action}` | **W** |
| `pvecli vm migrate <vmid> --target` | `POST .../qemu/{id}/migrate` | **W** |
| `pvecli vm rm <vmid>` | `DELETE .../qemu/{id}` | **W‼** |
| `pvecli vm snapshot ls\|create\|rollback\|rm` | `GET/POST .../qemu/{id}/snapshot[...]` | R/**W** |
| `pvecli vm template <vmid>` | `POST .../qemu/{id}/template` | **W** |
| `pvecli vm agent <vmid> ifaces` | `GET .../qemu/{id}/agent/network-get-interfaces` | R |
| `pvecli lxc …` | idem sur `/nodes/{n}/lxc` | R/**W** |
| `pvecli storage ls` | `GET /storage`, `GET /nodes/{n}/storage` | R |
| `pvecli storage content <store>` | `GET /nodes/{n}/storage/{s}/content` | R |
| `pvecli storage download-url <store>` | `POST .../storage/{s}/download-url` | **W** |
| `pvecli backup run <vmid>` | `POST /nodes/{n}/vzdump` | **W** |
| `pvecli backup ls` | `GET /nodes/{n}/storage/{s}/content?content=backup` | R |
| `pvecli backup restore <archive> --newid` | `POST /nodes/{n}/qemu` (param `archive`) | **W‼** |
| `pvecli task ls [--running]` | `GET /nodes/{n}/tasks` | R |
| `pvecli task show <upid>` | `GET /nodes/{n}/tasks/{upid}/status` | R |
| `pvecli task log <upid>` | `GET /nodes/{n}/tasks/{upid}/log` | R |
| `pvecli task wait <upid>` | polling jusqu'à état terminal | R |
| `pvecli access user ls` | `GET /access/users` | R |
| `pvecli access token create <user> <id>` | `POST /access/users/{u}/token/{t}` | **W‼** |
| `pvecli access acl ls` | `GET /access/acl` | R |
| `pvecli access acl set --path --role --token` | `PUT /access/acl` | **W‼** |
| `pvecli access whoami` | `GET /access/permissions` | R |
| `pvecli net ls <node>` | `GET /nodes/{n}/network` | R |
| `pvecli net apply <node>` | `PUT /nodes/{n}/network` | **W‼** |
| `pvecli pool ls\|create\|rm` | `/pools` | R/**W** |
| `pvecli iac inventory [--tag] [-o file]` | lecture API → YAML Ansible | R |
| `pvecli iac drift [--tfstate]` | API ∩ state Terraform | R |
| `pvecli iac adopt <vmid>` | génère un bloc `import` Terraform | R |
| `pvecli iac plan\|apply` | wrapper `terraform` + pré/post-checks API | **W** |
| `pvecli iac configure [--limit]` | wrapper `ansible-playbook` avec inventaire généré | **W** |
| `pvecli config init\|show\|set` | fichier local | — |
| `pvecli completion bash\|zsh\|fish` | Cobra | — |

Légende : **W** = écriture (confirmation requise) · **W‼** = destructif ou sensible (confirmation renforcée, jamais de `--yes` implicite via alias).

### 6.3 Règle de conception non négociable

> Avant d'implémenter *n'importe quelle* commande, le schéma exact de l'endpoint (paramètres, types, privilèges requis, valeur de retour) est vérifié contre l'API viewer officiel ou `bun .agents/skills/proxmox-api/scripts/search-pve-api.ts <terme>`.
> **Aucun endpoint écrit de mémoire.** Le chemin et la source sont notés dans `docs/API-MAP.md`.

## 7. Contrats transverses

### 7.1 Configuration (layering)

Priorité décroissante : **flags** > **variables d'environnement** > **fichier de config** > **défauts**.

```yaml
# ~/.config/pvecli/config.yaml
current_context: lab
contexts:
  lab:
    endpoint: https://192.0.2.23:8006
    token_id: automation@pve!pvectl
    node: pve                 # nœud par défaut, évite --node partout
    tls:
      fingerprint: "AB:CD:..."   # pinning du cert auto-signé
    iac:
      terraform_dir: ../proxmox-practice-lab/docs/infra/terraform
      ansible_dir:   ../proxmox-practice-lab/docs/infra/ansible
```

Variables d'environnement alignées sur le skill du repo, pour rester interopérable avec `pve-api` :
`PVE_API_URL`, `PVE_API_TOKEN_ID`, `PVE_API_TOKEN_SECRET`, `PVE_INSECURE`.

**Le secret du token n'est jamais écrit dans le fichier de config.** Il est lu depuis l'environnement, ou depuis le Keychain macOS (`security find-generic-password`) en v1.1.

### 7.2 Authentification

En-tête unique, sans CSRF :

```
Authorization: PVEAPIToken=automation@pve!pvectl=<secret>
```

Le secret ne doit jamais apparaître dans `ps`, ni dans les logs, ni dans un message d'erreur. Le mode `--verbose` trace les requêtes HTTP avec l'en-tête `Authorization` remplacé par `<redacted>`.

### 7.3 TLS

Le certificat du lab est auto-signé. Trois modes, par ordre de préférence :

1. **`tls.fingerprint`** (défaut recommandé) — pinning SHA-256 du certificat. Vérification réelle, sans CA.
2. **`tls.ca_file`** — CA du lab importée.
3. **`--insecure` / `PVE_INSECURE=1`** — désactivation. **Affiche un avertissement sur stderr à chaque appel.** Jamais le défaut.

C'est un point d'apprentissage volontaire : comprendre pourquoi `insecure=false` figure dans le `main.tf` du repo.

### 7.4 Sortie

| Mode | Usage |
| --- | --- |
| `table` (défaut) | lecture humaine, colonnes alignées, couleurs désactivées si non-TTY |
| `json` | pipe vers `jq`, scripts, tests golden |
| `yaml` | inspection de configs longues |

Discipline stdout/stderr : **les données vont sur stdout, tout le reste (progression, avertissements, confirmations) sur stderr.** `pvecli vm ls -o json | jq` doit toujours fonctionner.

### 7.5 Erreurs

Le client traduit chaque signal HTTP en erreur typée avec une piste de diagnostic — reprise du tableau de triage du repo :

| Code | Message `pvecli` |
| --- | --- |
| 401 | `authentification refusée — vérifier format de l'en-tête, token_id, secret, realm, expiration` |
| 403 | `privilège manquant sur <path> — vérifier ACL, propagation, privilege separation du token (pvecli access whoami)` |
| 400 | `paramètre invalide — comparer avec le schéma : search-pve-api.ts <endpoint>` |
| 404 | `ressource absente — vérifier node, vmid, storage, ou disponibilité dans cette version PVE` |
| lock | `ressource verrouillée — pvecli task ls --running` |
| UPID failed | affiche `exitstatus` + les 20 dernières lignes du log de tâche |

Codes de sortie : `0` succès · `1` erreur générique · `2` usage · `3` auth/authz · `4` tâche PVE échouée · `5` confirmation refusée.

### 7.6 Sécurité des écritures

- `--dry-run` sur **toutes** les commandes W : affiche méthode, chemin, payload redacté, effet attendu, rollback. Aucune requête d'écriture émise.
- Confirmation interactive par défaut sur W et W‼ ; `--yes` la court-circuite (usage script).
- Sur W‼, la confirmation exige de **retaper l'identifiant** de la cible (`210`), pas juste `y`.
- Aucun élargissement implicite : `vm start` ne modifie jamais la config ; `restore` n'écrase jamais un guest existant sans `--overwrite` explicite.
- Refus d'écriture sur toute ressource taguée `managed` (voir §5.4).

## 8. Parcours d'apprentissage — mapping manuel ↔ CLI

Le développement suit le manuel. Chaque lot n'est clos que lorsque sa **preuve** est obtenue — même règle que le repo : *« ne passe pas au niveau suivant tant que la preuve n'est pas obtenue »*.

| Lot | Chapitre du manuel | Ce qu'on code | Preuve de fin de lot |
| --- | --- | --- | --- |
| **M0** Socle | 00, 01 | `main.go`, Cobra, config layering, client HTTP, TLS pinning, `version`, `node ls` | `pvecli version` renvoie la version réelle du nœud, en TLS vérifié, avec un token non-root |
| **M1** Lecture | 02 | `vm ls`, `lxc ls`, `storage ls/content`, `task ls/log`, renderers table/json | `pvecli vm ls -o json \| jq '.[].name'` fonctionne ; `docs/API-MAP.md` référence chaque endpoint avec sa source |
| **M2** Tâches & états | 03 | `tasks.go` (parse UPID, polling, backoff), `vm start/stop/shutdown`, `lxc start/stop` | Un `stop` affiche l'UPID, attend l'`exitstatus`, puis relit `status/current` — démontré sur un LXC Nginx |
| **M3** Cycle de vie | 04, 05 | `vm create`, `clone`, `set`, `template`, `snapshot *`, `agent ifaces`, `--dry-run` | Un template cloud-init est créé puis cloné intégralement par `pvecli`, sans passer par l'UI |
| **M4** ACL & sécurité | 02, 09 | `access user/token/acl/whoami`, erreurs typées 401/403 | Un `403` est provoqué volontairement, diagnostiqué via `access whoami`, corrigé par ACL — et documenté dans `LEARNING-LOG.md` |
| **M5** Backup & PRA | 07 | `backup run/ls/restore`, suivi de tâche longue | Une VM est détruite puis restaurée par `pvecli`, RPO/RTO mesurés et notés |
| **M6** IaC | 08, 11, 12, 13 | `iac inventory`, `iac drift`, `iac adopt`, wrappers `plan/apply/configure`, garde `managed` | Chaîne complète : clone TF → `iac inventory` → Ansible → page Nginx servie ; `iac drift` détecte une modification faite à la main dans l'UI |
| **M7** Finition | 09, 10 | `net`, `pool`, complétion shell, `Makefile`, release cross-platform | Binaire installé et utilisable depuis le nœud lui-même ; `pvecli completion zsh` opérationnelle |

Le suivi vivant (statut, journal) se fait hors dépôt. Le détail de chaque lot — user stories, critères d'acceptation, preuves — vit dans [`stories/`](stories/README.md) : [M0](stories/M0-socle.md) · [M1](stories/M1-lecture.md) · [M2](stories/M2-taches-et-etats.md) · [M3](stories/M3-cycle-de-vie.md) · [M4](stories/M4-acl-et-securite.md) · [M5](stories/M5-backup-et-pra.md) · [M6](stories/M6-iac.md) · [M7](stories/M7-finition.md).

Chaque lot alimente `docs/LEARNING-LOG.md` : *quel endpoint, quelle surprise, quelle erreur commise, quelle règle retenue*. Ce journal est un livrable au même titre que le code.

## 9. Qualité & tests

| Niveau | Contenu | Sans nœud Proxmox ? |
| --- | --- | --- |
| Unitaire | parsing UPID, layering de config, mapping d'erreurs, rendus table/json (golden files) | ✅ |
| Client | `httptest.Server` rejouant des réponses capturées dans `testdata/` (anonymisées) | ✅ |
| Service | mocks d'interfaces : vérifie que la séquence pre-read → write → poll → post-read est bien respectée | ✅ |
| Intégration | tag `//go:build integration`, contre `192.0.2.23`, sur des VMID réservés au test (900-999) | ❌ (lab requis) |

Garde-fous :

- `go vet`, `staticcheck`, `golangci-lint` en CI.
- Couverture minimale **70 %** sur `internal/pve` et `internal/service`.
- Un test dédié vérifie qu'**aucun secret ne fuit** dans la sortie `--verbose` (scan de la trace pour le secret injecté).
- Le `Makefile` expose : `make build test lint fmt integration release`.

## 10. Distribution & DX

- Binaire statique `pvecli`, cross-compilé (`darwin/arm64` pour le poste, `linux/amd64` pour le nœud) via `go build` / GoReleaser en v1.1.
- Version injectée au build (`-ldflags "-X main.version=..."`), affichée par `pvecli --version` (à distinguer de `pvecli version` = version du nœud PVE).
- Complétion shell générée par Cobra, avec complétion dynamique des VMID (appel API en arrière-plan) en v1.1.
- Aide : chaque commande d'écriture documente en `Long` **l'endpoint appelé** — la CLI enseigne l'API en s'utilisant.

## 11. Risques & mitigations

| Risque | Impact | Mitigation |
| --- | --- | --- |
| Écrire les endpoints de mémoire (hallucination de schéma) | Bugs subtils, mauvais apprentissage | Règle §6.3 : vérification obligatoire contre le schéma + traçabilité dans `API-MAP.md` |
| Fuite du token dans un log / le shell history | Compromission du lab | Secret uniquement en env/Keychain, redaction systématique, test anti-fuite |
| Périmètre v1 très large (CRUD complet + IaC) | Projet jamais terminé | Découpage strict M0→M7, chaque lot livre une CLI *utilisable* ; pas de lot suivant sans preuve |
| Casser le lab (suppression, réseau) | Perte de temps, réinstallation | `--dry-run` partout, confirmation renforcée sur W‼, backups avant les lots M5/M6 |
| Divergence CLI ↔ Terraform (double source de vérité) | Dérive incompréhensible | Garde `managed` (§5.4) + `iac drift` comme réflexe systématique |
| `--insecure` qui devient l'habitude | Mauvais réflexe transposé en prod | Pinning de fingerprint par défaut, avertissement stderr bruyant sur `--insecure` |

## 12. Critères de succès du projet

La v1 est réussie si, sans ouvrir l'interface web :

1. `pvecli` provisionne une VM depuis un template cloud-init, la configure via Ansible et sert une page Nginx.
2. `pvecli iac drift` détecte une modification faite manuellement dans l'UI.
3. Une VM détruite est restaurée depuis une sauvegarde, avec preuve par relecture API.
4. Le token utilisé n'a **que** les privilèges nécessaires, et `pvecli access whoami` le prouve.
5. `docs/API-MAP.md` recense tous les endpoints implémentés avec leur source documentaire.
6. `docs/LEARNING-LOG.md` contient au moins une leçon écrite par lot.

## 13. Décisions ouvertes

| # | Question | À trancher avant |
| --- | --- | --- |
| D1 | Stockage du secret : env seul, ou Keychain macOS dès M0 ? | M0 |
| D2 | `iac drift` lit-il `terraform.tfstate` directement, ou passe-t-il par `terraform show -json` ? | M6 |
| D3 | Faut-il un cache local des `cluster/resources` pour la complétion dynamique ? | M7 |
| D4 | Support LXC et QEMU par du code générique (interface `Guest`) ou deux implémentations distinctes ? | M2 |

---

## Annexe A — Séquence d'amorçage (à exécuter avant M0)

Sur le nœud, en console web ou SSH, création du token dédié :

```bash
pveum user add automation@pve
pveum acl modify / --users automation@pve --roles PVEAuditor      # lecture seule d'abord
pveum user token add automation@pve pvecli --privsep 1 --expire <timestamp>
# → noter le secret affiché UNE SEULE FOIS
pveum acl modify / --tokens 'automation@pve!pvectl' --roles PVEAuditor
```

Puis, côté poste :

```bash
export PVE_API_URL="https://192.0.2.23:8006"
export PVE_API_TOKEN_ID="automation@pve!pvectl"
export PVE_API_TOKEN_SECRET="…"        # jamais commité
```

Les rôles seront élargis progressivement, lot par lot (`PVEVMAdmin` à M3, `PVEDatastoreAdmin` à M5), jamais d'un coup. **L'élargissement d'ACL est lui-même un exercice du parcours.**

## Annexe B — Séquence d'inspection minimale

Reprise du playbook du repo, à câbler comme *smoke test* de la CLI :

```
1. GET /version
2. GET /cluster/status
3. GET /nodes
4. plus petit endpoint utile de la ressource visée
```

Implémentée comme `pvecli doctor` : vérifie endpoint, TLS, token, privilèges, et affiche un diagnostic actionnable.
