package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// L'amorçage est le seul chemin de la CLI qui parle au nœud SANS token. Ce qui
// est vérifié ici, c'est qu'il le fait comme le fait l'interface web — ticket
// dans un cookie, jeton anti-CSRF sur les écritures — et qu'il ne laisse pas
// filer le mot de passe ni le secret ailleurs que là où on les attend.

func TestLoginEnvoieLeMotDePasseEnFormulaireEtRendUnTicket(t *testing.T) {
	var gotPath, gotUser, gotPassword, gotCT string

	var gotAuthz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuthz = r.Header.Get("Authorization")
		_ = r.ParseForm()
		gotUser = r.PostForm.Get("username")
		gotPassword = r.PostForm.Get("password")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ticket":"PVE:root@pam:AAA","CSRFPreventionToken":"CSRF:BBB","username":"root@pam"}}`))
	}))
	defer srv.Close()

	ticket, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if !strings.HasSuffix(gotPath, "/access/ticket") {
		t.Errorf("chemin = %q, attendu …/access/ticket", gotPath)
	}
	// PVE lit des paramètres de formulaire, pas du JSON : envoyer du JSON
	// rapporte un 400 qui accuse les paramètres.
	if !strings.HasPrefix(gotCT, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, attendu du formulaire", gotCT)
	}
	if gotUser != "root@pam" || gotPassword != "hunter2" {
		t.Errorf("identifiants transmis = %q/%q", gotUser, gotPassword)
	}
	if ticket.Ticket != "PVE:root@pam:AAA" || ticket.CSRF != "CSRF:BBB" {
		t.Errorf("ticket mal décodé : %+v", ticket)
	}
	if client == nil {
		t.Fatal("Login n'a pas rendu de client")
	}
	// La seule requête de toute la CLI qui ne présente rien. Un
	// « PVEAPIToken== » — identifiant vide — ferait refuser par un 401 la
	// requête même qui sert à obtenir de quoi ne plus en recevoir.
	if gotAuthz != "" {
		t.Errorf("Authorization = %q sur /access/ticket, attendu aucun en-tête", gotAuthz)
	}
}

func TestLeClientDeTicketUtiliseLeCookieEtLeCSRFPasAuthorization(t *testing.T) {
	var authz, csrfOnWrite, csrfOnRead, cookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			_, _ = w.Write([]byte(`{"data":{"ticket":"T","CSRFPreventionToken":"C","username":"root@pam"}}`))
			return
		case r.Method == http.MethodGet:
			csrfOnRead = r.Header.Get("CSRFPreventionToken")
			_, _ = w.Write([]byte(`{"data":{"version":"9.2.2"}}`))
		default:
			authz = r.Header.Get("Authorization")
			csrfOnWrite = r.Header.Get("CSRFPreventionToken")
			if ck, err := r.Cookie("PVEAuthCookie"); err == nil {
				cookie = ck.Value
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	_, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, err := client.Version(context.Background()); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if err := client.SetACL(context.Background(), ACLChange{
		Path: "/", Role: "PVEAdmin", Token: "automation@pve!pvectl",
	}); err != nil {
		t.Fatalf("SetACL: %v", err)
	}

	if cookie != "T" {
		t.Errorf("PVEAuthCookie = %q, attendu « T »", cookie)
	}
	// Un en-tête Authorization ici voudrait dire qu'on envoie
	// « PVEAPIToken==» — un identifiant vide, que le nœud refuse par un 401
	// qu'on passerait ensuite du temps à comprendre.
	if authz != "" {
		t.Errorf("Authorization = %q, attendu vide quand on tient un ticket", authz)
	}
	if csrfOnWrite != "C" {
		t.Errorf("CSRFPreventionToken sur écriture = %q, attendu « C »", csrfOnWrite)
	}
	if csrfOnRead != "" {
		t.Errorf("CSRFPreventionToken sur lecture = %q, PVE ne le demande pas là", csrfOnRead)
	}
}

func TestLoginRefuseUnUtilisateurSansRoyaume(t *testing.T) {
	// « root » n'est pas un utilisateur PVE : root@pam et root@pve sont deux
	// identités différentes, et l'oubli du royaume est l'erreur nº 1.
	_, _, err := Login(context.Background(), Options{Endpoint: "https://pve.example:8006"},
		"root", "hunter2")
	if err == nil {
		t.Fatal("attendu un refus pour « root » sans royaume")
	}
	if !strings.Contains(err.Error(), "royaume") {
		t.Errorf("erreur = %v, elle devrait nommer le royaume manquant", err)
	}
}

func TestLoginRefuseUnTicketVide(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	_, _, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err == nil {
		t.Fatal("un ticket vide doit être un échec, pas un client muet")
	}
}

func TestEnsureAutomationTokenEstRejouable(t *testing.T) {
	// Le nœud a déjà l'utilisateur ET le token : rien ne doit être recréé, et
	// surtout aucun secret ne doit être inventé.
	var created []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			_, _ = w.Write([]byte(`{"data":{"ticket":"T","CSRFPreventionToken":"C"}}`))
		case strings.Contains(r.URL.Path, "/access/users/automation@pve/token") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"tokenid":"pvectl","privsep":0}]}`))
		case strings.HasSuffix(r.URL.Path, "/access/users/automation@pve") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"userid":"automation@pve"}}`))
		case r.Method == http.MethodPost || r.Method == http.MethodPut:
			created = append(created, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"data":null}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	_, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := client.EnsureAutomationToken(context.Background(), Bootstrap{
		UserID: "automation@pve", TokenID: "pvectl", Role: "PVEAdmin", Path: "/",
	})
	if err != nil {
		t.Fatalf("EnsureAutomationToken: %v", err)
	}

	if res.UserCreated || res.TokenCreated {
		t.Errorf("rien ne devait être créé : %+v", res)
	}
	if res.Secret != "" {
		t.Error("un secret a été rendu pour un token existant — le nœud ne le " +
			"conserve pas en clair, il ne peut pas être relu")
	}
	if !res.ACLSet {
		t.Error("l'ACL doit être réappliquée même quand tout existe déjà")
	}
	if res.FullTokenID != "automation@pve!pvectl" {
		t.Errorf("FullTokenID = %q", res.FullTokenID)
	}
	for _, c := range created {
		if strings.Contains(c, "/token/") {
			t.Errorf("un token a été recréé alors qu'il existait : %s", c)
		}
	}
}

