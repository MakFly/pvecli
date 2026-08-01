# Journal d'apprentissage

Une entrée par story : quel endpoint, quelle surprise, quelle erreur commise,
quelle règle retenue. C'est un livrable au même titre que le code.

---

## 2026-07-31 — PVX-001, amorcer le projet Go et la racine Cobra

**Endpoint** — aucun. Story d'infra.

**Ce que j'ai appris** — `pvecli --version` et `pvecli version` ne parlent pas de
la même chose, et la plupart des CLI les confondent :

| | Répond quoi | D'où vient l'info |
| --- | --- | --- |
| `pvecli --version` | version **du binaire** | injectée au link : `-ldflags "-X main.version=…"` |
| `pvecli version` | version **du nœud PVE** | `GET /api2/json/version` |

La confusion n'est pas cosmétique : sur un poste, `--version` répond hors ligne
et sans token, alors que `version` a besoin du réseau, du TLS et d'une
authentification. Deux commandes qui échouent pour des raisons totalement
différentes ne doivent pas porter le même nom.

**Surprise** — Cobra s'empare du raccourci `-v` pour `--version` dès qu'on
renseigne le champ `Version`, *sauf* si le flag `version` est déjà déclaré. Or
`-v` / `-vv` sont réservés à `--verbose` (PVX-009). D'où le flag déclaré à la
main dans `cmd/root.go` avant l'affectation de `root.Version` — et un test qui
vérifie que `-v` reste libre, pour que le jour où quelqu'un simplifie ce code,
la régression se voie.

**Erreur commise** — avoir cru que `go build ./...` écrivait le binaire. Non :
dès qu'il y a plus d'un paquet dans la liste, Go compile pour vérifier et jette
le résultat. Ce qui produit `pvecli`, c'est `go build .` (ou `make build`, qui
seul injecte la version). Le critère d'acceptation de la story est donc à lire
comme « la compilation passe ».

**Règle retenue** — le `version` du nœud est réservé dès le premier jour, avec
une commande qui échoue explicitement en pointant PVX-005, plutôt qu'un verbe
laissé libre qu'on finira par câbler sur la mauvaise valeur.

