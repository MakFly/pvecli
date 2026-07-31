//go:build integration

// Ces tests parlent à un VRAI nœud Proxmox. Ils sont derrière un tag de build
// parce que la CI n'a pas de nœud, et parce qu'un test qui crée des VM ne doit
// jamais partir par accident :
//
//	make test          les ignore
//	make integration   les lance, contre $PVE_API_URL
//
// La plage 900-999 leur est RÉSERVÉE (LAB.md). Rien d'autre ne doit y vivre, et
// ces tests ne touchent jamais rien en dehors — c'est la seule garantie qui
// permet de les lancer contre le lab sans relire le code à chaque fois.
package pve

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/MakFly/pvectl/internal/config"
)

// Les VMID de cette suite. Bornés haut et bas, et vérifiés : une faute de
// frappe qui viserait 210 détruirait la VM de Terraform.
const (
	integrationVMID = 990
	reservedLow     = 900
	reservedHigh    = 999
)

func init() {
	if integrationVMID < reservedLow || integrationVMID > reservedHigh {
		panic("les tests d'intégration ne doivent viser que la plage 900-999")
	}
}

// liveConfig résout la configuration exactement comme la CLI le fait :
// environnement par-dessus fichier. C'est ce qui rend ces tests utiles.
//
// La confiance TLS ne vit PAS dans l'environnement. Le certificat du lab est
// auto-signé, et l'empreinte épinglée par `pvectl config trust` est écrite dans
// le fichier de configuration — `~/.config/pvectl/env` ne l'exporte pas et n'a
// pas à le faire. Une suite qui ne lisait que `PVE_TLS_FINGERPRINT` montait donc
// un client en vérification système là où `pvectl doctor`, juste à côté,
// répondait quatre ✓ : le test échouait sur le certificat sans que rien du
// produit ne soit en cause. Résoudre par la même chaîne que la CLI supprime
// l'écart au lieu de le contourner par un `--insecure`.
func liveConfig(t *testing.T) *config.Effective {
	t.Helper()

	path, err := config.Path(os.Getenv("PVECTL_CONFIG"))
	if err != nil {
		t.Fatalf("chemin de configuration : %v", err)
	}
	// Un fichier absent n'est pas une panne : l'environnement seul suffit à
	// faire tourner la suite, il lui manquera juste l'empreinte épinglée.
	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("configuration %s : %v", path, err)
	}
	eff, err := config.Resolve(nil, f)
	if err != nil {
		t.Fatalf("résolution de la configuration : %v", err)
	}
	return eff
}

