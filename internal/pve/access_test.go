package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MakFly/pvectl/internal/testutil"
)

func accessClient(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := testutil.New(t, "../../testdata", routes)
	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A role is a bundle of privileges, and PVE describes that bundle two different
// ways depending on which endpoint you ask. Decoding one shape where the other
// arrives is a decoding error, not a 404 — the confusing kind.
func TestRoleHasTwoShapes(t *testing.T) {
	c := accessClient(t, map[string]string{
		"GET /api2/json/access/roles":            "roles.json",
		"GET /api2/json/access/roles/PVEVMAdmin": "roles.json",
	})
	ctx := context.Background()

	roles, err := c.Roles(ctx)
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}
	var vmadmin Role
	for _, r := range roles {
		if r.RoleID == "PVEVMAdmin" {
			vmadmin = r
		}
	}
	// The index gives a comma-separated string.
	if len(vmadmin.Privileges()) < 2 {
		t.Errorf("l'index rend « privs » en chaîne à virgules: %+v", vmadmin)
	}
	if !vmadmin.IsBuiltin() {
		t.Error("PVEVMAdmin est un rôle intégré")
	}
}

// The privsep bit is the one that explains most inexplicable 403s, so it has to
// survive both JSON types PVE uses for it.
func TestTokenPrivsepDecodesFromBothTypes(t *testing.T) {
	var asNumber Token
	if err := json.Unmarshal([]byte(`{"tokenid":"pvectl","privsep":1,"expire":1801412319}`), &asNumber); err != nil {
		t.Fatalf("forme entière: %v", err)
	}
	// This is what POST /access/users/{u}/token/{t} actually answers. Rejecting
	// it destroyed a secret that is returned exactly once.
	var asString Token
	if err := json.Unmarshal([]byte(`{"privsep":"1","expire":"1785608487"}`), &asString); err != nil {
		t.Fatalf("forme chaîne: %v", err)
	}

	if !asNumber.Separated() || !asString.Separated() {
		t.Errorf("privsep=1 dans les deux formes: %+v %+v", asNumber, asString)
	}
	if asString.Expire.Int() != 1785608487 {
		t.Errorf("expire = %d", asString.Expire.Int())
	}
}

// The API names its ACL parameters in the plural. Sending "role" instead of
// "roles" earns a 400 that blames the schema, not the typo.
func TestACLPayloadUsesThePluralNames(t *testing.T) {
	v := ACLChange{
		Path: "/vms/120", Role: "PVEVMAdmin",
		Token: "automation@pve!readonly", Propagate: true,
	}.Values()

	for _, key := range []string{"roles", "tokens", "path", "propagate"} {
		if v.Get(key) == "" {
			t.Errorf("le paramètre %q est absent: %v", key, v)
		}
	}
	if _, present := v["role"]; present {
		t.Errorf("« role » au singulier n'existe pas: %v", v)
	}

	// propagate defaults to 1 in the schema, so "off" has to be said out loud
	// rather than left unsaid.
	off := ACLChange{Path: "/vms/120", Role: "PVEVMAdmin", User: "a@pve"}.Values()
	if off.Get("propagate") != "0" {
		t.Errorf("--no-propagate doit envoyer propagate=0: %v", off)
	}
}

// An empty ACL listing means "none you may modify", never "none exist". The
// sort is what makes inheritance readable.
func TestACLIsSortedFromGeneralToSpecific(t *testing.T) {
	c := accessClient(t, map[string]string{"GET /api2/json/access/acl": "acl.json"})

	entries, err := c.ACL(context.Background())
	if err != nil {
		t.Fatalf("ACL: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("la fixture doit contenir des entrées")
	}
	if entries[0].Path != "/" {
		t.Errorf("la racine vient en premier, got %q", entries[0].Path)
	}
	for i := 1; i < len(entries); i++ {
		if pathDepth(entries[i-1].Path) > pathDepth(entries[i].Path) {
			t.Errorf("ordre cassé entre %q et %q", entries[i-1].Path, entries[i].Path)
		}
	}
}

// Effective privileges at a path must be RESOLVED by the node, because a path
// inherits from its parents. Filtering the full dump client-side answers "no
// privilege" on /vms/120 whose rights come from a propagated ACL on /vms — the
// exact shape of a wrong "non" from `whoami --can`.
func TestPermissionsAsksTheNodeForThePath(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"/vms/120":{"VM.PowerMgmt":1}}}`))
	}))
	defer srv.Close()

	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "automation@pve!pvectl", Secret: "s3cr3t",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}

	perms, err := c.Permissions(context.Background(), "/vms/120")
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !strings.Contains(gotQuery, "path=%2Fvms%2F120") {
		t.Errorf("le chemin doit partir à l'API, query = %q", gotQuery)
	}
	if perms["/vms/120"]["VM.PowerMgmt"] != 1 {
		t.Errorf("réponse mal décodée: %v", perms)
	}
}

// A full token id splits on the FIRST '!', unlike the Authorization header
// whose secret splits on the LAST '='.
func TestSplitTokenID(t *testing.T) {
	user, token, ok := SplitTokenID("automation@pve!pvectl")
	if !ok || user != "automation@pve" || token != "pvectl" {
		t.Errorf("SplitTokenID = %q, %q, %v", user, token, ok)
	}
	if _, _, ok := SplitTokenID("automation@pve"); ok {
		t.Error("un identifiant sans « ! » n'est pas un token")
	}
}

// A token expiry of 0 means "never" — a value, not an absence. Dropping it
// would let PVE apply its own default instead of the one that was asked for.
func TestTokenOptionsAlwaysSendExpire(t *testing.T) {
	v := TokenOptions{Expire: 0, Separated: true}.Values()
	if v.Get("expire") != "0" {
		t.Errorf("--no-expire doit envoyer expire=0: %v", v)
	}
	if v.Get("privsep") != "1" {
		t.Errorf("privsep=1 doit être explicite: %v", v)
	}
	if (TokenOptions{Expire: 1, Separated: false}).Values().Get("privsep") != "0" {
		t.Error("--no-privsep doit envoyer privsep=0")
	}
}
