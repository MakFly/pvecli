# M7 — Finition, exploitation & distribution

> Chapitres 09 & 10 du manuel · PVX-049 → PVX-055
> **Preuve de fin de lot** : le binaire est installé sur le nœud `192.0.2.23` et utilisable en local, la complétion zsh est opérationnelle sur le poste.

Lot de consolidation : les domaines restants de l'API, puis tout ce qui fait qu'un outil est réellement utilisé plutôt qu'abandonné.

---

### PVX-049 — Inspecter et appliquer la configuration réseau
**Taille** M · **Type** R / W‼ · **Dépend de** PVX-021 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli net ls|show|apply`, afin de comprendre le chemin réseau du chapitre 02 sans risquer de me couper l'accès.

**Critères d'acceptation**
- `net ls <node>` → `GET /nodes/{n}/network` : interface, type (bridge/bond/vlan), ports, adresse, actif, **modifications en attente**.
- `net show <node> <iface>` détaille une interface.
- `net apply <node>` → `PUT /nodes/{n}/network` (**W‼**) : applique les changements en attente.
- **Avertissement bloquant** : une erreur de configuration réseau coupe l'accès au nœud. La confirmation exige de retaper le nom du nœud et rappelle qu'un accès console (IPMI/écran physique) doit être disponible.
- `net revert <node>` → `DELETE /nodes/{n}/network` annule les changements non appliqués : documenté comme le réflexe de secours.
- La CLI n'expose **pas** la création/modification d'interfaces en v1 : lecture, application et annulation seulement.

**Preuve**
```bash
pvecli net ls pve                # colonne "pending" visible
pvecli net apply pve --dry-run
```

**Ce que ça doit t'apprendre** — Que PVE sépare *écrire la config réseau* et *l'appliquer* : c'est ce délai qui permet de se rattraper. Comprendre `revert` avant d'avoir besoin de `revert`.

---

### PVX-050 — Gérer les pools de ressources
**Taille** S · **Type** R / W · **Dépend de** PVX-021 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli pool ls|create|rm|add`, afin de regrouper les ressources et de simplifier les ACL.

**Critères d'acceptation**
- `pool ls` → `GET /pools` ; `pool show <id>` liste les membres.
- `pool create <id> --comment`, `pool rm <id>` (**W‼**, refuse si non vide sauf `--force`).
- `pool add <id> --vmid 210,211` / `pool remove`.
- L'aide explique le lien avec les ACL : un rôle attribué sur `/pool/<id>` couvre tous ses membres.

**Preuve**
```bash
pvecli pool create lab --comment "ressources du parcours"
pvecli pool add lab --vmid 210
pvecli access acl set --path /pool/lab --role PVEVMAdmin --token 'automation@pve!pvectl' --dry-run
```

**Ce que ça doit t'apprendre** — Que les pools existent d'abord pour les autorisations : c'est un chemin ACL, pas un simple dossier de rangement.

---

### PVX-051 — Alimenter les stockages en ISO et templates
**Taille** M · **Type** W · **Dépend de** PVX-014 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli storage download-url` et `storage upload`, afin de récupérer une image cloud sans passer par l'interface web.

**Critères d'acceptation**
- `storage download-url <store> --url <u> --content iso|vztmpl --filename <f>` → `POST /nodes/{n}/storage/{s}/download-url`, avec `--checksum` et `--checksum-algorithm`.
- Tâche asynchrone : polling UPID, progression sur stderr.
- Pre-read : le storage accepte le content type demandé → sinon erreur listant les storages éligibles.
- Le **checksum est fortement encouragé** : un avertissement s'affiche s'il est omis.
- `storage upload` (`POST .../upload`, multipart) pour un fichier local, avec barre de progression.
- `storage rm <store> <volid>` (**W‼**) supprime un volume, avec confirmation renforcée.

**Preuve**
```bash
pvecli storage download-url local --content iso \
  --url https://cloud-images.ubuntu.com/... --filename noble.img \
  --checksum <sha256> --checksum-algorithm sha256
pvecli storage content local --content iso
```

**Ce que ça doit t'apprendre** — Que le nœud télécharge lui-même (l'image ne transite pas par ton poste), et pourquoi vérifier un checksum sur une image qui deviendra un template n'est pas optionnel.

---

### PVX-052 — Migrer un guest
**Taille** S · **Type** W · **Dépend de** PVX-021 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli vm migrate <vmid> --target <node>`, afin d'avoir la commande prête le jour où un second nœud rejoint le lab.

