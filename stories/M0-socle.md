# M0 — Socle

> Chapitres 00 & 01 du manuel · PVX-001 → PVX-009
> **Preuve de fin de lot** : `pvecli version` renvoie la version réelle du nœud `192.0.2.23`, en TLS vérifié (pas `--insecure`), avec un token non-root.

À la fin de ce lot, la CLI ne sait presque rien faire — mais tout ce qu'elle fait, elle le fait proprement : config, auth, TLS, erreurs, traces. C'est le lot qui détermine si le reste du projet sera agréable ou pénible.

---

### PVX-001 — Amorcer le projet Go et la racine Cobra
**Taille** S · **Type** ⚙ · **Dépend de** — · **PRD** §5.2

En tant que développeur de la CLI, je veux un squelette Go avec Cobra et une arborescence figée, afin que chaque story suivante ait une place évidente où atterrir.

**Critères d'acceptation**
- Étant donné un dossier vide, quand je lance `go build ./...`, alors un binaire `pvecli` est produit.
- `pvecli` sans argument affiche l'aide et sort en code `0`.
- `pvecli --version` affiche la version du binaire, injectée au build via `-ldflags "-X main.version=..."`.
- L'arborescence `cmd/`, `internal/{config,pve,service,output,log}`, `testdata/`, `docs/` existe.
- Un `Makefile` expose `build`, `test`, `lint`, `fmt`.

**Preuve**
```bash
make build && ./pvecli --version   # → pvecli dev (commit abc1234)
```

**Ce que ça doit t'apprendre** — La distinction `pvecli --version` (version du binaire) vs `pvecli version` (version du nœud PVE) : deux notions que la plupart des CLI confondent.

---

### PVX-002 — Charger la configuration par couches
**Taille** M · **Type** ⚙ · **Dépend de** PVX-001 · **PRD** §7.1

En tant qu'opérateur, je veux que la config se résolve dans l'ordre flags > env > fichier > défauts, afin de ne pas retaper l'endpoint et le nœud à chaque commande.

**Critères d'acceptation**
- Étant donné `~/.config/pvecli/config.yaml` contenant un contexte `lab`, quand je lance une commande sans flag, alors l'endpoint du contexte courant est utilisé.
- Étant donné `PVE_API_URL` défini, quand le fichier définit aussi un endpoint, alors l'environnement gagne.
- Étant donné `--endpoint`, alors il gagne sur tout le reste.
- `pvecli config init` crée le fichier avec les bons droits (`0600`), `config show` l'affiche **avec le secret masqué**, `config set <clé> <valeur>` le modifie.
- Le champ `token_secret` est **refusé** dans le fichier : erreur explicite invitant à passer par l'environnement.
- Les noms d'env sont `PVE_API_URL`, `PVE_API_TOKEN_ID`, `PVE_API_TOKEN_SECRET`, `PVE_INSECURE` — identiques au client `pve-api` du repo, pour rester interopérable.

**Preuve**
```bash
pvecli config init && pvecli config show   # secret absent, endpoint=https://192.0.2.23:8006
PVE_API_URL=https://autre:8006 pvecli config show | grep autre
```

**Ce que ça doit t'apprendre** — Pourquoi un secret ne vit jamais dans un fichier de config versionnable, et comment une CLI signale une mauvaise pratique au lieu de l'accepter silencieusement.

---

### PVX-003 — Construire le client HTTP PVE authentifié par token
**Taille** M · **Type** ⚙ · **Dépend de** PVX-002 · **PRD** §7.2

En tant que développeur, je veux un client HTTP unique qui porte l'authentification par token, afin qu'aucune commande n'ait à connaître le format de l'en-tête.

