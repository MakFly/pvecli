package pve

import (
	"regexp"
	"testing"
)

// parseExecStream doit isoler la sortie réelle du bruit d'un vrai terminal :
// l'écho de la ligne tapée, la bannière, les séquences ANSI (bracketed paste),
// les retours chariot — et lire le code retour dans le marqueur de fin.
func TestParseExecStream(t *testing.T) {
	rcRe := regexp.MustCompile(`__PVE_RC_(\d+)__`)

	cases := []struct {
		name     string
		raw      string
		wantOut  string
		wantCode int
	}{
		{
			name:     "sortie propre entre marqueurs",
			raw:      "__PVE_BEGIN__\nHELLO\nroot\n__PVE_RC_0__\n",
			wantOut:  "HELLO\nroot",
			wantCode: 0,
		},
		{
			name: "écho de la commande + ANSI + CR, puis la vraie sortie",
			raw: "printf %s 'ZWNobw' | base64 -d | sh\r\n\x1b[?2004l\r" +
				"__PVE_BEGIN__\r\r\nHELLO\r\r\nroot\r\r\n__PVE_RC_0__\r\r\n\x1b[?2004hroot@infra-01:~# ",
			wantOut:  "HELLO\nroot",
			wantCode: 0,
		},
		{
			name:     "code retour non nul",
			raw:      "__PVE_BEGIN__\nls: cannot access '/x': No such file or directory\n__PVE_RC_2__\n",
			wantOut:  "ls: cannot access '/x': No such file or directory",
			wantCode: 2,
		},
		{
			name:     "sortie vide, code nul",
			raw:      "__PVE_BEGIN__\n__PVE_RC_0__\n",
			wantOut:  "",
			wantCode: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := parseExecStream([]byte(tc.raw), rcRe)
			if out != tc.wantOut {
				t.Errorf("sortie = %q, voulu %q", out, tc.wantOut)
			}
			if code != tc.wantCode {
				t.Errorf("code = %d, voulu %d", code, tc.wantCode)
			}
		})
	}
}
