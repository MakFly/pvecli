# M5 — Sauvegarde & plan de reprise

> Chapitre 07 du manuel · PVX-037 → PVX-040
> **Preuve de fin de lot** : une VM est détruite puis restaurée par `pvectl`, avec RPO et RTO réellement mesurés et consignés.

Lot indépendant de M4. Il introduit les tâches **longues** : le poller de PVX-019 y est mis à l'épreuve pour la première fois sur plusieurs minutes.

---

### PVX-037 — Lancer une sauvegarde
**Taille** M · **Type** W · **Dépend de** PVX-021 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl backup run <vmid>`, afin de produire une sauvegarde à la demande avant toute opération risquée.

**Critères d'acceptation**
- `POST /nodes/{n}/vzdump` avec `vmid`, `storage`, `mode` (`snapshot`/`suspend`/`stop`), `compress` (`zstd`/`gzip`/`lzo`), `notes-template`, `remove=0`.
- Pre-read : le storage cible accepte le content type `backup` → sinon erreur explicite listant les storages éligibles (réutilise PVX-014).
- L'aide explique les trois modes et leur impact sur la disponibilité du guest — `snapshot` n'est pas gratuit, il dépend du storage.
- Tâche longue : progression sur stderr, `--detach` pour rendre la main immédiatement en affichant l'UPID.
- Post-read : la nouvelle archive apparaît dans `GET /nodes/{n}/storage/{s}/content?content=backup` — c'est la preuve, pas l'`exitstatus` seul.
- `--all` sauvegarde tous les guests ; `--vmid 210,211` en cible plusieurs.

**Preuve**
```bash
pvectl backup run 210 --storage local --mode snapshot --compress zstd --dry-run
pvectl backup run 210 --storage local --mode snapshot --compress zstd
pvectl backup ls --vmid 210        # l'archive existe
```

**Ce que ça doit t'apprendre** — Ce que « mode snapshot » implique réellement : une cohérence au niveau bloc, pas au niveau applicatif. Une base de données peut se restaurer incohérente malgré un `exitstatus OK`.

---

### PVX-038 — Lister les sauvegardes disponibles
**Taille** S · **Type** R · **Dépend de** PVX-014 · **PRD** §6.2

En tant qu'opérateur, je veux `pvectl backup ls`, afin de savoir ce que je peux restaurer et depuis quand.

**Critères d'acceptation**
- `GET /nodes/{n}/storage/{s}/content?content=backup`, sur tous les storages acceptant `backup` si `--storage` est omis.
- Colonnes : volid, VMID, date, taille, format, notes.
- Tri par date décroissante ; `--vmid` filtre.
- **Colonne `âge`** mise en avant : c'est la mesure directe du RPO effectif.
- `pvectl backup ls --check` signale les guests **sans aucune sauvegarde** — l'information la plus utile du lot.

**Preuve**
```bash
pvectl backup ls
pvectl backup ls --check     # liste les VM/LXC non sauvegardés
```

**Ce que ça doit t'apprendre** — Que le RPO ne se décrète pas : il se lit sur l'âge de la sauvegarde la plus récente. Un guest absent de la liste a un RPO infini.

---

### PVX-039 — Restaurer une sauvegarde
**Taille** L · **Type** W‼ · **Dépend de** PVX-038 · **PRD** §6.2, §7.6

En tant qu'opérateur, je veux `pvectl backup restore <volid> --newid <id>`, afin de vérifier qu'une sauvegarde est réellement exploitable.

**Critères d'acceptation**
- `POST /nodes/{n}/qemu` (ou `/lxc`) avec le paramètre `archive`, plus `vmid`, `storage`, `force`.
- **Ne jamais écraser** un guest existant : si le VMID cible est occupé, la commande refuse, sauf `--overwrite` explicite qui déclenche une confirmation renforcée (retaper le VMID).
- Restauration vers un **nouveau** VMID recommandée par défaut ; l'aide explique pourquoi (tester sans détruire l'original).
- Tâche longue : progression + `--detach`.
- Post-read : la config du guest restauré est affichée ; la CLI **rappelle explicitement** que le démarrage et la vérification applicative restent à faire.
- L'aide avertit qu'une restauration peut réutiliser une adresse MAC et une IP déjà en service.

**Preuve**
```bash
pvectl backup restore local:backup/vzdump-qemu-210-....zst --newid 910 --dry-run
pvectl backup restore local:backup/vzdump-qemu-210-....zst --newid 910
pvectl vm start 910 && pvectl vm ip 910
```

**Ce que ça doit t'apprendre** — La règle du playbook : *une sauvegarde n'est validée que par une restauration réellement testée*. Un `exitstatus OK` sur un `vzdump` ne prouve rien du contenu de l'archive.

---

### PVX-040 — Conduire un exercice de reprise chronométré
**Taille** M · **Type** ⚙ · **Dépend de** PVX-039 · **PRD** §12

En tant qu'apprenant, je veux détruire puis restaurer une VM en mesurant le temps, afin de transformer « RPO/RTO » en deux nombres vérifiés.

**Critères d'acceptation**
- Scénario complet exécuté **uniquement** avec `pvectl` : sauvegarde → destruction → restauration → démarrage → validation applicative (`curl` sur le service).
- **RPO mesuré** = écart entre la dernière sauvegarde et le moment de la panne simulée.
- **RTO mesuré** = durée entre la destruction et le service à nouveau fonctionnel.
- Les deux valeurs, la procédure suivie et les écarts par rapport à l'attendu sont consignés dans `docs/LEARNING-LOG.md`.
- Ce qui **n'a pas** été restauré (règles de pare-feu, entrées DNS, secrets hors image, adhésion Tailscale) est listé explicitement.
- Une commande de confort `pvectl dr drill --vmid <id>` enchaîne le scénario en `--dry-run` par défaut.

**Preuve** *(preuve de fin de lot M5)*
```bash
pvectl backup run 210 --storage local
pvectl vm rm 210 --force-unmanaged      # panne simulée, chronomètre lancé
pvectl backup restore <volid> --newid 210
pvectl vm start 210 && curl -sI http://$(pvectl vm ip 210)/ | head -1
# → RPO et RTO notés dans docs/LEARNING-LOG.md
```

**Ce que ça doit t'apprendre** — Que le RTO réel est presque toujours dominé par ce qui n'était pas dans la sauvegarde. C'est la leçon centrale du chapitre 07, et elle ne s'acquiert qu'en la vivant.
