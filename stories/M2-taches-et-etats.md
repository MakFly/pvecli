# M2 — Tâches asynchrones & changements d'état

> Chapitre 03 du manuel · PVX-018 → PVX-023
> **Preuve de fin de lot** : un `pvectl lxc stop <ctid>` affiche l'UPID, attend l'`exitstatus`, puis relit `status/current` — démontré sur le conteneur Nginx du chapitre 03.

C'est le lot le plus important du projet. Tout ce qui suit (M3 à M7) réutilise le pipeline de mutation construit ici. Une écriture qui ne passe pas par ce pipeline est un bug.

---

### PVX-018 — Modéliser l'UPID
**Taille** S · **Type** ⚙ · **Dépend de** PVX-015 · **PRD** §5.3

En tant que développeur, je veux un type `UPID` qui parse et sérialise l'identifiant de tâche, afin de manipuler ses composants plutôt qu'une chaîne opaque.

**Critères d'acceptation**
- Parse `UPID:<node>:<pid>:<pstart>:<starttime>:<type>:<id>:<user>:` en champs typés.
- `String()` restitue exactement l'UPID d'origine (aller-retour sans perte).
- Fournit l'encodage URL correct pour l'insertion dans un chemin.
- Une chaîne malformée produit une erreur explicite, pas un panic.
- Le champ `node` extrait de l'UPID est utilisé pour le polling — **sans jamais présumer** que c'est le nœud par défaut.

**Preuve**
```bash
go test ./internal/pve -run TestUPID   # round-trip + encodage + cas malformés
```

**Ce que ça doit t'apprendre** — Que l'UPID contient le nœud d'exécution : c'est ce qui permet de suivre une tâche déclenchée sur un nœud depuis n'importe où.

---

### PVX-019 — Suivre une tâche jusqu'à son état terminal
**Taille** M · **Type** R · **Dépend de** PVX-018 · **PRD** §5.3

En tant qu'opérateur, je veux que la CLI attende la fin réelle d'une tâche, afin de ne jamais déclarer un succès sur un simple HTTP 200.

**Critères d'acceptation**
- `TaskService.Wait(upid)` interroge `GET /nodes/{node}/tasks/{upid}/status` avec un backoff 1 s → 5 s.
- Attend un état terminal (`status = stopped`) puis lit `exitstatus`.
- `exitstatus = "OK"` → succès ; toute autre valeur → échec, code de sortie `4`, et récupération automatique des 20 dernières lignes de `GET .../tasks/{upid}/log`.
- `--timeout` global respecté ; à expiration, l'UPID est affiché avec la commande exacte pour reprendre le suivi (`pvectl task wait <upid>`).
- Un `Ctrl-C` pendant l'attente **n'annule pas la tâche côté serveur** : le message le dit explicitement et rappelle l'UPID.
- Indicateur de progression sur stderr, désactivé si non-TTY.
- `pvectl task wait <upid>` expose la fonction en commande autonome.

**Preuve**
```bash
go test ./internal/service -run TestWaitPollsUntilTerminal
pvectl task wait <UPID>    # sur une tâche déjà terminée → réponse immédiate
```

**Ce que ça doit t'apprendre** — La règle centrale du playbook : *« Treat acceptance as the beginning of the operation, not success »*. Un 200 signifie « j'ai accepté la demande », rien de plus.

---

### PVX-020 — Poser les garde-fous d'écriture (`--dry-run`, confirmation)
**Taille** M · **Type** ⚙ · **Dépend de** PVX-007 · **PRD** §7.6

En tant qu'opérateur, je veux voir exactement ce qui va être envoyé avant que ça parte, afin de ne jamais casser le lab par surprise.

**Critères d'acceptation**
- `--dry-run` est disponible sur **toutes** les commandes d'écriture et affiche : nœud, méthode, chemin, payload (secrets masqués), effet attendu, rollback proposé, validation prévue. **Aucune requête d'écriture n'est émise.**
- Confirmation interactive par défaut sur les écritures ; `--yes` la court-circuite.
- Sur une opération destructive ou sensible (W‼), la confirmation exige de **retaper l'identifiant de la cible** (`210`), pas juste `y`.
- Si stdin n'est pas un TTY et que `--yes` est absent, la commande refuse d'agir avec le code de sortie `5`.
- La confirmation et le plan sont écrits sur **stderr** — `--dry-run -o json` reste pipeable.

**Preuve**
```bash
pvectl vm stop 100 --dry-run     # affiche le plan, n'appelle rien
echo | pvectl vm stop 100; echo $?   # → 5, refus sans TTY ni --yes
```

