# M1 — Lecture

> Chapitre 02 du manuel · PVX-010 → PVX-017
> **Preuve de fin de lot** : `pvecli vm ls -o json | jq '.[].name'` fonctionne, et `docs/API-MAP.md` référence chaque endpoint implémenté avec sa source documentaire.

Lot 100 % read-only : aucun risque pour le lab, mais c'est là qu'on découvre la forme réelle des données PVE. Toutes les structures typées créées ici seront réutilisées par les lots d'écriture.

---

### PVX-010 — Rendre la sortie en table, JSON et YAML
**Taille** M · **Type** ⚙ · **Dépend de** PVX-006 · **PRD** §7.4

En tant qu'opérateur, je veux choisir le format de sortie, afin de lire confortablement à l'écran et de piper vers `jq` dans un script.

**Critères d'acceptation**
- `--output|-o table|json|yaml`, `table` par défaut.
- **Les données vont sur stdout, tout le reste sur stderr** (progression, avertissements, confirmations).
- Les couleurs et l'alignement sont désactivés si stdout n'est pas un TTY.
- En mode `json`, la sortie est le tableau/objet brut typé — pas un enrobage maison — de sorte que `jq` fonctionne sans détour.
- `--no-headers` et `--columns nom,statut,…` pour le mode table.
- Tests golden : une fixture d'entrée → une sortie table attendue, octet pour octet.

**Preuve**
```bash
pvecli node ls -o json | jq -e '.[0].node' >/dev/null && echo ok
pvecli node ls > /dev/null   # aucune sortie parasite sur stdout
```

**Ce que ça doit t'apprendre** — La discipline stdout/stderr est ce qui sépare une CLI scriptable d'une CLI décorative.

---

### PVX-011 — Lister les VM QEMU
**Taille** M · **Type** R · **Dépend de** PVX-010 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli vm ls`, afin de voir l'inventaire des machines virtuelles sans ouvrir l'interface web.

**Critères d'acceptation**
- Appelle `GET /nodes/{node}/qemu` ; `--node` surcharge le nœud par défaut.
- Colonnes : VMID, nom, statut, CPU, RAM, disque, uptime, tags.
- `--tag <tag>` filtre côté client sur les tags (préparation de la garde `managed` du lot M6).
- `--all` interroge tous les nœuds retournés par `GET /nodes` (utile même en mono-nœud pour valider le code).
- Le champ `template` est visible : un template ne doit pas être confondu avec une VM éteinte.

**Preuve**
```bash
pvecli vm ls
pvecli vm ls -o json | jq '.[] | select(.template == 1) | .name'
```

**Ce que ça doit t'apprendre** — Qu'un template PVE est une VM avec un drapeau, pas un objet d'un autre type. Cette découverte conditionne toute la story de clonage (PVX-024).

---

### PVX-012 — Lister les conteneurs LXC
**Taille** S · **Type** R · **Dépend de** PVX-011 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli lxc ls`, afin de voir les conteneurs au même endroit que les VM.

**Critères d'acceptation**
- Appelle `GET /nodes/{node}/lxc`, mêmes colonnes et mêmes filtres que `vm ls`.
- La sortie indique si le conteneur est **non privilégié** (`unprivileged`) — c'est la compétence validée par le chapitre 03.
- `pvecli guest ls` (alias transverse) fusionne VM et LXC avec une colonne `type`.

**Preuve**
```bash
pvecli lxc ls    # colonne unprivileged visible
pvecli guest ls  # qemu + lxc dans un seul tableau
```

**Ce que ça doit t'apprendre** — Les endpoints QEMU et LXC sont quasi symétriques mais **pas identiques** : repérer dès maintenant où ils divergent tranchera la décision ouverte D4 du PRD (interface `Guest` commune ou non).

---

### PVX-013 — Décrire un guest en détail
**Taille** M · **Type** R · **Dépend de** PVX-012 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli vm show <vmid>` et `lxc show <ctid>`, afin de voir la configuration *et* l'état courant dans une seule vue.

**Critères d'acceptation**
- Combine `GET /nodes/{n}/qemu/{id}/config` et `GET /nodes/{n}/qemu/{id}/status/current`.
- Sections affichées : identité, CPU/RAM, disques, interfaces réseau, cloud-init si présent, tags, verrou (`lock`) éventuel.
- Les clés `netN`, `scsiN`, `virtioN` (chaînes à options séparées par virgules) sont **parsées en structures**, pas affichées brutes.
- `--raw` affiche la réponse API telle quelle.
- Une VM inexistante produit un `404` traduit lisiblement (PVX-007).

**Preuve**
```bash
pvecli vm show 100
pvecli vm show 100 --raw -o json | jq .config
```

**Ce que ça doit t'apprendre** — Le format « chaîne à options » de PVE (`virtio0: local-lvm:vm-100-disk-0,size=20G`) : le comprendre est indispensable pour écrire une config (PVX-026).

---

### PVX-014 — Explorer les stockages et leur contenu
**Taille** M · **Type** R · **Dépend de** PVX-010 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli storage ls` et `storage content <store>`, afin de comprendre où atterrissent images, ISO, templates et sauvegardes.