func TestEnsureAutomationTokenCreeToutSurUnNoeudVierge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			_, _ = w.Write([]byte(`{"data":{"ticket":"T","CSRFPreventionToken":"C"}}`))
		case strings.HasSuffix(r.URL.Path, "/access/users/automation@pve") && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusInternalServerError) // PVE : « no such user »
			_, _ = w.Write([]byte(`{"data":null}`))
		case strings.Contains(r.URL.Path, "/token") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/token/") && r.Method == http.MethodPost:
			out, _ := json.Marshal(map[string]any{"data": map[string]any{
				"full-tokenid": "automation@pve!pvectl",
				"value":        "11111111-2222-3333-4444-555555555555",
			}})
			_, _ = w.Write(out)
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	_, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := client.EnsureAutomationToken(context.Background(), Bootstrap{
		UserID: "automation@pve", TokenID: "pvectl", Role: "PVEAdmin", Path: "/",
	})
	if err != nil {
		t.Fatalf("EnsureAutomationToken: %v", err)
	}

	if !res.UserCreated || !res.TokenCreated || !res.ACLSet {
		t.Errorf("amorçage incomplet : %+v", res)
	}
	if res.Secret != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("secret = %q — c'est la seule fois où le nœud le donne", res.Secret)
	}
}

