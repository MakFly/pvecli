package iac

import (
	"strings"
	"testing"
)

func TestLoadDeclarationTreatsAMissingFileAsEmptyLXCs(t *testing.T) {
	d, err := LoadDeclaration(t.TempDir())
	if err != nil {
		t.Fatalf("LoadDeclaration: %v", err)
	}
	if len(d.LXCs) != 0 {
		t.Errorf("LXCs = %v, attendu vide", d.LXCs)
	}
}

func TestSaveThenLoadRoundTripsLXCsAlongsideVMs(t *testing.T) {
	dir := t.TempDir()
	vm := VM{VMID: 220, Cores: 2, Memory: 8192, Tags: []string{"managed", "pvecli"}}
	ct := LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200, Unprivileged: true,
		Services: []string{"docker"}, Tags: []string{"managed", "pvecli", "svc_docker"}}

	d := &Declaration{VMs: map[string]VM{"app-01": vm}, LXCs: map[string]LXC{"ct-01": ct}}
	if err := d.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := LoadDeclaration(dir)
	if err != nil {
		t.Fatalf("LoadDeclaration: %v", err)
	}
	if _, ok := back.VMs["app-01"]; !ok {
		t.Error("app-01 absente après relecture")
	}
	got, ok := back.LXCs["ct-01"]
	if !ok {
		t.Fatal("ct-01 absent après relecture")
	}
	if got.Template != ct.Template || got.Memory != ct.Memory || !got.Unprivileged {
		t.Errorf("relecture = %+v, écrit = %+v", got, ct)
	}
}

// A bool is never dropped by omitempty: an explicit "unprivileged": false must
// round-trip as false, not vanish and let Terraform's optional(bool, true)
// default silently turn a privileged container back into an unprivileged one.
func TestUnprivilegedFalseSurvivesRender(t *testing.T) {
	d := &Declaration{LXCs: map[string]LXC{"ct-01": {VMID: 221, Cores: 1, Memory: 2048, Template: 200, Unprivileged: false}}}
	raw, err := d.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"unprivileged": false`) {
		t.Errorf("« unprivileged: false » doit être écrit explicitement, rendu :\n%s", raw)
	}
}

func TestValidateLXC(t *testing.T) {
	base := LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200}

	cases := []struct {
		name string
		ct   LXC
		id   string
		want string
	}{
		{"correcte", base, "ct-01", ""},
		{"le piège des 8 Mio", LXC{VMID: 221, Cores: 1, Memory: 8, Template: 200}, "ct-01", "8192"},
		{"vmid réservé", LXC{VMID: 42, Cores: 1, Memory: 2048, Template: 200}, "ct-01", "vmid"},
		{"zéro cœur", LXC{VMID: 221, Cores: 0, Memory: 2048, Template: 200}, "ct-01", "cores"},
		{"template manquant", LXC{VMID: 221, Cores: 1, Memory: 2048}, "ct-01", "template"},
		{"nom en majuscules", base, "CT01", "invalide"},
		{"ip sans préfixe", LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200, IP: "192.168.1.221"}, "ct-01", "préfixe"},
		{"passerelle en dhcp", LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200, IP: "dhcp", Gateway: "192.168.1.1"}, "ct-01", "passerelle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ct.Validate(tc.id)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refusée à tort : %v", err)
			case tc.want != "" && err == nil:
				t.Errorf("acceptée à tort, attendu un message contenant %q", tc.want)
			case tc.want != "" && err != nil && !strings.Contains(err.Error(), tc.want):
				t.Errorf("message = %q, attendu contenant %q", err, tc.want)
			}
		})
	}
}

func TestDiffLXC(t *testing.T) {
	before := &LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200}
	after := &LXC{VMID: 221, Cores: 1, Memory: 4096, Template: 200}

	got := DiffLXC(before, after)
	if len(got) != 1 {
		t.Fatalf("DiffLXC = %+v, un seul champ change", got)
	}
	if got[0].Field != "memory" || got[0].Before != "2048 Mio" || got[0].After != "4096 Mio" {
		t.Errorf("DiffLXC = %+v", got[0])
	}
}

// A privileged→unprivileged flip must show up: it is a real security-relevant
// change, and "false" is never mistaken for the field's absence.
func TestDiffLXCReportsUnprivilegedFlip(t *testing.T) {
	before := &LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200, Unprivileged: true}
	after := &LXC{VMID: 221, Cores: 1, Memory: 2048, Template: 200, Unprivileged: false}

	got := DiffLXC(before, after)
	if len(got) != 1 || got[0].Field != "unprivileged" || got[0].Before != "true" || got[0].After != "false" {
		t.Errorf("DiffLXC = %+v", got)
	}
}
