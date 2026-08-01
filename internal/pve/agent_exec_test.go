package pve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exécuter dans l'invité par l'hyperviseur, c'est le canal qui reste quand SSH
// n'est pas une option : ni compte, ni clé, ni port, ni réseau de la VM.

func TestAgentExecEnvoieUnArgumentParElementEtRendLaSortie(t *testing.T) {
	var gotCommands []string
	var gotPID string
	polls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/agent/exec"):
			_ = r.ParseForm()
			gotCommands = r.PostForm["command"]
			_, _ = w.Write([]byte(`{"data":{"pid":4242}}`))
		case strings.HasSuffix(r.URL.Path, "/agent/exec-status"):
			gotPID = r.URL.Query().Get("pid")
			polls++
			if polls < 2 {
				// Pas encore fini : c'est le cas normal d'un build long.
				_, _ = w.Write([]byte(`{"data":{"exited":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"exited":1,"exitcode":0,"out-data":"app-01\n"}}`))
		}
	}))
	defer srv.Close()

	c, err := New(Options{Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s",
		Transport: srv.Client().Transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	res, err := c.AgentExec(context.Background(), "pve", 210,
		[]string{"/bin/sh", "-c", "hostname"}, time.Millisecond)
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}

	// Un argument par paramètre « command ». Une seule chaîne serait comprise
	// comme un exécutable dont le nom contient des espaces.
	want := []string{"/bin/sh", "-c", "hostname"}
	if len(gotCommands) != len(want) {
		t.Fatalf("command = %q, attendu %q", gotCommands, want)
	}
	for i := range want {
		if gotCommands[i] != want[i] {
			t.Errorf("command[%d] = %q, attendu %q", i, gotCommands[i], want[i])
		}
	}
	if gotPID != "4242" {
		t.Errorf("pid interrogé = %q, attendu celui rendu par exec", gotPID)
	}
	if polls < 2 {
		t.Error("la fin n'a pas été attendue : un build long serait rendu inachevé")
	}
	if res.OutData != "app-01\n" || res.ExitCode != 0 {
		t.Errorf("résultat = %+v", res)
	}
}

func TestAgentExecRemonteLeCodeDeRetourDeLInvite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/agent/exec") {
			_, _ = w.Write([]byte(`{"data":{"pid":7}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"exited":1,"exitcode":137,"err-data":"Killed\n"}}`))
	}))
	defer srv.Close()

	c, _ := New(Options{Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s",
		Transport: srv.Client().Transport})

	res, err := c.AgentExec(context.Background(), "pve", 210, []string{"true"}, time.Millisecond)
	if err != nil {
		t.Fatalf("AgentExec: %v", err)
	}
	// Un échec DANS la VM n'est pas une erreur de transport : c'est un résultat,
	// et c'est à l'appelant d'en faire un code de sortie.
	if res.ExitCode != 137 || res.ErrData != "Killed\n" {
		t.Errorf("résultat = %+v, le code de l'invité doit remonter tel quel", res)
	}
}

func TestAgentExecRefuseUneCommandeVide(t *testing.T) {
	c, _ := New(Options{Endpoint: "https://pve.example:8006", TokenID: "a@pve!t", Secret: "s"})
	if _, err := c.AgentExec(context.Background(), "pve", 210, nil, time.Second); err == nil {
		t.Fatal("attendu un refus avant tout appel réseau")
	}
}

func TestAgentExecDitQueLaCommandeTourneEncoreQuandOnRenonce(t *testing.T) {
	// Le délai est celui du CLIENT. Le processus, lui, continue dans l'invité :
	// laisser croire qu'il est mort ferait relancer un build déjà en cours.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/agent/exec") {
			_, _ = w.Write([]byte(`{"data":{"pid":99}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"exited":0}}`)) // jamais fini
	}))
	defer srv.Close()

	c, _ := New(Options{Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s",
		Transport: srv.Client().Transport})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	_, err := c.AgentExec(ctx, "pve", 210, []string{"sleep", "1000"}, 10*time.Millisecond)
	if err == nil {
		t.Fatal("attendu une erreur au dépassement du délai")
	}
	if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), "toujours en cours") {
		t.Errorf("erreur = %v — elle doit nommer le PID et dire que ça tourne encore", err)
	}
}
