# Posé une fois par « pvecli iac scaffold ». Relis-le, puis versionne-le.
#
# Symétrique à pvecli-vms.tf : les LXC sont de la DONNÉE, dans le même
# pvecli.auto.tfvars.json, sous la clé « lxcs ». « pvecli lxc declare » l'écrit.
# pvecli n'écrit toujours aucun HCL -- déclarer un conteneur ne demande jamais
# de toucher à ce fichier-ci.

variable "lxcs" {
  description = "Les conteneurs LXC déclarés par pvecli, indexés par nom d'hôte."

  type = map(object({
    vmid    = number
    cores   = number
    memory  = number # MIBIOCTETS. 8 Go = 8192. Écrire 8 donne 8 Mio.
    disk    = optional(number, 8)
    ip      = optional(string, "dhcp") # "192.168.1.220/24" ou "dhcp"
    gateway = optional(string)
    node    = optional(string)
    # ctid à cloner. Pas de défaut partagé comme var.template_vm_id côté VM :
    # un conteneur cloné à partir du mauvais template ne prévient personne.
    template     = number
    unprivileged = optional(bool, true)
    services     = optional(list(string), [])
    tags         = optional(list(string), [])
    on_boot      = optional(bool, true)
  }))
  default = {}
}

resource "proxmox_virtual_environment_container" "pvecli" {
  for_each = var.lxcs

  node_name     = coalesce(each.value.node, var.node_name)
  vm_id         = each.value.vmid
  description   = "Déclaré par pvecli — services: ${join(", ", each.value.services)}"
  start_on_boot = each.value.on_boot
  started       = true

  # Un conteneur privilégié partage l'espace de noms utilisateur de l'hôte --
  # root dedans est root dehors. Le défaut reste celui de Proxmox : non
  # privilégié, et une décision explicite pour en sortir.
  unprivileged = each.value.unprivileged

  # Docker ne démarre pas dans un conteneur sans « nesting » (cgroups imbriqués)
  # ni « keyctl » : le démon échoue à l'init du graphdriver, alors que l'apply
  # Terraform, lui, a réussi. Accordé au seul conteneur qui en a besoin --
  # « nesting » est un cran de privilège, et l'ouvrir partout annulerait le
  # défaut non privilégié posé juste au-dessus.
  # Sur un conteneur déjà en marche, la bascule part en « pending » : PVE ne
  # hotplug pas « features » et le provider ne le redémarre pas pour autant --
  # il faut un stop/start pour que Docker démarre enfin.
  features {
    nesting = contains(each.value.services, "docker")
    keyctl  = contains(each.value.services, "docker")
  }

  # Même remarque que pour les VM : ces tags décident quel groupe Ansible
  # reçoit ce conteneur, via « pvecli iac inventory ».
  tags = each.value.tags

  clone {
    vm_id = each.value.template
    full  = true
  }

  cpu {
    cores = each.value.cores
  }

  memory {
    dedicated = each.value.memory
  }

  initialization {
    hostname = each.key

    ip_config {
      ipv4 {
        address = each.value.ip
        # Une passerelle avec une adresse en DHCP est refusée par l'API.
        gateway = each.value.ip == "dhcp" ? null : coalesce(each.value.gateway, var.vm_ipv4_gateway)
      }
    }
    dns {
      servers = [var.vm_ipv4_gateway]
    }
    user_account {
      keys = [var.ssh_public_key]
    }
  }

  network_interface {
    name   = "eth0"
    bridge = var.bridge
  }

  disk {
    datastore_id = var.datastore_id
    size         = each.value.disk
  }
}

# Ce que pvecli relit après un apply. La preuve reste la relecture par l'API
# (« postflight ») ; ceci n'est qu'un raccourci de lecture pour un humain.
output "pvecli_lxcs" {
  description = "vmid, nœud et services de chaque LXC déclaré par pvecli."
  value = {
    for name, ct in proxmox_virtual_environment_container.pvecli : name => {
      vmid     = ct.vm_id
      node     = ct.node_name
      services = var.lxcs[name].services
    }
  }
}
