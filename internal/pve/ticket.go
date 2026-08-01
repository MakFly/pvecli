package pve

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Ticket authentication — the only way to talk to a node one has no token for.
//
// C'est le trou que ce fichier bouche. Toute la CLI s'authentifie par token, ce
// qui est le bon choix partout… sauf au premier contact : sur une machine neuve,
// on n'a pas de token, et rien dans la CLI ne savait en fabriquer un. Il fallait
// aller à la main sur le nœud lancer trois `pveum`. Autrement dit, l'outil censé
// administrer le nœud ne pouvait pas franchir sa propre porte d'entrée.
//
// L'API, elle, sait faire : POST /access/ticket contre un couple
// utilisateur/mot de passe rend un ticket et un jeton anti-CSRF, avec lesquels
// on peut créer un utilisateur, un token et une ACL. C'est exactement ce que
// fait l'interface web quand on s'y connecte.
//
// Deux différences avec un token, qui expliquent la forme du code :
//
//   - le ticket voyage dans un COOKIE (PVEAuthCookie), pas dans Authorization ;
//   - il exige un en-tête CSRFPreventionToken sur toute écriture. Un token en
//     est dispensé (voir auth.go) précisément parce qu'il n'est jamais envoyé
//     tout seul par un navigateur ; un cookie, si.
//
// Un ticket vit deux heures. On ne l'écrit jamais sur le disque : il sert le
// temps d'un `pvecli login`, et ce qui survit à la commande est le token, qui
// est révocable un par un.

// epTicket is declared here rather than in endpoints.go because it is the one
// endpoint reachable WITHOUT credentials — it is what produces them.
var epTicket = endpoint{"POST", "/access/ticket"}

// Ticket is a session opened with a password. Short-lived, never persisted.
type Ticket struct {
	Ticket   string `json:"ticket"`
	CSRF     string `json:"CSRFPreventionToken"`
	Username string `json:"username"`
}

// Login exchanges a password for a ticket, against a node we have no token for.
//
// Deliberately a package function and not a Client method: a Client cannot be
// built without a token (see New), and requiring one here would be the circular
// dependency this whole file exists to break.
func Login(ctx context.Context, o Options, user, password string) (*Ticket, *Client, error) {
	if user == "" {
		return nil, nil, fmt.Errorf("aucun utilisateur — attendu la forme « root@pam »")
	}
	if !strings.Contains(user, "@") {
		return nil, nil, fmt.Errorf("utilisateur %q sans royaume — PVE attend « %s@pam » (compte système) "+
			"ou « %s@pve » (compte PVE)", user, user, user)
	}
	if password == "" {
		return nil, nil, fmt.Errorf("mot de passe vide")
	}

	// Un client « nu » : même transport, même vérification TLS, mais sans
	// identité. Il ne sert qu'à cet unique appel.
	bare, err := newBare(o)
	if err != nil {
		return nil, nil, err
	}

	form := url.Values{"username": {user}, "password": {password}}
	var t Ticket
	if err := bare.write(ctx, epTicket.Method, epTicket.Pattern, form, &t); err != nil {
		return nil, nil, err
	}
	if t.Ticket == "" {
		return nil, nil, &AuthError{
			Reason: "le nœud n'a pas rendu de ticket",
			Hint:   "Vérifie l'utilisateur et le royaume : root@pam n'est pas root@pve.",
		}
	}

	bare.ticket, bare.csrf = t.Ticket, t.CSRF
	return &t, bare, nil
}

