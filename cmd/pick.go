package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/MakFly/pvecli/internal/catalog"
	"github.com/MakFly/pvecli/internal/pve"
)

// pickServices asks which services to install.
//
// Interactive only. A pipeline must never block on a keystroke: without a
// terminal the command refuses and names the flag that would have answered for
// it. A prompt that silently times out, or one that defaults to "none", both
// produce a VM nobody asked for.
func pickServices(cmd *cobra.Command, cat *catalog.Catalog) ([]string, error) {
	if !stdinIsTerminal() {
		return nil, &exitError{
			code: pve.ExitUsage,
			msg: "aucun service demandé et pas de terminal pour en proposer la liste.\n" +
				"Précise-les : --with " + strings.Join(cat.IDs(), ",") + "\n" +
				"Ou, pour une VM sans service : --with ''",
		}
	}
	return runPicker(cmd.ErrOrStderr(), os.Stdin, cat.Services)
}

// runPicker draws a checklist and returns the chosen ids.
//
// Written against golang.org/x/term rather than pulling in a TUI framework:
// this binary has four dependencies and ships static, and a checklist is a
// hundred lines. The drawing goes to stderr, like every other prompt, so
// `-o json` on stdout stays pipeable.
func runPicker(w io.Writer, in *os.File, services []catalog.Service) ([]string, error) {
	if len(services) == 0 {
		return nil, nil
	}

	fd := int(in.Fd())
	restore, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("passage du terminal en mode brut : %w", err)
	}
	defer func() { _ = term.Restore(fd, restore) }()

	chosen := make([]bool, len(services))
	cursor := 0
	width := 0
	for _, s := range services {
		if len(s.ID) > width {
			width = len(s.ID)
		}
	}

	draw := func(first bool) {
		if !first {
			// Back to the top of the list, then wipe what follows.
			_, _ = fmt.Fprintf(w, "\x1b[%dA\x1b[J", len(services))
		}
		for i, s := range services {
			mark, arrow := " ", " "
			if chosen[i] {
				mark = "x"
			}
			if i == cursor {
				arrow = ">"
			}
			_, _ = fmt.Fprintf(w, "%s [%s] %-*s  %s\r\n", arrow, mark, width, s.ID, s.Summary)
		}
	}

	_, _ = fmt.Fprint(w, "\r\nServices à installer — ↑↓ pour naviguer, espace pour cocher, "+
		"entrée pour valider, q pour annuler\r\n\r\n")
	draw(true)

	buf := make([]byte, 3)
	for {
		n, err := in.Read(buf)
		if err != nil || n == 0 {
			return nil, &exitError{code: pve.ExitConfirm, msg: "sélection interrompue"}
		}

		switch {
		case n >= 3 && buf[0] == 0x1b && buf[1] == '[' && buf[2] == 'A': // ↑
			cursor = (cursor - 1 + len(services)) % len(services)
		case n >= 3 && buf[0] == 0x1b && buf[1] == '[' && buf[2] == 'B': // ↓
			cursor = (cursor + 1) % len(services)
		case buf[0] == 'k':
			cursor = (cursor - 1 + len(services)) % len(services)
		case buf[0] == 'j':
			cursor = (cursor + 1) % len(services)
		case buf[0] == ' ':
			chosen[cursor] = !chosen[cursor]
		case buf[0] == 'a':
			all := true
			for _, c := range chosen {
				all = all && c
			}
			for i := range chosen {
				chosen[i] = !all
			}
		case buf[0] == '\r' || buf[0] == '\n':
			draw(false)
			var out []string
			for i, c := range chosen {
				if c {
					out = append(out, services[i].ID)
				}
			}
			_, _ = fmt.Fprintf(w, "\r\n%s\r\n\r\n", selectionSummary(out))
			return out, nil
		case buf[0] == 'q' || buf[0] == 0x03 || buf[0] == 0x1b && n == 1: // q, Ctrl-C, Esc
			_, _ = fmt.Fprint(w, "\r\n")
			return nil, &exitError{code: pve.ExitConfirm, msg: "sélection annulée — rien n'a été déclaré"}
		default:
			continue
		}
		draw(false)
	}
}

func selectionSummary(ids []string) string {
	if len(ids) == 0 {
		return "aucun service — la VM sera nue"
	}
	return "services retenus : " + strings.Join(ids, ", ")
}
