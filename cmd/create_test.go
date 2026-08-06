package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dev-toolings/pvecli/internal/pve"
)

// The pool is not decoration: the node accepts VM.Allocate on /pool/{pool} as
// an alternative to /vms/{vmid}, so an identity scoped to a pool can only
// create by naming it. It has to reach the payload.
func TestCreatePayloadCarriesThePool(t *testing.T) {
	o := createOpts{cores: 2, memory: 2048, storage: "local-lvm", diskSize: "20G",
		bridge: "vmbr0", osType: "l26", pool: "collegue"}

	p, err := o.payload(210)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if got := p.Get("pool"); got != "collegue" {
		t.Errorf("pool = %q, want %q", got, "collegue")
	}
}

// Without --pool nothing is sent: an empty pool parameter is not the same as no
// pool, and PVE would reject the format.
func TestCreatePayloadOmitsAnEmptyPool(t *testing.T) {
	o := createOpts{cores: 2, memory: 2048, storage: "local-lvm", diskSize: "20G",
		bridge: "vmbr0", osType: "l26"}

	p, err := o.payload(210)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if _, present := p["pool"]; present {
		t.Error("pool envoyé alors qu'aucun n'a été demandé")
	}
}

// A 403 on creation reads as "you may not create VMs". It often means "you may,
// but only inside your pool, and you did not say which".
func TestForbiddenWithoutPoolPointsAtThePool(t *testing.T) {
	apiErr := &pve.APIError{Status: http.StatusForbidden, Method: "POST",
		Path: "/nodes/pve/qemu", Message: "Permission check failed"}

	got := explainCreateForbidden(apiErr, "")
	if !strings.Contains(got.Error(), "--pool") {
		t.Errorf("le refus doit orienter vers --pool, got: %v", got)
	}
	// The original error must survive: its own hint names the path refused.
	var unwrapped *pve.APIError
	if !errors.As(got, &unwrapped) {
		t.Error("l'erreur d'origine doit rester accessible")
	}

	// With a pool given, the advice would be wrong — the refusal is elsewhere.
	if got := explainCreateForbidden(apiErr, "collegue"); strings.Contains(got.Error(), "Aucun --pool") {
		t.Error("aucun conseil sur --pool quand --pool a été donné")
	}
	// And a non-403 must pass through untouched.
	other := &pve.APIError{Status: http.StatusBadRequest, Path: "/nodes/pve/qemu"}
	if got := explainCreateForbidden(other, ""); strings.Contains(got.Error(), "Aucun --pool") {
		t.Error("le conseil ne concerne que le 403")
	}
}
