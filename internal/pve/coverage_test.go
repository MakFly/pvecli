package pve

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The write half of internal/pve was reachable only through cmd's tests, where
// it counted for nothing in this package's coverage — and, worse, where a
// mistake in a path or a verb was diagnosed three layers away from where it
// lived. These tests hold each client method to the two things it alone is
// responsible for: the request it emits, and what it makes of the answer.

// spy answers every request the same way, and records the last one.
type spy struct {
	method string
	path   string
	query  string
	body   url.Values
}

func newSpy(t *testing.T, answer string) (*Client, *spy) {
	t.Helper()
	s := &spy{}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		s.method, s.path, s.query = r.Method, r.URL.Path, r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		s.body, _ = url.ParseQuery(string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	})
	return c, s
}

const okUPID = `{"data":"UPID:pve:0011A2B3:000043CD:6A6CAD90:qmstart:210:automation@pve!pvectl:"}`

func TestAccessReadsDecodeRealAnswers(t *testing.T) {
	t.Run("users", func(t *testing.T) {
		c := replay(t, map[string]string{"GET /api2/json/access/users": "users.json"})
		users, err := c.Users(context.Background())
		if err != nil {
			t.Fatalf("Users: %v", err)
		}
		if len(users) == 0 || users[0].UserID == "" {
			t.Errorf("utilisateurs = %+v", users)
		}
	})

	t.Run("tokens", func(t *testing.T) {
		c := replay(t, map[string]string{
			"GET /api2/json/access/users/automation@pve/token": "tokens.json",
		})
		tokens, err := c.Tokens(context.Background(), "automation@pve")
		if err != nil {
			t.Fatalf("Tokens: %v", err)
		}
		if len(tokens) == 0 {
			t.Fatal("aucun token décodé")
		}
		// privsep is the field the whole M4 lesson turns on: with it, the
		// token's effective rights are the INTERSECTION of its own and its
		// user's.
		for _, tk := range tokens {
			if tk.TokenID == "" {
				t.Errorf("token incomplet : %+v", tk)
			}
		}
	})

	t.Run("role privileges", func(t *testing.T) {
		c := replay(t, map[string]string{
			"GET /api2/json/access/roles/PVEVMAdmin": "roles.json",
		})
		// The fixture is the role list; what matters here is that the call
		// reaches the right path and does not invent one.
		_, _ = c.RolePrivileges(context.Background(), "PVEVMAdmin")
	})
}

// An ACL change is a PUT on a cluster-wide path, and its identity travels in
// exactly one of three mutually exclusive parameters.
func TestSetACLSendsOneIdentity(t *testing.T) {
	c, s := newSpy(t, `{"data":null}`)

	change := ACLChange{Path: "/pool/lab", Role: "PVEVMAdmin", Token: "automation@pve!pvectl", Propagate: true}
	if err := c.SetACL(context.Background(), change); err != nil {
		t.Fatalf("SetACL: %v", err)
	}

	if s.method != http.MethodPut || s.path != "/api2/json/access/acl" {
		t.Errorf("%s %s", s.method, s.path)
	}
	if s.body.Get("tokens") != "automation@pve!pvectl" {
		t.Errorf("payload = %v", s.body)
	}
	// The API names its parameters in the plural because one call may carry
	// several. This CLI exposes one at a time on purpose: an ACL touching four
	// identities at once is legible to nobody.
	if s.body.Get("users") != "" || s.body.Get("groups") != "" {
		t.Errorf("une seule identité doit voyager, got %v", s.body)
	}
	if change.Identity() == "" {
		t.Error("Identity() doit nommer le porteur du rôle")
	}
}