func TestLACLVaSurLUtilisateurEtPasSeulementSurLeToken(t *testing.T) {
	// Le point qui décide si l'amorçage sert à quelque chose. Avec privsep=0,
	// les privilèges effectifs du token SONT ceux de son utilisateur, et ses
	// propres ACL sont ignorées : une ACL posée sur le seul token donne un
	// token valide et sans aucun droit, dont chaque appel rend 403.
	var aclTargets []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			_, _ = w.Write([]byte(`{"data":{"ticket":"T","CSRFPreventionToken":"C"}}`))
		case strings.HasSuffix(r.URL.Path, "/access/acl"):
			_ = r.ParseForm()
			if u := r.PostForm.Get("users"); u != "" {
				aclTargets = append(aclTargets, "user:"+u)
			}
			if tk := r.PostForm.Get("tokens"); tk != "" {
				aclTargets = append(aclTargets, "token:"+tk)
			}
			_, _ = w.Write([]byte(`{"data":null}`))
		case strings.HasSuffix(r.URL.Path, "/access/users/automation@pve") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"userid":"automation@pve"}}`))
		case strings.Contains(r.URL.Path, "/token") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"tokenid":"pvectl","privsep":0}]}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	_, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := client.EnsureAutomationToken(context.Background(), Bootstrap{
		UserID: "automation@pve", TokenID: "pvectl", Role: "PVEAdmin", Path: "/",
	}); err != nil {
		t.Fatalf("EnsureAutomationToken: %v", err)
	}

	joined := strings.Join(aclTargets, " ")
	if !strings.Contains(joined, "user:automation@pve") {
		t.Errorf("ACL posée sur %v — sans celle de l'utilisateur, un token "+
			"privsep=0 n'a aucun droit", aclTargets)
	}
	// Et sur le token aussi, pour qu'un passage ultérieur en privsep=1 (où les
	// droits sont l'INTERSECTION des deux) ne vide pas les privilèges.
	if !strings.Contains(joined, "token:automation@pve!pvectl") {
		t.Errorf("ACL posée sur %v — il manque celle du token", aclTargets)
	}
}

func TestUnEchecApresLaCreationRendQuandMemeLeSecret(t *testing.T) {
	// PVE ne montre le secret qu'une fois. Si la pose de l'ACL échoue derrière,
	// rendre nil le perdrait pour toujours et laisserait un token à supprimer
	// à la main.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/access/ticket"):
			_, _ = w.Write([]byte(`{"data":{"ticket":"T","CSRFPreventionToken":"C"}}`))
		case strings.HasSuffix(r.URL.Path, "/access/users/automation@pve") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"userid":"automation@pve"}}`))
		case strings.Contains(r.URL.Path, "/token") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[]}`))
		case strings.Contains(r.URL.Path, "/token/") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"data":{"full-tokenid":"automation@pve!pvectl","value":"S3CR3T"}}`))
		case strings.HasSuffix(r.URL.Path, "/access/acl"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":{"roles":"role 'NEXISTEPAS' does not exist"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":null}`))
		}
	}))
	defer srv.Close()

	_, client, err := Login(context.Background(), Options{
		Endpoint: srv.URL, Transport: srv.Client().Transport,
	}, "root@pam", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := client.EnsureAutomationToken(context.Background(), Bootstrap{
		UserID: "automation@pve", TokenID: "pvectl", Role: "NEXISTEPAS", Path: "/",
	})
	if err == nil {
		t.Fatal("une ACL refusée doit être une erreur")
	}
	if res == nil {
		t.Fatal("res == nil : le secret unique est perdu avec lui")
	}
	if res.Secret != "S3CR3T" {
		t.Errorf("Secret = %q, il devait survivre à l'échec de l'ACL", res.Secret)
	}
	if !res.TokenCreated {
		t.Error("TokenCreated devrait dire vrai : le token existe sur le nœud")
	}
}

func TestNewBareRefuseUnServiceTokenCloudflareIncomplet(t *testing.T) {
	_, _, err := Login(context.Background(), Options{
		Endpoint:       "https://pve.example:8006",
		AccessClientID: "seulement-la-moitie",
	}, "root@pam", "hunter2")
	if err == nil {
		t.Fatal("attendu un refus : une moitié de service token donne un 403 trompeur")
	}
	if !strings.Contains(err.Error(), "CF_ACCESS_CLIENT_SECRET") {
		t.Errorf("erreur = %v, elle devrait nommer la variable manquante", err)
	}
}

func TestLaTraceMasqueLeTicketEtLeJetonAntiCSRF(t *testing.T) {
	// Un « pvecli login -vv » ne doit pas écrire sur stderr une session root
	// valide deux heures. Le ticket voyage dans un cookie, pas dans
	// Authorization : c'est le cas que la rédaction d'origine ne couvrait pas.
	req, _ := http.NewRequest(http.MethodPost, "https://pve.example/api2/json/access/acl", nil)
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: "PVE:root@pam:SECRET"})
	req.Header.Set("CSRFPreventionToken", "CSRF:SECRET")

	safe := redactedHeader(req, "automation@pve!pvectl")

	if strings.Contains(safe.Get("Cookie"), "SECRET") {
		t.Errorf("Cookie = %q, le ticket fuit dans la trace", safe.Get("Cookie"))
	}
	if strings.Contains(safe.Get("CSRFPreventionToken"), "SECRET") {
		t.Errorf("CSRFPreventionToken = %q, il fuit dans la trace", safe.Get("CSRFPreventionToken"))
	}
}
