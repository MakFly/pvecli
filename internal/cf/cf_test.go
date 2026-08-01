package cf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stub replays Cloudflare responses keyed by "METHOD /path".
func stub(t *testing.T, routes map[string]string) *Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer jeton" {
			t.Errorf("Authorization = %q", got)
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7000,"message":"route inconnue"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{Token: "jeton", AccountID: "compte", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ok(result string) string { return `{"success":true,"errors":[],"result":` + result + `}` }

func TestNewRefusesMissingCredentials(t *testing.T) {
	if _, err := New(Options{AccountID: "compte"}); err == nil {
		t.Error("un client sans jeton doit être refusé tout de suite")
	}
	if _, err := New(Options{Token: "jeton"}); err == nil {
		t.Error("un client sans compte doit être refusé tout de suite")
	}
}

// The API answers 200 with success=false often enough that trusting the status
// code is how a failure gets reported as a result.
func TestSuccessFalseIsAnErrorEvenOnHTTP200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"Invalid credentials"}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{Token: "jeton", AccountID: "compte", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Verify(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, attendu *APIError", err)
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Errorf("le code Cloudflare doit apparaître : %v", err)
	}
}

func TestTunnelByNameNamesTheAlternativesWhenItMisses(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /accounts/compte/cfd_tunnel": ok(`[{"id":"aaa","name":"homelab"},{"id":"bbb","name":"perso"}]`),
	})
	_, err := c.TunnelByName(context.Background(), "maison")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
	for _, want := range []string{"homelab", "perso"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("le message doit citer %q : %v", want, err)
		}
	}
}

// Cloudflare keeps returning tunnels it considers deleted; acting on one
// produces errors that blame the wrong thing.
func TestDeletedTunnelsAreNotListed(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /accounts/compte/cfd_tunnel": ok(
			`[{"id":"aaa","name":"vivant"},{"id":"bbb","name":"mort","deleted_at":"2026-01-01T00:00:00Z"}]`),
	})
	got, err := c.Tunnels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "vivant" {
		t.Errorf("Tunnels = %+v, un tunnel supprimé ne doit pas ressortir", got)
	}
}

// A duplicate name resolved silently works until the day it picks the other.
func TestDuplicateTunnelNamesAreRefused(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /accounts/compte/cfd_tunnel": ok(`[{"id":"aaa","name":"x"},{"id":"bbb","name":"x"}]`),
	})
	if _, err := c.TunnelByName(context.Background(), "x"); err == nil {
		t.Error("deux tunnels homonymes doivent forcer à désigner l'identifiant")
	}
}