**Ce que ça doit t'apprendre** — Qu'un `--dry-run` honnête (qui affiche le *payload résolu*, pas une paraphrase) est le meilleur outil d'apprentissage : il te montre l'API que tu es en train d'appeler.

---

### PVX-021 — Implémenter le pipeline de mutation générique
**Taille** L · **Type** ⚙ · **Dépend de** PVX-019, PVX-020 · **PRD** §5.3

En tant que développeur, je veux une fonction unique qui orchestre toute écriture, afin qu'aucune commande ne puisse oublier une étape de sécurité.

**Critères d'acceptation**
- Séquence imposée : **1. pre-read** (l'objet existe ? est-il verrouillé ?) → **2. plan** rendu → **3. gate** (`--dry-run` / confirmation) → **4. write** → **5. poll** UPID → **6. log** si échec → **7. post-read** (preuve indépendante).
- Si la réponse n'est pas un UPID (mutation synchrone), les étapes 5 et 6 sont sautées, jamais l'étape 7.
- Un verrou (`lock`) détecté au pre-read interrompt avant l'écriture et suggère `pvectl task ls --running`.
- Le résultat final affiché est **le post-read**, pas l'écho de la requête.
- Un test de service vérifie l'ordre exact des appels via un mock, et échoue si le post-read est absent.
- Documenté dans `docs/API-MAP.md` comme contrat d'écriture du projet.

**Preuve**
```bash
go test ./internal/service -run TestMutationPipelineOrder
```

**Ce que ça doit t'apprendre** — Le contrat de mutation du playbook, transformé en code : *ne jamais élargir une écriture demandée*, et toujours prouver le résultat par une lecture indépendante.

---

### PVX-022 — Piloter l'état des VM
**Taille** M · **Type** W · **Dépend de** PVX-021 · **PRD** §6.2

En tant qu'opérateur, je veux démarrer, arrêter et redémarrer une VM, afin de gérer le cycle d'exécution sans l'interface web.

**Critères d'acceptation**
- `vm start|stop|shutdown|reboot|reset|suspend|resume <vmid>` → `POST /nodes/{n}/qemu/{id}/status/{action}`.
- L'aide de chaque sous-commande documente **l'endpoint appelé** et la différence `stop` (coupure brutale) vs `shutdown` (ACPI, nécessite un OS coopératif).
- `shutdown` accepte `--timeout` et `--force-stop` ; le comportement de chaque option est explicité dans l'aide.
- Toutes passent par le pipeline PVX-021.
- Une action sur une VM déjà dans l'état cible ne produit pas d'erreur : message « déjà dans l'état demandé », code `0`, aucun appel d'écriture.

**Preuve**
```bash
pvectl vm stop 100 --dry-run
pvectl vm start 100          # UPID affiché → attente → post-read: status=running
```

**Ce que ça doit t'apprendre** — Que `stop` et `shutdown` sont deux endpoints différents avec deux conséquences différentes sur les données du guest. La confusion entre les deux est la première cause de corruption en homelab.

---

### PVX-023 — Piloter l'état des conteneurs LXC
**Taille** S · **Type** W · **Dépend de** PVX-022 · **PRD** §6.2

En tant qu'opérateur, je veux les mêmes actions sur les LXC, afin de valider le service Nginx du chapitre 03 de bout en bout.

**Critères d'acceptation**
- `lxc start|stop|shutdown|reboot|suspend|resume <ctid>` → `POST /nodes/{n}/lxc/{id}/status/{action}`.
- Les actions **non disponibles** pour LXC ne sont pas exposées : la CLI ne propose pas une commande qui renverra un `501`/`400`.
- Le code partagé avec PVX-022 est factorisé — c'est le moment de trancher la décision ouverte **D4** (interface `Guest` commune ou implémentations séparées) et de noter le choix dans `docs/LEARNING-LOG.md`.

**Preuve** *(preuve de fin de lot M2)*
```bash
pvectl lxc ls                        # repérer le CTID du Nginx
pvectl lxc stop <ctid>               # UPID → poll → exitstatus OK → post-read status=stopped
pvectl lxc start <ctid>
curl -sI http://<ip-du-lxc>/ | head -1   # → HTTP/1.1 200 OK
```

**Ce que ça doit t'apprendre** — Où l'API LXC diverge réellement de l'API QEMU. Cette réponse, obtenue par l'expérience et non par supposition, décide de l'architecture du reste de la CLI.
