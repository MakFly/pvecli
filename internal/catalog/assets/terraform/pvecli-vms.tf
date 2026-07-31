# Posé une fois par « pvecli iac scaffold ». Relis-le, puis versionne-le.
#
# pvecli n'écrit JAMAIS de HCL. Ce fichier est du code, relu par un humain ;
# les VM, elles, sont de la DONNÉE, et vivent dans pvecli.auto.tfvars.json que
# « pvecli vm declare » met à jour. Terraform charge tout fichier *.auto.tfvars
# tout seul, donc déclarer une VM ne demande jamais de toucher à ce fichier-ci.
#
# Conséquence directe : passer une VM de 8 à 16 Go, c'est un nombre qui change
# dans le fichier de données. Pas une ressource à réécrire.

variable "vms" {
  description = "Les VM déclarées par pvecli, indexées par nom d'hôte."

  # `optional(...)` porte le défaut. Ce qui vaut null ici retombe, dans la
  # ressource, sur la variable partagée du même nom — c'est ce qui permet à une
  # déclaration de ne dire que ce qui la distingue des autres.
  type = map(object({
    vmid     = number
    cores    = number
    memory   = number # MIBIOCTETS. 8 Go = 8192. Écrire 8 donne 8 Mio.
    disk     = optional(number, 20)
    ip       = optional(string, "dhcp") # "192.168.1.220/24" ou "dhcp"
    gateway  = optional(string)
    node     = optional(string)
    template = optional(number)
    user     = optional(string, "ops")
    services = optional(list(string), [])
    tags     = optional(list(string), [])
    on_boot  = optional(bool, true)
  }))
  default = {}
}

resource "proxmox_virtual_environment_vm" "pvecli" {
  for_each = var.vms

  name        = each.key
  vm_id       = each.value.vmid
  node_name   = coalesce(each.value.node, var.node_name)
  description = "Déclarée par pvecli — services: ${join(", ", each.value.services)}"
  on_boot     = each.value.on_boot

  # Les tags ne sont pas de la décoration : « svc_docker » est ce que
  # l'inventaire transforme en groupe Ansible, et donc ce qui décide quel rôle
  # est joué. Les enlever ne casse rien visiblement — le playbook trouve zéro
  # hôte et sort 0.
  tags = each.value.tags

  clone {
    vm_id = coalesce(each.value.template, var.template_vm_id)
    full  = true
  }

  cpu {
    cores = each.value.cores
  }
  memory {
    dedicated = each.value.memory
  }

  # Sans l'agent, Terraform attend une adresse que personne ne peut donner :
  # 11 min 54 s mesurées contre 18 s avec. Le template doit l'embarquer.
  agent {
    enabled = true
  }

  initialization {
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
      username = each.value.user
      keys     = [var.ssh_public_key]
    }
  }

  network_device {
    bridge = var.bridge
  }

  disk {
    datastore_id = var.datastore_id
    size         = each.value.disk
    interface    = "scsi0"
  }
}

# Ce que pvecli relit après un apply. La preuve reste la relecture par l'API
# (« postflight ») ; ceci n'est qu'un raccourci de lecture pour un humain.
output "pvecli_vms" {
  description = "vmid, nœud et services de chaque VM déclarée par pvecli."
  value = {
    for name, vm in proxmox_virtual_environment_vm.pvecli : name => {
      vmid     = vm.vm_id
      node     = vm.node_name
      services = var.vms[name].services
    }
  }
}
