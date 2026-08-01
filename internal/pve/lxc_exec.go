package pve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/coder/websocket"
)

// LXCExecResult is what a command left behind once its container shell finished.
type LXCExecResult struct {
	ExitCode  int
	Output    string
	Truncated bool
}

// maxExecOutput bornes ce qu'on garde d'un exec : un `apt-get` bavard ne doit
// pas faire gonfler la mémoire sans limite. Au-delà, on coupe et on le dit.
const maxExecOutput = 8 << 20 // 8 Mio

// Un conteneur LXC n'a pas d'agent invité : là où QEMU offre `agent/exec`, PVE
// n'expose côté LXC aucun exec REST. Le seul canal vers l'intérieur est la
// console. On l'emprunte : `termproxy` fabrique un ticket + un port, puis
// `vncwebsocket` ouvre un PTY par-dessus. On y tape une ligne de shell.
//
// Ce n'est pas `vm agent exec` : derrière il y a un VRAI terminal, pas un
// execve. Deux conséquences qu'on neutralise ici :
//   - le PTY renvoie en écho ce qu'on tape → `stty -echo` dès la première ligne ;
//   - il n'y a pas de code de retour porté par le protocole → on l'imprime
//     nous-mêmes entre deux sentinelles, et on le relit dans le flux.
//
// Le script est passé en base64 : quels que soient ses guillemets, ses sauts de
// ligne ou son shell, il traverse la ligne unique sans se faire déchirer.
func (c *Client) LXCExec(ctx context.Context, node string, vmid int, script string) (*LXCExecResult, error) {
	if strings.TrimSpace(script) == "" {
		return nil, fmt.Errorf("aucune commande à exécuter")
	}

	// 1. termproxy : un ticket à usage unique et le port de la console. PVE
	//    sérialise ce port tantôt en nombre, tantôt en chaîne selon la version —
	//    d'où le RawMessage puis un parse tolérant, comme pour out-truncated.
	var tp struct {
		Ticket string          `json:"ticket"`
		Port   json.RawMessage `json:"port"`
		User   string          `json:"user"`
	}
	if err := c.post(ctx, epLXCTermproxy, []string{node, strconv.Itoa(vmid)}, nil, &tp); err != nil {
		return nil, err
	}
	port, perr := strconv.Atoi(strings.Trim(string(tp.Port), `"`))
	if tp.Ticket == "" || perr != nil || port == 0 {
		return nil, fmt.Errorf("termproxy n'a rendu ni ticket ni port exploitable — la console n'a pas pu s'ouvrir")
	}

	// 2. vncwebsocket : le PTY. Même hôte, même TLS épinglé, même token que le
	//    reste de la CLI — on réutilise le transport et l'auth du client.
	wsURL := c.wsURL(epLXCVNCWebsocket.Path(node, strconv.Itoa(vmid)), port, tp.Ticket)

	// coder/websocket veut un client SANS timeout : le Timeout d'un http.Client
	// pose une échéance sur la connexion détournée et couperait le PTY en plein
	// milieu. On garde le transport (donc la TLS épinglée), on retire l'horloge.
	wsClient := &http.Client{Transport: c.http.Transport}

	hdr := http.Header{}
	if probe, err := http.NewRequest(http.MethodGet, wsURL, nil); err == nil {
		c.applyAuth(probe)
		c.setAccessHeaders(probe)
		hdr = probe.Header
	}

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: wsClient,
		HTTPHeader: hdr,
	})
	if err != nil {
		return nil, fmt.Errorf("ouverture de la console LXC %d : %w", vmid, err)
	}
	conn.SetReadLimit(maxExecOutput)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// 3. Authentification du websocket : premier message « user:ticket ». La
	//    console PVE attend des frames TEXTE (xterm.js fait socket.send(string)).
	if err := conn.Write(ctx, websocket.MessageText, []byte(tp.User+":"+tp.Ticket+"\n")); err != nil {
		return nil, fmt.Errorf("authentification de la console : %w", err)
	}
	// L'accusé « OK » et la bannière du getty arrivent souvent dans la MÊME
	// frame : les lire séparément consommerait le prompt de login et laisserait
	// la suite attendre à vide. On ne lit donc pas l'ack à part — consoleLogin
	// draine tout d'un bloc (OK + bannière + « login: »).

	// 3bis. La console d'un LXC, ce n'est pas un shell : c'est le `getty` du
	//    conteneur, donc un prompt de login. Il faut s'y authentifier avant de
	//    pouvoir taper quoi que ce soit. On draine jusqu'au prompt et, si c'est
	//    bien un login, on envoie l'identité et le mot de passe (env, comme le
	//    secret du token — jamais un flag). Un conteneur en autologin tombe
	//    directement sur un « # » : on saute alors cette étape.
	if err := c.consoleLogin(ctx, conn); err != nil {
		return nil, err
	}

	// 4. La ligne de commande. Les marqueurs sont FABRIQUÉS par printf (« %s » +
	//    argument) : la ligne tapée — renvoyée en écho avant que `stty -echo`
	//    n'agisse — ne contient donc jamais la chaîne « __PVE_BEGIN__ » ni
	//    « __PVE_RC_… » littérale ; seule la SORTIE les contient. On peut donc
	//    découper sans confondre l'écho et le résultat.
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	line := "stty -echo 2>/dev/null; printf '__PVE_%s__\\n' BEGIN; " +
		"printf %s '" + b64 + "' | base64 -d | sh; __rc=$?; " +
		"printf '__PVE_%s_%s__\\n' RC \"$__rc\"\n"
	if err := c.wsSend(ctx, conn, line); err != nil {
		return nil, fmt.Errorf("envoi de la commande : %w", err)
	}

	// 5. On lit le flux jusqu'au marqueur de fin (qui porte le code retour).
	rcRe := regexp.MustCompile(`__PVE_RC_(\d+)__`)
	debug := os.Getenv("PVECLI_WS_DEBUG") != ""
	var buf []byte
	truncated := false
	for {
		mt, data, rerr := conn.Read(ctx)
		if debug {
			fmt.Fprintf(os.Stderr, "[ws] type=%v err=%v frame=%q\n", mt, rerr, string(data))
		}
		if len(data) > 0 {
			buf = append(buf, data...)
		}
		if rcRe.Find(buf) != nil {
			break
		}
		if len(buf) > maxExecOutput {
			truncated = true
			break
		}
		if rerr != nil {
			if rcRe.Find(buf) != nil {
				break
			}
			return nil, fmt.Errorf("lecture de la console interrompue avant la fin : %w", rerr)
		}
	}

	out, code := parseExecStream(buf, rcRe)
	return &LXCExecResult{ExitCode: code, Output: out, Truncated: truncated}, nil
}

