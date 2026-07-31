package pve

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPoolsDecodesRealAnswer(t *testing.T) {
	c := serveFixture(t, "../../testdata/pools.json")

	pools, err := c.Pools(context.Background())
	if err != nil {
		t.Fatalf("Pools: %v", err)
	}
	if len(pools) == 0 {
		t.Fatal("aucun pool décodé")
	}
	if pools[0].PoolID == "" {
		t.Errorf("poolid vide dans %+v", pools[0])
	}
}

// GET /pools?poolid=… answers an ARRAY of one element, not an object: the
// handler pushes its single entry onto the same list the index builds. A
// client that decodes an object here fails with a type error that reads like a
// broken endpoint.
func TestPoolReadsAnArrayOfOne(t *testing.T) {
	c := serveFixture(t, "../../testdata/pool-members.json")

	pool, err := c.Pool(context.Background(), "lab")
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if pool.PoolID != "lab" {
		t.Errorf("PoolID = %q, want lab", pool.PoolID)
	}
	if len(pool.Members) < 2 {
		t.Fatalf("%d membre(s), la capture en porte deux", len(pool.Members))
	}

	var lxc, qemu *PoolMember
	for i := range pool.Members {
		switch pool.Members[i].Type {
		case "lxc":
			lxc = &pool.Members[i]
		case "qemu":
			qemu = &pool.Members[i]
		}
	}
	if lxc == nil || qemu == nil {
		t.Fatalf("le pool doit porter un lxc ET un qemu, membres = %+v", pool.Members)
	}
	// Un pool mélange les types : c'est un chemin d'ACL, pas un dossier de VM.
	if lxc.VMID == 0 || qemu.VMID == 0 || lxc.Node == "" {
		t.Errorf("vmid/node non décodés : lxc = %+v, qemu = %+v", lxc, qemu)
	}
	if qemu.Label() == "" || lxc.Label() == "" {
		t.Errorf("Label() vide")
	}
}

func TestPoolReportsAnAbsentPool(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	if _, err := c.Pool(context.Background(), "fantome"); err == nil {
		t.Error("un pool absent doit être une erreur, pas un pool vide")
	}
}

// The pool id travels as a PARAMETER, never as a path segment. PVE 9 marks
// /pools/{poolid} deprecated precisely because a nested pool is written
// "parent/enfant" — which a path segment cannot carry.
func TestPoolMutationsCarryPoolIDAsAParameter(t *testing.T) {
	cases := []struct {
		name       string
		call       func(*Client) error
		wantMethod string
		wantQuery  bool
	}{
		{
			name:       "create",
			call:       func(c *Client) error { return c.CreatePool(context.Background(), "lab", "hop") },
			wantMethod: http.MethodPost,
		},
		{
			name: "update",
			call: func(c *Client) error {
				return c.UpdatePool(context.Background(), PoolChange{PoolID: "lab", VMs: []int{120}})
			},
			wantMethod: http.MethodPut,
		},
		{
			name:       "delete",
			call:       func(c *Client) error { return c.DeletePool(context.Background(), "lab") },
			wantMethod: http.MethodDelete,
			// A DELETE carrying a form body earns a 501 from PVE's own HTTP
			// server, before the schema layer (PVX-031): its parameters must
			// travel in the query string.
			wantQuery: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var method, path, query, body string
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				method, path, query = r.Method, r.URL.Path, r.URL.RawQuery
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":null}`))
			})

			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if method != tc.wantMethod {
				t.Errorf("méthode = %s, want %s", method, tc.wantMethod)
			}
			if path != "/api2/json/pools" {
				t.Errorf("chemin = %q — le poolid ne doit pas être un segment", path)
			}
			if tc.wantQuery {
				if query != "poolid=lab" {
					t.Errorf("query = %q, want poolid=lab", query)
				}
				if body != "" {
					t.Errorf("un DELETE ne doit pas porter de corps, got %q", body)
				}
			} else if !strings.Contains(body, "poolid=lab") {
				t.Errorf("corps = %q, il doit porter poolid", body)
			}
		})
	}
}

func TestPoolChangeValues(t *testing.T) {
	v := PoolChange{
		PoolID: "lab", VMs: []int{210, 120}, Storage: []string{"local"},
		Delete: true, AllowMove: true,
	}.Values()

	if got := v.Get("vms"); got != "210,120" {
		t.Errorf("vms = %q — l'API attend une liste séparée par des virgules", got)
	}
	if v.Get("storage") != "local" || v.Get("delete") != "1" || v.Get("allow-move") != "1" {
		t.Errorf("payload = %v", v)
	}
	// allow-move, with a hyphen: PVE names it that way and a copy from memory
	// writes allow_move, which the schema layer rejects with a message about
	// « unknown parameter ».
	if v.Has("allow_move") {
		t.Error("le paramètre s'écrit allow-move, pas allow_move")
	}
}
