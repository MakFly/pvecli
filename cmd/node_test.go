package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dev-toolings/pvecli/internal/pve"
	"github.com/dev-toolings/pvecli/internal/testutil"
)

// Le plan d'un redémarrage de nœud doit dire ce qu'il coupe. C'est la commande
// au rayon de souffle le plus large de la CLI : elle arrête TOUS les invités à
// la fois, et son plan est le seul endroit où l'opérateur le lit avant de
// confirmer. Un plan qui dirait seulement « redémarre pve » serait exact et
// trompeur.
func TestNodeRebootPlanNamesWhatItTakesDown(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/status": "node-status.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "node", "reboot", "pve", "--dry-run")
	if err != nil {
		t.Fatalf("--dry-run ne doit rien écrire ni échouer : %v", err)
	}

	for _, want := range []string{
		"POST", "/nodes/pve/status", // l'endpoint réel, pas une paraphrase
		"command", "reboot", // le payload résolu
		"TOUS ses invités", // la conséquence, en toutes lettres
		"onboot=1",         // ce qui décide de ce qui revient
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("le plan ne contient pas %q :\n%s", want, stderr)
		}
	}
}

// Un redémarrage ne se défait pas. Le champ « retour » du plan doit le dire
// plutôt que de proposer une commande inverse qui n'existe pas — c'est le seul
// cas de la CLI où l'honnêteté du plan consiste à annoncer l'absence de retour.
func TestNodeRebootAdvertisesNoRollback(t *testing.T) {
	srv := testutil.New(t, "../testdata", map[string]string{
		"GET /api2/json/nodes/pve/status": "node-status.json",
	})
	point(t, srv.URL)

	_, stderr, err := run(t, "node", "reboot", "pve", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "aucun") {
		t.Errorf("le plan doit annoncer qu'il n'y a pas de retour arrière :\n%s", stderr)
	}
	if strings.Contains(stderr, "pvecli node start") {
		t.Errorf("le plan propose une commande de retour qui n'existe pas :\n%s", stderr)
	}
}

// LA garantie de « node reboot » : la preuve du redémarrage est un uptime qui
// REDESCEND, pas un nœud qui répond. Le nœud continue de répondre plusieurs
// secondes après avoir accepté la commande, pendant que systemd descend ses
// unités — une sonde qui s'arrête au premier GET réussi annonce « revenu » à
// propos d'une machine qui n'est même pas encore partie.
func TestNodeReturnProbeRejectsANodeThatNeverWentDown(t *testing.T) {
	const before = int64(240_000)

	calls := 0
	p := nodeReturnProbe{
		interval: time.Millisecond,
		timeout:  50 * time.Millisecond,
		status: func(context.Context) (*pve.NodeStatus, error) {
			calls++
			// Le nœud répond, et son uptime MONTE : il n'a jamais redémarré.
			return &pve.NodeStatus{Uptime: before + int64(calls)}, nil
		},
	}

	if _, err := p.wait(context.Background(), "pve", before); err == nil {
		t.Fatal("un nœud qui répond sans jamais redémarrer doit finir en délai dépassé, pas en succès")
	}
	if calls < 2 {
		t.Errorf("la sonde n'a interrogé le nœud que %d fois : elle ne boucle pas", calls)
	}
}

// Le chemin nominal : injoignable (le nœud est parti), puis de retour avec un
// uptime plus bas. L'erreur intermédiaire est le cours normal des choses et ne
// doit pas interrompre l'attente.
func TestNodeReturnProbeAcceptsALowerUptimeAfterAnOutage(t *testing.T) {
	const before = int64(240_000)

	step := 0
	p := nodeReturnProbe{
		interval: time.Millisecond,
		timeout:  2 * time.Second,
		status: func(context.Context) (*pve.NodeStatus, error) {
			step++
			switch {
			case step == 1:
				return &pve.NodeStatus{Uptime: before + 3}, nil // pas encore parti
			case step < 4:
				return nil, errors.New("connection refused") // en train de redémarrer
			default:
				return &pve.NodeStatus{Uptime: 42, KVersion: "7.0.14-8-pve"}, nil
			}
		},
	}

	st, err := p.wait(context.Background(), "pve", before)
	if err != nil {
		t.Fatalf("le retour du nœud doit être accepté : %v", err)
	}
	if st.Uptime != 42 {
		t.Errorf("uptime = %d, want 42", st.Uptime)
	}
}