**Clôture** — dépôt initialisé et publié sur
[MakFly/pvecli](https://github.com/MakFly/pvecli) ; `make build` injecte
désormais le vrai SHA court. Le module porte son chemin définitif
(`github.com/MakFly/pvecli`) dès la première story : le renommer coûtait trois
fichiers maintenant, quarante à M7. `golangci-lint` 2.12 passe à zéro issue,
configuration épinglée dans `.golangci.yml`.

---

## 2026-07-31 — PVX-002, charger la configuration par couches

**Endpoint** — toujours aucun. Dernière story avant le premier appel réseau.

**Ce que ça a appris** — la précédence `flags > env > fichier > défauts` tient en
quatre lignes quand on l'écrit soi-même, et Viper l'aurait résolue sans qu'on
voie comment. Le choix payant n'est pas le layering lui-même mais la trace de sa
provenance : chaque valeur retient la couche qui l'a gagnée, et `config show`
l'affiche. Une configuration en couches qui ne sait pas dire d'où vient une
valeur est une configuration qu'on débogue en devinant.

Le secret, lui, n'a qu'une source : l'environnement. Ni le fichier — refusé —
ni un flag : un flag est visible dans `ps` par tous les utilisateurs de la
machine et atterrit dans l'historique du shell. Ce n'est pas un manque à
combler plus tard.

**Surprise** — `os.WriteFile` n'applique son mode qu'à la **création**. Un
`config.yaml` déjà en `0644` garde ses droits pour toujours, malgré un
`WriteFile(path, data, 0o600)` qui a l'air de dire le contraire. D'où le
`os.Chmod` explicite derrière, et un test qui part d'un fichier en 0644.

**Erreur commise** — deux, dont une héritée de PVX-001.

1. J'avais activé `misspell` dans `.golangci.yml` à la story précédente, quand
   le code ne contenait aucun texte utilisateur. Dès que la CLI s'est mise à
   parler français, le linter a signalé « présence », « contextes »,
   « existant » comme des fautes d'anglais. Un linter qui ne connaît qu'une
   langue n'a pas sa place dans un projet qui en parle deux : retiré, plutôt
   qu'une liste d'exceptions qui aurait grossi à chaque message.

2. `MkdirAll(dir, 0o700)` ne change pas les droits d'un dossier **existant**.
   Mon test créait son dossier et validait `0700` ; sur la vraie machine,
   `~/.config/pvecli` préexistait en `0755` et y est resté. Le test disait vrai
   et se trompait de question.

**Règle retenue** — un test qui fabrique son propre environnement ne prouve rien
sur un environnement qui existait déjà. Les fonctions `MkdirAll` / `WriteFile`
sont idempotentes sur l'existence, pas sur les permissions : cette asymétrie ne
se voit que sur une machine qui a du passé.

**Reste ouvert** — `~/.config/pvecli` est en `0755`. Le fichier de config et le
fichier `env` sont bien en `0600`, donc leur contenu est protégé, mais le
dossier mériterait `chmod 700`. Non fait : il contient des fichiers antérieurs à
`pvecli`, le resserrement est une décision de l'opérateur.

---

## 2026-07-31 — PVX-003, le client HTTP authentifié par token

**Endpoint** — aucun appelé, mais le mécanisme d'authentification vérifié à la
source : le code Perl du nœud, pas la documentation. Détail dans
[`API-MAP.md`](API-MAP.md).

**Ce que ça a appris** — token et ticket sont deux mondes. Un ticket arrive dans
un cookie, que le navigateur attache **tout seul** à n'importe quelle requête :
d'où le `CSRFPreventionToken`, qui prouve que la page a voulu l'envoyer. Un
token, lui, n'est jamais attaché automatiquement — seul un client qui a décidé
de l'envoyer l'envoie. Le CSRF n'a donc rien à protéger. Ce n'est pas une
dispense arbitraire, c'est la conséquence directe du vecteur d'attaque.

Deuxième chose : déballer `{"data": …}` dans le client et **laisser le type de
`data` au décodeur appelant**. Ce champ est un objet pour `/version`, un tableau
pour `/nodes`, et une simple chaîne — un UPID — pour toute mutation. Un client
qui typerait `data` trop tôt se retrouverait à le détyper trois stories plus loin.

**Surprise** — le format de l'en-tête, je l'aurais écrit de mémoire sans me
tromper. La *règle de découpage*, non : `verify_token` coupe avec
`/^(.*)=(.*)$/`, dont le premier groupe est glouton — donc sur le **dernier**
`=`. Un nom de token peut contenir un `=`, un secret ne le peut pas. Aucune
documentation ne dit ça ; seul le source le dit. C'est exactement ce que la
règle « aucun endpoint écrit de mémoire » du PRD §6.3 vise à attraper.

**Erreur commise** — j'ai déclaré `--timeout` sur la racine alors qu'aucune
commande ne le lit encore : il sera consommé par le premier appel réseau réel
(PVX-005). Un flag visible qui ne fait rien est une petite dette ; je la note
plutôt que de la maquiller.

**Règle retenue** — quand le serveur est accessible, sa source est la meilleure
documentation : elle ne peut pas être en retard sur sa propre version. `ssh
root@nœud grep` a répondu en trente secondes à une question que la doc n'aborde
pas.

---

## 2026-07-31 — PVX-004 & PVX-005, vérifier le TLS et lire la version du nœud

Deux stories sur une seule branche : la preuve de PVX-004 est littéralement
`pvecli version` fonctionnant **sans** `--insecure`. L'une ne se démontre pas
sans l'autre.

**Endpoint** — `GET /version`, schéma vérifié par `pvesh get /version` sur le
nœud, réponse capturée dans `testdata/version.json`.

**Ce que ça a appris** — épingler une empreinte n'est pas « désactiver la
vérification en plus propre ». Techniquement, `InsecureSkipVerify: true` est bien
posé — mais il est remplacé par un `VerifyPeerCertificate` qui exige **ce
certificat précis**. Pour un hôte unique et connu, c'est strictement plus fort
qu'une chaîne de CA : une CA publique compromise signerait n'importe quel
certificat pour ce nom, une empreinte épinglée n'en accepte qu'un. Le lab n'est
pas un cas dégradé de la production, c'est un cas où le pinning est le bon outil.

L'autre distinction qui compte : **certificat inconnu** et **certificat changé**
ne sont pas le même événement. Le premier est une étape d'installation, il
propose `pvecli config trust`. Le second est un incident, il refuse et donne la
commande à lancer *sur la console du nœud* pour trancher. Les confondre sous un
« certificate verify failed » commun, c'est apprendre à cliquer sur « continuer ».

**Surprise** — `os.Stdin.Stat()` et `mode & os.ModeCharDevice` ne répondent pas à
la question « suis-je dans un terminal ». `/dev/null` **est** un périphérique
caractère : `pvecli config trust </dev/null` traversait le test, posait sa
question dans le vide, et échouait sur la lecture au lieu de refuser d'emblée.
Le code de sortie était bon (5) pour la mauvaise raison, et le message mentait.
`term.IsTerminal` fait l'ioctl qui répond vraiment. C'est exactement le piège
noté au journal lors de la rotation du mot de passe root — je l'ai reproduit en
code avant de le reconnaître.

**Erreur commise** — mes tests de `cmd` lisaient mon vrai `~/.config/pvecli`. Le
test de `version` passait parce que *ma* machine a une configuration valide ;
sur une machine vierge il aurait échoué pour une autre raison que celle testée.
Corrigé par un `TestMain` qui pointe `PVECLI_CONFIG` sur un dossier temporaire
et vide les variables `PVE_*`.

**Règle retenue** — un test qui n'isole pas son environnement ne teste pas le
code, il teste la machine. Et un message d'erreur qui donne le bon code pour la
mauvaise raison est plus dangereux qu'une erreur franche : il survit à la revue.

---

## 2026-07-31 — PVX-006 & PVX-007, lister les nœuds et traduire les erreurs

**Endpoints** — `GET /nodes` et `GET /nodes/{node}/status`, schémas relevés par
`pvesh get` sur le nœud, réponses capturées dans `testdata/`.

**Ce que ça a appris** — la différence entre `401` et `403` n'est pas une nuance
de code HTTP, c'est une différence de *geste correctif*. Un `401` dit « je ne
sais pas qui tu es » : on change de token, de secret, de realm. Un `403` dit
« je sais qui tu es, et non » : changer de token ne corrigera jamais rien, c'est
une ACL qu'il faut corriger. Un diagnostic qui ne fait pas cette distinction
envoie l'opérateur régénérer des tokens pendant une heure.

Le diagnostic du `403` nomme donc le **chemin** refusé — parce qu'une ACL se
pose sur un chemin — et rappelle la règle de `privsep=1` : les droits effectifs
d'un token sont l'**intersection** des siens et de ceux de son utilisateur.
C'est précisément le piège que le lab avait déjà tendu lors de la création du
token ; le code le rappelle maintenant tout seul.

**Surprise** — un nœud inexistant ne renvoie pas `404` mais `500`, avec
`hostname lookup 'inexistant' failed`. PVE résout le nom de nœud en nom d'hôte
avant de router : l'échec est donc une erreur d'exécution, pas un chemin
inconnu. Le `404` reste pour les endpoints qui n'existent pas — souvent parce
qu'ils appartiennent à une autre version de PVE.

Autre piège, celui qui coûte un facteur 100 : `cpu` est un **ratio 0..1**, pas
un pourcentage. Sur un nœud au repos, l'afficher brut donne « 0.0014 » et
personne ne remarque ; sur un nœud chargé, ça donne « 1 » là où on attend
« 100 % ». Les champs `mem`/`maxmem` sont en octets, et `maxcpu` compte les
threads (16) là où `cpuinfo.cores` compte les cœurs (8). Tout ça est consigné
dans `API-MAP.md`.

**Erreur commise** — j'ai commencé `adoptSingleNode` avec une interface
`pveNodeNamer` pour « ne pas dépendre du type concret ». Une abstraction pour un
seul appelant, dans un paquet qui importe déjà `pve` : supprimée avant même de
compiler. C'est le réflexe que le PRD appelle de la flexibilité non demandée.

**Règle retenue** — un message d'erreur utile ne décrit pas ce qui s'est passé,
il indique quoi vérifier, dans quel ordre. `HTTP 403` décrit ; « ACL, chemin,
propagation, privilege separation, dans cet ordre » indique.

---

## 2026-07-31 — PVX-008 & PVX-009, diagnostiquer et tracer

**Endpoints** — `GET /cluster/status` et `GET /access/permissions`, ajoutés à la
séquence de `doctor`.

**Ce que ça a appris** — l'ordre de diagnostic est le livrable, pas la liste des
vérifications. `doctor` monte du plus bas niveau au plus haut — réseau, TLS,
authentification, nœud, privilèges — et s'arrête à la première étape
**bloquante** en affichant les suivantes comme « non exécuté ». La raison est
concrète : la moitié des « 403 » d'un débutant sont en réalité un certificat
refusé ou un secret absent. Diagnostiquer une ACL avant d'avoir confirmé le TLS,
c'est chercher la panne au mauvais étage.

Sur la trace : la redaction est faite **deux fois, à deux endroits**. Le client
remplace l'en-tête `Authorization` avant même de passer quoi que ce soit au
traceur — aucun secret ne sort du paquet `pve` — et le traceur redacte à nouveau
par motifs et par littéraux. Ce n'est pas de la paranoïa décorative : le premier
niveau protège contre un traceur mal écrit, le second contre un secret qui
arriverait par un chemin qu'on n'a pas prévu (une URL de redirection, un message
d'erreur renvoyé par le nœud).

L'identifiant du token, lui, **reste lisible**. Ce n'est pas un secret, et c'est
exactement ce que le diagnostic du `401` demande d'aller vérifier — realm
compris. La coupure se fait sur le **dernier** `=`, comme le fait
`verify_token` côté serveur : redacter sur une frontière différente de celle où
le serveur découpe finirait par afficher une moitié de secret.

**Surprise** — le test écrit à PVX-001 pour garder `-v` libre a échoué
aujourd'hui, au moment exact où `--verbose` est arrivé et a pris le raccourci.
Le garde-fou a fonctionné : il a signalé un changement volontaire au lieu de le
laisser passer. Il est devenu une assertion positive — `-v` appartient à
`--verbose` — parce que l'inverse serait une régression silencieuse : `-v`
afficherait une version au lieu de tracer, et rien d'autre ne le remarquerait.

**Erreur commise** — j'ai d'abord écrit la redaction en deux expressions
régulières successives : une pour `PVEAPIToken=id=secret`, une pour la forme
sans identifiant. La seconde annulait la première, remplaçant
`PVEAPIToken=id=<redacted>` par `PVEAPIToken=<redacted>`. Deux règles qui se
marchent dessus valent moins qu'une fonction de remplacement qui décide.

**Règle retenue** — un test de non-fuite ne doit pas vérifier que la redaction a
été appliquée là où on y a pensé. Il injecte un secret connu et scanne **toute**
la sortie : c'est le seul protocole qui attrape le chemin auquel personne n'a
pensé.

---

## Lot M0 — clos le 2026-07-31

**Preuve obtenue** : `pvecli version` renvoie `PVE 9.2.2 (release 9.2, repoid
b9984c6d90a4bd80)`, en TLS vérifié par empreinte épinglée — pas `--insecure` —
avec le token non-root `automation@pve!pvectl`.

`pvecli doctor` confirme la chaîne complète : endpoint joignable, cluster
interrogeable, nœud `pve` online, privilèges en lecture seule (aucun droit hors
`*.Audit`).

---

## 2026-07-31 — PVX-010 & PVX-017, rendus et harness de test

**Endpoints** — aucun nouveau. Deux stories d'infrastructure qui conditionnent
tout M1.

**Ce que ça a appris** — en mode `json`, la sortie doit être **la valeur typée
elle-même**, pas un enrobage maison. Si on encapsule dans
`{"items": [...], "count": 2}`, chaque script devra écrire `.items[]` au lieu de
`.[]`, et les noms de champs cesseront de correspondre à ce que le nœud a
renvoyé. La table n'est qu'une projection humaine de la même donnée ; c'est la
donnée qui est le contrat.

L'autre décision : le remplissage des colonnes est désactivé quand stdout n'est
pas un terminal. Une sortie alignée à l'œil se lit mal avec `cut -f`, qui attend
des tabulations simples. La CLI produit donc deux formes de table selon son
lecteur — et une seule forme de JSON.

**Le vrai livrable de PVX-017** — la règle « aucun endpoint écrit de mémoire »
du PRD §6.3 est passée de l'intention au **test**. Deux garde-fous :

1. `TestNoInlineEndpoint` parcourt l'AST de `internal/pve` et échoue si un
   littéral de chemin est passé à `get`/`do`. Les chemins ne vivent que dans
   `endpoints.go`.
2. `TestAPIMapCoverage` échoue si un endpoint déclaré est absent de
   `docs/API-MAP.md`.

Conséquence : un endpoint non documenté ne peut plus atteindre `main`. La
méthode `Get` du client est devenue `get`, non exportée — un appelant extérieur
ne peut plus inventer un chemin, seulement appeler une méthode qui en déclare un.

`Path()` échappe aussi les valeurs substituées. Ça paraît gratuit aujourd'hui ;
ça ne le sera plus à PVX-015, où un UPID contient des `:` et voyage dans un
segment de chemin.

**Erreur commise** — la même que jeudi, un paquet plus loin. J'avais isolé les
tests de `cmd` de l'environnement du développeur, mais pas ceux de
`internal/config`. Un `export PVE_API_URL` dans mon shell a fait tomber deux
tests de précédence. Le correctif était connu, il n'avait simplement pas été
appliqué partout.

**Règle retenue** — quand on corrige une classe de défaut, la corriger dans un
seul paquet ne corrige rien : il faut se demander tout de suite où d'autre elle
existe. Une leçon apprise et appliquée à un seul endroit est une leçon à moitié
apprise.

---

## 2026-07-31 — PVX-011 → 016, l'inventaire en lecture

**Endpoints** — `/nodes/{n}/qemu`, `/nodes/{n}/lxc`, leurs `config` et
`status/current`, `/nodes/{n}/storage`, `.../storage/{s}/content`,
`/nodes/{n}/tasks` et ses `status`/`log`, `/cluster/resources`. Tous relevés à
la source, tous dans `API-MAP.md`.

**Ce que ça a appris** — trois formes de données qu'il fallait rencontrer avant
d'écrire quoi que ce soit :

1. **Un template est une VM avec un drapeau.** `template: 1` dans le même index
   que les VM, pas un objet d'un autre type. Tout le clonage en découle : on
   clone une VM, pas un « objet template ».
2. **La chaîne à options** — `virtio0: local-lvm:vm-100-disk-0,size=20G`. Le
   premier élément est tantôt positionnel (un volid pour un disque), tantôt
   déjà une paire clé=valeur (`virtio=AA:BB` pour une carte réseau). Cette
   asymétrie n'est documentée nulle part ; le parseur doit tolérer les deux, et
   surtout savoir se **relire** — PVX-026 modifiera une option et réécrira la
   valeur entière.
3. **Le `volid`** — `local:iso/debian.iso`, pas un chemin de fichier. Tous les
   endpoints attendent cette forme.

**Erreur commise, la plus instructive de la journée** — j'ai écrit
`?running=1` pour filtrer les tâches actives. De mémoire. Le paramètre n'existe
pas : `pvesh usage /nodes/pve/tasks -v` donne `--source <active|all|archive>`.
La règle §6.3 du PRD m'a attrapé en une minute, sur la seule story où j'avais
sauté la vérification. C'est exactement le scénario qu'elle décrit.

**Surprise** — `url.URL` garde chemin et query dans deux champs, et `String()`
échappe ce qui est dans `Path`. Concaténer `"?limit=5"` au chemin produit donc
une requête vers un chemin contenant littéralement un point d'interrogation, à
laquelle PVE répond **501 « method not implemented »** — un message qui fait
chercher un endpoint manquant pendant que le bug est dans la construction de
l'URL.

Deuxième surprise, plus discrète : une slice Go `nil` se sérialise en `null`,
pas en `[]`. Sur un lab vierge, `pvecli vm ls -o json | jq '.[].name'` échouait
donc, non pas parce qu'il n'y a pas de VM, mais parce que la liste vide était
mal encodée. La preuve de fin de lot passait à côté de son propre sujet.

**Règle retenue** — quand un message d'erreur du serveur ne colle pas au symptôme
(501 pour un endpoint qui existe, 500 pour un nom de nœud inconnu), l'hypothèse
la plus probable n'est pas que le serveur se trompe : c'est que la requête n'est
pas celle qu'on croit envoyer. `-vv` affiche l'URL réellement émise, et tranche
en une seconde.

---

## 2026-07-31 — M2 & M3, la première VM créée et accessible

**Endpoints** — `POST /nodes/{n}/qemu/{id}/status/{action}`, `POST /nodes/{n}/qemu`,
`PUT .../config`, `DELETE .../qemu/{id}`. Tous vérifiés dans `PVE::API2::Qemu`.

**Le résultat** — `lab-app-01` (vmid 211), Debian 13, créée par `pvecli`,
démarrée par `pvecli`, joignable en SSH depuis le poste avec la clé injectée par
cloud-init. Rien n'est passé par l'interface web.

**Ce que ça a appris — le moindre privilège coûte, et c'est le sujet.** Le token
avait `PVEVMAdmin` sur `/vms` et `PVEDatastoreUser` sur les stockages. Deux refus
successifs, tous deux instructifs :

1. `Only root can pass arbitrary filesystem paths` — impossible de faire
   `import-from=/var/lib/vz/import/image.qcow2` avec un token non-root. La bonne
   forme est un **volid** : `local:import/debian-13-genericcloud-amd64.qcow2`.
   Le chemin de fichier est un privilège root ; l'identifiant de volume est
   l'abstraction que l'API expose à tout le monde. `pvecli storage content local
   --content import` donne exactement cette chaîne — l'outil servait déjà.
2. `Permission check failed (/sdn/zones/localnetwork/vmbr0, SDN.Use)` — PVE 9
   protège les ponts réseau par le SDN. Attacher une carte à `vmbr0` exige
   `SDN.Use` sur la zone. Le message donne le chemin exact : l'ACL se corrige en
   une commande, sans rien deviner.

**Trois pièges d'encodage, tous silencieux, tous coûteux**

- `sshkeys` est stocké **URL-encodé** côté PVE (`uri_unescape` dans
  `Cloudinit.pm`). Il faut donc l'encoder *en plus* de l'encodage de formulaire.
  Mais `url.QueryEscape` encode l'espace en `+`, convention des formulaires HTML
  et non de la RFC 3986 : PVE refuse avec `invalid urlencoded string`. Il faut
  `%20`.
- `url.PathEscape` échappe le `!`. L'UPID d'une tâche lancée par le token
  `automation@pve!pvectl` revenait donc en `no such task` — **la VM était créée**,
  seul le suivi était cassé. C'est la forme la plus déroutante qu'un bug puisse
  prendre : l'écriture réussit, la commande échoue. Un chemin ne doit échapper
  que ce qui casserait sa structure : `%` et `/`.
- `{"data": null}` est une réponse **normale** à une mutation synchrone — un
  `PUT config` sur une VM éteinte. Mon client la traitait comme une erreur, et
  faisait donc échouer une écriture réussie.

**Erreur commise** — j'ai fait confiance à `message` dans les réponses d'erreur.
PVE y met souvent le générique `Parameter verification failed.` et met la
réponse utile dans `errors`. En inversant la priorité, `sshkeys: invalid format
- invalid urlencoded string` est apparu du premier coup, là où le message
générique m'avait envoyé lire un schéma pendant dix minutes.

**Règle retenue** — un `--dry-run` qui affiche le **payload résolu** vaut une
documentation. Les trois pièges ci-dessus se sont tous diagnostiqués en
comparant ce que le plan affichait avec ce que le nœud répondait. Une
paraphrase n'aurait rien montré.

---

## 2026-07-31 — PVX-024 & PVX-027, le template et le clone

**Endpoints** — `POST .../qemu/{id}/template` et `POST .../qemu/{id}/clone`,
schémas lus dans `PVE::API2::Qemu` (`clone_vm`, `template`) sur le nœud.

**Preuve de fin de lot M3 obtenue** — template `9000` (`debian13-cloudinit`)
construit par `pvecli`, cloné en `212` (`lab-app-02`), clone joignable en SSH
avec la clé que le template portait. Sans ouvrir l'interface web.

**Ce que ça a appris** — le niveau de confirmation doit suivre la
**réversibilité**, pas la brutalité du verbe. `vm template` ne supprime rien, et
c'est pourtant l'opération la plus irréversible du lot : une VM convertie ne
peut plus démarrer, ses disques deviennent des images de base en lecture seule,
et il n'existe aucune commande inverse. Elle est donc traitée comme destructive
— retaper le vmid, pas « y ». À l'inverse, `vm start` est bruyant mais anodin.
Classer les commandes par le mot qu'elles portent, plutôt que par ce qu'elles
coûtent à défaire, produit exactement les mauvais garde-fous.

Le clone lié, lui, est le vrai piège pédagogique : par défaut PVE fait un clone
**lié**, qui partage les blocs de sa source et ne stocke que ses différences. Il
se crée en une seconde et ne coûte presque rien — mais détruire le template
casse tous ses clones. L'interface web ne le dit pas ; l'aide de la commande le
dit.

**Erreur commise** — ma sortie affichait `template 1` même en `--dry-run`. La
ligne était codée en dur au lieu d'être lue dans le post-read, et le pipeline
renvoie le **pre-read** quand rien n'a été écrit. Une commande qui simule ne doit
pas afficher le résultat qu'elle aurait obtenu : c'est précisément ce que
`--dry-run` promet de ne pas faire.

**Reste ouvert** — seul le clone `--full` a été exercé contre le lab. Le
comportement du clone lié et la casse annoncée à la suppression du template sont
documentés d'après le schéma et le manuel, pas vérifiés ici.

---

## 2026-07-31 — PVX-028 & PVX-029, le filet et l'agent

**Endpoints** — `GET/POST .../qemu/{id}/snapshot`, `.../snapshot/{nom}/rollback`,
`DELETE .../snapshot/{nom}`, `GET .../agent/network-get-interfaces`. Vérifiés
par `pvesh usage` et, pour les équivalents LXC, dans
`PVE::API2::LXC::Snapshot` — que j'allais écrire par symétrie avec QEMU avant
que le test de couverture d'`API-MAP.md` ne m'y oblige.

**Ce que ça a appris — un snapshot n'est pas une sauvegarde.** Il vit sur le
**même stockage** que le disque qu'il protège : si ce stockage meurt, les deux
meurent ensemble. C'est un point de retour local, pas une copie indépendante.
Le snapshot est le filet qu'on pose avant une expérimentation ; la sauvegarde
(M5) est ce qui protège d'une panne. Les confondre est l'erreur de PRA qui ne se
découvre qu'au moment où l'on en a besoin.

**PVE ne connaît pas l'IP d'une VM en DHCP.** L'hyperviseur voit une adresse MAC
sur un pont, et rien de plus : c'est l'invité qui sait dans quel réseau il s'est
inséré. L'agent est le seul canal par lequel il peut le dire — d'où le lien
direct avec l'inventaire Ansible de PVX-042. Je l'avais vécu sans le nommer :
pour trouver l'adresse de la VM 211, j'avais dû lire la table ARP du nœud.
`pvecli vm ip 212` répond maintenant en une commande.

**Vérifié pour de vrai, pas seulement en code** — fichier témoin créé, snapshot
pris, fichier supprimé et remplacé par un autre, retour arrière : le témoin est
revenu, l'autre a disparu. Le retour arrière ne négocie pas avec le système
invité, il remplace ses disques — et PVE arrête la VM au passage, puisqu'un
snapshot sans `vmstate` ne peut pas rendre une machine en cours d'exécution.
Le post-read affichait bien `stopped` : la commande dit ce qui s'est passé, pas
ce qu'on espérait.

**Surprise** — la liste des snapshots contient une entrée `current`
(« You are here! ») qui n'est **pas un snapshot** : c'est le marqueur de position
dans l'arbre. La restaurer n'a aucun sens, et l'afficher comme les autres serait
inviter à le tenter. Elle est donc étiquetée explicitement dans la table.

**Erreur commise** — j'ai groupé les quatre endpoints LXC en une seule ligne
`…/snapshot[…]` dans `API-MAP.md`. Le test a refusé : il compare des motifs
exacts, pas des familles. Il avait raison — une ligne qui résume quatre
endpoints ne prouve la vérification d'aucun des quatre.

**Règle retenue** — un message d'erreur doit nommer le geste, pas le symptôme.
PVE répond `500` que l'agent soit absent, arrêté, ou la VM éteinte ; laisser
passer ce `500` fait chercher une panne d'hyperviseur. Le traduire en « installe
`qemu-guest-agent`, et redémarre si la VM tournait déjà » transforme une impasse
en tâche de trois minutes.

---

## 2026-07-31 — PVX-030 & PVX-031, le conteneur et le contrat de propriété

**Endpoints** — `POST /nodes/{node}/lxc`, `DELETE /nodes/{node}/lxc/{vmid}`,
`PUT .../lxc/{vmid}/config`, `POST .../lxc/{vmid}/clone`. Vérifiés un par un
par `pvesh usage … -v` sur le nœud, et consignés ligne par ligne : le test de
couverture compare des motifs exacts, la leçon de PVX-028 a resservi.

**Ce que « non privilégié » change vraiment.** Ce n'est pas un drapeau de
confort. Dans un conteneur non privilégié, les UID sont décalés vers une plage
inutilisée de l'hôte. Vu du dedans :

```
$ ssh root@192.0.2.120 id -u
0
```

Vu de l'hôte, au même instant, le PID 1 de ce conteneur :

```
# ps -o uid,args -p $(lxc-info -n 120 -p -H)
  UID COMMAND
100000 /sbin/init
```

Root dedans est l'uid 100000 dehors. Un processus qui s'échappe se retrouve
donc en tant qu'utilisateur qui ne possède rien. Dans un conteneur privilégié,
root dedans est root dehors, et la frontière du conteneur devient la seule
chose qui sépare les deux. C'est pourquoi le défaut est structurel dans le
code : le champ s'appelle `Privileged`, et non `Unprivileged`, pour que la
valeur zéro d'une structure Go **soit** le comportement sûr. Un défaut qui
dépend de quelqu'un qui pense à le mettre n'est pas un défaut.

**Un `HTTP 200` n'est pas un succès — et un `exitstatus` ≠ `OK` n'est pas un
échec.** La création du conteneur a fini en `WARNINGS: 1` (« Systemd 257
detected. You may need to enable nesting »). Le conteneur existait, et `pvecli`
annonçait un échec — pire, il sautait le post-read, donc il ne montrait pas la
chose qu'il venait de créer. Corrigé par `Task.Succeeded()` : `OK` **et**
`WARNINGS: n` sont des succès, et les avertissements sont affichés au lieu
d'être perdus. Nier un changement qui a eu lieu est un mensonge exactement
aussi grave que d'en annoncer un qui n'a pas eu lieu.

**Erreur commise — un `DELETE` ne porte pas de corps de requête.**

```
Error: DELETE /nodes/pve/lxc/121 : HTTP 501 — Unexpected content for method 'DELETE'
```

`purge` et `destroy-unreferenced-disks` partaient dans un corps form-encodé,
comme pour un `POST`. Le serveur HTTP de PVE refuse avant même sa couche de
schéma, et le `501` qu'il renvoie est **le même** que celui d'un chemin
inconnu : le message accuse la méthode, la cause est le corps. Les paramètres
d'un `DELETE` passent par la query string. Ça valait un helper `del()` distinct
de `post()`, et ça vaut d'être écrit ici : le code partagé avec `vm rm --purge`
était faux depuis M2, et aucun test ne pouvait le voir — le serveur de rejeu
accepte n'importe quoi.

**Le contrat de propriété.** `pvecli vm rm 212` sur une ressource taguée
`managed` est refusé, dans le pre-read, avant qu'une seule écriture ne parte.
Le message ne dit pas « interdit », il dit **qui** est le propriétaire :
`terraform destroy`. C'est la notion qui évitera, au lot M6, la dérive entre
l'état live et le state Terraform — celle qu'on ne comprend plus trois semaines
plus tard. `--force-unmanaged` existe et est déconseillé dans le même souffle :
un opérateur qui ne peut pas passer outre une garde finit par contourner l'outil
entier.

**Deux fois où la sortie a menti, et les deux fois de la même façon** — en
disant ce qui avait été demandé plutôt que ce qui s'était passé.

- Le clone d'un conteneur ordinaire affichait « clone lié » alors que PVE avait
  rsyncé 546 Mo. Le clone lié n'existe qu'à partir d'un **template** ; ailleurs
  le drapeau est ignoré. La ligne lit maintenant la source.
- Le message « arrête-le d'abord » proposait `pvecli qemu shutdown 900`. Le type
  de guest côté API s'appelle `qemu`, la commande s'appelle `vm` : une suggestion
  qu'on ne peut pas copier-coller est une suggestion qui ne sert à rien.

**Règle retenue** — une commande ne doit jamais afficher son intention à la
place de son résultat. C'est déjà la leçon du `template = 1` en `--dry-run` de
PVX-027 ; elle s'est représentée deux fois en un lot, ce qui suggère que c'est
le mode de défaillance par défaut d'une CLI, pas un accident.

---

## 2026-07-31 — PVX-032 → PVX-036, un 403 est une information

**Endpoints** — `/access/permissions`, `/access/users`, `/access/roles[/{id}]`,
`/access/acl` (GET et PUT), `/access/users/{u}/token[/{t}]` en GET, POST et
DELETE. Neuf lignes dans `API-MAP.md`, une par méthode.

**Le modèle, en trois objets.** Un *privilège* est un atome (`VM.PowerMgmt`).
Un *rôle* est un paquet nommé de privilèges (`PVEVMAdmin`). Une *ACL* est un
triplet (chemin, identité, rôle). Les droits se lisent sur un **chemin**, jamais
sur un objet. Tant que ces trois-là ne sont pas séparés, chaque 403 reste une
énigme — et c'était le cas depuis M0, où le message d'erreur renvoyait déjà vers
un `pvecli access whoami` qui n'existait pas.

**Une ACL plus profonde REMPLACE l'héritée, elle ne s'y ajoute pas.** C'est la
découverte du lot, et elle s'est faite en me tirant dans le pied : j'ai accordé
`PVEUserAdmin` sur `/access` au token, qui y avait `PVEAuditor` hérité de `/`.
Résultat, il a **perdu** `Sys.Audit` sur `/access` — et `access acl ls` s'est mis
à répondre une liste vide. Le code du nœud ne laisse aucun doute :

```perl
# PVE/AccessControl.pm:1848
$roles = $new;   # overwrite previous settings
```

Pour cumuler, il faut les deux rôles sur le **même chemin**. Ce que j'ai fait,
et les privilèges se sont additionnés.

**Une liste vide n'est pas une absence.** `GET /access/acl` ne renvoie que les
objets dont on a le droit de modifier les permissions. Une table vide qui veut
dire « tu n'as pas le droit de regarder » mais qui se lit « il n'y a pas d'ACL »
est la manière la plus rassurante possible de se tromper. La commande le dit
maintenant explicitement, et le pre-read d'`acl set` écrit « aucune ACL
**VISIBLE** » plutôt que « aucune ACL ».

**Un token n'est pas son utilisateur.** Le schéma de `generate_token` annonce
`['or', ['userid-param', 'self'], …]`, ce qui se lit « un utilisateur peut créer
ses propres tokens ». Testé avec le token `automation@pve!pvectl` sur son propre
utilisateur : **403**. Le « self » désigne l'utilisateur authentifié, et un token
n'en est pas un. D'où l'amorçage : accorder à `pvecli` le droit d'accorder des
droits ne peut pas venir de `pvecli`. Une seule fois, en SSH root, et ciblé.

**Erreur commise, la plus coûteuse du projet jusqu'ici.** La création du token a
réussi côté nœud, puis le décodage a échoué :

```
Error: … décodage de « data » : json: cannot unmarshal string into
       Go struct field Token.info.privsep of type int
```

`GET …/token` renvoie `{"privsep":1,"expire":1785621600}` — des entiers.
Le `POST` qui crée le même token renvoie `{"privsep":"1","expire":"1785608487"}`
— des **chaînes**. Mêmes champs, même ressource, deux types JSON. Le décodage
strict a donc détruit un secret qui n'est rendu **qu'une fois** : le token
existait sur le nœud et plus personne ne pouvait s'en servir.

Deux corrections, et la seconde compte autant que la première :

1. `flexInt` accepte les deux formes.
2. Le secret est désormais imprimé **avant** que l'erreur ne soit renvoyée.
   Une valeur non reproductible ne doit jamais dépendre du succès de ce qui la
   suit.

**Le 403, provoqué puis corrigé.** Token jetable `automation@pve!readonly`,
`privsep=1`, aucune ACL — donc aucun droit :

```
$ pvecli lxc start 120            → HTTP 403, code de sortie 3
$ pvecli access whoami --can VM.PowerMgmt --path /vms/120   → non, code 1
$ pvecli access acl set --path /vms/120 --role PVEVMAdmin --token …!readonly
$ pvecli access whoami --can VM.PowerMgmt --path /vms/120   → oui, code 0
$ pvecli lxc start 120            → running
```

Le **privilège manquant** était `VM.PowerMgmt`, le **chemin** `/vms/120`, le
**rôle choisi** `PVEVMAdmin` — parce que c'est le plus petit rôle intégré qui
contienne `VM.PowerMgmt` avec de quoi piloter un guest, et qu'il est posé sur le
seul conteneur concerné plutôt que sur `/vms`. Aucune escalade, pas de
`root@pam`, pas d'`Administrator`. Le token a été révoqué et son ACL retirée.

**Bug attrapé avant de sortir.** `whoami --path` filtrait le dump complet côté
client. Or le dump ne liste que les chemins **portant** une ACL : `/vms/120`
n'y figure pas, alors que ses droits viennent d'une ACL propagée sur `/vms`.
La commande aurait répondu « non » sur un privilège réellement détenu. C'est
l'API qui doit résoudre l'héritage — `GET /access/permissions?path=…` — et un
test le vérifie maintenant sur la query émise, pas sur le résultat.

**Règle retenue** — quand une commande ne peut pas savoir, elle doit le dire au
lieu d'affirmer. « Aucune ACL visible » et « aucune ACL » diffèrent d'un mot et
de tout le sens.

---

## 2026-07-31 — PVX-037 → PVX-040, deux nombres au lieu de deux sigles

**Endpoint** — un seul de neuf pour tout le lot : `POST /nodes/{node}/vzdump`.
Restaurer n'a pas d'endpoint : c'est `POST /nodes/{n}/qemu` (ou `/lxc`) avec
`archive=<volid>`, et le schéma conditionne explicitement `force` à la présence
d'`archive`. La restauration **est** une création.

**L'exercice, chronométré pour de vrai.** VM 212, nginx installé, page servie.

| Repère | Horodatage (UTC) |
| --- | --- |
| référence : `curl` répond 200 | 18:36:26 |
| archive écrite | 18:36:33 |
| fichier écrit **après** la sauvegarde | 18:36:47 |
| panne simulée (`vm rm 212 --force`) | 18:36:52 |
| service à nouveau rendu | +20 s |

**RPO mesuré : 19 s.** Pas déduit — *démontré* : le fichier écrit après la
sauvegarde répond `404` sur la VM restaurée, l'`index.html` d'origine est
revenu. Ce qui a été écrit entre l'archive et la panne n'existe plus.

**RTO mesuré : 20 s**, de la destruction au `200 OK`. Dont ~11 s de
restauration et ~9 s de démarrage. Et c'est là que le chiffre ment : il est
**anormalement bon**, parce que ce lab est le cas dégénéré. La VM est
autonome — IP fixée par cloud-init, nginx dans l'image, archive sur le même
hôte. Le RTO réel est presque toujours dominé par ce qui n'était pas dans
l'archive ; ici, presque tout y était.

**Ce qui n'a PAS été restauré**, vérifié plutôt qu'affirmé :

- **les 19 s d'écritures** — prouvé par le `404` ;
- **la configuration de pare-feu du guest** (`/etc/pve/firewall/212.fw`) :
  supprimée par la destruction (constaté avant toute restauration), et absente
  de l'archive puisqu'elle a été créée après. Les ACL sur `/vms/212` disparaissent
  de même — `PVE::AccessControl::remove_vm_access` est appelé sans condition,
  `--purge` ou non ;
- **l'indépendance du stockage** : l'archive était sur `local`, c'est-à-dire le
  **même hôte physique** que le disque qu'elle protège. Une panne de cette
  machine emporte les deux. C'est la distinction snapshot/sauvegarde de
  PVX-028, et ce lab ne la respecte qu'à moitié. Le PRA de ce homelab a donc un
  point de défaillance unique, et le savoir vaut mieux que le croire résolu.

**Erreur commise, et elle vaut la leçon de M4 à elle seule.** Pour vérifier ce
qui survit à une destruction, j'ai posé une ACL `PVEVMUser` sur `/vms/212` —
qui a **remplacé** le `PVEVMAdmin` hérité de `/vms`. `PVEVMUser` ne contient pas
`VM.Allocate` :

```
Error: DELETE /nodes/pve/qemu/212?purge=1 : HTTP 403 — Permission check failed (/vms/212, VM.Allocate)
```

**Ajouter un droit m'en a retiré un.** Pire : retirer l'ACL exige
`Permissions.Modify` **ou** `VM.Allocate` sur ce chemin — les deux que je venais
de perdre. Verrouillé sur moi-même, il a fallu le nœud pour sortir.

Et j'ai failli en tirer une conclusion fausse. J'avais d'abord observé que l'ACL
et le pare-feu « survivaient » à la destruction, contre ce que dit la doc du
nœud. La vraie explication : la destruction n'avait jamais eu lieu — le `403`
partait dans un `2>/dev/null`. Une observation contre la documentation mérite
d'abord qu'on vérifie l'observation.

**Trois surprises de schéma :**

- `remove` vaut **1 par défaut** sur `vzdump` : une sauvegarde en supprime
  d'autres, en appliquant la rétention du stockage. `pvecli` envoie `remove=0`
  sauf `--prune`. Effet de bord heureux : `remove=1` exige `Datastore.Allocate`,
  que le token de moindre privilège n'a pas — le défaut sûr est aussi le seul
  qui marche.
- `compress=0` veut dire **aucune compression**, pas « niveau zéro ». Les autres
  valeurs sont des noms d'algorithmes, pas des niveaux.
- `bwlimit`, `ionice` et `performance` exigent `Sys.Modify` sur `/`. Un token de
  moindre privilège ne peut pas les passer, donc `pvecli` ne les expose pas.

**La preuve d'une sauvegarde n'est pas son `exitstatus`.** Le post-read compte
les archives avant et après, et échoue si aucune n'est apparue. Un `vzdump` qui
répond OK sans rien écrire est exactement le mode de défaillance que ce projet
existe pour attraper.

**Règle retenue** — un RPO et un RTO ne se décrètent pas, ils se lisent : l'un
sur l'âge de l'archive la plus récente, l'autre au chronomètre, et il s'arrête
quand le **service** répond, pas quand la restauration se termine.

---

## 2026-07-31 — PVX-041 → PVX-048, la chaîne complète et ce qu'elle a révélé

**Aucun nouvel endpoint.** Tout le lot M6 tient sur des endpoints déjà
implémentés : `/cluster/resources`, `/nodes/{n}/qemu/{vmid}/config`,
`/nodes/{n}/qemu/{vmid}/agent/network-get-interfaces`. Ce lot n'ajoute pas
d'API, il ajoute une **confrontation** entre deux sources de vérité.

### D2 tranchée : on interroge Terraform, on ne lit pas son state

`pvecli iac state` appelle `terraform show -json` et ne touche jamais
`terraform.tfstate`. Le state est une **base de données** dont Terraform possède
le format ; ce format a déjà changé entre versions mineures. Un outil qui le
parse casse à une mise à jour à laquelle il n'a pas participé.

Le test qui garantit la lecture seule n'inspecte pas le résultat, il inspecte
**l'argv** : `terraform` doit être appelé une seule fois, en `show -json`. C'est
la seule façon de prouver une négation — `refresh`, `apply` et `state rm`
écrivent tous, et en attraper un dans un futur refactor serait autrement
invisible.

**Une forme de schéma qu'on ne devine pas.** Le provider `bpg/proxmox` modélise
`cpu`, `memory`, `disk` et `network_device` en **blocs imbriqués**, que la
sortie JSON rend en **listes d'objets**, même quand le schéma n'en autorise
qu'un. `values["cpu"]["cores"]` ne renvoie rien — silencieusement. Il faut
`values["cpu"][0]["cores"]`. Vérifié contre une vraie capture, pas contre une
intuition.

### La garde qui se referme sur celui qui la pose

PVX-041 généralise la garde `managed` à toutes les écritures de **configuration
déclarée** : `set`, `clone`, `template`, `snapshot rollback`, `rm`. Les
changements d'**état d'exécution** restent autorisés — Terraform déclare
`on_boot`, pas « cette VM tourne en ce moment ».

La frontière la moins évidente est celle du snapshot : `rollback` est gardé,
`create` ne l'est pas. Un snapshot PVE contient la **configuration** du guest en
plus de ses disques ; un rollback réécrit cores, mémoire et tags. C'est une
écriture de configuration déguisée en restauration.

**Vérifié sur le nœud, et la garde m'a mordu.** Tag `managed` posé sur 212 →
`vm set` refusé, `vm start` accepté. Puis, pour retirer le tag : refusé aussi,
puisque retirer un tag est une écriture de configuration. Il a fallu
`--force-unmanaged` pour défaire ce que la garde protégeait. Même forme que
l'ACL de M4 : *ajouter une protection m'a retiré le droit de l'enlever.*

Le test qui compte parcourt **l'arbre Cobra réel**, pas une liste tenue à la
main : toute commande sous `vm`/`lxc` portant `--dry-run` doit porter
`--force-unmanaged`, sauf à figurer dans une table d'exemptions qui dit
pourquoi. Un futur `vm migrate` fera échouer ce test le jour où il est ajouté —
le seul moment où la question « la garde s'applique-t-elle ? » est bon marché.

### Trois bugs dans le dépôt d'infrastructure, trouvés en l'exécutant

Le lot a commencé par ne pas démarrer. `proxmox-practice-lab` contenait :

1. **`variables.tf` n'était pas du HCL valide** — `variable "x" { type = string;
   sensitive = true }`. HCL refuse le point-virgule en bloc mono-ligne.
   `terraform init` échouait d'emblée.
2. **`insecure = false` codé en dur** face à un nœud à certificat auto-signé.
   Et le piège est là : `terraform plan` **passait**, parce qu'il n'appelait pas
   l'API. Seul `apply` mourait, sur `certificate is not trusted`. Un plan vert
   ne dit rien de la joignabilité.
3. **Les deux moitiés du dépôt ne se rejoignaient pas.** `main.tf` taguait la VM
   `lab,terraform,managed` ; `site.yml` cible `hosts: lab_apps`. Ansible
   répondait `skipping: no hosts matched` — et sortait **0**. Un playbook qui ne
   trouve aucun hôte est un succès pour un code de retour.

### Un « 200 OK » ne prouve pas que ton application est servie

La chaîne a d'abord semblé complète : `curl` répondait `200`. C'était la **page
d'accueil de Nginx**. Debian livre un vhost `default` déclaré
`listen 80 default_server`, qui remporte toute requête sans `server_name` ;
`site.yml` activait bien son site mais ne désactivait jamais celui-là.

C'est exactement la règle que cette CLI applique aux tâches PVE — *une
acceptation n'est pas un résultat* — appliquée à HTTP. D'où
`--verify-contains`, qui regarde le corps de la réponse et pas seulement son
statut. Sans lui, M6 se serait clos sur une preuve fausse.

### L'idempotence est une mesure, et elle a recalé le playbook Docker

`--idempotence` rejoue le playbook et échoue si le second passage rapporte le
moindre `changed`. `site.yml` passe. `docker.yml` a échoué, et il a fallu deux
tentatives pour le corriger, parce que **deux signaux mentent** :

| Signal | Ce qu'il fait vraiment |
| --- | --- |
| stdout de `docker stack deploy` | écrit « Updating service … » à **chaque** appel |
| `.Version.Index` du service | s'incrémente à chaque appel, spec identique comprise |
| `.Spec` du service | **stable** tant que rien ne change réellement |

`docker stack deploy` est un convergeur : il ne dit pas s'il a changé quelque
chose, il dit qu'il a convergé. Le `changed_when` d'origine s'appuyait sur le
mot « Updating » et rapportait donc `changed=1` pour toujours. Mesuré sur le
nœud, pas supposé : deux `sha256sum` de `{{json .Spec}}` autour d'un déploiement
à vide sont identiques.

### Le template qui coûtait 11 minutes à chaque apply

Premier `terraform apply` : **11 min 54 s**. Le provider attend, avec
`agent { enabled = true }`, que l'agent invité rende une adresse — et le
template `9000` n'avait jamais eu le paquet `qemu-guest-agent`. L'apply s'est
terminé **à la seconde où je l'ai installé à la main par SSH**.

Le template a donc été reconstruit avec `pvecli` lui-même : `vm clone 9000
--newid 9001 --full`, `vm start`, installation de l'agent, `cloud-init clean
--logs --seed`, `machine-id` vidé, `vm shutdown`, `vm template 9001`.

**Le même apply, ensuite : 23 secondes.** Un facteur 31, et rien d'autre n'a
changé que le contenu de l'image. Ce que le template ne contient pas, chaque
clone le paiera — à chaque clone.

*(Détail de terrain sans rapport avec Proxmox : macOS tronque le zéro de tête
dans `arp -an`, `bc:24:11:35:ea:06` s'y affiche `…:ea:6`. Un `grep` sur
l'adresse MAC complète ne trouve rien, et on cherche une VM réseau qui va très
bien.)*

### La chaîne, chronométrée

| Étape | Horodatage (UTC) |
| --- | --- |
| `pvecli iac apply --yes` — 210 détruite puis recréée | 19:52:53 → 19:53:16 (23 s) |
| `pvecli iac inventory` — IP via l'agent QEMU | 19:53:27 |
| `iac configure site.yml --idempotence` — Nginx natif | 19:53:43 → 19:53:59 |
| `iac configure docker.yml --idempotence` — Caddy sur Swarm | 19:54:09 → 19:55:21 |

Les deux applications servies, contenu vérifié :

```
:80    HTTP/1.1 200 OK | <h1>Native app deployed by Ansible</h1>
:8080  HTTP/1.1 200 OK | Caddy app deployed by Ansible
```

Puis la dérive : `memory=3072` posé par un **appel API direct** — ni Terraform,
ni pvecli, c'est-à-dire ce que fait l'interface web quand on clique.
`pvecli iac drift` l'attrape (`memory  déclaré 2048 Mio · réel 3072 Mio`,
sortie 1), `pvecli iac apply` la résorbe, le post-vol le relit par l'API.

**Et la correction n'est pas gratuite.** Ramener la mémoire à 2048 a **redémarré
la VM**. Nginx est revenu tout de suite ; le service Swarm a mis ~25 s à
reconverger. Une dérive de configuration corrigée est une interruption de
service — le genre de coût qu'on découvre en le mesurant, jamais en le lisant.

**Règle retenue** — une chaîne IaC ne se valide pas outil par outil. Terraform
disait vrai, Ansible disait vrai, et l'application servait la mauvaise page. Ce
que chaque outil rapporte, c'est ce que **ses** tâches ont fait ; personne ne
rapporte ce que l'utilisateur reçoit. C'est la seule chose qu'il fallait
vérifier.

---

## 2026-07-31 — PVX-049 → PVX-055, ce que le nœud refuse et pourquoi

Le dernier lot devait être de la finition. Il a surtout été une série de refus,
et chacun disait quelque chose que la lecture du schéma ne disait pas.

### Toutes les réponses de PVE ne tiennent pas dans `data`

Le client déballait `{"data": …}` et jetait le reste. C'était vrai partout —
jusqu'à `GET /nodes/{node}/network`.

Le diff des modifications réseau en attente n'est pas dans `data`. Le handler
appelle `set_result_attrib('changes', …)` (`PVE/API2/Network.pm:418`), et la clé
atterrit **à côté** de `data` dans l'enveloppe JSON :

```json
{ "changes": "--- /etc/network/interfaces\n+++ …", "data": [ … ] }
```

Un client qui déballe `data` et s'arrête là est aveugle à la seule chose qu'un
opérateur doit voir avant de toucher au réseau. Le client a gagné un `getRaw`
qui rend l'enveloppe entière.

Et il n'y a **aucun champ `pending` par interface**. La colonne ATTENTE est
déduite en parcourant le diff et en attribuant chaque ligne modifiée à la
strophe `iface` qui l'englobe — lignes de contexte comprises, sinon
« `+bridge-ports nic0 nic1` » n'appartient à personne. Vérifié en posant une
modification inoffensive sur le nœud puis en l'annulant, jamais en l'appliquant.

**Règle retenue** — l'enveloppe d'une API est une convention, pas une loi. Le
jour où une réponse ne rentre pas dedans, c'est en général parce qu'elle porte
l'information la plus importante.

### `net apply` s'arrête sur un 403, et c'est la fin correcte de l'histoire

Le token n'a pas `Sys.Modify` sur `/nodes/pve`, et il ne l'aura pas. Appliquer
une configuration réseau est le seul geste de toute cette CLI qui peut rendre le
nœud injoignable, et `LAB.md` ne documente aucun accès console.

Le 403 obtenu n'est pas un trou dans le lot : c'est la démonstration que le
diagnostic de M4 fonctionne. Il nomme le privilège exact
(`Permission check failed (/nodes/pve, Sys.Modify)`) au lieu de dire « échec ».

### PVE 9 a déplacé les pools, et le dit lui-même

`GET|PUT|DELETE /pools/{poolid}` répond encore, et le nœud déclare la forme
dépréciée dans sa propre description : *« no support for nested pools, use
`/pools/?poolid=…` »*. Un pool imbriqué s'écrit `parent/enfant` — exactement ce
qu'un segment d'URL ne peut pas porter. D'où le passage du poolid en paramètre.

Autre surprise du même endpoint : `GET /pools?poolid=lab` répond un **tableau
d'un élément**, pas un objet. Le handler empile son entrée unique sur la même
liste que l'index. Décoder un objet ici donne une erreur de type qui ressemble à
un endpoint cassé.

Enfin, `delete_pool` refuse tout pool non vide, et **il n'y a pas de `force`
dans l'API**. Le `--force` de `pvecli pool rm` n'est donc pas un drapeau
transmis au nœud : ce sont deux requêtes, et le plan les affiche toutes les
deux. Vider un pool est une écriture à part entière.

### Faire télécharger une URL par le nœud est un privilège en soi

`Datastore.AllocateTemplate` sur le stockage ne suffit pas. `download_url`
exige **en plus** `Sys.Audit` **et** `Sys.Modify` sur `/`, ou le plus récent
`Sys.AccessNetwork` sur le nœud. La raison est écrite dans le schéma :

> as this allows one to probe the (local!) host network indirectly

Faire télécharger une URL arbitraire par le nœud, c'est lui faire ouvrir une
connexion qu'on choisit — vers son propre réseau d'administration si on veut.
C'est du SSRF, et PVE le traite comme tel.

Seul `Administrator` porte `Sys.AccessNetwork`. Le poser reviendrait à
`root@pam` sous un autre nom, donc le lab a reçu un **rôle sur mesure d'un seul
privilège** :

```bash
pveum role add URLFetch --privs Sys.AccessNetwork
pveum acl modify /nodes/pve --roles URLFetch,PVEAuditor --users automation@pve \
      --tokens 'automation@pve!pvectl'
```

Détail au passage : PVE réserve le préfixe `PVE` à ses propres rôles.
`pveum role add PVEURLFetch` répond *« cannot use role ID starting with the
(case-insensitive) 'PVE' namespace »*.

**Règle retenue** — le moindre privilège finit parfois par créer un rôle. Les
rôles fournis sont un point de départ, pas un catalogue fermé.

### Trois refus pour un seul upload

Le premier appel non form-encodé de la CLI a coûté trois allers-retours contre
le nœud, dont deux qu'aucune relecture n'aurait trouvés.

**501 — `chunked transfer encoding not supported`.** Le corps était un
`io.Pipe` : longueur inconnue, donc `net/http` bascule en chunked, et le serveur
HTTP de PVE le refuse. Le multipart est désormais assemblé en `head + fichier +
tail` avec un `Content-Length` explicite. Le fichier reste streamé — seul le
préambule est en mémoire.

**L'ordre des parties n'est pas décoratif.** PVE ne parse pas le multipart avec
un parseur généraliste : `PVE::APIServer::AnyEvent::file_upload_multipart` est
une machine à états dont chaque regex est **ancrée en début de tampon**. Elle
extrait `content`, puis `checksum-algorithm`, puis `checksum`, puis le fichier —
dans cet ordre. Envoyées autrement, les parties sont silencieusement ignorées et
l'échec parle d'un paramètre manquant que la requête contenait visiblement.

Et la partie fichier doit s'appeler `filename`, sinon le nœud meurt sur
*« wrong field name … expected 'filename' »*.

**500 — `unable to parse directory volume name 'iso%2Fx.iso'`.** Un volid porte
un `/` et PVE le découpe lui-même : `{volume}` ne doit **pas** être échappé.
L'échappement des chemins avait raison partout ailleurs, et tort ici. La règle
vit maintenant avec le placeholder, là où elle est vraie, pas avec l'endpoint.

Ce dernier est le plus vicieux des trois : le message accuse le **nom du
fichier**, alors que la cause est l'**encodage de l'URL**. On cherche pendant
dix minutes pourquoi un nom parfaitement valide ne l'est pas.

### La somme de contrôle, vérifiée en la faisant échouer

Une somme volontairement fausse : la tâche télécharge les 64 Mio, calcule,
compare, échoue, et le nœud **supprime le fichier partiel**. `pvecli` rend 4 —
le code d'une tâche PVE échouée, pas d'une requête refusée — et affiche les
dernières lignes du journal.

Un `--checksum` qu'on n'a jamais vu refuser quelque chose est un `--checksum`
dont on ne sait rien.

### QEMU et LXC n'écrivent pas les mêmes champs de la même façon

`GET …/migrate` — l'endpoint que PVE appelle lui-même *« Get preconditions for
migration »* — répond :

```
qemu   allowed_nodes   not_allowed_nodes   local_disks     (underscores)
lxc    allowed-nodes   not-allowed-nodes                   (tirets)
```

Une seule structure Go pour les deux décode **vide** d'un côté. Et un
`allowed_nodes` vide, c'est exactement à quoi ressemble « tu ne peux pas
migrer » : le bug se serait caché derrière une réponse plausible. Les deux
graphies sont désormais lues, et les deux captures du lab sont dans `testdata/`.

### Le harnais de M6 a fait exactement ce pour quoi il avait été écrit

`cmd/managed_test.go` portait ce commentaire, écrit à M6 :

> A future `vm migrate` (M7) joins the tree with --dry-run and no
> --force-unmanaged, and this test fails the day it is added.

Il a échoué le jour où `vm migrate` a rejoint l'arbre. Et la bonne réponse
n'était pas d'exempter la commande : le provider `bpg` déclare `node_name`, donc
déplacer un guest le sort de son propre state. Une migration ressemble à une
opération et **est** une déclaration.

L'ordre des refus compte aussi. « ce guest appartient à Terraform » est vrai que
la cible existe ou non — la garde de propriété passe donc avant le contrôle de
cible.

### Une complétion qui parle est une complétion qu'on désactive

Trois contraintes, et la troisième n'était pas évidente :

1. **cache 10 s** — marteler Tab ne doit pas marteler l'API ;
2. **budget 2 s** — un shell figé trente secondes est pire que pas de
   complétion du tout ;
3. **silence total** — et c'est là que `newClient` était un piège. Il imprime
   l'avertissement `--insecure` sur stderr à chaque appel, ce qui est juste pour
   une commande et catastrophique pour une complétion : la ligne atterrirait au
   milieu de l'invite en train d'être tapée. La complétion construit donc son
   propre client, sans trace et sans avertissement.

Un défaut trouvé en la testant, pas en la lisant : la garde « seul le premier
argument positionnel se complète » tuait aussi la complétion des **flags** quand
un argument les précédait. `vm clone 9001 --pool <Tab>` ne proposait rien. Les
deux usages partagent la signature de Cobra et pas la même lecture de `args`.

### Le certificat vu depuis `localhost`

Preuve de fin de lot : le binaire installé sur le nœud, pointant sur
`https://localhost:8006`. Premier essai :

```
✗  endpoint joignable et version lue
le certificat de localhost:8006 est inconnu du système (auto-signé ?).
```

Attendu, et instructif. Le certificat porte `CN=pve.example` : depuis le poste on
l'atteint par son IP, depuis le nœud par `localhost`, et aucun des deux ne
correspond au nom du certificat. Une vérification par autorité et par nom
échouerait dans les deux cas.

L'épinglage d'empreinte, lui, **ne dépend d'aucun nom**. La même empreinte
fonctionne des deux côtés, et `pvecli config trust` sur le nœud a suffi :

```
TLS       empreinte épinglée
✓  endpoint joignable et version lue — PVE 9.2.2
✓  privilèges du token — 15 chemin(s)
```

Le choix de M0 — épingler plutôt que `--insecure` — est ce qui a rendu ce cas
trivial. Le contournement facile aurait marché aussi, et n'aurait rien appris.

**Règle retenue** — un outil distribué rencontre des environnements qu'on
n'avait pas prévus. Les décisions de sécurité prises au début se paient ou se
récupèrent à ce moment-là, pas avant.

### Le seuil de couverture, vérifié en le faisant échouer

`make cover` refuse sous 70 % sur `internal/pve` et `internal/service`. Au début
du lot : 68,8 % et **57,5 %**. La dette était concentrée sur les chemins
d'écriture du client et sur `TaskWaiter` — c'est-à-dire sur le suivi de tâche,
la mécanique la plus centrale du projet, qui n'était testée qu'indirectement à
travers `cmd`.

Fin du lot : 79,3 % et 85,1 %. Et le seuil a été vérifié en le montant à 99 %
pour confirmer qu'il échoue vraiment — un garde-fou qu'on n'a jamais vu se
déclencher ne garde rien.

## Lot M7 — clos le 2026-07-31

`make release && make install-node`, puis, depuis le nœud :

```
PVE_API_URL=https://localhost:8006 pvecli doctor
TLS       empreinte épinglée
✓  endpoint joignable et version lue — PVE 9.2.2
```

**55 stories sur 55. M0 → M7.**

## Recette finale — le harnais d'intégration ne se vérifiait pas lui-même

`make integration` échouait sur les deux tests qui parlent au vrai nœud, à
l'instant précis où `pvecli doctor` répondait quatre ✓ juste à côté :

```
--- FAIL: TestLiveVersionAndInventory
    x509: “pve.example” certificate is not trusted
```

La suite ne lisait la confiance TLS que depuis `PVE_TLS_FINGERPRINT`. Or
l'empreinte épinglée par `pvecli config trust` est écrite dans le **fichier** de
configuration, et `~/.config/pvecli/env` ne l'exporte pas — il n'a pas à le
faire, c'est une donnée de contexte, pas un secret. Le harnais montait donc un
client en vérification système là où la CLI, elle, épinglait.

Deux corrections étaient possibles. Exporter la variable dans l'environnement du
poste aurait rendu la cible verte sur cette machine et sur aucune autre. La
seconde a été retenue : le test résout sa configuration par
`config.Load` + `config.Resolve`, **la même chaîne que la CLI**. L'écart
disparaît au lieu d'être contourné, et `--insecure` n'entre jamais en jeu.

Conséquence directe sur `TestLiveAuthFailureIsDiagnosed`, qui provoquait son 401
en désactivant la vérification TLS : seul le secret est désormais faussé. Un 401
obtenu en cessant de vérifier à qui l'on parle ne prouve pas que le nœud refuse
le token.

**Règle retenue** — un harnais de test qui n'emprunte pas le chemin du produit
teste autre chose que le produit. Ici, la différence était une ligne de
configuration, et elle suffisait à rendre la cible de recette inutilisable.

## L'agent, livré avec le binaire plutôt qu'à côté

La CLI installe désormais un sous-agent Claude Code dans la configuration
**globale** de l'utilisateur (`~/.claude/agents/proxmox-ops.md`), et `make
install` le pose en même temps que le binaire.

Trois décisions, chacune contre une alternative plus simple.

**`go:embed` plutôt qu'un fichier livré à côté.** Un agent qui décrit des
options que le binaire local n'a pas est pire qu'aucun agent : il fait perdre du
temps avec autorité. Embarquée, la définition ne peut pas diverger de la version
qu'elle documente, et `pvecli ai install` ne va rien chercher sur le réseau.

**Un refus d'écraser, plutôt qu'une écriture idempotente.** Si le fichier
présent diffère de la définition embarquée, l'installation s'arrête. La
différence est soit une personnalisation écrite par l'utilisateur, soit une
version antérieure — deux choses qu'on regarde avant de perdre. La comparaison
se fait **octet par octet**, pas sur un numéro de version : un numéro dit ce que
le fichier prétend être, jamais si quelqu'un l'a modifié depuis.

**`~/.local` par défaut, pas `/usr/local`.** Le second exige `sudo` sur macOS, et
une cible d'installation qui réclame root pour poser un binaire d'utilisateur
est une cible qu'on finit par lancer en `sudo` sans réfléchir. `PREFIX` reste
surchargeable.

Ce que l'agent porte, et que `--help` ne peut pas porter : la règle « une
acceptation n'est pas un résultat », la garde de propriété du tag `managed`, les
refus volontaires (`Sys.Modify`, `Permissions.Modify`) à **rapporter** et jamais
à contourner, la plage 900-999, le protocole de confirmation avant toute
destruction, et les pièges déjà payés — 8192 et non 8, l'agent invité manquant
qui transforme 18 secondes en douze minutes, le `200 OK` de la page par défaut
de Debian.

Deux tests gardent ce contenu : l'un vérifie que le front matter porte encore
`name`, `description`, `tools` et `model` — sans quoi Claude Code ignore
silencieusement le fichier, installé et jamais invoqué — l'autre que les règles
qui ont coûté du temps sont toujours dans le texte. Sans eux, l'agent se dégrade
sans que rien n'échoue.

**Limite assumée** : une session Claude Code déjà ouverte ne voit pas un agent
écrit après son démarrage. L'installation le dit ; l'agent n'a donc pas pu être
exercé de bout en bout dans la session qui l'a écrit.

---

## 2026-08-01 — PVX-070 → PVX-073, ouvrir le lab à quelqu'un d'autre

**Endpoints** — `POST /access/users` et `GET /access/users/{userid}` côté PVE,
relevés par `search-pve-api.ts` ; `/accounts/{a}/access/apps`,
`.../apps/{app}/policies` et `/accounts/{a}/access/service_tokens` côté
Cloudflare, relevés sur la doc officielle. Aucun écrit de mémoire, comme
toujours — et c'est ce qui a fait tomber la première hypothèse du lot.

**Le rôle demandé n'existe pas.** La demande était « un `PVEVMUser` qui peut
créer, modifier et supprimer ses propres VM ». Les deux moitiés sont fausses.
Sur les rôles réels du nœud, `PVEVMUser` vaut `VM.Audit, VM.Backup,
VM.Config.CDROM, VM.Config.Cloudinit, VM.Console, VM.GuestAgent.*,
VM.PowerMgmt` : il démarre, arrête et ouvre la console, mais il n'a ni
`VM.Allocate` — donc ni création ni destruction — ni les `VM.Config.CPU`,
`Memory`, `Disk`, `Network` — donc aucun redimensionnement. Le rôle qui
correspond à l'intention est `PVEVMAdmin`.

**Et « ses propres VM » n'existe pas non plus.** Proxmox n'a aucune notion de
propriétaire par VM. Le seul mécanisme d'isolation est le **pool** : `/pool/<id>`
est un chemin d'ACL, et une VM qui y entre hérite des droits du pool sur son
propre `/vms/{vmid}`.

**Ce que le schéma dit, et qu'aucune documentation d'usage ne répète** :

> You need 'VM.Allocate' permissions on /vms/{vmid} **or on the VM pool
> /pool/{pool}**. If you create disks you need 'Datastore.AllocateSpace' on any
> used storage. If you use a bridge/vlan, you need 'SDN.Use' on any used
> bridge/vlan.

Trois conséquences, dont une qui manquait au code. La création accepte le
droit porté par le pool — mais seulement si l'appel **nomme** ce pool, puisque
c'est `pool` qui désigne le chemin sur lequel PVE ira vérifier. Une identité
bornée à un pool et qui appelle sans `--pool` reçoit donc un `403` sur une VM
qu'elle a pourtant le droit de créer, et le refus, lu littéralement, dit
l'inverse : qu'elle n'a pas le droit de créer. `pvecli vm create` n'avait pas
ce paramètre ; il l'a, et le `403` sans `--pool` oriente désormais vers le pool
plutôt que vers une ACL à élargir.

Le `DELETE`, lui, vérifie `VM.Allocate` sur `/vms/{vmid}` et **non** sur le
pool. Il fonctionne quand même pour un membre du pool — parce que l'ACL du pool
porte sur ses membres. C'est le mécanisme, pas une tolérance, et la distinction
compte : elle explique pourquoi retirer une VM d'un pool retire aussi le droit
de la détruire.

**Le piège le plus cher du lot ne produit aucune erreur.** Un service token
placé dans une policy Cloudflare Access `allow` est accepté par l'API — et
laisse passer **toutes** les requêtes, y compris celles qui ne présentent aucun
token. `allow` veut dire « une identité s'est authentifiée » ; un service token
n'en porte aucune. La décision correcte est `non_identity`, « Service Auth »
dans le dashboard. Le symptôme d'une erreur ici est que tout fonctionne, pour
n'importe qui. `Policy.Validate` refuse la combinaison, et le test qui le prouve
est le seul du lot qui protège d'une faille plutôt que d'une panne.

**Erreur commise — une option posée à la main ne survit pas.** L'interface
Proxmox est en HTTPS avec un certificat auto-signé : sans
`originRequest.noTLSVerify`, `cloudflared` refuse le certificat de l'origine et
répond `502` alors que le tunnel est monté, le nom résout et Access laisse
passer. La panne est au dernier saut, celui qu'on regarde en dernier. Le
réflexe — poser la case dans le dashboard Cloudflare — ne tient pas : `cf route
add` relit la table d'ingress, la modifie et la réécrit **entière**, donc toute
clé absente des structs Go traverse le décodage sans être retenue et disparaît
de *toutes* les règles au premier ajout suivant. Il a fallu modéliser le champ
pour pouvoir le poser, et un test vérifie maintenant que les options d'une route
survivent à l'écriture d'une autre.

**Le tunnel expose, Access protège, et rien n'oblige à poser le second.** Ce
sont deux objets indépendants côté Cloudflare. Tant que la porte se posait à la
main, `cf route add` pouvait publier le formulaire de login de Proxmox sur
l'internet ouvert sans un mot. Il interroge désormais les applications Access
après avoir écrit la route et le dit quand le nom n'est couvert par aucune —
c'est le seul moment où quelqu'un regarde. Le manuel du dépôt voisin le disait
déjà en toutes lettres au chapitre 02 ; ce lot rend la règle exécutable.

**Règle retenue** — quand une demande nomme un rôle, une décision ou un réglage
précis, vérifier ce que ce nom **contient** avant de l'appliquer. `PVEVMUser` et
`allow` décrivaient tous les deux l'inverse de l'intention, aucun des deux
n'aurait produit d'erreur, et le second aurait ouvert l'hyperviseur à tout
l'internet en ayant l'air correct.