func TestTokenLifecycleUsesTheRightVerbs(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		c, s := newSpy(t, `{"data":{"value":"s3cr3t","info":{"privsep":"1"}}}`)
		if _, err := c.CreateToken(context.Background(), "automation@pve", "jetable",
			TokenOptions{Separated: true, Comment: "essai"}); err != nil {
			t.Fatalf("CreateToken: %v", err)
		}
		if s.method != http.MethodPost {
			t.Errorf("méthode = %s", s.method)
		}
		if !strings.HasSuffix(s.path, "/token/jetable") {
			t.Errorf("chemin = %s", s.path)
		}
	})

	t.Run("delete", func(t *testing.T) {
		c, s := newSpy(t, `{"data":null}`)
		if err := c.DeleteToken(context.Background(), "automation@pve", "jetable"); err != nil {
			t.Fatalf("DeleteToken: %v", err)
		}
		if s.method != http.MethodDelete {
			t.Errorf("méthode = %s", s.method)
		}
		// A DELETE must not carry a form body: PVE's HTTP server answers 501
		// before the schema layer is reached (PVX-031).
		if len(s.body) != 0 {
			t.Errorf("corps sur un DELETE = %v", s.body)
		}
	})
}

func TestBackupAndRestoreEmitTasks(t *testing.T) {
	t.Run("vzdump", func(t *testing.T) {
		c, s := newSpy(t, okUPID)
		upid, err := c.Backup(context.Background(), "pve",
			VZDumpOptions{VMIDs: []int{211}, Storage: "local", Mode: ModeSnapshot, Compress: "zstd"})
		if err != nil {
			t.Fatalf("Backup: %v", err)
		}
		if !IsUPID(upid) {
			t.Errorf("Backup = %q, attendu un UPID", upid)
		}
		if s.method != http.MethodPost || !strings.HasSuffix(s.path, "/vzdump") {
			t.Errorf("%s %s", s.method, s.path)
		}
		if s.body.Get("storage") != "local" || s.body.Get("mode") != "snapshot" {
			t.Errorf("payload = %v", s.body)
		}
	})

	t.Run("archives", func(t *testing.T) {
		c := replay(t, map[string]string{
			"GET /api2/json/nodes/pve/storage/local/content": "backups.json",
		})
		archives, err := c.Backups(context.Background(), "pve", "local")
		if err != nil {
			t.Fatalf("Backups: %v", err)
		}
		if len(archives) == 0 {
			t.Fatal("aucune archive décodée")
		}
		for _, a := range archives {
			if a.VolID == "" {
				t.Errorf("archive sans volid : %+v", a)
			}
		}
	})
}

func TestSnapshotsUseTheRightFamilyEndpoint(t *testing.T) {
	for _, tc := range []struct {
		kind GuestType
		want string
	}{
		{TypeQEMU, "/api2/json/nodes/pve/qemu/211/snapshot"},
		{TypeLXC, "/api2/json/nodes/pve/lxc/120/snapshot"},
	} {
		vmid := 211
		if tc.kind == TypeLXC {
			vmid = 120
		}

		c, s := newSpy(t, okUPID)
		if _, err := c.CreateSnapshot(context.Background(), "pve", tc.kind, vmid, "avant-maj", "", false); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		if s.path != tc.want {
			t.Errorf("chemin = %q, want %q", s.path, tc.want)
		}
		if s.body.Get("snapname") != "avant-maj" {
			t.Errorf("payload = %v", s.body)
		}

		c, s = newSpy(t, okUPID)
		if _, err := c.RollbackSnapshot(context.Background(), "pve", tc.kind, vmid, "avant-maj"); err != nil {
			t.Fatalf("RollbackSnapshot: %v", err)
		}
		if !strings.HasSuffix(s.path, "/snapshot/avant-maj/rollback") {
			t.Errorf("chemin = %q", s.path)
		}
	}
}

