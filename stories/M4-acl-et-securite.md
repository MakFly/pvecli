# M4 — ACL, tokens & sécurité

> Chapitres 02 & 09 du manuel · PVX-032 → PVX-036
> **Preuve de fin de lot** : un `403` est provoqué volontairement, diagnostiqué via `pvectl access whoami`, corrigé par une ACL (et non par une élévation de privilège), puis documenté dans `docs/LEARNING-LOG.md`.

Lot indépendant de M5 : les deux peuvent être menés dans l'ordre qu'on veut après M3.

---

### PVX-032 — Savoir ce que je peux faire (`access whoami`)
**Taille** M · **Type** R · **Dépend de** PVX-010 · **PRD** §6.2, §7.5

En tant qu'opérateur, je veux `pvectl access whoami`, afin de comprendre un `403` sans deviner.

**Critères d'acceptation**
- Appelle `GET /access/permissions` pour le token courant.
- Affiche les chemins ACL et les privilèges effectifs, groupés par chemin, triés du plus général (`/`) au plus spécifique.
- `--path /vms/210` filtre sur un chemin donné et répond à la question « ai-je le droit ici ? ».
- `--can VM.PowerMgmt --path /vms/210` répond `oui`/`non` avec un code de sortie exploitable en script.
- Signale si le token est en **privilege separation** (`privsep=1`) — cas où les droits du token sont l'intersection avec ceux de l'utilisateur.
- Le message d'erreur `403` de PVX-007 renvoie explicitement vers cette commande.

**Preuve**
```bash
pvectl access whoami
pvectl access whoami --can VM.Allocate --path /vms; echo $?
```

**Ce que ça doit t'apprendre** — La *privilege separation* : un token peut avoir moins de droits que son utilisateur, jamais plus. C'est la cause n°1 des `403` inexplicables en homelab.

---

### PVX-033 — Inspecter utilisateurs, rôles et ACL
**Taille** M · **Type** R · **Dépend de** PVX-032 · **PRD** §6.2

En tant qu'opérateur, je veux lire le modèle d'autorisation complet, afin de savoir quel rôle donne quel privilège avant d'en attribuer un.

**Critères d'acceptation**
- `access user ls` → `GET /access/users` (avec `--full` pour les tokens associés).
- `access role ls` → `GET /access/roles` ; `access role show <role>` détaille les privilèges du rôle.
- `access acl ls` → `GET /access/acl` : chemin, type (user/group/token), identité, rôle, propagation.
- `access token ls <user>` → `GET /access/users/{u}/token`, avec la date d'expiration mise en évidence (colorée si proche).
- Aucun secret n'est affiché nulle part (les secrets de token ne sont de toute façon pas relisibles côté API).

**Preuve**
```bash
pvectl access role show PVEVMAdmin     # liste des privilèges accordés
pvectl access acl ls
```

**Ce que ça doit t'apprendre** — Qu'un rôle est un *paquet de privilèges* et une ACL un *triplet (chemin, identité, rôle)*. Tant que ce modèle n'est pas clair, chaque `403` reste un mystère.

---

### PVX-034 — Créer un token d'API
**Taille** M · **Type** W‼ · **Dépend de** PVX-033, PVX-021 · **PRD** §6.2, §7.6

En tant qu'opérateur, je veux `pvectl access token create`, afin de délivrer des identifiants dédiés et à durée limitée (par exemple pour Terraform).

**Critères d'acceptation**
- `POST /access/users/{userid}/token/{tokenid}` avec `--privsep` (défaut `1`), `--expire`, `--comment`.
- **Le secret n'est retourné qu'une fois** : la CLI l'écrit sur stdout, seul, sans décoration, et rappelle sur stderr qu'il est irrécupérable.
- Le secret n'apparaît **jamais** dans les traces `--verbose`, ni dans un fichier de config, ni dans un message d'erreur (couvert par le test anti-fuite de PVX-009).
- `--expire` est **obligatoire** sauf `--no-expire` explicite : un token sans expiration doit être un choix conscient.
- Confirmation renforcée : c'est une opération sensible même si elle n'est pas destructive.