// consoleLogin franchit le prompt getty du conteneur. Il draine la bannière
// jusqu'à voir « login: » (getty) ou déjà un « # » (autologin). Dans le premier
// cas, il envoie root puis le mot de passe lu dans PVE_LXC_PASSWORD.
func (c *Client) consoleLogin(ctx context.Context, conn *websocket.Conn) error {
	debug := os.Getenv("PVECLI_WS_DEBUG") != ""
	// La console d'un LXC ne flushe sa sortie qu'après avoir reçu une entrée :
	// un getty au repos n'envoie rien tant qu'on n'a pas frappé une touche. On
	// pousse donc un saut de ligne pour faire (ré)afficher son prompt.
	if err := c.wsSend(ctx, conn, "\n"); err != nil {
		return err
	}
	_, hit, err := c.readUntil(ctx, conn, []string{"login:", "# ", "$ "})
	if debug {
		fmt.Fprintf(os.Stderr, "[login] hit=%q err=%v\n", hit, err)
	}
	if err != nil {
		return fmt.Errorf("la console ne présente ni login ni shell : %w", err)
	}
	if hit != "login:" {
		return nil // autologin : on est déjà dans un shell
	}

	pw := os.Getenv("PVE_LXC_PASSWORD")
	if pw == "" {
		return fmt.Errorf("la console du conteneur attend un login mais PVE_LXC_PASSWORD est vide\n\n" +
			"  export PVE_LXC_PASSWORD=\"…\"\n\n" +
			"Le mot de passe root du conteneur — jamais un flag (visible dans « ps »). " +
			"Un conteneur créé sans mot de passe n'a pas de console utilisable : crée-le " +
			"avec « lxc create … --password-stdin », ou active l'autologin sur la console.")
	}

	user := os.Getenv("PVE_LXC_USER")
	if user == "" {
		user = "root"
	}
	if err := c.wsSend(ctx, conn, user+"\n"); err != nil {
		return err
	}
	if _, _, err := c.readUntil(ctx, conn, []string{"assword"}); err != nil {
		return fmt.Errorf("le prompt de mot de passe n'est jamais venu : %w", err)
	}
	if err := c.wsSend(ctx, conn, pw+"\n"); err != nil {
		return err
	}
	_, hit2, err := c.readUntil(ctx, conn, []string{"# ", "$ ", "ncorrect", "ailure"})
	if debug {
		fmt.Fprintf(os.Stderr, "[login] after-pw hit=%q err=%v\n", hit2, err)
	}
	if err != nil {
		return fmt.Errorf("le shell n'est jamais apparu après le mot de passe : %w", err)
	}
	if hit2 == "ncorrect" || hit2 == "ailure" {
		return fmt.Errorf("login refusé par le conteneur — vérifie PVE_LXC_PASSWORD")
	}
	return nil
}