// The rule the whole ingress table hangs on.
func TestNormaliseAlwaysEndsOnExactlyOneCatchAll(t *testing.T) {
	cases := []struct {
		name string
		in   Config
	}{
		{"table vide", Config{}},
		{"catch-all en premier", Config{Ingress: []IngressRule{
			{Service: CatchAllService},
			{Hostname: "a.tld", Service: "http://10.0.0.1:80"},
		}}},
		{"deux catch-all", Config{Ingress: []IngressRule{
			{Service: CatchAllService},
			{Hostname: "a.tld", Service: "http://10.0.0.1:80"},
			{Service: CatchAllService},
		}}},
		{"aucun catch-all", Config{Ingress: []IngressRule{
			{Hostname: "a.tld", Service: "http://10.0.0.1:80"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.in
			cfg.Normalise()
			if err := cfg.Validate(); err != nil {
				t.Fatalf("après Normalise, la table doit être valide : %v", err)
			}
			count := 0
			for _, r := range cfg.Ingress {
				if r.IsCatchAll() {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%d catch-all, attendu exactement 1 : %+v", count, cfg.Ingress)
			}
			if !cfg.Ingress[len(cfg.Ingress)-1].IsCatchAll() {
				t.Errorf("le catch-all doit être le DERNIER : %+v", cfg.Ingress)
			}
		})
	}
}

// A catch-all in the middle swallows everything below it, silently: the tunnel
// runs, the hostname resolves, every request returns 404.
func TestValidateRefusesACatchAllBeforeTheEnd(t *testing.T) {
	cfg := Config{Ingress: []IngressRule{
		{Service: CatchAllService},
		{Hostname: "a.tld", Service: "http://10.0.0.1:80"},
		{Service: CatchAllService},
	}}
	if err := cfg.Validate(); err == nil {
		t.Error("un catch-all avant la fin doit être refusé")
	}
}

func TestValidateRefusesAnEmptyTable(t *testing.T) {
	if err := (Config{}).Validate(); err == nil {
		t.Error("une table vide doit être refusée : cloudflared ne démarrerait pas")
	}
}

func TestAddRouteReplacesTheRuleForTheSameHostname(t *testing.T) {
	cfg := Config{}
	cfg.AddRoute("n8n.tld", "http://10.0.0.1:5678", nil)
	cfg.AddRoute("git.tld", "http://10.0.0.1:3000", nil)
	cfg.AddRoute("n8n.tld", "http://10.0.0.2:5678", nil)

	seen := 0
	for _, r := range cfg.Ingress {
		if r.Hostname == "n8n.tld" {
			seen++
			if r.Service != "http://10.0.0.2:5678" {
				t.Errorf("service = %q, la dernière déclaration gagne", r.Service)
			}
		}
	}
	if seen != 1 {
		t.Errorf("%d règles pour n8n.tld, attendu 1", seen)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("table invalide après AddRoute : %v", err)
	}
}

func TestRemoveRouteReportsWhetherItFoundAnything(t *testing.T) {
	cfg := Config{}
	cfg.AddRoute("n8n.tld", "http://10.0.0.1:5678", nil)

	if !cfg.RemoveRoute("n8n.tld") {
		t.Error("RemoveRoute doit signaler qu'il a trouvé la règle")
	}
	if cfg.RemoveRoute("n8n.tld") {
		t.Error("RemoveRoute doit signaler qu'il n'a rien trouvé la seconde fois")
	}
	// Removing the last route must not leave a table cloudflared refuses.
	if err := cfg.Validate(); err != nil {
		t.Errorf("table invalide après le retrait de la dernière route : %v", err)
	}
}

// Proxmox serves its API under a self-signed certificate: without this option
// the tunnel comes up and the origin answers 502.
func TestAddRouteCarriesTheOriginOptions(t *testing.T) {
	cfg := Config{}
	cfg.AddRoute("pve.tld", "https://10.0.0.23:8006", &OriginRequest{NoTLSVerify: true})

	if got := len(cfg.Ingress); got != 2 {
		t.Fatalf("%d règles, attendu la route et le catch-all", got)
	}
	if !cfg.Ingress[0].SkipsTLSVerify() {
		t.Error("la route doit porter noTLSVerify : sans lui cloudflared refuse le certificat de Proxmox")
	}
	// The catch-all is not an origin: giving it options would be meaningless.
	if cfg.Ingress[1].SkipsTLSVerify() {
		t.Error("le catch-all ne doit pas hériter des options d'origine")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("table invalide : %v", err)
	}
}

// The PUT replaces the whole table, so writing one route re-sends every other.
// A rule whose options do not survive that round trip is a rule silently
// downgraded: the tunnel keeps running and the origin starts answering 502.
func TestWritingOneRoutePreservesTheOptionsOfTheOthers(t *testing.T) {
	var sent struct {
		Config Config `json:"config"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(ok(`{"config":{"ingress":[
				{"hostname":"pve.tld","service":"https://10.0.0.23:8006","originRequest":{"noTLSVerify":true}},
				{"service":"http_status:404"}]}}`)))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(ok(`{}`)))
	}))
	defer srv.Close()

	c, err := New(Options{Token: "jeton", AccountID: "compte", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg, err := c.TunnelConfig(ctx, "aaa")
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddRoute("n8n.tld", "http://10.0.0.220:5678", nil)
	if err := c.SetTunnelConfig(ctx, "aaa", cfg); err != nil {
		t.Fatal(err)
	}

	var pve *IngressRule
	for i, r := range sent.Config.Ingress {
		if r.Hostname == "pve.tld" {
			pve = &sent.Config.Ingress[i]
		}
	}
	if pve == nil {
		t.Fatal("la règle pve.tld a disparu de la table réécrite")
	}
	if !pve.SkipsTLSVerify() {
		t.Error("noTLSVerify perdu au passage lecture → écriture")
	}
}

// The longest suffix wins, or a record lands in a zone that does not serve it.
func TestZoneForHostPicksTheLongestMatchingZone(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /zones": ok(`[{"id":"z1","name":"example.com"},{"id":"z2","name":"lab.example.com"}]`),
	})
	got, err := c.ZoneForHost(context.Background(), "n8n.lab.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "z2" {
		t.Errorf("zone = %+v, attendu lab.example.com", got)
	}
}

func TestZoneForHostRefusesAnUnknownDomain(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /zones": ok(`[{"id":"z1","name":"example.com"}]`),
	})
	_, err := c.ZoneForHost(context.Background(), "n8n.autre.tld")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "example.com") {
		t.Errorf("le message doit citer les zones visibles : %v", err)
	}
}

// A name already serving something else is a collision for the operator to
// resolve, not a formality this tool gets to decide.
func TestPointAtTunnelRefusesToOverwriteAForeignRecord(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /zones/z1/dns_records": ok(`[{"id":"r1","type":"A","name":"n8n.example.com","content":"203.0.113.7"}]`),
	})
	_, err := c.PointAtTunnel(context.Background(),
		Zone{ID: "z1", Name: "example.com"}, "n8n.example.com", Tunnel{ID: "aaa", Name: "homelab"})
	if err == nil {
		t.Fatal("un enregistrement A existant ne doit pas être écrasé")
	}
	if !strings.Contains(err.Error(), "203.0.113.7") {
		t.Errorf("le message doit montrer ce qui est déjà là : %v", err)
	}
}

// An unproxied CNAME to cfargotunnel.com resolves to something no client can
// reach, and the symptom is a timeout rather than an error.
func TestPointAtTunnelAlwaysProxies(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(ok(`[]`)))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(ok(`{"id":"r9","type":"CNAME","name":"n8n.example.com"}`)))
	}))
	defer srv.Close()

	c, err := New(Options{Token: "jeton", AccountID: "compte", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PointAtTunnel(context.Background(),
		Zone{ID: "z1", Name: "example.com"}, "n8n.example.com", Tunnel{ID: "aaa", Name: "homelab"}); err != nil {
		t.Fatal(err)
	}
	if sent["proxied"] != true {
		t.Errorf("proxied = %v, doit toujours être vrai", sent["proxied"])
	}
	if sent["content"] != "aaa."+TunnelDomain {
		t.Errorf("content = %v, attendu le CNAME du tunnel", sent["content"])
	}
}

// The endpoint table is the only place a path may be written.
func TestEveryEndpointIsRegistered(t *testing.T) {
	if len(AllEndpoints) == 0 {
		t.Fatal("AllEndpoints est vide")
	}
	seen := map[string]bool{}
	for _, e := range AllEndpoints {
		key := e.Method + " " + e.Pattern
		if seen[key] {
			t.Errorf("endpoint déclaré deux fois : %s", key)
		}
		seen[key] = true
		if !strings.HasPrefix(e.Pattern, "/") {
			t.Errorf("%s : un chemin doit commencer par « / »", key)
		}
	}
}

func TestEndpointPathSubstitutesInOrder(t *testing.T) {
	got := epTunnelDelete.path("compte", "aaa")
	if got != "/accounts/compte/cfd_tunnel/aaa" {
		t.Errorf("path = %q", got)
	}
}

// The expensive one. A service token inside an "allow" policy is accepted by
// the API and lets EVERY request through, authenticated or not — because
// "allow" means "an identity authenticated" and a service token carries none.
func TestServiceTokenInAnAllowPolicyIsRefused(t *testing.T) {
	p := Policy{Name: "cli", Decision: DecisionAllow,
		Include: []Rule{IncludeServiceToken("abc")}}

	err := p.Validate()
	if err == nil {
		t.Fatal("un service token dans une policy « allow » doit être refusé")
	}
	if !strings.Contains(err.Error(), DecisionServiceAuth) {
		t.Errorf("le refus doit nommer la décision correcte, got: %v", err)
	}

	// The same rule with the right decision is exactly what we want.
	p.Decision = DecisionServiceAuth
	if err := p.Validate(); err != nil {
		t.Errorf("une policy « %s » avec un service token est valide : %v", DecisionServiceAuth, err)
	}
}

// A policy with no include admits nobody, which is not the same as protecting
// nothing — but it is never what someone meant to write.
func TestPolicyWithoutIncludeIsRefused(t *testing.T) {
	if err := (Policy{Decision: DecisionAllow}).Validate(); err == nil {
		t.Error("une policy sans include doit être refusée")
	}
}

// The table an operator reads before trusting the door has to say who gets in.
func TestPolicyDescribesWhoItAdmits(t *testing.T) {
	p := Policy{Decision: DecisionAllow, Include: []Rule{
		IncludeEmail("moi@exemple.tld"), IncludeEmailDomain("exemple.tld"),
	}}
	got := p.Describe()
	if !strings.Contains(got, "moi@exemple.tld") || !strings.Contains(got, "@exemple.tld") {
		t.Errorf("Describe() = %q, doit nommer qui passe", got)
	}
}

// An application is identified by the hostname it protects: two apps may share
// a display name, never a domain. Cloudflare stores the domain with an optional
// path, so a bare hostname has to match the app that covers it.
func TestAppByDomainMatchesAPathScopedApplication(t *testing.T) {
	c := stub(t, map[string]string{
		"GET /accounts/compte/access/apps": ok(`[
			{"id":"a1","name":"autre","domain":"git.example.com"},
			{"id":"a2","name":"proxmox","domain":"pve.example.com/"}]`),
	})

	app, err := c.AppByDomain(context.Background(), "pve.example.com")
	if err != nil {
		t.Fatalf("AppByDomain: %v", err)
	}
	if app.ID != "a2" {
		t.Errorf("app = %q, attendu celle qui couvre le nom", app.ID)
	}

	if _, err := c.AppByDomain(context.Background(), "absent.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("un nom non couvert doit répondre ErrNotFound, got %v", err)
	}
}

// The type is not the caller's to choose: a service behind a tunnel is
// self-hosted, and letting it be anything else produces an application that
// protects nothing recognisable.
func TestCreateAppAlwaysDeclaresSelfHosted(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		_, _ = w.Write([]byte(ok(`{"id":"a1","domain":"pve.example.com"}`)))
	}))
	defer srv.Close()

	c, err := New(Options{Token: "jeton", AccountID: "compte", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateApp(context.Background(),
		App{Name: "proxmox", Domain: "pve.example.com", SessionDuration: "24h"}); err != nil {
		t.Fatal(err)
	}
	if sent["type"] != AppTypeSelfHosted {
		t.Errorf("type = %v, want %q", sent["type"], AppTypeSelfHosted)
	}
}
