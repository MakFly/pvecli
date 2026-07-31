# Fabriquer une VM 2 vCPU / 8 Go avec Terraform et Ansible

> Expliqué très simplement, de zéro, sans rien supposer connu.
> **Chaque commande de ce document a été jouée pour de vrai le 2026-07-31.**
> Les durées indiquées sont mesurées, pas estimées.
>
> ⚠️ **Les adresses sont des exemples.** `192.0.2.0/24` est la plage que la
> RFC 5737 réserve à la documentation, et `pve.example` un nom réservé par la
> RFC 2606 : aucune des deux n'existe sur ton réseau. Remplace-les partout par
> les tiennes — le nœud, la passerelle, et l'adresse que tu veux donner à la VM.
> Le déroulé, lui, est celui qui a réellement tourné.

---

## 1. L'histoire, avec des mots simples

Imagine que tu veux un **gâteau**.

| Le vrai monde | Ici |
| --- | --- |
| Le **four**, la grosse machine dans la cuisine | le serveur Proxmox (`pve`) |
| Le **moule** à gâteau | le *template* — une VM modèle, figée |
| La **recette écrite** sur un papier | le fichier Terraform |
| Le **cuisinier** qui suit la recette | Terraform |
| Le **décorateur** qui met le glaçage | Ansible |
| Ta **télécommande** pour regarder dans le four | `pvecli` |

Trois règles, et c'est tout le truc :

1. **On ne fabrique pas un gâteau à partir de rien.** On fait d'abord un moule.
   Le moule, on ne le fait **qu'une fois**.
2. **On écrit la recette avant de cuisiner.** Comme ça, si on veut le même
   gâteau demain, on relit le papier au lieu de se souvenir.
3. **Le gâteau sort nature, on le décore après.** Terraform fait la VM ; Ansible
   installe ce qui va dedans.

---

## 2. Le dessin

```
   TON MAC                                       LE SERVEUR PROXMOX (pve)
   ───────                                       ────────────────────────

   pvecli  ─────── crée le moule ──────────────►  ┌────────────────────┐
   (télécommande)                                 │  template 9001     │
                                                  │  Debian 13 + agent │
                                                  └─────────┬──────────┘
                                                            │ on le copie
   main.tf  ────►  terraform  ──── copie ────────────────►  ▼
   (la recette)    (le cuisinier)                  ┌────────────────────┐
      2 vCPU                                       │  VM 210            │
      8192 Mio                                     │  2 vCPU / 8 Go     │
      IP .210                                      │  192.0.2.210     │
                                                   └─────────┬──────────┘
                                                             │
   pvecli iac inventory ◄── « elle est où ? » ───────────────┤
        │        (l'agent dans la VM répond son adresse)     │
        ▼                                                    │
   ansible ───────── installe nginx ───────────────────────► ▼
   (le décorateur)                                  http://192.0.2.210/
                                                    « Native app deployed »
```

Le seul fil un peu magique : **comment Ansible sait où est la VM ?**
Proxmox ne connaît pas l'adresse d'une VM — il voit une carte réseau, rien de
plus. C'est un petit programme **dans** la VM (`qemu-guest-agent`) qui la dit.
C'est pour ça que le moule doit contenir cet agent. Sans lui, tout le reste
marche, mais Ansible ne trouve personne à décorer.

---

## 3. Avant de commencer (une seule fois)

```bash
source ~/.config/pvecli/env      # charge l'adresse du serveur et le token
pvecli doctor                    # doit afficher quatre ✓
```

Si les quatre ✓ ne sont pas là, **arrête-toi ici** : rien de ce qui suit ne
marchera, et tu chercherais la panne au mauvais endroit.

---

## 4. Étape 1 — fabriquer le moule (≈ 4 min, une seule fois)

Le moule s'appelle un **template**. On part d'une image officielle Debian déjà
posée sur le serveur (`local:import/debian-13-genericcloud-amd64.qcow2`).

### 4.1 — Créer une VM ordinaire à partir de l'image

```bash
pvecli vm create 9001 \
  --name debian13-cloudinit-agent \
  --import-from local:import/debian-13-genericcloud-amd64.qcow2 \
  --storage local-lvm \
  --cloud-init --ci-user ops --ssh-keys ~/.ssh/id_ed25519.pub \
  --agent --cores 2 --memory 2048 \
  --tags template,debian13 --yes
```

`--cloud-init` est **obligatoire**. Sans lui la VM démarre… et personne ne peut
entrer : pas d'utilisateur, pas de clé, pas de réseau configuré.

### 4.2 — Lui donner une adresse fixe, et l'allumer

```bash
pvecli vm set 9001 \
  --set ipconfig0='ip=192.0.2.211/24,gw=192.0.2.1' \
  --set nameserver='192.0.2.1' --yes
pvecli vm start 9001 --yes
```

