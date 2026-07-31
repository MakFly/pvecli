# Backlog `pvecli` — index des user stories

Découpage opérationnel du [`prd.md`](../prd.md). Une story = une unité livrable, testable, qui laisse la CLI dans un état utilisable.

| Lot | Fichier | Chapitres du manuel | Stories | Thème |
| --- | --- | --- | --- | --- |
| **M0** | [M0-socle.md](M0-socle.md) | 00, 01 | PVX-001 → 009 | Squelette Go, config, auth, TLS, premières lectures |
| **M1** | [M1-lecture.md](M1-lecture.md) | 02 | PVX-010 → 017 | Inventaire read-only complet, rendus, harness de test |
| **M2** | [M2-taches-et-etats.md](M2-taches-et-etats.md) | 03 | PVX-018 → 023 | UPID, polling, garde-fous d'écriture, start/stop |
| **M3** | [M3-cycle-de-vie.md](M3-cycle-de-vie.md) | 04, 05 | PVX-024 → 031 | create / clone / set / snapshot / template / rm |
| **M4** | [M4-acl-et-securite.md](M4-acl-et-securite.md) | 02, 09 | PVX-032 → 036 | Users, tokens, ACL, diagnostic du 403 |
| **M5** | [M5-backup-et-pra.md](M5-backup-et-pra.md) | 07 | PVX-037 → 040 | vzdump, restore, exercice PRA chronométré |
| **M6** | [M6-iac.md](M6-iac.md) | 08, 11, 12, 13 | PVX-041 → 048 | Inventaire dynamique, drift, adopt, wrappers TF/Ansible |
| **M7** | [M7-finition.md](M7-finition.md) | 09, 10 | PVX-049 → 055 | Réseau, pools, migration, complétion, CI, release |

**55 stories.** Aucun lot ne démarre avant que la *preuve de fin* du lot précédent ne soit obtenue (règle du manuel).

Ces fichiers sont la référence détaillée du backlog. Le suivi vivant — statut par story, journal d'apprentissage — se tient hors dépôt.

## Graphe de dépendances

```
M0 ── socle ──────────────────────────────────────────────┐
 │  001 bootstrap                                          │
 │   └─002 config ──003 client HTTP ──004 TLS              │
 │                       └─007 erreurs ──005 version       │
 │                             └─006 node ──008 doctor     │
 │                                   009 verbose+redaction │
 ▼                                                         │
M1 ── lecture ────────────────────────────────────────────┤
 │  010 renderers ──011 vm ls ──013 show                   │
 │  012 lxc ls   014 storage   015 task   016 cluster      │
 │  017 API-MAP + harness de test  (transverse)            │
 ▼                                                         │
M2 ── écriture sûre ──────────────────────────────────────┤
 │  018 UPID ──019 poller ──021 pipeline mutation          │
 │  020 dry-run/confirm ────────┘   └─022 vm status        │
 │                                  └─023 lxc status       │
 ▼                                                         │
M3 ── cycle de vie ───────────────────────────────────────┤
 │  024 clone  025 create  026 set  027 template           │
 │  028 snapshot  029 agent  030 lxc CRUD  031 rm+managed  │
 ├──────────────┬──────────────────────────────────────────┤
 ▼              ▼                                          │
M4 ACL       M5 backup ─── (indépendants entre eux)        │
 032→036      037→040                                      │
 └──────┬───────┘                                          │
        ▼                                                  │
M6 ── IaC (nécessite M3 + M5) ─────────────────────────────┤
 041 garde managed  042 inventory  043 tfstate  044 drift  │
 045 adopt  046 plan/apply  047 configure  048 E2E         │
        ▼                                                  ▼
M7 ── finition ───────────────────────────────────────────┘
 049 net  050 pool  051 storage upload  052 migrate
 053 completion  054 CI  055 release
```

## Convention d'écriture

```
### PVX-0XX — Titre à l'infinitif
**Taille** S | M | L        **Type** R (lecture) · W (écriture) · W‼ (destructif/sensible) · ⚙ (infra projet)
**Dépend de** : …           **PRD** : §…

En tant qu'… / je veux … / afin de …

**Critères d'acceptation**  (Étant donné / Quand / Alors)
**Preuve**                  commande à exécuter et sortie attendue
**Ce que ça doit t'apprendre**  l'objectif pédagogique réel de la story
```

## Definition of Done (globale, toutes stories)

Une story n'est close que si **tous** ces points sont vrais :

1. **Schéma vérifié** — l'endpoint appelé a été confronté à l'API viewer officiel ou à `search-pve-api.ts` ; chemin + source ajoutés à `docs/API-MAP.md`. *Aucun endpoint écrit de mémoire* (PRD §6.3).
2. **Tests** — au moins un test unitaire ou un test client sur `httptest` avec fixture dans `testdata/`. `make test lint` vert.
3. **Écritures** — si la story est de type W : `--dry-run` implémenté, confirmation en place, séquence pre-read → plan → gate → write → poll → post-read respectée (PRD §5.3).
4. **Secrets** — aucune fuite en sortie standard, en trace `--verbose` ou en message d'erreur.
5. **Journal** — une entrée dans `docs/LEARNING-LOG.md` : quel endpoint, quelle surprise, quelle erreur commise, quelle règle retenue.

## Suivi