**Critères d'acceptation**
- Le client émet `Authorization: PVEAPIToken=<user>@<realm>!<tokenid>=<secret>` sur chaque requête.
- Aucun CSRF n'est demandé (spécifique aux tickets, pas aux tokens) — un commentaire le documente.
- Timeout par défaut de 30 s, surchargeable par `--timeout`.
- Le corps de réponse `{"data": …}` est déballé automatiquement ; le type de `data` est laissé au décodeur appelant.
- Si `PVE_API_TOKEN_SECRET` est absent, la commande échoue **avant** tout appel réseau, avec le code de sortie `3` et un message qui explique où le définir.
- Un `RoundTripper` de test permet d'injecter un `httptest.Server` — le client est utilisable sans nœud réel.

**Preuve**
```bash
go test ./internal/pve -run TestAuthHeader   # vérifie le format exact de l'en-tête
```

**Ce que ça doit t'apprendre** — La différence token vs ticket dans PVE, et pourquoi le token dispense du `CSRFPreventionToken`.

---

### PVX-004 — Vérifier le TLS du lab sans le désactiver
**Taille** M · **Type** ⚙ · **Dépend de** PVX-003 · **PRD** §7.3

En tant qu'opérateur d'un lab à certificat auto-signé, je veux vérifier réellement le certificat par pinning d'empreinte, afin de ne pas prendre l'habitude de `--insecure`.

**Critères d'acceptation**
- Trois modes : `tls.fingerprint` (SHA-256, recommandé), `tls.ca_file`, `--insecure` / `PVE_INSECURE=1`.
- Le mode par défaut est la vérification standard ; si le certificat est auto-signé et qu'aucun fingerprint n'est configuré, l'erreur **propose la commande exacte** pour récupérer et enregistrer l'empreinte.
- Étant donné un fingerprint configuré, quand le certificat du serveur ne correspond pas, alors la connexion est refusée avec un message distinguant clairement « certificat inconnu » de « certificat changé ».
- `--insecure` fonctionne mais écrit un avertissement sur **stderr à chaque appel**, jamais sur stdout.
- Une sous-commande `pvecli config trust` récupère l'empreinte du serveur, l'affiche et demande confirmation avant de l'enregistrer.

**Preuve**
```bash
pvecli config trust                 # affiche l'empreinte de 192.0.2.23, demande confirmation
pvecli version                      # fonctionne SANS --insecure
pvecli version --insecure 2>&1 >/dev/null | grep -i warning
```

**Ce que ça doit t'apprendre** — Pourquoi `insecure = false` figure dans le `main.tf` du repo, et ce que ça coûte réellement de faire les choses correctement (5 minutes, une fois).

---

### PVX-005 — Afficher la version du nœud (`pvecli version`)
**Taille** S · **Type** R · **Dépend de** PVX-003, PVX-004 · **PRD** §6.2

En tant qu'opérateur, je veux lire la version de PVE, afin d'avoir un premier appel API de bout en bout et de connaître la version qui conditionne tous les schémas d'endpoints.

**Critères d'acceptation**
- Appelle `GET /version`.
- Affiche `version`, `release`, `repoid`.
- Le résultat est mémorisé dans le contexte de config (`detected_version`) — il servira à trancher les questions de disponibilité d'endpoints.

**Preuve**
```bash
pvecli version    # → PVE 8.x.y (release …, repoid …)
```

**Ce que ça doit t'apprendre** — Que la version du nœud est la première information à établir : un endpoint « qui n'existe pas » est très souvent un endpoint d'une autre version.

---

### PVX-006 — Lister et décrire les nœuds
**Taille** S · **Type** R · **Dépend de** PVX-005 · **PRD** §6.2

En tant qu'opérateur, je veux `pvecli node ls` et `node show <node>`, afin de connaître le nom exact du nœud que toutes les autres commandes exigeront dans leur chemin.

**Critères d'acceptation**
- `node ls` appelle `GET /nodes` : colonnes nom, statut, CPU, RAM utilisée/totale, uptime.
- `node show <node>` appelle `GET /nodes/{node}/status` : version du kernel, charge, mémoire, rootfs.
- Si un seul nœud existe, il devient le `node` par défaut de la config lors du premier `node ls`.

**Preuve**
```bash
pvecli node ls    # → une ligne, le nœud du lab, statut online
```

