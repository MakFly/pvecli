# M3 — Cycle de vie des guests

> Chapitres 04 & 05 du manuel · PVX-024 → PVX-031
> **Preuve de fin de lot** : un template cloud-init est créé puis cloné intégralement par `pvectl`, sans jamais passer par l'interface web ; la VM clonée est joignable en SSH.

Lot le plus dense. Toutes les stories passent par le pipeline de mutation de PVX-021 — aucune exception.

---

### PVX-024 — Cloner une VM depuis un template
**Taille** M · **Type** W · **Dépend de** PVX-021 · **PRD** §5.3, §6.2

En tant qu'opérateur, je veux `pvectl vm clone <src> --newid <id>`, afin de produire une VM à partir du template cloud-init du chapitre 04.

**Critères d'acceptation**
- `POST /nodes/{n}/qemu/{src}/clone` avec `newid`, `name`, `full`, `target`, `storage`, `description`, `pool`.
- Pre-read : le template source existe (`GET .../qemu/{src}/config`) **et** le `newid` est libre (`404` attendu sur `status/current`) ; sinon refus avant écriture.
- `--full` (clone complet) vs clone lié : la différence est expliquée dans l'aide de la commande.
- Post-read : `GET .../qemu/{newid}/config` affiché comme résultat.
- `--dry-run` affiche le payload résolu exact.

**Preuve**
```bash
pvectl vm clone 9000 --newid 210 --name lab-app-01 --full --dry-run
pvectl vm clone 9000 --newid 210 --name lab-app-01 --full
pvectl vm show 210
```

**Ce que ça doit t'apprendre** — Le clone lié partage les blocs du template : supprimer le template casse les clones. C'est exactement le genre de contrainte que l'interface web masque.

---

### PVX-025 — Créer une VM de zéro
**Taille** L · **Type** W · **Dépend de** PVX-024 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl vm create`, afin de comprendre chaque paramètre que le wizard de l'interface web remplit à ma place.

**Critères d'acceptation**
- `POST /nodes/{n}/qemu` avec au minimum `vmid`, `name`, `cores`, `memory`, `net0`, `scsi0`/`virtio0`, `ostype`, `scsihw`, `boot`.
- Les flags de haut niveau (`--disk local-lvm:20`, `--net vmbr0`) sont **traduits** en chaînes à options PVE, et le résultat de la traduction est visible en `--dry-run`.
- `--agent` active l'agent QEMU ; l'aide précise qu'il faut aussi l'installer *dans* l'invité.
- Pre-read : le VMID est libre ; le storage cible accepte le content type `images`.
- Un VMID déjà pris produit une erreur claire **avant** l'appel, avec suggestion du prochain ID libre.

**Preuve**
```bash
pvectl vm create --vmid 211 --name scratch --cores 2 --memory 2048 \
  --disk local-lvm:10 --net vmbr0 --ostype l26 --dry-run
```

**Ce que ça doit t'apprendre** — Le sens réel de `scsihw`, `ostype`, `boot`, `net0`. Après cette story, l'interface web devient lisible.

---

### PVX-026 — Modifier la configuration d'une VM
**Taille** M · **Type** W · **Dépend de** PVX-025 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl vm set <vmid>`, afin d'ajuster CPU, RAM, tags et cloud-init sans recréer la machine.

