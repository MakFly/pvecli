# API-MAP — endpoints implémentés par `pvecli`

Règle non négociable du PRD §6.3 : **aucun endpoint écrit de mémoire.** Chaque
ligne n'est ajoutée qu'après vérification du schéma contre l'API viewer officiel
de PVE **9.x** (le lab tourne en 9.2.2, pas en 8.x) ou via
`bun .agents/skills/proxmox-api/scripts/search-pve-api.ts <terme>`.

| Endpoint | Méthode | Commande `pvecli` | Story | Vérifié le | Source |
| --- | --- | --- | --- | --- | --- |
| `/version` | GET | `pvecli version` | PVX-005 | 2026-07-31 | `pvesh get /version` sur le nœud (PVE 9.2.2) |
| `/nodes` | GET | `pvecli node ls` | PVX-006 | 2026-07-31 | `pvesh get /nodes` sur le nœud |
| `/nodes/{node}/status` | GET | `pvecli node show` | PVX-006 | 2026-07-31 | `pvesh get /nodes/pve/status` sur le nœud |
| `/nodes/{node}/status` | POST | `pvecli node reboot` | PVX-084 | 2026-08-03 | schéma de l'API : `command` ∈ {reboot, shutdown}, privilège `Sys.PowerMgmt` sur `/nodes/{node}` |
| `/cluster/status` | GET | `pvecli doctor` | PVX-008 | 2026-07-31 | `pvesh get /cluster/status` sur le nœud |
| `/cluster/nextid` | GET | `pvecli vm declare --suggest-id`, `pvecli lxc declare --suggest-id` | — | 2026-08-03 | forme du corps confirmée en direct sur le nœud du lab le 03-08-2026 (`pve-api-daemon/3.0`) : `{"data":"<vmid libre>"}`, une CHAÎNE. `scripts/capture.sh` exige `PVE_API_URL` **exporté**, ce que la CLI ne fait pas (elle lit l'endpoint dans `config.yaml`) — le nœud n'est pas injoignable, c'est ce script-là qui a une prémisse différente ; fixture `testdata/cluster-nextid.json` écrite à la main plutôt que rejouée avec `make capture` |
| `/access/permissions` | GET | `pvecli doctor` | PVX-008 | 2026-07-31 | appel réel avec le token `automation@pve!pvectl` |
| `/cluster/resources` | GET | `pvecli cluster resources`, `iac inventory\|drift\|adopt` | PVX-016 · 042 · 044 | 2026-07-31 | `pvesh usage /cluster/resources` sur le nœud |
| `/access/users` | GET | `pvecli access user ls` | PVX-033 | 2026-07-31 | `pvesh get /access/users` sur le nœud |
| `/access/users` | POST | `pvecli access user create` | PVX-070 | 2026-08-01 | `search-pve-api.ts "/access/users"` — exige `Realm.AllocateUser` sur `/access/realm/<realm>` **et** `User.Modify` sur `/access/groups` |
| `/access/users/{userid}` | GET | pre-read et post-read de `user create` | PVX-070 | 2026-08-01 | `search-pve-api.ts "/access/users"` — la réponse ne **répète pas** `userid` : l'appelant l'a demandé |
| `/access/roles` | GET | `pvecli access role ls` | PVX-033 | 2026-07-31 | `pvesh get /access/roles` sur le nœud |
| `/access/roles/{roleid}` | GET | `pvecli access role show` | PVX-033 | 2026-07-31 | `pvesh get /access/roles/PVEVMAdmin` sur le nœud |
| `/access/roles` | POST | `pvecli access role add` | PVX-077 | 2026-08-02 | `search-pve-api.ts "/access/roles"` + source `pve-access-control/src/PVE/API2/Role.pm` — `roleid` obligatoire (format `pve-roleid`), `privs` **optionnel** côté API (`pve-priv-list`, chaîne à virgules) et exigé par `pvecli`. Privilège : **`Sys.Modify` sur `/access`**, pas sur `/` |
| `/access/roles/{roleid}` | PUT | `pvecli access role set` | PVX-077 | 2026-08-02 | `search-pve-api.ts "/access/roles"` + source `Role.pm` (`update_role`) — le PUT **REMPLACE** `privs` (`$usercfg->{roles}->{$role} = {} if !$param->{append}`). `append` (booléen, `requires: privs`) ferait l'union côté nœud ; `pvecli` ne l'envoie **jamais** et fusionne avant. Refusé sur un rôle `special`. Privilège : **`Sys.Modify` sur `/access`** |
| `/access/roles/{roleid}` | DELETE | `pvecli access role rm` | PVX-077 | 2026-08-02 | `search-pve-api.ts "/access/roles"` + source `Role.pm` (`delete_role`) — les ACL qui posaient le rôle n'accordent plus rien. Refusé sur un rôle `special` (`auto-generated role cannot be deleted`). Privilège : **`Sys.Modify` sur `/access`** |
| `/access/acl` | GET | `pvecli access acl ls` | PVX-033 | 2026-07-31 | `pvesh get /access/acl` sur le nœud |
| `/access/acl` | PUT | `pvecli access acl set` | PVX-035 | 2026-07-31 | `pvesh usage /access/acl -v` sur le nœud |
| `/access/ticket` | POST | `pvecli login` | PVX-079 | 2026-08-01 | le SEUL endpoint appelé sans identifiant — c'est lui qui en produit. Rend `ticket` (cookie `PVEAuthCookie`) et `CSRFPreventionToken`, exigé sur les écritures ; durée 2 h |
| `/access/users/{userid}/token` | GET | `pvecli access token ls` | PVX-033 | 2026-07-31 | `pvesh get /access/users/automation@pve/token` |
| `/access/users/{userid}/token/{tokenid}` | GET | post-read de `token create\|rm` | PVX-034 | 2026-07-31 | `pvesh usage /access/users/automation@pve/token/pvecli -v` |
| `/access/users/{userid}/token/{tokenid}` | POST | `pvecli access token create` | PVX-034 | 2026-07-31 | `pvesh usage` sur le nœud + `PVE::API2::User::generate_token` |
| `/access/users/{userid}/token/{tokenid}` | DELETE | `pvecli access token rm` | PVX-034 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/pools` | GET | `pvecli pool ls\|show` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` + `PVE::API2::Pool::index` (renvoie un **tableau** même pour un seul pool) |
| `/pools` | POST | `pvecli pool create` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` (`create_pool`, `Pool.Allocate` sur `/pool/{poolid}`) |
| `/pools` | PUT | `pvecli pool add\|remove` | PVX-050 | 2026-07-31 | `pvesh usage /pools -v` (`update_pool` — la forme `/pools/{poolid}` est déclarée dépréciée par le nœud) |
| `/pools` | DELETE | `pvecli pool rm` | PVX-050 | 2026-07-31 | `PVE::API2::Pool::delete_pool` ligne 484 : « You can only delete empty pools » |
| `/nodes/{node}/qemu` | GET | `pvecli vm ls` | PVX-011 | 2026-07-31 | `PVE::QemuServer::vmstatus_return_properties`, lu dans le source du nœud |
| `/nodes/{node}/qemu` | POST | `pvecli vm create` | PVX-025 | 2026-07-31 | `pvesh usage /nodes/pve/qemu -v` |
| `/nodes/{node}/qemu/{vmid}` | DELETE | `pvecli vm rm` | PVX-031 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/config` | PUT | `pvecli vm set` | PVX-026 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/clone` | POST | `pvecli vm clone` | PVX-024 | 2026-07-31 | `PVE::API2::Qemu::clone_vm`, lu dans le source du nœud |
| `/nodes/{node}/qemu/{vmid}/template` | POST | `pvecli vm template` | PVX-027 | 2026-07-31 | `PVE::API2::Qemu` (`template`), lu dans le source du nœud |
| `/nodes/{node}/qemu/{vmid}/snapshot` | GET · POST | `pvecli vm snapshot ls\|create` | PVX-028 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/212/snapshot -v` |
| `/nodes/{node}/qemu/{vmid}/snapshot/{name}/rollback` | POST | `pvecli vm snapshot rollback` | PVX-028 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/snapshot/{name}` | DELETE | `pvecli vm snapshot rm` | PVX-028 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/migrate` | GET | pre-read de `pvecli vm migrate` | PVX-052 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/211/migrate -v` — « Get preconditions for migration » |
| `/nodes/{node}/qemu/{vmid}/migrate` | POST | `pvecli vm migrate` | PVX-052 | 2026-07-31 | `pvesh usage … -v` (`online`, `with-local-disks`, `targetstorage`, `bwlimit`) |
| `/nodes/{node}/lxc/{vmid}/migrate` | GET · POST | `pvecli lxc migrate` | PVX-052 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/120/migrate` + réponse réelle (champs en **tirets**, pas en underscores) |
| `/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces` | GET | `pvecli vm agent ifaces`, `vm ip`, `iac inventory` | PVX-029 · 042 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/agent/exec` | POST | `pvecli vm agent exec` | PVX-078 | 2026-08-01 | `command` est répété une fois par argument — une seule chaîne serait lue comme un exécutable dont le nom contient des espaces ; il n'y a **pas** de shell derrière |
| `/nodes/{node}/qemu/{vmid}/agent/exec-status` | GET | `pvecli vm agent exec` (attente) | PVX-078 | 2026-08-01 | rend `exited`, `exitcode`, `out-data`, `err-data` (champs en **tirets**) ; interrogé avec le `pid` rendu par `agent/exec` |
| `/nodes/{node}/lxc/{vmid}/snapshot` | GET · POST | `pvecli lxc snapshot ls\|create` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, lignes 24-109 du source du nœud |
| `/nodes/{node}/lxc/{vmid}/snapshot/{name}/rollback` | POST | `pvecli lxc snapshot rollback` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, ligne 269 |
| `/nodes/{node}/lxc/{vmid}/snapshot/{name}` | DELETE | `pvecli lxc snapshot rm` | PVX-028 | 2026-07-31 | `PVE::API2::LXC::Snapshot`, ligne 169 |
| `/nodes/{node}/qemu/{vmid}/config` | GET | `pvecli vm show`, `iac drift\|adopt` | PVX-013 · 044 · 045 | 2026-07-31 | `pvesh usage /nodes/pve/qemu/{vmid}/config` |
| `/nodes/{node}/qemu/{vmid}/status/current` | GET | `pvecli vm show` | PVX-013 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/lxc` | GET | `pvecli lxc ls` | PVX-012 | 2026-07-31 | `pvesh usage /nodes/pve/lxc` |
| `/nodes/{node}/lxc` | POST | `pvecli lxc create` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc -v` |
| `/nodes/{node}/lxc/{vmid}` | DELETE | `pvecli lxc rm` | PVX-031 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100 -v` |
| `/nodes/{node}/lxc/{vmid}/config` | PUT | `pvecli lxc set` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100/config -v` |
| `/nodes/{node}/lxc/{vmid}/clone` | POST | `pvecli lxc clone` | PVX-030 | 2026-07-31 | `pvesh usage /nodes/pve/lxc/100/clone -v` |
| `/nodes/{node}/lxc/{vmid}/termproxy` | POST | `pvecli lxc exec` (amorçage) | PVX-074 | 2026-08-01 | rend `{user, ticket, port}` — LXC n'a pas d'`agent/exec` ; la console est le seul canal vers l'intérieur (`PVE::API2::LXC::Status::termproxy`) |
| `/nodes/{node}/lxc/{vmid}/vncwebsocket` | GET | `pvecli lxc exec` (PTY) | PVX-074 | 2026-08-01 | websocket ; 1er message `user:ticket\n` → `OK`, puis entrée framée `0:len:data`, sortie brute du PTY |
| `/cluster/firewall/options` | GET | `pvecli lxc firewall show` (avertissement) | PVX-075 | 2026-08-01 | `enable` datacenter : sans lui, aucun firewall guest ne filtre — d'où l'avertissement |
| `/nodes/{node}/lxc/{vmid}/firewall/options` | GET · PUT | `pvecli lxc firewall show\|enable\|disable` | PVX-075 | 2026-08-01 | `enable`, `policy_in`, `policy_out` ; `PVE::API2::Firewall::CT` |
| `/nodes/{node}/lxc/{vmid}/firewall/rules` | GET · POST | `pvecli lxc firewall rules\|allow` | PVX-075 | 2026-08-01 | règle : `type=in action=ACCEPT proto dport source enable` |
| `/nodes/{node}/lxc/{vmid}/firewall/rules/{pos}` | DELETE | `pvecli lxc firewall rm` | PVX-075 | 2026-08-01 | supprime la règle à la position `pos` |
| `/cluster/firewall/ipset` | GET · POST | `pvecli fw ipset ls\|create` | PVX-075 | 2026-08-01 | set d'IP réutilisable au niveau datacenter |
| `/cluster/firewall/ipset/{name}` | GET · POST | `pvecli fw ipset show\|add` | PVX-075 | 2026-08-01 | liste / ajoute une entrée `cidr` au set |
| `/cluster/firewall/ipset/{name}/{cidr}` | DELETE | `pvecli fw ipset del` | PVX-075 | 2026-08-01 | retire une entrée du set |
| `/nodes/{node}/lxc/{vmid}/config` | GET | `pvecli lxc show`, `iac drift\|adopt`, `iac inventory` | PVX-013 · 044 · 045 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/lxc/{vmid}/status/current` | GET | `pvecli lxc show` | PVX-013 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/storage` | GET | `pvecli storage ls` | PVX-014 | 2026-07-31 | `pvesh get /nodes/pve/storage` |
| `/nodes/{node}/storage/{storage}/content` | GET | `pvecli storage content` | PVX-014 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/storage/{storage}/download-url` | POST | `pvecli storage download-url` | PVX-051 | 2026-07-31 | `pvesh usage /nodes/pve/storage/local/download-url -v` |
| `/nodes/{node}/storage/{storage}/upload` | POST | `pvecli storage upload` | PVX-051 | 2026-07-31 | `pvesh usage … -v` + `PVE::APIServer::AnyEvent::file_upload_multipart` (ordre des parties du multipart) |
| `/nodes/{node}/storage/{storage}/content/{volume}` | DELETE | `pvecli storage rm` | PVX-051 | 2026-07-31 | `PVE::API2::Storage::Content::delete` ligne 453 (`Datastore.Allocate`) |
| `/storage` | GET | `pvecli storage def ls` | PVX-083 | 2026-08-02 | `search-pve-api.ts "/storage"` + capture réelle du nœud (`testdata/storage-defs.json`) — endpoint de **cluster**, pas de nœud : la définition vit dans `/etc/pve/storage.cfg`, répliquée. Liste **filtrée** : `Datastore.Audit` ou `Datastore.AllocateSpace` sur `/storage/<id>` |
| `/storage/{storage}` | GET | `pvecli storage def show`, pre-read et post-read de `add\|set\|rm` | PVX-083 | 2026-08-02 | `search-pve-api.ts "/storage"` + capture réelle (`testdata/storage-def.json`) — un identifiant inconnu répond **500**, pas 404. `Datastore.Allocate` sur `/storage/{storage}` |
| `/storage` | POST | `pvecli storage def add` | PVX-083 | 2026-08-02 | `search-pve-api.ts "/storage"` — rend `{storage, type, config}`. Privilège : **`Datastore.Allocate` sur `/storage`**, pas `Sys.Modify` — `PVEDatastoreAdmin` le porte déjà |
| `/storage/{storage}` | PUT | `pvecli storage def set` | PVX-083 | 2026-08-02 | `search-pve-api.ts "/storage"` — PUT **partiel**, mais `export`/`share`/`datastore`/`path`/`type` en sont **absents**. `digest` couvre tout `storage.cfg`. **`Datastore.Allocate` sur `/storage`** |
| `/storage/{storage}` | DELETE | `pvecli storage def rm` | PVX-083 | 2026-08-02 | `search-pve-api.ts "/storage"` — supprime l'**entrée de configuration**, pas les données du partage. Rend `null`. **`Datastore.Allocate` sur `/storage`** |
| `/nodes/{node}/network` | GET | `pvecli net ls` | PVX-049 | 2026-07-31 | `pvesh usage /nodes/pve/network -v` + `PVE::API2::Network` ligne 418 (`set_result_attrib('changes')`) |
| `/nodes/{node}/network/{iface}` | GET | `pvecli net show` | PVX-049 | 2026-07-31 | `pvesh usage /nodes/pve/network/vmbr0` |
| `/nodes/{node}/network` | PUT | `pvecli net apply` | PVX-049 | 2026-07-31 | `PVE::API2::Network::reload_network_config` (ligne 885 : `Sys.Modify`, ligne 903 : renvoie un UPID) |
| `/nodes/{node}/network` | DELETE | `pvecli net revert` | PVX-049 | 2026-07-31 | `PVE::API2::Network::revert_network_changes` ligne 511 (`unlink /etc/network/interfaces.new`) |
| `/nodes/{node}/vzdump` | POST | `pvecli backup run` | PVX-037 | 2026-07-31 | `pvesh usage /nodes/pve/vzdump -v` + `PVE::API2::VZDump` (privilèges) |
| `/cluster/backup` | GET | `pvecli backup job ls` | PVX-076 | 2026-08-02 | `search-pve-api.ts "/cluster/backup"` — endpoint de **cluster**, pas de nœud : la définition vit dans `/etc/pve/jobs.cfg` et `node` n'y est qu'un filtre d'exécution. Lecture : `Sys.Audit` sur `/` |
| `/cluster/backup` | POST | `pvecli backup job create` | PVX-076 | 2026-08-02 | `search-pve-api.ts "/cluster/backup"` — `id` est **optionnel** (généré par PVE) et la réponse ne le rend pas : la création se vérifie en relisant la liste. `Sys.Modify` sur `/` ; `dumpdir`/`tmpdir`/`script` sont en plus réservés à `root@pam` |
| `/cluster/backup/{id}` | GET | `pvecli backup job show` | PVX-076 | 2026-08-02 | `search-pve-api.ts "/cluster/backup"` — rend `next-run` (epoch), la seule preuve que le planificateur a retenu le `schedule` |
| `/cluster/backup/{id}` | PUT | `pvecli backup job set` | PVX-076 | 2026-08-02 | `search-pve-api.ts "/cluster/backup"` — PUT **partiel** : seules les clés envoyées changent. `Sys.Modify` sur `/` |
| `/cluster/backup/{id}` | DELETE | `pvecli backup job rm` | PVX-076 | 2026-08-02 | `search-pve-api.ts "/cluster/backup"` — supprime la **définition**, pas les archives déjà écrites. `Sys.Modify` sur `/` |
| `/nodes/{node}/tasks` | GET | `pvecli task ls` | PVX-015 | 2026-07-31 | `pvesh get /nodes/pve/tasks` |
| `/nodes/{node}/tasks/{upid}/status` | GET | `pvecli task show` | PVX-015 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/tasks/{upid}/log` | GET | `pvecli task log` | PVX-015 | 2026-07-31 | `pvesh usage` sur le nœud |
| `/nodes/{node}/qemu/{vmid}/status/{action}` | POST | `pvecli vm start\|stop\|shutdown\|reboot\|reset\|suspend\|resume` | PVX-022 | 2026-07-31 | `PVE::API2::Qemu` (`vm_stop`, `vm_shutdown`), lu dans le source du nœud |
| `/nodes/{node}/lxc/{vmid}/status/{action}` | POST | `pvecli lxc start\|stop\|shutdown\|reboot\|reset` | PVX-023 | 2026-07-31 | `PVE::API2::LXC::Status` |

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

En-tête émis par `pvecli` :

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
| `append` (`PUT /access/roles/{roleid}`) | sans lui, le PUT **REMPLACE** toute la liste de privilèges — vérifié dans le source : `update_role` fait `$usercfg->{roles}->{$role} = {} if !$param->{append};` avant de réappliquer `privs`. Avec lui, l'union se fait **côté nœud**, donc la liste résultante n'est connue qu'**après** l'écriture et un `--dry-run` ne peut pas la montrer. `pvecli` ne l'envoie jamais : il relit, fusionne, et envoie la liste finale complète. C'est aussi la seule façon de **retirer** un privilège, l'API n'ayant aucune primitive pour ça |
| `roleid` (`POST /access/roles`) | **`PVE` est un espace de noms RÉSERVÉ**, et le refus est côté nœud : `create_role` fait `raise_param_exc` sur `$role =~ /^PVE/i` — préfixe **insensible à la casse**, donc `pveBackup` est refusé comme `PVEBackup`. Un rôle sur mesure ne peut donc **pas** s'appeler `PVEBackupJobAdmin`. Format `pve-roleid` par ailleurs : `[A-Za-z0-9.-_]+` |
| `PUT`/`DELETE /access/roles/{roleid}` sur un rôle intégré | `update_role` et `delete_role` meurent sur `auto-generated role '<r>' cannot be modified/deleted` dès que `role_is_special()` répond oui. Cette table contient `NoAccess`, `Administrator` et tout ce que `create_roles()` génère sous `PVE…`. Le champ `special` de `GET /access/roles` est le même prédicat, rendu par l'API |
| `Sys.Modify` dans les rôles intégrés | **seul `Administrator` le porte** (capture réelle : `testdata/roles-with-custom.json`). Comme une ACL n'accorde qu'un **rôle**, jamais un privilège, un **rôle sur mesure** est la seule sortie de moindre privilège pour tout ce qui exige `Sys.Modify` — `/cluster/backup` compris |
| écritures sur `/access/roles` | le `permissions.check` du schéma est `["perm","/access",["Sys.Modify"]]` : le privilège se lit sur **`/access`**, pas sur `/`. Chercher `Sys.Modify` sur `/` fait diagnostiquer le mauvais chemin |
| `expire` (token, user) | **secondes depuis l'epoch**, jamais une date. `0` veut dire « jamais », et c'est une valeur, pas une absence |
| `POST …/token/{id}` | renvoie `value` (le secret) **une seule fois**. Aucun `GET` ne le rend ensuite : PVE ne le stocke que haché |
| `Permissions.Modify` | seul `Administrator` le porte parmi les rôles intégrés. Mais `PVE/API2/ACL.pm:190` autorise l'attribution d'un rôle **sans** ce privilège si l'appelant détient déjà tous les privilèges du rôle, avec propagation |
| identité d'un token | un token **n'est pas** son utilisateur : `POST /access/users/{u}/token/{t}` avec un token de `{u}` répond `403`, alors que le schéma dit `userid-param self` |
| `remove` (vzdump) | vaut **1 par défaut** et déclenche `prune-backups` : une sauvegarde en supprime d'autres. Exige en plus `Datastore.Allocate`. `pvecli` envoie `remove=0` sauf `--prune` |
| `compress` (vzdump) | `0` veut dire **aucune compression**, pas « niveau zéro ». Les autres valeurs sont des noms d'algorithmes (`zstd`, `gzip`, `lzo`), pas des niveaux |
| restauration | **il n'existe pas d'endpoint « restore »** : c'est `POST /nodes/{n}/qemu` (ou `/lxc`) avec `archive=<volid>`. Le schéma conditionne explicitement `force` à la présence d'`archive` |
| `bwlimit`, `ionice`, `performance` (vzdump) | exigent `Sys.Modify` sur `/` — un token de moindre privilège ne peut pas les passer |
| `prune-backups` (job) | option string `keep-last=3,keep-daily=7`. Elle **remplace** la rétention du stockage, elle ne s'y ajoute pas. Défaut du schéma : `keep-all=1`, c'est-à-dire **rien ne purge** — un job planifié sans rétention remplit le stockage jusqu'à la panne. `pvecli backup job create` l'exige donc |
| `remove` + `prune-backups` (job) | les deux ne valent rien l'un sans l'autre : `remove=1` sans `prune-backups` applique une politique qu'on n'a pas écrite, `prune-backups` sans `remove` n'a **aucun effet**. `remove` est **rendu par le GET** : une politique désarmée s'affiche sinon comme une politique normale |
| `prune-backups` est **une** valeur | pas six champs. Un `PUT` qui n'envoie que le compteur modifié **efface les autres** — et la purge suivante supprime des archives non visées. `backup job set` relit et fusionne |
| `delete` (`PUT /cluster/backup/{id}`) | c'est par là qu'on **vide** une clé. Envoyer une valeur vide sur un champ typé (`all=`, `node=`) échoue en 400 (`type check ('boolean') failed`) |
| cible d'un job (`PUT`) | envoyer `vmid`, `pool` **ou** `all` suffit : `PVE::API2::Backup::update_job` efface les deux autres avant `verify_vzdump_parameters` |
| `enabled` (job) | déclaré à **1 par défaut** : son absence dans une réponse ne veut pas dire « désactivé ». Le décoder en `int` nu ferait afficher « inactif » sur un job qui tourne |
| `vmid` (job) | une **liste CSV** (`220,221`), pas un entier — contrairement au `{vmid}` de tous les chemins de guest |
| `all` / `pool` / `vmid` (job) | trois cibles **exclusives**, et `all` écrase les deux autres côté nœud. `exclude` suppose `all` |
| `next-run` (job) | epoch en secondes, **absent quand le nœud n'a pas retenu le `schedule`**. C'est la seule preuve qu'une planification est vivante : un job mal planifié a exactement la même tête qu'un job sain dans une liste de noms |
| `id` (`POST /cluster/backup`) | **optionnel** — PVE en génère un — et la réponse ne le rend pas. Savoir lequel vient d'être créé impose de relire `/cluster/backup` |
| `mailnotification`, `mailto` (job) | déclarés **dépréciés** par le schéma 9.x au profit des cibles/matchers de notification. Exposés par `pvecli` mais jamais envoyés par défaut |
| `pool` (`POST /nodes/{n}/qemu`) | le schéma le donne pour optionnel, et il ne l'est pas pour tout le monde : la création exige `VM.Allocate` sur `/vms/{vmid}` **ou sur `/pool/{pool}`**. Une identité dont le droit tient au pool ne peut créer qu'en le nommant. Le même appel réclame en plus `Datastore.AllocateSpace` sur le stockage et `SDN.Use` sur le pont |
| `DELETE /nodes/{n}/qemu/{vmid}` | vérifie `VM.Allocate` sur **`/vms/{vmid}`**, pas sur le pool. La destruction ne marche pour un membre de pool que parce que l'ACL du pool porte sur les VM qu'il contient — c'est le mécanisme, pas une tolérance |
| `export`, `share`, `datastore`, `path`, `type` (`/storage`) | présents dans le schéma du **POST**, **absents** de celui du **PUT**. Un stockage ne se **repointe pas** ailleurs : il faut supprimer la définition et la recréer. `storage def set` refuse ces drapeaux localement plutôt que de laisser le nœud rendre un 400 qui ne dit pas pourquoi |
| `GET /storage/{storage}` sur un nom inconnu | répond **500**, pas 404 : `{"data":null,"message":"storage 'x' does not exist\n"}`. Le motif « une erreur au pre-read = l'identifiant est libre » reste juste, mais l'erreur brute parlera d'erreur **interne du nœud** pour un simple nom absent |
| `content` (`/storage`) | l'**ordre n'est pas stable** : le même stockage `local` rend `backup,import,vztmpl,iso` par l'index et `import,backup,vztmpl,iso` par le détail, à une seconde d'intervalle. Comparer deux chaînes octet à octet conclut à un changement inexistant — la comparaison doit porter sur des **ensembles** (`pve.SameContentTypes`) |
| `digest` (`PUT /storage/{storage}`) | couvre **tout `/etc/pve/storage.cfg`**, pas l'entrée seule : les deux stockages du lab portent le même `921a2c39…`. Un changement sur **n'importe quel** stockage entre la lecture et l'écriture fait échouer le PUT. C'est la garde anti-écrasement concurrent, et l'échec est voulu : bruyant et rejouable |
| `content` (`PUT /storage/{storage}`) | **remplacé**, pas fusionné. Contrairement à `prune-backups` d'un job, l'unité du drapeau CLI est ici la même que celle de l'API (une chaîne à virgules pour une chaîne à virgules) : c'est un remplacement honnête, à condition d'écrire la liste **complète** |
| `content` d'un stockage `pbs` | **`backup` uniquement**. Y déclarer `iso` est accepté par le nœud et rien n'y atterrira jamais — un stockage d'apparence normale et définitivement vide. `pvecli` le refuse localement |
| `password` (`/storage`) | présent sur le **POST et le PUT**. `service.redactValue` le masque dans le plan ; `pvecli` ne l'accepte **ni en drapeau ni en configuration** — `PVE_STORAGE_PASSWORD` ou saisie masquée, comme le secret d'un token |


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
