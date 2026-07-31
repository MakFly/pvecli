package iac

import (
	"strings"
	"testing"
)

// A real two-host recap, as ansible-playbook 2.21 prints it.
const recapOutput = `
TASK [Publish a native static application] *************************************
changed: [lab-app-01]

PLAY RECAP *********************************************************************
lab-app-01                 : ok=7    changed=6    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0
web2                       : ok=3    changed=0    unreachable=1    failed=0    skipped=0    rescued=0    ignored=0
`

func TestParseRecapReadsThePlayRecap(t *testing.T) {
	recaps := ParseRecap(recapOutput)

	if len(recaps) != 2 {
		t.Fatalf("deux hôtes attendus, reçu %d : %+v", len(recaps), recaps)
	}
	if r := recaps[0]; r.Host != "lab-app-01" || r.OK != 7 || r.Changed != 6 {
		t.Errorf("premier hôte mal lu : %+v", r)
	}
	if r := recaps[1]; r.Host != "web2" || r.Unreachable != 1 {
		t.Errorf("second hôte mal lu : %+v", r)
	}

	if got := TotalChanged(recaps); got != 6 {
		t.Errorf("TotalChanged = %d, attendu 6", got)
	}
	if got := UnreachableHosts(recaps); len(got) != 1 || got[0] != "web2" {
		t.Errorf("UnreachableHosts = %v", got)
	}
}

// A task's own output can perfectly well contain "changed=". Only what follows
// the PLAY RECAP banner is the summary.
func TestOutputBeforeTheBannerIsNotTheSummary(t *testing.T) {
	out := `
ok: [host] => {"msg": "the api returned changed=99"}
imposteur                  : ok=1    changed=99   unreachable=0    failed=0

PLAY RECAP *********************************************************************
lab-app-01                 : ok=7    changed=0    unreachable=0    failed=0
`
	recaps := ParseRecap(out)

	if len(recaps) != 1 || recaps[0].Host != "lab-app-01" {
		t.Fatalf("seule la ligne du recap compte : %+v", recaps)
	}
	if TotalChanged(recaps) != 0 {
		t.Errorf("le faux « changed=99 » a été compté : %+v", recaps)
	}
}

// The measurement that decides idempotence, and the reason it exists: a
// playbook that redoes everything on every run still exits 0. Only changed=0 on
// the second pass tells the two apart.
func TestIdempotenceIsAMeasurementNotAnExitCode(t *testing.T) {
	second := ParseRecap(`
PLAY RECAP *********************************************************************
lab-app-01                 : ok=12   changed=1    unreachable=0    failed=0
`)

	if TotalChanged(second) == 0 {
		t.Fatal("ce second passage a changé quelque chose, il n'est pas idempotent")
	}
	if got := ChangedHosts(second); len(got) != 1 || !strings.Contains(got[0], "lab-app-01") {
		t.Errorf("l'échec doit nommer l'hôte fautif : %v", got)
	}
}

// An AD-HOC run prints no PLAY RECAP at all — that banner belongs to
// ansible-playbook. The first version of the ping pre-check fed this output to
// ParseRecap, got an empty slice, and announced « ✓ 0 hôte(s) répondent »:
// zero failures read as success.
func TestAdHocPingHasItsOwnFormat(t *testing.T) {
	out := `lab-app-01 | SUCCESS => {
    "changed": false,
    "ping": "pong"
}
web2 | UNREACHABLE! => {
    "changed": false,
    "msg": "Failed to connect to the host via ssh"
}`

	if got := ParseRecap(out); len(got) != 0 {
		t.Errorf("un run ad-hoc n'a pas de PLAY RECAP : %+v", got)
	}

	pings := ParsePing(out)
	if len(pings) != 2 {
		t.Fatalf("deux hôtes attendus : %+v", pings)
	}
	if !pings[0].OK() || pings[0].Host != "lab-app-01" {
		t.Errorf("l'hôte joignable est mal lu : %+v", pings[0])
	}
	if pings[1].OK() || pings[1].Result != "UNREACHABLE" {
		t.Errorf("l'hôte injoignable doit être détecté : %+v", pings[1])
	}
}
