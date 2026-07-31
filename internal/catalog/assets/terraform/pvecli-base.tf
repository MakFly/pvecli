# Posé par « pvecli iac scaffold » UNIQUEMENT si le dossier ne déclarait pas
# déjà un « provider "proxmox" ». Un dossier Terraform existant garde le sien —
# deux blocs provider dans le même dossier, c'est un « terraform init » qui
# échoue avant d'avoir rien fait.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    proxmox = {
      source  = "bpg/proxmox"
      version = "~> 0.107"
    }
  }
}

provider "proxmox" {
  endpoint  = var.proxmox_endpoint
  api_token = var.proxmox_api_token

  # Un Proxmox neuf sert un certificat signé par sa propre CA, qu'aucun poste ne
  # connaît. Le défaut reste false : la réponse honnête est de faire confiance à
  # /etc/pve/pve-root-ca.pem, pas de désactiver la vérification.
  insecure = var.proxmox_insecure
}

variable "proxmox_endpoint" {
  type      = string
  sensitive = true
}

# Alimentée par TF_VAR_proxmox_api_token, que « pvecli iac apply » exporte
# depuis le keychain. Jamais écrite dans un terraform.tfvars.
variable "proxmox_api_token" {
  type      = string
  sensitive = true
}

variable "proxmox_insecure" {
  type    = bool
  default = false
}

variable "node_name" {
  description = "Nœud par défaut, quand une VM ne nomme pas le sien."
  type        = string
}

variable "template_vm_id" {
  description = "Template cloud-init par défaut, avec qemu-guest-agent installé."
  type        = number
}

variable "ssh_public_key" {
  type = string
}

variable "datastore_id" {
  type    = string
  default = "local-lvm"
}

variable "bridge" {
  type    = string
  default = "vmbr0"
}

variable "vm_ipv4_gateway" {
  type    = string
  default = "192.168.1.1"
}