**Critères d'acceptation**
- `PUT /nodes/{n}/qemu/{id}/config` avec seulement les clés modifiées.
- Flags : `--cores`, `--memory`, `--name`, `--description`, `--tags`, `--onboot`, `--ciuser`, `--sshkeys`, `--ipconfig0`.
- `--delete <clé>` supprime une clé de config (paramètre `delete` de l'API).
- Pre-read + diff : la CLI affiche **ce qui change** (avant → après), pas seulement ce qu'on envoie.
- La commande ne touche **aucune** clé non demandée : un test le vérifie sur le payload émis.
- Certaines modifications ne prennent effet qu'après redémarrage (`pending`) : la CLI le signale et propose `pvectl vm reboot`.

**Preuve**
```bash
pvectl vm set 210 --cores 4 --dry-run    # diff: cores 2 → 4
pvectl vm set 210 --tags lab,pvectl
```

**Ce que ça doit t'apprendre** — La notion de configuration *pending* dans PVE, et la règle « ne jamais élargir une écriture demandée » appliquée concrètement.

---

### PVX-027 — Transformer une VM en template
**Taille** S · **Type** W‼ · **Dépend de** PVX-026 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl vm template <vmid>`, afin de figer une VM préparée en modèle clonable.

**Critères d'acceptation**
- `POST /nodes/{n}/qemu/{id}/template`.
- Traitée comme **irréversible** : confirmation renforcée (retaper le VMID), avertissement explicite qu'une VM convertie ne peut plus démarrer telle quelle.
- Pre-read : refuse si la VM est en cours d'exécution.
- Post-read : `template = 1` dans la config.

**Preuve**
```bash
pvectl vm template 9000 --dry-run
pvectl vm ls -o json | jq '.[] | select(.template==1)'
```

**Ce que ça doit t'apprendre** — Qu'une opération peut être irréversible sans être une suppression. Le niveau de confirmation doit suivre la réversibilité, pas le nom du verbe.

---

### PVX-028 — Gérer les snapshots
**Taille** M · **Type** W / W‼ · **Dépend de** PVX-026 · **PRD** §6.2

En tant qu'opérateur, je veux créer, lister, restaurer et supprimer des snapshots, afin d'avoir un filet avant chaque expérimentation.

**Critères d'acceptation**
- `vm snapshot ls <vmid>` → `GET .../qemu/{id}/snapshot` (arborescence parent/enfant lisible).
- `vm snapshot create <vmid> <nom>` → `POST .../snapshot`, options `--description`, `--vmstate`.
- `vm snapshot rollback <vmid> <nom>` → `POST .../snapshot/{nom}/rollback` — **W‼**, avertit que tout changement postérieur au snapshot est perdu.
- `vm snapshot rm <vmid> <nom>` → `DELETE .../snapshot/{nom}` — **W‼**.
- Toutes ces opérations sont asynchrones : polling UPID obligatoire.
- Équivalents LXC exposés sous `lxc snapshot …`.

**Preuve**
```bash
pvectl vm snapshot create 210 pre-ansible --description "avant playbook"
pvectl vm snapshot ls 210
pvectl vm snapshot rollback 210 pre-ansible --dry-run
```

**Ce que ça doit t'apprendre** — La différence entre un snapshot (retour arrière local, dépend du storage) et une sauvegarde (copie indépendante, lot M5). Confondre les deux est une erreur de PRA classique.

---

### PVX-029 — Interroger l'agent QEMU
**Taille** S · **Type** R · **Dépend de** PVX-013 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl vm agent <vmid> ifaces`, afin de connaître l'IP réelle d'une VM — information indispensable à la génération d'inventaire Ansible (PVX-042).

**Critères d'acceptation**
- `GET /nodes/{n}/qemu/{id}/agent/network-get-interfaces`.
- Affiche interface, MAC, adresses IPv4/IPv6, en excluant `lo` par défaut (`--all` pour tout voir).
- Si l'agent n'est pas installé ou non démarré, l'erreur le dit **explicitement** au lieu de renvoyer un `500` brut, et rappelle d'installer `qemu-guest-agent` dans l'invité.
- `pvectl vm ip <vmid>` : raccourci renvoyant la première IPv4 non-loopback, exploitable en script.

**Preuve**
```bash
pvectl vm agent 210 ifaces
ssh ops@$(pvectl vm ip 210) hostname
```

**Ce que ça doit t'apprendre** — Que PVE ne connaît pas l'IP d'une VM en DHCP : seul l'agent invité peut la lui dire. C'est le lien entre l'hyperviseur et l'inventaire d'automatisation.

---

### PVX-030 — CRUD complet des conteneurs LXC
**Taille** L · **Type** W / W‼ · **Dépend de** PVX-023, PVX-026 · **PRD** §6.2

En tant qu'opérateur, je veux créer, configurer et supprimer des LXC, afin de couvrir le chapitre 03 entièrement par la CLI.

**Critères d'acceptation**
- `lxc create` → `POST /nodes/{n}/lxc` : `vmid`, `ostemplate`, `hostname`, `cores`, `memory`, `rootfs`, `net0`, `password` ou `ssh-public-keys`, **`unprivileged=1` par défaut**.
- Le mot de passe n'est jamais accepté en argument de ligne de commande (visible dans `ps` et l'historique) : lecture sur stdin ou variable d'environnement dédiée.
- Pre-read : le template (`vztmpl`) existe sur le storage indiqué → sinon message listant les templates disponibles.
- `lxc set` → `PUT .../lxc/{id}/config` ; `lxc rm` → `DELETE .../lxc/{id}` (**W‼**).
- `lxc clone` → `POST .../lxc/{id}/clone`.

**Preuve**
```bash
pvectl storage content local --content vztmpl
pvectl lxc create --vmid 120 --hostname web --ostemplate local:vztmpl/... \
  --cores 1 --memory 512 --rootfs local-lvm:8 --net vmbr0 --dry-run
```

**Ce que ça doit t'apprendre** — Pourquoi le chapitre 03 insiste sur *non privilégié* : ce que le drapeau change réellement en matière d'isolation, et pourquoi c'est le défaut correct.

---

### PVX-031 — Supprimer un guest, avec garde de propriété
**Taille** M · **Type** W‼ · **Dépend de** PVX-030 · **PRD** §5.4, §7.6

En tant qu'opérateur, je veux `pvectl vm rm <vmid>` protégé par une garde, afin de ne jamais détruire à la main une ressource possédée par Terraform.

**Critères d'acceptation**
- `DELETE /nodes/{n}/qemu/{id}` (et équivalent LXC), avec `--purge` et `--destroy-unreferenced-disks` documentés.
- Pre-read : refuse si la VM tourne (sauf `--force` qui arrête d'abord, en le disant).
- **Garde `managed`** : si la ressource porte le tag `managed`, la suppression est refusée avec un message expliquant que c'est à Terraform de la détruire (`terraform destroy`), et le contournement `--force-unmanaged` est mentionné mais déconseillé.
- Confirmation renforcée : retaper le VMID.
- Post-read : vérifie que `GET .../qemu/{id}/config` renvoie bien `404`.

**Preuve** *(preuve de fin de lot M3)*
```bash
pvectl vm set 210 --tags lab,terraform,managed
pvectl vm rm 210            # → refus, message sur la propriété Terraform
pvectl vm rm 211            # → confirmation par saisie de "211", puis suppression prouvée
```

**Ce que ça doit t'apprendre** — Le contrat de propriété : *qui possède quoi*. C'est la notion qui évitera, au lot M6, la dérive incompréhensible entre l'état live et le state Terraform.