| Lot | Preuve de fin de lot | État |
| --- | --- | --- |
| M0 | `pvecli version` renvoie la version réelle du nœud, TLS vérifié, token non-root | ☑ **obtenue** — PVE 9.2.2, empreinte épinglée, `automation@pve!pvectl` |
| M1 | `pvecli vm ls -o json \| jq '.[].name'` fonctionne ; `API-MAP.md` complet | ☑ **obtenue** — et `API-MAP.md` est vérifié par un test, pas par relecture |
| M2 | Un `stop` affiche l'UPID, attend l'`exitstatus`, relit l'état — sur un LXC Nginx | ☑ **obtenue** — `pvecli lxc stop 120` : UPID `vzstop`, `exitstatus`, relecture à `stopped` |
| M3 | Template cloud-init créé puis cloné intégralement par `pvecli`, sans l'UI | ☑ **obtenue** — template `9000` construit, cloné en `212`, clone joignable en SSH ; garde `managed` opposée à `vm rm 212` |
| M4 | Un `403` provoqué, diagnostiqué via `access whoami`, corrigé par ACL et documenté | ☑ **obtenue** — token jetable sans ACL → 403 ; `whoami --can` dit lequel ; une ACL ciblée sur `/vms/120` corrige ; token révoqué |
| M5 | VM détruite puis restaurée par `pvecli`, RPO/RTO mesurés | ☑ **obtenue** — VM 212 sauvegardée, détruite, restaurée, service rendu. **RPO 19 s** (prouvé par un fichier écrit après l'archive et absent au retour), **RTO 20 s** |
| M6 | Chaîne TF → `iac inventory` → Ansible → page Nginx servie ; `iac drift` détecte une modif UI | ☑ **obtenue** — Terraform crée `210` en 23 s, l'inventaire trouve son IP par l'agent, Ansible sert **Nginx natif sur :80 et Caddy conteneurisé sur :8080**, idempotence mesurée (`changed=0` au 2ᵉ passage) et contenu vérifié — pas seulement le code HTTP. Un `memory=3072` posé hors Terraform est attrapé par `iac drift` puis résorbé par `iac apply` |
| M7 | Binaire installé et utilisable depuis le nœud ; complétion zsh opérationnelle | ☑ **obtenue** — `make release VERSION=v0.1.0` puis `make install-node` ; depuis le nœud, `PVE_API_URL=https://localhost:8006 pvecli doctor` répond **quatre ✓** après un `pvecli config trust` local. Le certificat porte `CN=pve.example` : ni l'IP du poste ni `localhost` n'y correspondent, et c'est l'épinglage d'empreinte — indépendant du nom — qui rend le cas trivial. Complétion zsh générée, chargée, et proposant les VMID avec leur nom |


## Avancement au 2026-07-31

**55 stories terminées sur 55. M0 à M7 sont clos.**

| Lot | Fait | Reste |
| --- | --- | --- |
| M0 | 001 → 009 | — |
| M1 | 010 → 017 | — |
| M2 | 018 → 023 | — |
| M3 | 024 → 031 | — |
| M4 | 032 → 036 | — |
| M5 | 037 → 040 | — |
| M6 | 041 → 048 | — |
| M7 | 049 → 055 | — |

M4 a soldé une dette de M0 : le message d'erreur d'un `403` renvoyait déjà, en
dur, vers un `pvecli access whoami` qui n'existait pas. Il existe.

Il a aussi soldé une dette de méthode. Le token avait été élargi trois fois à la
main sur le nœud (`import-from`, `SDN.Use`, puis `/access` pour ce lot) ; c'est
désormais `pvecli access acl set` qui fait le geste — sauf le dernier, qui ne
pouvait pas venir de l'outil : accorder le droit d'accorder des droits doit être
fait par ailleurs.

M7 a rencontré la même limite deux fois de plus, et l'a documentée plutôt que de
la contourner. `Pool.Allocate` et `Datastore.AllocateTemplate` ont dû être posés
depuis le nœud, parce que le token n'a pas `Permissions.Modify` et qu'on ne peut
pas accorder un privilège qu'on ne détient pas. Le refus a été capturé avant le
geste : `pvecli access acl set --path /pool …` répond
`403 — Permission check failed (/pool, Permissions.Modify)`.

Et `Sys.Modify` sur `/nodes/pve` est resté volontairement refusé : c'est le
privilège de `net apply`, la seule commande qui peut rendre le nœud injoignable,
sans accès console pour se rattraper.

État du lab, entièrement construit par `pvecli` :

| VMID | Nom | Rôle |
| --- | --- | --- |
| 9000 | `debian13-cloudinit` | template cloud-init, sans agent — il documente le coût de son absence |
| 9001 | `debian13-cloudinit-agent` | template cloné par Terraform |
| 210 | `lab-app-01` | VM possédée par Terraform, taguée `managed` |
| 211 | `lab-app-01` | VM créée de zéro — `192.0.2.211`, membre du pool `lab` |
| 212 | `lab-app-02` | clone complet, détruit puis restauré par `pvecli backup restore` |
| 120 | `web` | conteneur LXC non privilégié — `192.0.2.120`, membre du pool `lab` |

Les deux VM et le conteneur sont joignables en SSH avec la clé injectée à la
création. Le pool `lab` existe surtout comme chemin d'ACL : `/pool/lab` couvre
ses membres présents et à venir.

La plage **900-999** reste réservée aux tests d'intégration (`make integration`),
qui créent, démarrent, arrêtent et détruisent la VM `990` à chaque exécution et
ne touchent jamais rien en dehors.
