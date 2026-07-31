package pve

import "testing"

// The safe default has to survive a caller who says nothing at all: an
// unprivileged container is not what the operator asks for, it is what they get
// unless they insist otherwise.
func TestContainerIsUnprivilegedUnlessAsked(t *testing.T) {
	v := CTOptions{OSTemplate: "local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst"}.Values()

	if v.Get("unprivileged") != "1" {
		t.Errorf("un conteneur doit être non privilégié par défaut: %v", v)
	}

	privileged := CTOptions{OSTemplate: "local:vztmpl/x.tar.zst", Privileged: true}.Values()
	if _, present := privileged["unprivileged"]; present {
		t.Errorf("--privileged ne doit pas envoyer « unprivileged »: %v", privileged)
	}
}

// The translation from flags to option strings is the part --dry-run exists to
// show, so it is the part a test has to pin down.
func TestCTOptionsTranslateToOptionStrings(t *testing.T) {
	v := CTOptions{
		OSTemplate: "local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst",
		Hostname:   "web", Cores: 1, Memory: 512,
		RootFS: "local-lvm:8", Bridge: "vmbr0", IP: "192.0.2.120/24",
		Gateway: "192.0.2.1", Tags: "lab,pvectl",
	}.Values()

	// name= is not decoration: PVE rejects a container interface without it.
	if got, want := v.Get("net0"), "name=eth0,bridge=vmbr0,ip=192.0.2.120/24,gw=192.0.2.1"; got != want {
		t.Errorf("net0 = %q, want %q", got, want)
	}
	if got, want := v.Get("rootfs"), "local-lvm:8"; got != want {
		t.Errorf("rootfs = %q, want %q", got, want)
	}
	// Tags are semicolon-separated in PVE, comma-separated for a human.
	if got, want := v.Get("tags"), "lab;pvectl"; got != want {
		t.Errorf("tags = %q, want %q", got, want)
	}

	// Nothing that was not asked for: swap has a node-side default, and
	// sending 0 would silently override it.
	if _, present := v["swap"]; present {
		t.Errorf("un swap non demandé ne doit pas être envoyé: %v", v)
	}
}

// A volid is "storage:type/file", never a filesystem path — that is what lets
// the pre-read look for the template on the right storage.
func TestTemplateStorage(t *testing.T) {
	if got := TemplateStorage("local:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst"); got != "local" {
		t.Errorf("TemplateStorage = %q, want \"local\"", got)
	}
	if got := TemplateStorage("/var/lib/vz/template/cache/debian.tar.zst"); got != "" {
		t.Errorf("un chemin de fichier n'est pas un volid, got %q", got)
	}
}