> **Pourquoi pas le DHCP ?** Sur ce réseau, le serveur DHCP ne répond pas de
> façon fiable aux nouvelles machines. Une adresse fixe, c'est une machine
> qu'on retrouve à coup sûr.

### 4.3 — Installer l'agent DANS la VM

Attends ~45 s que la VM démarre, puis :

```bash
ssh ops@192.0.2.211 \
  'sudo apt-get update -qq && \
   sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq qemu-guest-agent && \
   sudo systemctl enable --now qemu-guest-agent'
```

> **C'est l'étape que tout le monde oublie, et elle coûte cher.** Un template
> sans agent : le premier `terraform apply` a mis **11 min 54 s** (Terraform
> attendait une adresse que personne ne pouvait lui donner). Avec l'agent :
> **18 s**. Quarante fois plus rapide, pour un seul paquet.

### 4.4 — Effacer l'identité, puis figer le moule

```bash
ssh ops@192.0.2.211 \
  'sudo cloud-init clean --logs --seed && \
   sudo truncate -s 0 /etc/machine-id && \
   sudo rm -f /etc/ssh/ssh_host_*'

pvecli vm stop 9001 --yes
pvecli vm set 9001 --set ipconfig0='ip=dhcp' --yes
pvecli vm template 9001 --yes
```

Traduction en langage de 8 ans : **on efface le nom écrit sur le moule** avant
de le ranger. Sinon tous les gâteaux qui en sortiront porteront le même nom, la
même identité et les mêmes clés — et deux machines identiques sur un réseau,
c'est une machine qui n'existe pas.

Vérifie :

```bash
pvecli guest ls
# VMID  NOM                        STATUT   TEMPLATE
# 9001  debian13-cloudinit-agent   stopped  oui        ← « oui » = c'est un moule
```

---

## 5. Étape 2 — écrire la recette (les 2 vCPU et les 8 Go)

Fichier : `docs/infra/terraform/main.tf` du dépôt `proxmox-practice-lab`.

Les deux blocs qui portent ta demande :

```hcl
  # 2 vCPU. « cores » est PAR socket, et un nombre de sockets non précisé vaut
  # 1 — donc c'est bien 2 vCPU au total, pas 2 × N.
  cpu {
    cores = 2
  }

  # 8 Go. Toujours en MIBIOCTETS. 8 Go demandés = 8192 écrits ici.
  # Écrire « 8 » donne une VM de 8 Mio, qui démarre juste assez loin pour
  # échouer d'une façon incompréhensible.
  memory {
    dedicated = 8192
  }
```

Et l'adresse, pour la retrouver :

```hcl
  initialization {
    ip_config {
      ipv4 {
        address = "192.0.2.210/24"
        gateway = "192.0.2.1"
      }
    }
    user_account {
      username = "ops"
      keys     = [var.ssh_public_key]
    }
  }
```

Le reste du fichier dit juste : *copie le moule 9001, appelle-la `lab-app-01`,
donne-lui un disque de 20 Go, branche-la sur `vmbr0`*.

> ⚠️ **Les tags ne sont pas de la décoration.** `lab_apps` dans la liste des
> tags est **ce que le playbook Ansible cherche**. Enlève-le et Ansible dira
> « aucun hôte » — puis sortira avec le code 0, c'est-à-dire en ayant l'air
> d'avoir réussi.

---

## 6. Étape 3 — cuisiner (18 s)

```bash
source ~/.config/pvecli/env

pvecli iac plan     # regarde ce qui VA se passer, ne fait rien
pvecli iac apply    # le fait
```

`plan` d'abord, **toujours**. C'est relire la recette avant d'allumer le four.

Ce que `pvecli` ajoute autour de Terraform, et qui vaut la peine :

```
pré-vol
  ✓ https://192.0.2.23:8006 — PVE 9.2.2, TLS empreinte épinglée
  ✓ aucune dérive : le state décrit ce que le nœud contient

  … terraform crée la VM …

post-vol — relecture par l'API, pas par terraform
VMID  NOM         STATUT RÉEL  CŒURS  RAM        TAGS
210   lab-app-01  présent      2      8192 Mio   lab,lab_apps,managed,terraform
aucune dérive après apply : le déclaré et le réel concordent.
```

La dernière ligne est le point important : **on ne croit pas Terraform sur
parole**, on redemande au serveur ce qu'il contient vraiment.

### Vérifier avec ses propres yeux

```bash
pvecli vm show 210        # 2 vcpu · 8.0 GiB — vu par Proxmox
ssh ops@192.0.2.210 'nproc; free -h'
#   2
#   Mem: 7.8Gi          ← vu par la VM elle-même
```

*(7,8 Gi et pas 8,0 : le noyau se garde quelques miettes. C'est normal.)*

---

## 7. Étape 4 — décorer (Ansible)

```bash
pvecli iac configure --playbook site.yml --limit lab_apps \
  --idempotence \
  --verify-url 'http://{{host}}/' \
  --verify-contains 'Native app deployed by Ansible'
```

Une commande, quatre choses :

1. **elle demande au serveur où sont les VM** et fabrique l'inventaire Ansible
   à l'instant (jamais un fichier gardé de la veille — un bail réseau renouvelé
   suffirait à le rendre faux sans prévenir) ;
