package pve

import "testing"

// The three shapes actually met in a guest configuration.
func TestParseOptionString(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantValue string
		wantOpts  map[string]string
	}{
		{
			name:      "disque : valeur positionnelle puis options",
			in:        "local-lvm:vm-100-disk-0,size=20G,cache=writeback",
			wantValue: "local-lvm:vm-100-disk-0",
			wantOpts:  map[string]string{"size": "20G", "cache": "writeback"},
		},
		{
			// net0 n'a PAS de valeur positionnelle : le modèle de carte est
			// lui-même une clé. C'est l'asymétrie qu'aucun schéma ne documente.
			name:      "réseau : le premier élément est déjà une paire",
			in:        "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1",
			wantValue: "",
			wantOpts:  map[string]string{"virtio": "AA:BB:CC:DD:EE:FF", "bridge": "vmbr0", "firewall": "1"},
		},
		{
			name:      "valeur seule",
			in:        "local:iso/debian.iso",
			wantValue: "local:iso/debian.iso",
			wantOpts:  map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseOptionString(tc.in)
			if got.Value != tc.wantValue {
				t.Errorf("Value = %q, want %q", got.Value, tc.wantValue)
			}
			for k, want := range tc.wantOpts {
				if got.Get(k) != want {
					t.Errorf("Get(%q) = %q, want %q", k, got.Get(k), want)
				}
			}
			if len(got.Opts) != len(tc.wantOpts) {
				t.Errorf("Opts = %v, want %v", got.Opts, tc.wantOpts)
			}
		})
	}
}

// Round-tripping matters: PVX-026 changes one option and writes the whole
// value back. Losing an option there silently reconfigures a VM.
func TestOptionStringRoundTrip(t *testing.T) {
	const in = "local-lvm:vm-100-disk-0,cache=writeback,size=20G"

	if got := ParseOptionString(in).String(); got != in {
		t.Errorf("aller-retour = %q, want %q", got, in)
	}
}

func TestGuestTags(t *testing.T) {
	g := Guest{Tags: "managed;prod"}

	if !g.HasTag("managed") || !g.HasTag("PROD") {
		t.Errorf("HasTag ne trouve pas les tags de %q", g.Tags)
	}
	if g.HasTag("dev") {
		t.Error("HasTag trouve un tag absent")
	}
	if len(g.TagList()) != 2 {
		t.Errorf("TagList = %v", g.TagList())
	}
}

// A template is a VM with a flag, not another kind of object.
func TestTemplateIsAFlag(t *testing.T) {
	if !(Guest{Template: 1}).IsTemplate() {
		t.Error("template=1 doit être reconnu")
	}
	if (Guest{Status: "stopped"}).IsTemplate() {
		t.Error("une VM éteinte n'est pas un template")
	}
}

func TestStorageContentTypes(t *testing.T) {
	local := Storage{Storage: "local", Content: "iso,vztmpl,backup,import"}
	lvm := Storage{Storage: "local-lvm", Content: "images,rootdir"}

	if !local.Accepts("iso") || local.Accepts("images") {
		t.Errorf("local accepte %v", local.ContentTypes())
	}
	if !lvm.Accepts("images") || lvm.Accepts("iso") {
		t.Errorf("local-lvm accepte %v", lvm.ContentTypes())
	}
}

func TestGuestConfigHelpers(t *testing.T) {
	cfg := GuestConfig{
		"name":      "lab-app-01",
		"cores":     float64(2),
		"net0":      "virtio=AA:BB,bridge=vmbr0",
		"net1":      "virtio=CC:DD,bridge=vmbr1",
		"nettoyage": "piège",
		"scsi0":     "local-lvm:vm-100-disk-0",
	}

	if cfg.String("cores") != "2" {
		t.Errorf("String(cores) = %q, un entier JSON doit se lire en texte", cfg.String("cores"))
	}
	nets := cfg.KeysWithPrefix("net")
	if len(nets) != 2 || nets[0] != "net0" || nets[1] != "net1" {
		t.Errorf("KeysWithPrefix(net) = %v — « nettoyage » n'est pas une interface", nets)
	}
}
