package iac

import (
	"strings"
	"testing"
)

func adoptable() Live {
	return Live{
		VMID: 211, Name: "lab-app-01", Node: "pve",
		Cores: 2, Memory: 2048,
		Tags: []string{"lab"}, OnBoot: boolp(true),
		Disks: map[string]LiveDisk{
			"scsi0": {Datastore: "local-lvm", SizeGiB: 3},
			"ide2":  {Datastore: "local-lvm"},
		},
		Networks: []LiveNIC{{Bridge: "vmbr0", Model: "virtio"}},
	}
}

// The import block is the whole point, and its id is the provider's addressing
// scheme rather than PVE's: bpg/proxmox expects "<node>/<vmid>". A bare vmid
// parses fine and then fails at read time, complaining about a node.
func TestImportBlockCarriesTheProviderIDFormat(t *testing.T) {
	out := Adopt(adoptable(), false)

	if !strings.Contains(out, "import {") {
		t.Fatalf("aucun bloc import :\n%s", out)
	}
	if !strings.Contains(out, `id = "pve/211"`) {
		t.Errorf("l'identifiant attendu par bpg/proxmox est <node>/<vmid> :\n%s", out)
	}
	if !strings.Contains(out, "to = proxmox_virtual_environment_vm.lab_app_01") {
		t.Errorf("« to » doit désigner la ressource générée :\n%s", out)
	}
}

// Declaring `clone` on an import is the mistake that costs the resource: the
// plan proposes to destroy and recreate exactly what the import was meant to
// preserve.
func TestTheDraftNeverDeclaresAClone(t *testing.T) {
	out := Adopt(adoptable(), false)

	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "clone {") {
			t.Errorf("un bloc « clone » recréerait la ressource :\n%s", out)
		}
	}
	if !strings.Contains(out, "PAS de bloc « clone »") {
		t.Errorf("l'omission doit être expliquée, sinon elle passe pour un oubli :\n%s", out)
	}
}

// Every VM cloned from a cloud-init template carries an ide2 volume that the
// provider declares under `initialization`, not under `disk`. Emitting it as a
// disk is what makes the first plan propose a deletion.
func TestTheCloudInitDriveIsFlaggedNotEmittedAsADisk(t *testing.T) {
	out := Adopt(adoptable(), false)

	if strings.Contains(out, `interface    = "ide2"`) {
		t.Errorf("le lecteur cloud-init ne se déclare pas dans un bloc disk :\n%s", out)
	}
	if !strings.Contains(out, "initialization") {
		t.Errorf("sa présence doit être signalée à l'opérateur :\n%s", out)
	}
	if !strings.Contains(out, `interface    = "scsi0"`) {
		t.Errorf("le vrai disque, lui, doit être déclaré :\n%s", out)
	}
}

// The two resource types are close enough to invite one code path and
// different enough to punish it. Checked against the provider's own schema:
// a container has no top-level name — it declares `initialization.hostname` —
// and it says `start_on_boot` where a VM says `on_boot`.
//
// The first version of Adopt got this wrong on both counts, and it was
// `terraform validate` against the real schema that said so.
func TestAContainerUsesItsOwnAttributeNames(t *testing.T) {
	ct := Live{
		VMID: 120, Name: "web", Node: "pve",
		Cores: 1, Memory: 512, OnBoot: boolp(true),
		Disks:    map[string]LiveDisk{"rootfs": {Datastore: "local-lvm", SizeGiB: 8}},
		Networks: []LiveNIC{{Bridge: "vmbr0"}},
	}

	out := Adopt(ct, true)

	if !strings.Contains(out, "resource \"proxmox_virtual_environment_container\"") {
		t.Errorf("mauvais type de ressource :\n%s", out)
	}
	if !strings.Contains(out, "start_on_boot = true") {
		t.Errorf("un conteneur dit « start_on_boot », pas « on_boot » :\n%s", out)
	}
	for _, forbidden := range []string{"\n  name      =", "\n  hostname  ="} {
		if strings.Contains(out, forbidden) {
			t.Errorf("un conteneur n'a pas de nom au premier niveau :\n%s", out)
		}
	}
	if !strings.Contains(out, "initialization { hostname = \"web\" }") {
		t.Errorf("le nom doit être indiqué à sa vraie place :\n%s", out)
	}
	if !strings.Contains(out, "network_interface {") {
		t.Errorf("un conteneur déclare « network_interface », pas « network_device » :\n%s", out)
	}
	// A container's disk block has no `interface` attribute at all.
	if strings.Contains(out, "interface    =") {
		t.Errorf("le bloc disk d'un conteneur n'a pas d'attribut « interface » :\n%s", out)
	}
}

// The label has to be a valid Terraform identifier, and PVE guest names are
// not: « lab-app-01 » contains dashes, and a name may be empty entirely.
func TestResourceNameIsAlwaysAValidIdentifier(t *testing.T) {
	cases := map[string]string{
		"lab-app-01": "lab_app_01",
		"web":        "web",
		"01-front":   "_01_front",
		"":           "guest_211",
	}
	for name, want := range cases {
		if got := ResourceName(name, 211); got != want {
			t.Errorf("ResourceName(%q) = %q, attendu %q", name, got, want)
		}
	}
}

// The generated file is a set of instructions as much as a set of blocks. The
// sequence is what makes the import work; without it, an operator applies a
// draft nobody checked.
func TestTheOutputCarriesTheSequenceThatMakesImportWork(t *testing.T) {
	out := Adopt(adoptable(), false)

	for _, want := range []string{"terraform plan", "No changes", "terraform apply", "--force-unmanaged"} {
		if !strings.Contains(out, want) {
			t.Errorf("la marche à suivre ne mentionne pas %q :\n%s", want, out)
		}
	}
}