2. **elle vérifie que chaque machine répond** avant de jouer quoi que ce soit ;
3. `--idempotence` : **elle joue le playbook DEUX fois** et exige que le second
   passage ne change rien. Un playbook qui réussit deux fois n'est pas
   idempotent ; il l'est si le second tour est à `changed=0`.
4. `--verify-contains` : elle **lit le texte de la page**, pas seulement le code
   HTTP. Un `200 OK` ne prouve rien — Debian livre un site « par défaut » qui
   répond 200 en affichant sa propre page d'accueil. Ce lab s'est fait avoir.

Résultat obtenu :

```
idempotence VÉRIFIÉE : second passage à changed=0 sur 1 hôte(s).
  ✓ http://192.0.2.210/ → 200 OK
```

```bash
curl http://192.0.2.210/
# <h1>Native app deployed by Ansible</h1>
```

---

## 8. La version paresseuse : demander à l'agent

Tout ce qui précède, tu peux aussi le **demander**. `pvecli` installe un
assistant qui connaît ce lab par cœur.

### L'installer (une fois)

```bash
make install          # pose pvecli dans ~/.local/bin ET l'agent dans ~/.claude
pvecli ai status      # → état  à jour
```

L'agent est **dans le binaire**. Il n'y a rien à télécharger, et il ne peut pas
parler d'options que ta version de `pvecli` n'aurait pas.

### L'utiliser

Ouvre Claude Code, et écris en français :

```
> crée-moi une VM 4 vCPU 16 Go qui s'appelle api-01
```

Il enchaîne tout seul : `doctor`, l'édition du `main.tf`, `iac plan`,
`iac apply`, puis la relecture par l'API.

### Ce qu'il ne fera pas — et c'est le plus important

C'est un **assistant**, pas un pilote automatique. Sa fiche de poste lui
interdit explicitement :

- de **détruire** quoi que ce soit sans te montrer d'abord ce qui disparaît et
  attendre ton accord clair — et il te proposera une sauvegarde avant ;
- de toucher à une VM taguée `managed` autrement que par Terraform ;
- d'écrire ou d'afficher le secret du token ;
- de **contourner** un refus du serveur : un `403` sur `net apply`, il te le
  rapporte et t'indique la commande `pveum` que **toi** tu peux décider de
  lancer. Il n'élargit jamais ses propres droits ;
- de faire passer une erreur de certificat avec `--insecure` ;
- de créer ou détruire dans la plage 900-999, réservée aux tests ;
- d'annoncer un succès qu'il n'a pas relu à la source.

Traduction pour un enfant de 8 ans : **il a le droit de construire tout seul,
mais il doit demander avant de casser.** Et quand il dit « c'est fait », il doit
montrer la preuve, pas juste dire que la commande n'a pas râlé.

---

## 9. Tout défaire

```bash
cd ~/Documents/lab/sandbox/proxmox-practice-lab/docs/infra/terraform
export TF_VAR_proxmox_api_token="${PVE_API_TOKEN_ID}=${PVE_API_TOKEN_SECRET}"
terraform destroy -auto-approve
```

**Pour la VM 210, c'est `terraform destroy` et pas `pvecli vm rm`.** Elle porte
le tag `managed` : `pvecli` refuse d'y toucher exprès, et te renvoie vers son
propriétaire. Deux outils qui écrivent au même endroit, c'est une infrastructure
dont plus personne ne sait qui décide.

Le moule, lui, se supprime avec `pvecli` — il n'appartient à personne d'autre :

```bash
pvecli vm rm 9001 --purge --yes
```

---

## 10. Les cinq pièges, en une ligne chacun

| Piège | Ce que tu vois | La cause |
| --- | --- | --- |
| `memory = 8` | la VM ne démarre pas | c'est 8 **Mio**, il faut `8192` |
| pas de `qemu-guest-agent` | `apply` bloqué 12 min | Terraform attend une adresse que personne ne donne |
| tag `lab_apps` oublié | Ansible « ok », rien n'est installé | le playbook n'a trouvé aucun hôte, et sort 0 |
| `Host key verification failed` | Ansible s'arrête au pré-vol | une ancienne VM avait la même IP → `ssh-keygen -R 192.0.2.210` |
| on a cru le `200 OK` | page nginx par défaut | il faut lire le **contenu**, pas le code |

---

## 11. La seule phrase à retenir

> **Une commande qui répond « OK » n'a rien prouvé.**
> Un `200` n'est pas ta page. Un `apply` réussi n'est pas une VM conforme. Un
> playbook qui passe n'est pas un playbook idempotent.
> On relit toujours le résultat à la source — et c'est exactement ce que
> `pvecli` fait autour de Terraform et d'Ansible.