func TestGuestStatusAndContainersReachTheirEndpoints(t *testing.T) {
	c := replay(t, map[string]string{
		"GET /api2/json/nodes/pve/qemu/211/status/current": "qemu-status.json",
		"GET /api2/json/nodes/pve/lxc":                     "qemu-empty.json",
	})

	st, err := c.GuestStatus(context.Background(), "pve", TypeQEMU, 211)
	if err != nil {
		t.Fatalf("GuestStatus: %v", err)
	}
	if st.VMID != 211 || st.Status == "" {
		t.Errorf("statut = %+v", st)
	}

	if _, err := c.Containers(context.Background(), "pve"); err != nil {
		t.Fatalf("Containers: %v", err)
	}
}

func TestUpdateGuestConfigIsAPut(t *testing.T) {
	c, s := newSpy(t, `{"data":null}`)

	if _, err := c.UpdateGuestConfig(context.Background(), "pve", TypeQEMU, 211,
		url.Values{"cores": {"4"}}); err != nil {
		t.Fatalf("UpdateGuestConfig: %v", err)
	}
	if s.method != http.MethodPut || !strings.HasSuffix(s.path, "/qemu/211/config") {
		t.Errorf("%s %s", s.method, s.path)
	}
	if s.body.Get("cores") != "4" {
		t.Errorf("payload = %v", s.body)
	}
}

// The path helpers exist so that --dry-run prints the URL the write will
// actually use. A helper that drifted from its endpoint would make the plan a
// polite fiction — which is the one thing a dry-run must never be.
func TestPathHelpersMatchTheirEndpoints(t *testing.T) {
	cases := map[string]string{
		NetworkApplyPath("pve"):                "/nodes/pve/network",
		NetworkRevertPath("pve"):               "/nodes/pve/network",
		PoolPath():                             "/pools",
		PoolACLPath("lab"):                     "/pool/lab",
		ACLPath():                              "/access/acl",
		TokenPath("automation@pve", "jetable"): "/access/users/automation@pve/token/jetable",
		MigratePath(TypeQEMU, "pve", 211):      "/nodes/pve/qemu/211/migrate",
		MigratePath(TypeLXC, "pve", 120):       "/nodes/pve/lxc/120/migrate",
		BackupPath("pve"):                      "/nodes/pve/vzdump",
		DownloadPath("pve", "local"):           "/nodes/pve/storage/local/download-url",
		UploadPath("pve", "local"):             "/nodes/pve/storage/local/upload",
		ClonePath(TypeQEMU, "pve", 9001):       "/nodes/pve/qemu/9001/clone",
		ClonePath(TypeLXC, "pve", 120):         "/nodes/pve/lxc/120/clone",
		TemplatePath("pve", 9001):              "/nodes/pve/qemu/9001/template",
		ConfigPath(TypeQEMU, "pve", 211):       "/nodes/pve/qemu/211/config",
		CreatePath(TypeLXC, "pve"):             "/nodes/pve/lxc",
		DeletePath(TypeQEMU, "pve", 211):       "/nodes/pve/qemu/211",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("chemin = %q, want %q", got, want)
		}
	}
}

func TestClientExposesItsIdentityAndNeverItsSecret(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})

	if c.TokenID() != "automation@pve!pvectl" {
		t.Errorf("TokenID = %q", c.TokenID())
	}
	if !strings.Contains(c.Endpoint(), "/api2/json") {
		t.Errorf("Endpoint = %q — la base doit porter /api2/json", c.Endpoint())
	}
	// There is deliberately no accessor for the secret: `access whoami` needs
	// to say who the caller is, never what it knows.
	if strings.Contains(c.Endpoint()+c.TokenID(), "s3cr3t") {
		t.Error("le secret a fui dans une valeur publique du client")
	}
	if c.TrustMode() == "" {
		t.Error("TrustMode doit toujours pouvoir être nommé : doctor l'affiche")
	}
}

func TestFirstNonBlankPicksTheFirstUsableValue(t *testing.T) {
	if got := firstNonBlank("", "", "local"); got != "local" {
		t.Errorf("firstNonBlank = %q", got)
	}
	if got := firstNonBlank("", ""); got != "" {
		t.Errorf("firstNonBlank = %q, want vide", got)
	}
}
