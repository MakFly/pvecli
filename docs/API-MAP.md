# API-MAP — endpoints implémentés par `pvectl`

Règle non négociable du PRD §6.3 : **aucun endpoint écrit de mémoire.** Chaque
ligne n'est ajoutée qu'après vérification du schéma contre l'API viewer officiel
de PVE **9.x** (le lab tourne en 9.2.2, pas en 8.x) ou via
`bun .agents/skills/proxmox-api/scripts/search-pve-api.ts <terme>`.

| Endpoint | Méthode | Commande `pvectl` | Story | Vérifié le | Source |
| --- | --- | --- | --- | --- | --- |
| `/version` | GET | `pvectl version` | PVX-005 | 2026-07-31 | `pvesh get /version` sur le nœud (PVE 9.2.2) |
| `/nodes` | GET | `pvectl node ls` | PVX-006 | 2026-07-31 | `pvesh get /nodes` sur le nœud |
| `/nodes/{node}/status` | GET | `pvectl node show` | PVX-006 | 2026-07-31 | `pvesh get /nodes/pve/status` sur le nœud |
| `/cluster/status` | GET | `pvectl doctor` | PVX-008 | 2026-07-31 | `pvesh get /cluster/status` sur le nœud |
| `/access/permissions` | GET | `pvectl doctor` | PVX-008 | 2026-07-31 | appel réel avec le token `automation@pve!pvectl` |
| `/cluster/resources` | GET | `pvectl cluster resources`, `iac inventory\|drift\|adopt` | PVX-016 · 042 · 044 | 2026-07-31 | `pvesh usage /cluster/resources` sur le nœud |
| `/access/users` | GET | `pvectl access user ls` | PVX-033 | 2026-07-31 | `pvesh get /access/users` sur le nœud |
| `/access/roles` | GET | `pvectl access role ls` | PVX-033 | 2026-07-31 | `pvesh get /access/roles` sur le nœud |
| `/access/roles/{roleid}` | GET | `pvectl access role show` | PVX-033 | 2026-07-31 | `pvesh get /access/roles/PVEVMAdmin` sur le nœud |
| `/access/acl` | GET | `pvectl access acl ls` | PVX-033 | 2026-07-31 | `pvesh get /access/acl` sur le nœud |
| `/access/acl` | PUT | `pvectl access acl set` | PVX-035 | 2026-07-31 | `pvesh usage /access/acl -v` sur le nœud |
| `/access/users/{userid}/token` | GET | `pvectl access token ls` | PVX-033 | 2026-07-31 | `pvesh get /access/users/automation@pve/token` |
| `/access/users/{userid}/token/{tokenid}` | GET | post-read de `token create\|rm` | PVX-034 | 2026-07-31 | `pvesh usage /access/users/automation@pve/token/pvectl -v` |
| `/access/users/{userid}/token/{tokenid}` | POST | `pvectl access token create` | PVX-034 | 2026-07-31 | `pvesh usage` sur le nœud + `PVE::API2::User::generate_token` |
| `/access/users/{userid}/token/{tokenid}` | DELETE | `pvectl access token rm` | PVX-034 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/pools` | GET | `pvectl pool ls\|show` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` + `PVE::API2::Pool::index` (renvoie un **tableau** même pour un seul pool) |
| `/pools` | POST | `pvectl pool create` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` (`create_pool`, `Pool.Allocate` sur `/pool/{poolid}`) |
| `/pools` | PUT | `pvectl pool add\|remove` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` (`update_pool` — la forme `/pools/{poolid}` est déclarée dépréciée par le nœud) |
| `/pools` | DELETE | `pvectl pool rm` | PVX-050 | 2026-07-31 | `PVE::API2::Pool::delete_pool` ligne 484 : « You can only delete empty pools » |
| `/nodes/{node}/qemu` | GET | `pvectl vm ls` | PVX-011 | 2026-07-31 | `PVE::QemuServer::vmstatus_return_properties`, lu dans le source du nœud |
| `/nodes/{node}/qemu` | POST | `pvectl vm create` | PVX-025 | 2026-07-31 | `pvesh usage /nodes/pve/qemu -v` |
| `/nodes/{node}/qemu/{vmid}` | DELETE | `pvectl vm rm` | PVX-031 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/config` | PUT | `pvectl vm set` | PVX-026 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/clone` | POST | `pvectl vm clone` | PVX-024 | 2026-07-31 | `PVE::API2::Qemu::clone_vm`, lu dans le source du nœud |
| `/nodes/{node}/qemu/{vmid}/template` | POST | `pvectl vm template` | PVX-027 | 2026-07-31 | `PVE::API2::Qemu` (`template`), lu dans le source du nœud |
| `/nodes/{node}/qemu/{vmid}/snapshot` | GET · POST | `pvectl vm snapshot ls\|create` | PVX-028 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/212/snapshot -v` |
| `/nodes/{node}/qemu/{vmid}/snapshot/{name}/rollback` | POST | `pvectl vm snapshot rollback` | PVX-028 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/snapshot/{name}` | DELETE | `pvectl vm snapshot rm` | PVX-028 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/migrate` | GET | pre-read de `pvectl vm migrate` | PVX-052 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/211/migrate -v` — « Get preconditions for migration » |
| `/nodes/{node}/qemu/{vmid}/migrate` | POST | `pvectl vm migrate` | PVX-052 | 2026-07-31 | `pvesh usage … -v` (`online`, `with-local-disks`, `targetstorage`, `bwlimit`) |
| `/nodes/{node}/lxc/{vmid}/migrate` | GET · POST | `pvectl lxc migrate` | PVX-052 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/120/migrate` + réponse réelle (champs en **tirets**, pas en underscores) |
| `/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces` | GET | `pvectl vm agent ifaces`, `vm ip`, `iac inventory` | PVX-029 · 042 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/lxc/{vmid}/snapshot` | GET · POST | `pvectl lxc snapshot ls\|create` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, lignes 24-109 du source du nœud |
| `/nodes/{node}/lxc/{vmid}/snapshot/{name}/rollback` | POST | `pvectl lxc snapshot rollback` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, ligne 269 |
| `/nodes/{node}/lxc/{vmid}/snapshot/{name}` | DELETE | `pvectl lxc snapshot rm` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, ligne 169 |
| `/nodes/{node}/qemu/{vmid}/config` | GET | `pvectl vm show`, `iac drift\|adopt` | PVX-013 · 044 · 045 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/{vmid}/config` |
| `/nodes/{node}/qemu/{vmid}/status/current` | GET | `pvectl vm show` | PVX-013 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/lxc` | GET | `pvectl lxc ls` | PVX-012 | 2026-07-31 | `pvesh usage /nodes/pve/lxc` |
| `/nodes/{node}/lxc` | POST | `pvectl lxc create` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc -v` |
| `/nodes/{node}/lxc/{vmid}` | DELETE | `pvectl lxc rm` | PVX-031 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100 -v` |
| `/nodes/{node}/lxc/{vmid}/config` | PUT | `pvectl lxc set` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100/config -v` |
| `/nodes/{node}/lxc/{vmid}/clone` | POST | `pvectl lxc clone` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100/clone -v` |
| `/nodes/{node}/lxc/{vmid}/config` | GET | `pvectl lxc show`, `iac drift\|adopt` | PVX-013 · 044 · 045 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/lxc/{vmid}/status/current` | GET | `pvectl lxc show` | PVX-013 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/storage` | GET | `pvectl storage ls` | PVX-014 | 2026-07-31 | `pvesh get /nodes/pve/storage` |
| `/nodes/{node}/storage/{storage}/content` | GET | `pvectl storage content` | PVX-014 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/storage/{storage}/download-url` | POST | `pvectl storage download-url` | PVX-051 | 2026-07-31 | `pvesh usage /nodes/pve/storage/local/download-url -v` |
| `/nodes/{node}/storage/{storage}/upload` | POST | `pvectl storage upload` | PVX-051 | 2026-07-31 | `pvesh usage … -v` + `PVE::APIServer::AnyEvent::file_upload_multipart` (ordre des parties du multipart) |
| `/nodes/{node}/storage/{storage}/content/{volume}` | DELETE | `pvectl storage rm` | PVX-051 | 2026-07-31 | `PVE::API2::Storage::Content::delete` ligne 453 (`Datastore.Allocate`) |
| `/nodes/{node}/network` | GET | `pvectl net ls` | PVX-049 | 2026-07-31 | `pvesh usage /nodes/pve/network -v` + `PVE::API2::Network` ligne 418 (`set_result_attrib('changes')`) |
| `/nodes/{node}/network/{iface}` | GET | `pvectl net show` | PVX-049 | 2026-07-31 | `pvesh usage /nodes/pve/network/vmbr0` |
| `/nodes/{node}/network` | PUT | `pvectl net apply` | PVX-049 | 2026-07-31 | `PVE::API2::Network::reload_network_config` (ligne 885 : `Sys.Modify`, ligne 903 : renvoie un UPID) |
| `/nodes/{node}/network` | DELETE | `pvectl net revert` | PVX-049 | 2026-07-31 | `PVE::API2::Network::revert_network_changes` ligne 511 (`unlink /etc/network/interfaces.new`) |
| `/nodes/{node}/vzdump` | POST | `pvectl backup run` | PVX-037 | 2026-07-31 | `pvesh usage /nodes/pve/vzdump -v` + `PVE::API2::VZDump` (privilèges) |
| `/nodes/{node}/tasks` | GET | `pvectl task ls` | PVX-015 | 2026-07-31 | `pvesh get /nodes/pve/tasks` |
| `/nodes/{node}/tasks/{upid}/status` | GET | `pvectl task show` | PVX-015 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/tasks/{upid}/log` | GET | `pvectl task log` | PVX-015 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/status/{action}` | POST | `pvectl vm start\|stop\|shutdown\|reboot\|reset\|suspend\|resume` | PVX-022 | 2026-07-31 | `PVE::API2::Qemu` (`vm_stop`, `vm_shutdown`), lu dans le source du nœud |
| `/nodes/{node}/lxc/{vmid}/status/{action}` | POST | `pvectl lxc start\|stop\|shutdown\|reboot\|reset` | PVX-023 | 2026-07-31 | `PVE::API2::LXC::Status` |

Réponse observée, capturée dans [`testdata/version.json`](../testdata/version.json) :

```json
{ "data": { "release": "9.2", "repoid": "b9984c6d90a4bd80", "version": "9.2.2" } }
```

Les trois champs sont des chaînes. `version` est mémorisé dans le contexte
courant sous `detected_version` : c'est lui qui tranchera les questions de
disponibilité d'endpoints dans les lots suivants.

## Authentification

Vérifiée le 2026-07-31 dans le source du nœud (PVE 9.2.2), pas dans un souvenir :

| Point | Fichier sur le nœud | Ce qu'il établit |
| --- | --- | --- |
| Nom de l'en-tête | `PVE/APIServer/AnyEvent.pm:2081` | `apitoken_name` vaut `PVEAPIToken` |
| Extraction de la valeur | `PVE/APIServer/Formatter.pm:83` | `/(?:^\|\s)PVEAPIToken(?:=\| )([^;]*)/` puis `uri_unescape` — d'où le préfixe `PVEAPIToken=` et l'interdiction du `;` dans la valeur |
| Découpage tokenid / secret | `PVE/AccessControl.pm:493` | `/^(.*)=(.*)$/`, premier groupe **glouton** : la coupure se fait sur le **dernier** `=` |
| Exemption CSRF | `PVE/HTTPServer.pm:85` | un `api_token` va droit à `verify_token()` ; l'en-tête `CSRFPreventionToken` n'est jamais consulté sur ce chemin |

En-tête émis par `pvectl` :

```
Authorization: PVEAPIToken=<user>@<realm>!<tokenname>=<secret>
```

## Pièges de champs relevés

| Champ | Piège |
| --- | --- |
| `cpu` (`/nodes`, `/nodes/{n}/status`) | c'est un **ratio 0..1**, pas un pourcentage. L'afficher tel quel donne « 0.001 % » de charge sur un nœud à 100 % |
| `mem`, `maxmem`, `disk`, `maxdisk` | en **octets**, jamais en Mo |
| `maxcpu` | nombre de **threads** (16 ici), alors que `cpuinfo.cores` vaut 8 |
| `loadavg` | tableau de **chaînes**, pas de nombres |
| nœud inexistant | répond **500**, pas 404 : `hostname lookup 'x' failed`. Le 404 est réservé aux chemins inconnus |
| `template` (`/nodes/{n}/qemu`) | un template est une **VM portant un drapeau**, dans le même index que les VM |
| `tags` | chaîne séparée par des **points-virgules**, pas un tableau |
| `volid` | identifiant `storage:type/fichier`, **jamais** un chemin de système de fichiers |
| filtre des tâches actives | `?source=active`, et **non** `?running=1` — ce paramètre n'existe pas |
| chaînes à options | le premier élément est positionnel pour un disque (`local-lvm:vm-100-disk-0,size=20G`) mais déjà une paire pour une carte réseau (`virtio=AA:BB,bridge=vmbr0`) |
| `net0` d'un LXC | rien à voir avec celui d'une VM : pas de modèle positionnel, et **`name=` est obligatoire** (`name=eth0,bridge=vmbr0,ip=dhcp`). Sans lui, `missing property` |
| `hostname` vs `name` | le clone LXC nomme le nouveau guest par **`hostname`** là où le clone QEMU prend `name`. La symétrie s'arrête là |
| `unprivileged` | le schéma annonce `default=0`, mais **la création applique 1**. Lire le défaut du schéma revient ici à documenter l'inverse du comportement |
| `force` (DELETE) | existe sur `/lxc/{vmid}`, **pas** sur `/qemu/{vmid}`. Une VM qui tourne doit être arrêtée par un appel séparé |
| `ssh-public-keys` (LXC) | clés **brutes**, une par ligne — pas d'encodage URL, contrairement à `sshkeys` côté cloud-init |
| `/access/permissions` | répond une **map de maps** `{chemin: {privilège: 1}}`, pas une liste. La valeur `1` est le **bit de propagation**, pas un booléen « accordé » |
| `/access/roles` vs `/access/roles/{id}` | l'index renvoie `privs` en **chaîne à virgules**, le détail une **map** privilège→1. Même information, deux schémas |
| `PUT /access/acl` | paramètres au **pluriel** : `roles`, `users`, `tokens`, `groups`. `delete` est un **booléen** (« retire au lieu d'ajouter »), pas une liste de clés |
| `expire` (token, user) | **secondes depuis l'epoch**, jamais une date. `0` veut dire « jamais », et c'est une valeur, pas une absence |
| `POST …/token/{id}` | renvoie `value` (le secret) **une seule fois**. Aucun `GET` ne le rend ensuite : PVE ne le stocke que haché |
| `Permissions.Modify` | seul `Administrator` le porte parmi les rôles intégrés. Mais `PVE/API2/ACL.pm:190` autorise l'attribution d'un rôle **sans** ce privilège si l'appelant détient déjà tous les privilèges du rôle, avec propagation |
| identité d'un token | un token **n'est pas** son utilisateur : `POST /access/users/{u}/token/{t}` avec un token de `{u}` répond `403`, alors que le schéma dit `userid-param self` |
| `remove` (vzdump) | vaut **1 par défaut** et déclenche `prune-backups` : une sauvegarde en supprime d'autres. Exige en plus `Datastore.Allocate`. `pvectl` envoie `remove=0` sauf `--prune` |
| `compress` (vzdump) | `0` veut dire **aucune compression**, pas « niveau zéro ». Les autres valeurs sont des noms d'algorithmes (`zstd`, `gzip`, `lzo`), pas des niveaux |
| restauration | **il n'existe pas d'endpoint « restore »** : c'est `POST /nodes/{n}/qemu` (ou `/lxc`) avec `archive=<volid>`. Le schéma conditionne explicitement `force` à la présence d'`archive` |
| `bwlimit`, `ionice`, `performance` (vzdump) | exigent `Sys.Modify` sur `/` — un token de moindre privilège ne peut pas les passer |


## Contrat d'écriture du projet (PVX-021)

Toute mutation passe par `service.Runner.Run`. Une écriture qui ne passe pas par
ce chemin est un bug, pas une variante.

```
1. PRE-READ    la cible existe ? est-elle verrouillée ?
2. PLAN        rendu du payload RÉSOLU sur stderr — pas une paraphrase
3. GATE        --dry-run s'arrête ici ; sinon confirmation
               (W‼ : retaper l'identifiant de la cible, pas « y »)
4. WRITE       POST / PUT / DELETE
5. POLL        HTTP 200 = demande acceptée, PAS succès.
               On attend l'exitstatus de l'UPID.
6. LOG         si exitstatus ≠ OK : 20 dernières lignes du log de tâche,
               code de sortie 4
7. POST-READ   relecture indépendante — c'est ELLE qui est affichée,
               jamais l'écho de la requête
```

Les étapes 5 et 6 sont sautées pour une mutation synchrone (réponse sans UPID).
**L'étape 7 n'est jamais sautée** — `TestPostReadIsNeverSkipped` échoue sinon.

Paramètres vérifiés au passage : `shutdown` prend `timeout` et **`forceStop`**
(S majuscule), lus dans `PVE::API2::Qemu::vm_shutdown`.