**Critères d'acceptation**
- `storage ls` combine `GET /storage` (définition cluster) et `GET /nodes/{n}/storage` (état sur le nœud) : type, `content` autorisés, actif, espace utilisé/total.
- `storage content <store>` appelle `GET /nodes/{n}/storage/{s}/content`, filtrable par `--content iso|vztmpl|backup|images|rootdir|snippets`.
- La colonne `content` est mise en avant : c'est elle qui explique les erreurs de dépôt de fichier.

**Preuve**
```bash
pvecli storage ls
pvecli storage content local --content iso
```

**Ce que ça doit t'apprendre** — Pourquoi un `.qcow2` ne peut pas être déposé sur un storage déclaré `content=iso` : le type de contenu est une contrainte de l'API, pas une convention de nommage.

---

### PVX-015 — Consulter les tâches et leurs journaux
**Taille** M · **Type** R · **Dépend de** PVX-010 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli task ls|show|log`, afin d'observer ce que le nœud fait — y compris ce que fait l'interface web pendant que je clique.

**Critères d'acceptation**
- `task ls` appelle `GET /nodes/{n}/tasks` : UPID tronqué, type, cible, utilisateur, début, durée, `status`, `exitstatus`. `--running` filtre les tâches actives, `--limit` borne le nombre.
- `task show <upid>` appelle `GET /nodes/{n}/tasks/{upid}/status`.
- `task log <upid>` appelle `GET /nodes/{n}/tasks/{upid}/log`, avec `--tail N`.
- **L'UPID est URL-encodé** dans le chemin ; un test le vérifie explicitement (l'UPID contient des `:`).
- L'UPID complet est toujours récupérable via `-o json` même si la table le tronque.

**Preuve**
```bash
# lancer un démarrage depuis l'UI, puis :
pvecli task ls --running
pvecli task log <UPID> --tail 20
```

**Ce que ça doit t'apprendre** — Que l'interface web n'est qu'un client de la même API : chacun de tes clics laisse une tâche que tu peux relire. C'est le meilleur moyen d'apprendre quels endpoints appeler.

---

### PVX-016 — Lire l'état du cluster et l'inventaire global
**Taille** S · **Type** R · **Dépend de** PVX-010 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli cluster status` et `cluster resources`, afin d'obtenir une vue transversale sans boucler nœud par nœud.

**Critères d'acceptation**
- `cluster status` appelle `GET /cluster/status` (quorum, nœuds, version corosync) et reste lisible en mono-nœud.
- `cluster resources` appelle `GET /cluster/resources`, filtrable par `--type vm|storage|node|pool`.
- La sortie `cluster resources` est le socle réutilisé plus tard par la complétion dynamique (PVX-053) et par `iac drift` (PVX-044).

**Preuve**
```bash
pvecli cluster resources --type vm -o json | jq '.[].vmid'
```

**Ce que ça doit t'apprendre** — Qu'un seul appel `/cluster/resources` remplace souvent N appels `/nodes/{n}/…` : savoir choisir l'endpoint agrégé est une compétence à part entière.

---

### PVX-017 — Installer le harness de test et la carte des endpoints
**Taille** M · **Type** ⚙ · **Dépend de** PVX-011 · **PRD** §6.3, §9

En tant que développeur, je veux rejouer des réponses API réelles dans les tests et tenir une carte des endpoints, afin de développer sans nœud allumé et de ne jamais coder un endpoint de mémoire.

**Critères d'acceptation**
- `internal/testutil` expose un `httptest.Server` qui sert les fixtures de `testdata/` selon méthode + chemin.
- Une commande de capture (`make capture ENDPOINT=/nodes/pve/qemu`) enregistre une réponse réelle **anonymisée** (IP, noms d'hôte, identifiants remplacés) dans `testdata/`.
- `docs/API-MAP.md` liste, pour chaque endpoint implémenté : méthode, chemin, commande `pvecli` correspondante, source consultée (URL de l'API viewer ou commande `search-pve-api.ts`), date de vérification.
- Un test de non-régression échoue si un endpoint utilisé dans le code est absent de `API-MAP.md`.
- Couverture ≥ 70 % sur `internal/pve`.

**Preuve**
```bash
make test           # passe sans accès au nœud
go test ./... -run TestAPIMapCoverage
```

**Ce que ça doit t'apprendre** — Que la traçabilité documentaire n'est pas de la bureaucratie : c'est le seul garde-fou contre le fait d'inventer un schéma d'endpoint plausible mais faux.