**Preuve**
```bash
pvectl access token create automation@pve terraform --expire 2026-12-31 --dry-run
pvectl access token create automation@pve terraform --expire 2026-12-31 > /tmp/secret
```

**Ce que ça doit t'apprendre** — Pourquoi le repo insiste : token dédié, à privilèges séparés, expirant, jamais `root@pam`. Et pourquoi une CLI doit rendre la bonne pratique plus facile que la mauvaise.

---

### PVX-035 — Attribuer et retirer des droits
**Taille** M · **Type** W‼ · **Dépend de** PVX-034 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl access acl set`, afin d'élargir les droits du token `pvectl` lot par lot, au lieu de tout accorder d'emblée.

**Critères d'acceptation**
- `PUT /access/acl` avec `--path`, `--role`, `--user`/`--token`/`--group`, `--propagate`, `--delete` (pour retirer).
- Pre-read : affiche l'ACL actuelle sur le chemin visé, puis le diff.
- **Refuse** d'attribuer `Administrator` sur `/` sans `--i-know-what-im-doing`, avec un message expliquant l'alternative (rôle ciblé sur un chemin précis).
- Post-read : relit `GET /access/acl` et affiche la ligne créée.
- L'aide de la commande donne la table de progression du PRD (Annexe A) : `PVEAuditor` à M0, `PVEVMAdmin` à M3, `PVEDatastoreAdmin` à M5.

**Preuve**
```bash
pvectl access acl set --path /vms --role PVEVMAdmin \
  --token 'automation@pve!pvectl' --propagate --dry-run
pvectl access whoami --can VM.PowerMgmt --path /vms/210
```

**Ce que ça doit t'apprendre** — Le principe de moindre privilège appliqué dans le temps : élargir quand on en a besoin, pas « au cas où ». Et la propagation d'ACL le long de l'arbre des chemins.

---

### PVX-036 — Provoquer, diagnostiquer et corriger un `403`
**Taille** S · **Type** ⚙ · **Dépend de** PVX-035 · **PRD** §3.1, §12

En tant qu'apprenant, je veux fabriquer volontairement une erreur d'autorisation, afin d'avoir déjà vécu le diagnostic quand elle surviendra pour de vrai.

**Critères d'acceptation**
- Un token de test est créé avec le seul rôle `PVEAuditor` sur `/`.
- Une commande d'écriture avec ce token produit un `403`, code de sortie `3`.
- Le message de PVX-007 mène à `pvectl access whoami`, qui montre l'absence du privilège requis.
- La correction se fait **par ACL ciblée**, jamais en basculant sur `root@pam` ni en élargissant à `Administrator`.
- L'ensemble (privilège manquant exact, chemin, rôle choisi, raison de ce rôle) est consigné dans `docs/LEARNING-LOG.md`.
- Le token de test est révoqué à la fin (`DELETE /access/users/{u}/token/{t}`).

**Preuve** *(preuve de fin de lot M4)*
```bash
PVE_API_TOKEN_ID='automation@pve!readonly' pvectl vm stop 210 --yes; echo $?  # → 3
pvectl access whoami --can VM.PowerMgmt --path /vms/210                       # → non
pvectl access acl set --path /vms/210 --role PVEVMAdmin --token 'automation@pve!readonly'
PVE_API_TOKEN_ID='automation@pve!readonly' pvectl vm stop 210 --yes; echo $?  # → 0
```

**Ce que ça doit t'apprendre** — La règle du playbook : *ne jamais désactiver TLS, élargir une ACL au maximum, utiliser `root@pam` ou supprimer un verrou comme première réaction*. Un `403` est une information, pas un obstacle.
