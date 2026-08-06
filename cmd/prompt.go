package cmd

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dev-toolings/pvecli/internal/pve"
)

// promptString asks a question on stderr and reads one line from stdin. An
// empty answer keeps def.
//
// Callers check stdinIsTerminal() first, the same rule pickServices already
// follows: a pipeline must never block on a keystroke, so this is only ever
// reached when there is a human on the other end to answer.
func promptString(cmd *cobra.Command, question, def string) (string, error) {
	suffix := ""
	if def != "" {
		suffix = fmt.Sprintf(" [%s]", def)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s%s : ", question, suffix)

	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return "", &exitError{code: pve.ExitConfirm, msg: "saisie interrompue"}
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

// promptInt is for fields with no sane default -- vmid, cores, memory. There
// is nothing useful an empty answer could mean, so it just asks again rather
// than silently accepting a 0 that Validate would reject two steps later.
func promptInt(cmd *cobra.Command, question string, def int) (int, error) {
	defStr := ""
	if def != 0 {
		defStr = strconv.Itoa(def)
	}
	for {
		s, err := promptString(cmd, question, defStr)
		if err != nil {
			return 0, err
		}
		if s == "" {
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  « %s » n'est pas un nombre entier.\n", s)
			continue
		}
		return n, nil
	}
}

// promptIntOptional is for fields where 0 is a real, meaningful answer --
// "leave it, the module's shared default applies". Unlike promptInt it never
// loops: an empty answer IS the answer.
func promptIntOptional(cmd *cobra.Command, question string, def int) (int, error) {
	defStr := ""
	if def != 0 {
		defStr = strconv.Itoa(def)
	}
	s, err := promptString(cmd, question, defStr)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, &exitError{code: pve.ExitUsage, msg: fmt.Sprintf("« %s » n'est pas un nombre entier", s)}
	}
	return n, nil
}

// promptBool asks a yes/no question.
func promptBool(cmd *cobra.Command, question string, def bool) (bool, error) {
	defStr := "o"
	if !def {
		defStr = "n"
	}
	for {
		s, err := promptString(cmd, question+" (o/n)", defStr)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "o", "oui", "y", "yes":
			return true, nil
		case "n", "non", "no":
			return false, nil
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "  réponds « o » ou « n ».")
	}
}