// liveClient monte un client sur le nœud réel, ou saute le test.
func liveClient(t *testing.T) (*Client, string) {
	t.Helper()

	eff := liveConfig(t)
	if eff.Endpoint == "" || eff.TokenID == "" || eff.TokenSecret == "" {
		t.Skip("endpoint / token_id / PVE_API_TOKEN_SECRET absents — source ~/.config/pvectl/env")
	}

	node := eff.Node
	if v := os.Getenv("PVE_NODE"); v != "" {
		node = v
	}
	if node == "" {
		node = "pve"
	}

	c, err := New(Options{
		Endpoint: eff.Endpoint,
		TokenID:  eff.TokenID,
		Secret:   eff.TokenSecret,
		Timeout:  60 * time.Second,
		Trust: TrustOptions{
			Fingerprint: eff.TLS.Fingerprint,
			CAFile:      eff.TLS.CAFile,
			Insecure:    eff.Insecure,
		},
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c, node
}

// waitTask suit une tâche jusqu'à son état terminal. Un 200 n'est qu'une
// acceptation — c'est la règle centrale du projet, et elle vaut aussi ici.
func waitTask(t *testing.T, c *Client, raw string) *Task {
	t.Helper()
	if raw == "" {
		return nil
	}
	upid, err := ParseUPID(raw)
	if err != nil {
		t.Fatalf("UPID illisible : %v", err)
	}

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		task, err := c.TaskStatus(context.Background(), upid.Node, upid.String())
		if err != nil {
			t.Fatalf("TaskStatus: %v", err)
		}
		if task.Status == "stopped" {
			if !task.Succeeded() {
				lines, _ := c.TaskLog(context.Background(), upid.Node, upid.String(), 20)
				for _, l := range lines {
					t.Log(l.Text)
				}
				t.Fatalf("tâche %s échouée : %s", upid.Type, task.ExitStatus)
			}
			return task
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("tâche %s toujours en cours après 5 minutes", upid)
	return nil
}

// Le nœud répond, et il répond ce qu'on croit.
func TestLiveVersionAndInventory(t *testing.T) {
	c, node := liveClient(t)
	ctx := context.Background()

	v, err := c.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v.Version == "" {
		t.Error("le nœud n'a pas rendu de version")
	}
	t.Logf("PVE %s (release %s)", v.Version, v.Release)

	stores, err := c.Storages(ctx, node)
	if err != nil {
		t.Fatalf("Storages: %v", err)
	}
	if len(stores) == 0 {
		t.Error("aucun stockage — le nœud est-il bien celui du lab ?")
	}
}

// Le cycle de vie complet, sur un VMID réservé : créer, démarrer, arrêter,
// détruire. C'est le test qui vérifie ce qu'aucune fixture ne peut vérifier —
// que la séquence tient contre le vrai ordonnanceur du nœud.
func TestLiveGuestLifecycle(t *testing.T) {
	c, node := liveClient(t)
	ctx := context.Background()

	// Un reste d'une exécution précédente est nettoyé plutôt que de faire
	// échouer la suite sur « VM already exists » à chaque fois.
	if st, err := c.GuestStatus(ctx, node, TypeQEMU, integrationVMID); err == nil {
		t.Logf("VM %d survivante (%s), on la retire d'abord", integrationVMID, st.Status)
		destroyIntegrationVM(t, c, node)
	}

	upid, err := c.CreateGuest(ctx, node, TypeQEMU, integrationVMID, url.Values{
		"vmid":   {strconv.Itoa(integrationVMID)},
		"name":   {"pvectl-integration"},
		"cores":  {"1"},
		"memory": {"512"},
		"tags":   {"pvectl;integration"},
	})
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	waitTask(t, c, upid)

	// La preuve est la relecture, jamais l'écho de la requête.
	st, err := c.GuestStatus(ctx, node, TypeQEMU, integrationVMID)
	if err != nil {
		t.Fatalf("la VM n'existe pas après création : %v", err)
	}
	if st.Status != "stopped" {
		t.Errorf("statut à la création = %q, want stopped", st.Status)
	}

	t.Cleanup(func() { destroyIntegrationVM(t, c, node) })

	// Une VM sans disque démarre quand même : elle tombera sur « no bootable
	// device », ce qui est exactement ce qu'on veut ici — l'ordonnanceur du
	// nœud a bien fait tourner un processus QEMU.
	upid, err = c.SetGuestStatus(ctx, node, TypeQEMU, integrationVMID, ActionStart, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTask(t, c, upid)

	if st, err = c.GuestStatus(ctx, node, TypeQEMU, integrationVMID); err != nil {
		t.Fatal(err)
	} else if st.Status != "running" {
		t.Errorf("statut après start = %q, want running", st.Status)
	}

	upid, err = c.SetGuestStatus(ctx, node, TypeQEMU, integrationVMID, ActionStop, nil)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitTask(t, c, upid)

	if st, err = c.GuestStatus(ctx, node, TypeQEMU, integrationVMID); err != nil {
		t.Fatal(err)
	} else if st.Status != "stopped" {
		t.Errorf("statut après stop = %q, want stopped", st.Status)
	}
}

func destroyIntegrationVM(t *testing.T, c *Client, node string) {
	t.Helper()
	if integrationVMID < reservedLow || integrationVMID > reservedHigh {
		t.Fatalf("refus de détruire %d : hors de la plage réservée", integrationVMID)
	}

	ctx := context.Background()
	if st, err := c.GuestStatus(ctx, node, TypeQEMU, integrationVMID); err == nil && st.Status == "running" {
		if upid, err := c.SetGuestStatus(ctx, node, TypeQEMU, integrationVMID, ActionStop, nil); err == nil {
			waitTask(t, c, upid)
		}
	}

	upid, err := c.DeleteGuest(ctx, node, TypeQEMU, integrationVMID, DeleteOptions{Purge: true})
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == 500 {
			return // déjà partie
		}
		t.Errorf("DeleteGuest: %v", err)
		return
	}
	waitTask(t, c, upid)

	if _, err := c.GuestStatus(ctx, node, TypeQEMU, integrationVMID); err == nil {
		t.Errorf("la VM %d existe encore après destruction", integrationVMID)
	}
}

// Le 403 est une réponse, pas une panne : la CLI doit savoir le nommer. Ce test
// le provoque pour de vrai, avec un secret faux.
func TestLiveAuthFailureIsDiagnosed(t *testing.T) {
	_, _ = liveClient(t) // valide la présence de l'environnement, puis on le casse
	eff := liveConfig(t)

	// Seul le secret est faussé. La confiance TLS reste celle de la CLI : un 401
	// obtenu en désactivant la vérification ne prouverait pas que le nœud refuse
	// le token, seulement qu'on a cessé de vérifier à qui on parlait.
	c, err := New(Options{
		Endpoint: eff.Endpoint,
		TokenID:  eff.TokenID,
		Secret:   "ce-secret-est-faux",
		Timeout:  15 * time.Second,
		Trust: TrustOptions{
			Fingerprint: eff.TLS.Fingerprint,
			CAFile:      eff.TLS.CAFile,
			Insecure:    eff.Insecure,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.Nodes(context.Background())
	if err == nil {
		t.Fatal("un secret faux doit être refusé par le nœud")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Errorf("erreur = %v — attendu un 401 typé, pas un échec générique", err)
	}
}
