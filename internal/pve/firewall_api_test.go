package pve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// La surface HTTP du firewall. `firewall_test.go` couvre déjà la logique pure
// de withFirewallFlag ; ce qui manquait, c'est ce qui part sur le réseau.
//
// Chaque test vérifie deux choses distinctes : la route et le verbe — une
// faute là ne se voit que contre un vrai nœud, jamais à la compilation — et le
// décodage de la réponse.

type fwCall struct {
	method string
	path   string
	form   url.Values
}

func fwServer(t *testing.T, body func(r *http.Request) string) (*Client, *[]fwCall) {
	t.Helper()
	var calls []fwCall

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		calls = append(calls, fwCall{method: r.Method, path: r.URL.Path, form: r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body(r)))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{
		Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s",
		Transport: srv.Client().Transport,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, &calls
}

func lastCall(t *testing.T, calls *[]fwCall) fwCall {
	t.Helper()
	if len(*calls) == 0 {
		t.Fatal("le client n'a émis aucune requête")
	}
	return (*calls)[len(*calls)-1]
}

func TestClusterFirewallEnabled(t *testing.T) {
	// La bascule maîtresse. En inverser le sens ferait taire l'avertissement
	// « rien ne filtre » exactement quand il faut le donner.
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"data":{"enable":1}}`, true},
		{`{"data":{"enable":0}}`, false},
		{`{"data":{}}`, false},
	} {
		c, calls := fwServer(t, func(*http.Request) string { return tc.body })
		got, err := c.ClusterFirewallEnabled(context.Background())
		if err != nil {
			t.Fatalf("ClusterFirewallEnabled(%s): %v", tc.body, err)
		}
		if got != tc.want {
			t.Fatalf("%s → %v, attendu %v", tc.body, got, tc.want)
		}
		if call := lastCall(t, calls); call.path != "/api2/json/cluster/firewall/options" {
			t.Fatalf("route = %s", call.path)
		}
	}
}

func TestLXCFwOptionsReadsPolicies(t *testing.T) {
	c, calls := fwServer(t, func(*http.Request) string {
		return `{"data":{"enable":1,"policy_in":"DROP","policy_out":"ACCEPT"}}`
	})

	opts, err := c.LXCFwOptions(context.Background(), "pve", 221)
	if err != nil {
		t.Fatalf("LXCFwOptions: %v", err)
	}
	if opts.Enable != 1 || opts.PolicyIn != "DROP" || opts.PolicyOut != "ACCEPT" {
		t.Fatalf("options = %+v", opts)
	}
	call := lastCall(t, calls)
	if call.method != http.MethodGet || call.path != "/api2/json/nodes/pve/lxc/221/firewall/options" {
		t.Fatalf("%s %s", call.method, call.path)
	}
}

func TestSetLXCFwOptionsUsesPUT(t *testing.T) {
	// PVE distingue POST et PUT sur cette route : un POST n'y modifie rien.
	c, calls := fwServer(t, func(*http.Request) string { return `{"data":null}` })

	v := url.Values{}
	v.Set("enable", "1")
	v.Set("policy_in", "DROP")
	if err := c.SetLXCFwOptions(context.Background(), "pve", 221, v); err != nil {
		t.Fatalf("SetLXCFwOptions: %v", err)
	}

	call := lastCall(t, calls)
	if call.method != http.MethodPut {
		t.Fatalf("méthode = %s, attendu PUT", call.method)
	}
	if call.form.Get("enable") != "1" || call.form.Get("policy_in") != "DROP" {
		t.Fatalf("corps = %v", call.form)
	}
}

func TestLXCFwRulesKeepsOrder(t *testing.T) {
	// L'ordre EST la sémantique d'un firewall : la première règle qui matche
	// gagne. Un tri, une inversion, et ce qui passe change.
	c, _ := fwServer(t, func(*http.Request) string {
		return `{"data":[
			{"pos":0,"type":"in","action":"ACCEPT","proto":"tcp","dport":"5432"},
			{"pos":1,"type":"in","action":"DROP","proto":"tcp","dport":"22"}
		]}`
	})

	rules, err := c.LXCFwRules(context.Background(), "pve", 221)
	if err != nil {
		t.Fatalf("LXCFwRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("%d règles, attendu 2", len(rules))
	}
	if rules[0].Pos != 0 || rules[0].Action != "ACCEPT" || rules[0].Dport != "5432" {
		t.Fatalf("règle 0 = %+v", rules[0])
	}
	if rules[1].Pos != 1 || rules[1].Action != "DROP" {
		t.Fatalf("règle 1 = %+v", rules[1])
	}
}

func TestAddAndDeleteLXCFwRule(t *testing.T) {
	c, calls := fwServer(t, func(*http.Request) string { return `{"data":null}` })

	v := url.Values{}
	v.Set("type", "in")
	v.Set("action", "ACCEPT")
	v.Set("dport", "7700")
	if err := c.AddLXCFwRule(context.Background(), "pve", 221, v); err != nil {
		t.Fatalf("AddLXCFwRule: %v", err)
	}
	call := lastCall(t, calls)
	if call.method != http.MethodPost || call.path != "/api2/json/nodes/pve/lxc/221/firewall/rules" {
		t.Fatalf("%s %s", call.method, call.path)
	}
	if call.form.Get("dport") != "7700" {
		t.Fatalf("corps = %v", call.form)
	}

	if err := c.DeleteLXCFwRule(context.Background(), "pve", 221, 3); err != nil {
		t.Fatalf("DeleteLXCFwRule: %v", err)
	}
	// La position va dans le CHEMIN, pas dans le corps. Une règle supprimée au
	// mauvais index, c'est une porte laissée ouverte sans que rien ne le dise.
	call = lastCall(t, calls)
	if call.method != http.MethodDelete || call.path != "/api2/json/nodes/pve/lxc/221/firewall/rules/3" {
		t.Fatalf("%s %s", call.method, call.path)
	}
}

func TestSetLXCNICFirewallPreservesTheRestOfNet0(t *testing.T) {
	// Le piège : net0 porte aussi le bridge, la MAC, l'IP et la passerelle.
	// Réécrire l'option en n'y mettant que le drapeau couperait le réseau du
	// conteneur — depuis l'hyperviseur, sans moyen d'y revenir.
	const net0 = "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=192.168.1.221/24,gw=192.168.1.1"

	c, calls := fwServer(t, func(r *http.Request) string {
		if r.Method == http.MethodGet {
			return `{"data":{"net0":"` + net0 + `"}}`
		}
		return `{"data":"UPID:pve:0000:task"}`
	})

	changed, err := c.SetLXCNICFirewall(context.Background(), "pve", 221, true)
	if err != nil {
		t.Fatalf("SetLXCNICFirewall: %v", err)
	}
	if !changed {
		t.Fatal("net0 n'avait pas de drapeau : un changement devait être signalé")
	}

	sent := lastCall(t, calls).form.Get("net0")
	if !strings.Contains(sent, "firewall=1") {
		t.Fatalf("firewall=1 absent : %q", sent)
	}
	for _, keep := range []string{"bridge=vmbr0", "hwaddr=BC:24:11:00:00:01", "ip=192.168.1.221/24", "gw=192.168.1.1"} {
		if !strings.Contains(sent, keep) {
			t.Fatalf("%q perdu dans la réécriture : %q", keep, sent)
		}
	}
}

func TestSetLXCNICFirewallIsIdempotent(t *testing.T) {
	// Drapeau déjà posé : ne rien écrire. Un PUT inutile sur la config d'un
	// guest n'est pas gratuit — il passe par une tâche PVE, qui verrouille.
	c, calls := fwServer(t, func(*http.Request) string {
		return `{"data":{"net0":"name=eth0,bridge=vmbr0,firewall=1"}}`
	})

	changed, err := c.SetLXCNICFirewall(context.Background(), "pve", 221, true)
	if err != nil {
		t.Fatalf("SetLXCNICFirewall: %v", err)
	}
	if changed {
		t.Fatal("le drapeau était déjà posé : aucun changement à signaler")
	}
	if n := len(*calls); n != 1 {
		t.Fatalf("%d requêtes, attendu 1 (la lecture seule)", n)
	}
}

func TestSetLXCNICFirewallRefusesAContainerWithoutNet0(t *testing.T) {
	c, _ := fwServer(t, func(*http.Request) string { return `{"data":{}}` })

	_, err := c.SetLXCNICFirewall(context.Background(), "pve", 221, true)
	if err == nil {
		t.Fatal("un conteneur sans net0 doit être refusé, pas silencieusement ignoré")
	}
	if !strings.Contains(err.Error(), "net0") {
		t.Fatalf("l'erreur doit nommer l'interface manquante : %v", err)
	}
}

func TestIPSetsAndEntries(t *testing.T) {
	c, calls := fwServer(t, func(r *http.Request) string {
		if strings.HasSuffix(r.URL.Path, "/ipset") {
			return `{"data":[{"name":"infra_clients","comment":"app-01"}]}`
		}
		return `{"data":[{"cidr":"192.168.1.220","comment":"app-01"},{"cidr":"10.0.0.0/8","nomatch":1}]}`
	})

	sets, err := c.IPSets(context.Background())
	if err != nil {
		t.Fatalf("IPSets: %v", err)
	}
	if len(sets) != 1 || sets[0].Name != "infra_clients" {
		t.Fatalf("ipsets = %+v", sets)
	}

	entries, err := c.IPSetEntries(context.Background(), "infra_clients")
	if err != nil {
		t.Fatalf("IPSetEntries: %v", err)
	}
	if len(entries) != 2 || entries[0].CIDR != "192.168.1.220" || entries[1].Nomatch != 1 {
		t.Fatalf("entrées = %+v", entries)
	}
	if call := lastCall(t, calls); call.path != "/api2/json/cluster/firewall/ipset/infra_clients" {
		t.Fatalf("route = %s", call.path)
	}
}

func TestCreateIPSetOmitsAnEmptyComment(t *testing.T) {
	// Envoyer comment="" n'est pas neutre : PVE l'écrit tel quel, et écrase un
	// commentaire existant par du vide.
	c, calls := fwServer(t, func(*http.Request) string { return `{"data":null}` })

	if err := c.CreateIPSet(context.Background(), "infra_clients", ""); err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}
	call := lastCall(t, calls)
	if _, present := call.form["comment"]; present {
		t.Fatalf("« comment » vide ne doit pas être envoyé : %v", call.form)
	}
	if call.form.Get("name") != "infra_clients" {
		t.Fatalf("corps = %v", call.form)
	}

	if err := c.CreateIPSet(context.Background(), "autre", "un commentaire"); err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}
	if got := lastCall(t, calls).form.Get("comment"); got != "un commentaire" {
		t.Fatalf("commentaire = %q", got)
	}
}

func TestAddAndDelIPSetEntry(t *testing.T) {
	c, calls := fwServer(t, func(*http.Request) string { return `{"data":null}` })

	if err := c.AddIPSetEntry(context.Background(), "infra_clients", "192.168.1.220", ""); err != nil {
		t.Fatalf("AddIPSetEntry: %v", err)
	}
	call := lastCall(t, calls)
	if call.method != http.MethodPost || call.path != "/api2/json/cluster/firewall/ipset/infra_clients" {
		t.Fatalf("%s %s", call.method, call.path)
	}
	if call.form.Get("cidr") != "192.168.1.220" {
		t.Fatalf("corps = %v", call.form)
	}
	if _, present := call.form["comment"]; present {
		t.Fatalf("« comment » vide ne doit pas être envoyé : %v", call.form)
	}

	if err := c.DelIPSetEntry(context.Background(), "infra_clients", "192.168.1.220"); err != nil {
		t.Fatalf("DelIPSetEntry: %v", err)
	}
	call = lastCall(t, calls)
	if call.method != http.MethodDelete || call.path != "/api2/json/cluster/firewall/ipset/infra_clients/192.168.1.220" {
		t.Fatalf("%s %s", call.method, call.path)
	}
}

func TestFirewallCallsPropagateAPIErrors(t *testing.T) {
	// Un 403 sur une route de firewall veut dire que le token n'a pas
	// Sys.Modify. Le rendre tel quel est ce qui permet de le comprendre —
	// avaler l'erreur laisserait croire que la règle a été posée.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":{"permissions":"Sys.Modify requis"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{Endpoint: srv.URL, TokenID: "a@pve!t", Secret: "s", Transport: srv.Client().Transport})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"ClusterFirewallEnabled": func() error { _, e := c.ClusterFirewallEnabled(ctx); return e },
		"LXCFwOptions":           func() error { _, e := c.LXCFwOptions(ctx, "pve", 221); return e },
		"LXCFwRules":             func() error { _, e := c.LXCFwRules(ctx, "pve", 221); return e },
		"SetLXCFwOptions":        func() error { return c.SetLXCFwOptions(ctx, "pve", 221, url.Values{}) },
		"AddLXCFwRule":           func() error { return c.AddLXCFwRule(ctx, "pve", 221, url.Values{}) },
		"DeleteLXCFwRule":        func() error { return c.DeleteLXCFwRule(ctx, "pve", 221, 0) },
		"SetLXCNICFirewall":      func() error { _, e := c.SetLXCNICFirewall(ctx, "pve", 221, true); return e },
		"IPSets":                 func() error { _, e := c.IPSets(ctx); return e },
		"IPSetEntries":           func() error { _, e := c.IPSetEntries(ctx, "x"); return e },
		"CreateIPSet":            func() error { return c.CreateIPSet(ctx, "x", "") },
		"AddIPSetEntry":          func() error { return c.AddIPSetEntry(ctx, "x", "1.2.3.4", "") },
		"DelIPSetEntry":          func() error { return c.DelIPSetEntry(ctx, "x", "1.2.3.4") },
	} {
		if err := call(); err == nil {
			t.Errorf("%s a avalé le 403", name)
		}
	}
}