**Critères d'acceptation**
- `POST /nodes/{n}/qemu/{id}/migrate` avec `target`, `online`, `with-local-disks`, `targetstorage`.
- Pre-read : le nœud cible existe, est en ligne, et le storage est accessible des deux côtés → sinon message explicatif.
- En mono-nœud, la commande **explique pourquoi elle ne peut rien faire** au lieu de renvoyer une erreur API obscure.
- Tâche longue : polling + progression.
- Équivalent LXC (`lxc migrate`), avec la contrainte `restart` documentée.

**Preuve**
```bash
pvecli vm migrate 210 --target pve2 --dry-run   # explicite si pve2 n'existe pas
```

**Ce que ça doit t'apprendre** — Les prérequis réels d'une migration (storage partagé ou copie de disques locaux) : la commande est triviale, les conditions ne le sont pas.

---

### PVX-053 — Fournir la complétion shell
**Taille** M · **Type** ⚙ · **Dépend de** PVX-016 · **PRD** §10

En tant qu'opérateur, je veux compléter commandes, flags et VMID au `Tab`, afin de ne pas mémoriser d'identifiants numériques.

**Critères d'acceptation**
- `pvecli completion bash|zsh|fish|powershell` génère le script (Cobra).
- **Complétion dynamique** des VMID/CTID, noms de nœuds, storages, tags et pools, alimentée par `GET /cluster/resources`.
- Réponse mise en cache localement (TTL court, ~10 s) pour ne pas marteler l'API à chaque `Tab`.
- La complétion **échoue silencieusement** si le nœud est injoignable : elle ne bloque jamais le shell et n'affiche jamais d'erreur.
- Instructions d'installation dans l'aide de la commande.

**Preuve**
```bash
pvecli completion zsh > "${fpath[1]}/_pvecli" && exec zsh
pvecli vm show <Tab>     # propose les VMID existants avec leur nom
```

**Ce que ça doit t'apprendre** — Qu'une CLI se juge à l'usage quotidien : la complétion dynamique est ce qui fait qu'on cesse d'ouvrir l'interface web « juste pour retrouver l'ID ».

---

### PVX-054 — Verrouiller la qualité en intégration continue
**Taille** M · **Type** ⚙ · **Dépend de** PVX-017 · **PRD** §9

En tant que développeur, je veux une CI qui refuse une régression, afin que le projet reste sain jusqu'au bout.

**Critères d'acceptation**
- Pipeline : `go vet`, `staticcheck`/`golangci-lint`, `go test ./...`, contrôle de couverture.
- **Couverture ≥ 70 %** sur `internal/pve` et `internal/service`, en échec sinon.
- Le test anti-fuite de secret (PVX-009) et le test de couverture d'`API-MAP.md` (PVX-017) sont bloquants.
- Les tests d'intégration (`//go:build integration`) sont **exclus** de la CI et lancés manuellement par `make integration` sur des VMID réservés (900-999).
- `make` reste le point d'entrée unique : la CI n'appelle que des cibles du `Makefile`.

**Preuve**
```bash
make lint test          # vert, sans accès au nœud
make integration        # contre 192.0.2.23, VMID 9xx uniquement
```

**Ce que ça doit t'apprendre** — Que la testabilité était une décision d'architecture prise à M0 (interfaces mockables, aucun appel HTTP dans `cmd/`), pas une couche ajoutée à la fin.

---

### PVX-055 — Distribuer le binaire
**Taille** M · **Type** ⚙ · **Dépend de** PVX-054 · **PRD** §10

En tant qu'opérateur, je veux un binaire installable sur mon poste et sur le nœud, afin d'utiliser la même CLI des deux côtés.

**Critères d'acceptation**
- `make release` produit `darwin/arm64` et `linux/amd64`, statiques, avec version et commit injectés.
- Une somme de contrôle accompagne chaque artefact.
- `make install-node` copie le binaire sur `192.0.2.23` via `scp` et vérifie qu'il s'exécute (`pvecli --version`).
- Depuis le nœud, `pvecli` fonctionne en pointant sur `https://localhost:8006`, avec le même modèle de token (pas de bascule sur `root@pam`).
- Le `README.md` du projet documente : installation, création du token (Annexe A du PRD), première commande, et renvoie vers `stories/` pour la suite.

**Preuve** *(preuve de fin de lot M7)*
```bash
make release && make install-node
ssh root@192.0.2.23 'PVE_API_URL=https://localhost:8006 pvecli doctor'
```

**Ce que ça doit t'apprendre** — Qu'un outil distribué se confronte à des environnements qu'on n'avait pas prévus (certificat sur `localhost`, PATH, absence de TTY) : c'est là que les choix de M0 sur la config et le TLS sont réellement mis à l'épreuve.