// readUntil lit le flux jusqu'à croiser l'une des sous-chaînes, et rend celle
// qui a été vue. Sert à synchroniser sur les prompts d'un vrai terminal.
func (c *Client) readUntil(ctx context.Context, conn *websocket.Conn, subs []string) (string, string, error) {
	var buf []byte
	for {
		_, data, rerr := conn.Read(ctx)
		if len(data) > 0 {
			buf = append(buf, data...)
		}
		for _, s := range subs {
			if strings.Contains(string(buf), s) {
				return string(buf), s, nil
			}
		}
		if rerr != nil {
			return string(buf), "", rerr
		}
		if len(buf) > maxExecOutput {
			return string(buf), "", fmt.Errorf("trop de sortie avant le prompt attendu")
		}
	}
}

// wsURL bâtit l'URL wss à partir de la base du client (schéma https → wss,
// même hôte, même préfixe /api2/json), avec le port et le ticket de termproxy.
func (c *Client) wsURL(path string, port int, ticket string) string {
	u := *c.base
	u.Scheme = "wss"
	u.Path += path
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("vncticket", ticket)
	u.RawQuery = q.Encode()
	return u.String()
}

// wsSend émet une frame d'entrée au format attendu par la console PVE :
// « 0:<longueur en octets>:<données> ».
func (c *Client) wsSend(ctx context.Context, conn *websocket.Conn, data string) error {
	frame := "0:" + strconv.Itoa(len(data)) + ":" + data
	return conn.Write(ctx, websocket.MessageText, []byte(frame))
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

// parseExecStream isole la sortie réelle entre le marqueur de début et le code
// retour, puis nettoie l'habillage terminal (séquences ANSI, retours chariot).
func parseExecStream(buf []byte, rcRe *regexp.Regexp) (string, int) {
	s := string(buf)

	code := 0
	if m := rcRe.FindStringSubmatch(s); m != nil {
		code, _ = strconv.Atoi(m[1])
		s = s[:strings.Index(s, m[0])]
	}
	if i := strings.Index(s, "__PVE_BEGIN__"); i >= 0 {
		s = s[i+len("__PVE_BEGIN__"):]
	}

	s = ansiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.Trim(s, "\n"), code
}