**Ce que ça doit t'apprendre** — Que presque tous les chemins de l'API PVE sont préfixés `/nodes/{node}/` : le nom du nœud est une donnée structurante, pas un détail.

---

### PVX-007 — Traduire les erreurs HTTP en diagnostics actionnables
**Taille** M · **Type** ⚙ · **Dépend de** PVX-003 · **PRD** §7.5

En tant qu'opérateur, je veux qu'une erreur me dise quoi vérifier, afin de ne pas relire la doc à chaque échec.

**Critères d'acceptation**
- Type `pve.APIError` portant : code HTTP, méthode, chemin, message serveur, piste de diagnostic.
- Mapping conforme au tableau du PRD §7.5 : `401` (format d'en-tête, token_id, secret, realm, expiration), `403` (ACL, propagation, privilege separation → suggère `pvecli access whoami`), `400` (schéma → suggère `search-pve-api.ts`), `404` (node/vmid/storage/version), verrou, échec de tâche.
- Codes de sortie : `0` succès, `1` générique, `2` usage, `3` auth/authz, `4` tâche PVE échouée, `5` confirmation refusée.
- Le message serveur brut reste consultable via `--verbose`, **sans jamais** exposer de secret.

**Preuve**
```bash
PVE_API_TOKEN_SECRET=faux pvecli node ls; echo $?   # → 3, message parlant du token
```

**Ce que ça doit t'apprendre** — La différence entre `401` (je ne sais pas qui tu es) et `403` (je sais qui tu es, tu n'as pas le droit) : le second ne se corrige jamais en changeant de token, mais en corrigeant une ACL.

---

### PVX-008 — Diagnostiquer la cible (`pvecli doctor`)
**Taille** M · **Type** R · **Dépend de** PVX-006, PVX-007 · **PRD** Annexe B

En tant qu'opérateur, je veux une commande qui vérifie toute la chaîne d'accès, afin de savoir en une seule fois si le problème vient du réseau, du TLS, du token ou des privilèges.

**Critères d'acceptation**
- Exécute la séquence d'inspection minimale du playbook : `GET /version` → `GET /cluster/status` → `GET /nodes` → `GET /access/permissions`.
- Affiche une ligne par vérification avec ✓ / ✗ et, en cas d'échec, la piste de diagnostic issue de PVX-007.
- S'arrête à la première étape bloquante mais indique les étapes non exécutées.
- Signale explicitement si `--insecure` est actif et si le token utilisé est `root@pam`.

**Preuve**
```bash
pvecli doctor
# ✓ endpoint joignable   ✓ TLS vérifié (fingerprint)   ✓ token valide
# ✓ nœud pve online      ⚠ privilèges: PVEAuditor (lecture seule)
```

**Ce que ça doit t'apprendre** — L'ordre de diagnostic : toujours du plus bas niveau (réseau) au plus haut (privilèges). Diagnostiquer une ACL avant d'avoir confirmé le TLS fait perdre des heures.

---

### PVX-009 — Tracer les requêtes sans fuiter de secret
**Taille** S · **Type** ⚙ · **Dépend de** PVX-007 · **PRD** §7.2, §9

En tant que développeur, je veux `--verbose` qui trace les échanges HTTP avec les secrets masqués, afin de déboguer sans risque.

**Critères d'acceptation**
- `--verbose` écrit sur **stderr** : méthode, URL, code de réponse, durée.
- `-vv` ajoute les en-têtes et le corps ; l'en-tête `Authorization` est remplacé par `<redacted>`.
- Les champs sensibles du corps (`password`, `ticket`, `csrf`, `value` d'un token créé, clés privées) sont masqués.
- Un test injecte un secret connu et **scanne toute la sortie** pour vérifier qu'il n'apparaît nulle part.

**Preuve**
```bash
go test ./internal/log -run TestNoSecretLeak
pvecli node ls -vv 2>&1 | grep -c "PVEAPIToken=" # → 0
```

**Ce que ça doit t'apprendre** — Qu'une fuite de secret n'arrive presque jamais par le code métier, mais par les logs de debug écrits à la va-vite.