// newBare builds a Client with no identity, for the ticket exchange only.
//
// It repeats the transport wiring of New rather than relaxing New's checks:
// those checks are what stop every OTHER command from opening a socket with
// nothing to authenticate with, and weakening them for this one case would
// remove the guarantee everywhere.
func newBare(o Options) (*Client, error) {
	if o.Endpoint == "" {
		return nil, fmt.Errorf("aucun endpoint configuré — lance « pvecli config init --endpoint https://…:8006 » ou exporte PVE_API_URL")
	}
	// Même refus que New : avec une moitié de service token, Cloudflare rend un
	// 403 — ou pire, la page de connexion — et `login` échouerait sur un message
	// qui n'a rien à voir avec la cause.
	if (o.AccessClientID == "") != (o.AccessClientSecret == "") {
		missing, present := "CF_ACCESS_CLIENT_SECRET", "CF_ACCESS_CLIENT_ID"
		if o.AccessClientID == "" {
			missing, present = present, missing
		}
		return nil, &AuthError{
			Reason: "service token Cloudflare Access incomplet : " + present + " est défini, " + missing + " manque",
			Hint:   "Les deux vont ensemble, ou aucun des deux.",
		}
	}

	base, err := normalizeBase(o.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	transport := o.Transport
	if transport == nil {
		tc, err := tlsConfig(o.Trust, base.Host)
		if err != nil {
			return nil, err
		}
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = tc
		transport = t
	}
	return &Client{
		base:         base,
		trust:        o.Trust,
		accessID:     o.AccessClientID,
		accessSecret: o.AccessClientSecret,
		trace:        o.Trace,
		http:         &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// applyAuth puts the right credential on a request: a token if we have one, the
// ticket cookie otherwise. One place, so no request can be built without one.
func (c *Client) applyAuth(req *http.Request) {
	if c.ticket != "" {
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: c.ticket})
		// Seulement sur les écritures : PVE ne réclame le jeton anti-CSRF que
		// là, et l'envoyer sur un GET ne ferait qu'ajouter du bruit à la trace.
		if c.csrf != "" && req.Method != http.MethodGet {
			req.Header.Set("CSRFPreventionToken", c.csrf)
		}
		return
	}
	// Ni ticket ni token : c'est l'échange initial contre /access/ticket, la
	// seule requête de toute la CLI qui n'a rien à présenter. Il faut alors
	// n'envoyer AUCUN en-tête d'authentification — poser « PVEAPIToken== », un
	// identifiant vide, ferait refuser par un 401 la requête même qui sert à
	// obtenir de quoi ne plus en recevoir.
	if c.tokenID == "" && c.secret == "" {
		return
	}
	req.Header.Set("Authorization", authHeader(c.tokenID, c.secret))
}

// Bootstrap is what `pvecli login` actually does once the ticket is in hand.
type Bootstrap struct {
	UserID  string // automation@pve
	TokenID string // pvectl — the name, without the user part
	Role    string // PVEAdmin, PVEAuditor…
	Path    string // "/" — where the ACL is attached
	Comment string
}

// BootstrapResult reports what was created and what already existed. Saying
// which is not cosmetic: re-running the command must be safe, and the operator
// needs to know whether a token was just minted or left alone.
type BootstrapResult struct {
	UserCreated  bool
	TokenCreated bool
	ACLSet       bool
	Secret       string // only ever returned, never written to disk
	FullTokenID  string // automation@pve!pvectl
}

// EnsureAutomationToken creates, idempotently, the identity the CLI will use.
//
// Idempotent with one hard exception, and it is not a limitation but a property
// of the node: PVE reveals a token's secret ONCE, at creation. If the token
// already exists, its secret is unrecoverable — so we say so rather than
// pretend, and leave the choice of rotating it to whoever runs the command.
func (c *Client) EnsureAutomationToken(ctx context.Context, b Bootstrap) (*BootstrapResult, error) {
	res := &BootstrapResult{FullTokenID: b.UserID + "!" + b.TokenID}

	if _, err := c.UserInfo(ctx, b.UserID); err != nil {
		// Sans mot de passe : cette identité ne porte que des tokens, elle n'a
		// aucune raison de pouvoir se connecter à l'interface web.
		if err := c.CreateUser(ctx, b.UserID, UserOptions{
			Comment: b.Comment,
		}); err != nil {
			return res, fmt.Errorf("création de %s : %w", b.UserID, err)
		}
		res.UserCreated = true
	}

	existing, err := c.Tokens(ctx, b.UserID)
	if err != nil {
		return res, fmt.Errorf("lecture des tokens de %s : %w", b.UserID, err)
	}
	found := false
	for _, t := range existing {
		if t.TokenID == b.TokenID {
			found = true
			break
		}
	}

	if !found {
		// Separated=false : le token porte les droits de son utilisateur. Avec
		// privsep=1 les privilèges effectifs sont l'INTERSECTION de ceux du
		// token et de ceux de l'utilisateur — et un token tout neuf n'a aucune
		// ACL à lui, donc aucun droit. C'est la cause du 403 inexplicable que
		// tout le monde rencontre une fois.
		nt, err := c.CreateToken(ctx, b.UserID, b.TokenID, TokenOptions{
			Comment:   b.Comment,
			Separated: false,
		})
		if err != nil {
			return res, fmt.Errorf("création du token %s : %w", res.FullTokenID, err)
		}
		res.Secret = nt.Value
		res.TokenCreated = true
	}

	// L'ACL va sur l'UTILISATEUR, et c'est le point qui décide si tout ceci
	// sert à quelque chose. Avec privsep=0, les privilèges effectifs du token
	// SONT ceux de son utilisateur, et ses propres ACL sont ignorées : une ACL
	// posée sur le seul token produirait un token parfaitement valide et
	// parfaitement sans droits, dont chaque appel rendrait 403.
	//
	// On la pose aussi sur le token, pour une raison précise : si quelqu'un
	// bascule un jour ce token en privsep=1, les droits effectifs deviennent
	// l'INTERSECTION des deux, et sans ACL côté token cette intersection est
	// vide. Deux appels ici évitent une panne incompréhensible plus tard.
	//
	// À partir d'ici le secret existe peut-être déjà. Ce qui suit peut échouer,
	// et un échec ne doit PAS emporter le secret avec lui : le nœud ne le
	// montre qu'une fois. On rend donc `res` même en erreur.
	if err := c.SetACL(ctx, ACLChange{
		Path:      b.Path,
		Role:      b.Role,
		User:      b.UserID,
		Propagate: true,
	}); err != nil {
		return res, fmt.Errorf("pose de l'ACL %s sur %s pour %s : %w", b.Role, b.Path, b.UserID, err)
	}
	if err := c.SetACL(ctx, ACLChange{
		Path:      b.Path,
		Role:      b.Role,
		Token:     res.FullTokenID,
		Propagate: true,
	}); err != nil {
		return res, fmt.Errorf("pose de l'ACL %s sur %s pour %s : %w", b.Role, b.Path, res.FullTokenID, err)
	}
	res.ACLSet = true

	return res, nil
}

// TicketLifetime is what PVE grants. Documented here because it is the reason
// nothing in this file is ever persisted.
const TicketLifetime = 2 * time.Hour
