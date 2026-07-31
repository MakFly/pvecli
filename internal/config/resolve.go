package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

// Effective is the configuration a command actually runs with, once the four
// layers have been collapsed.
type Effective struct {
	ContextName string
	Endpoint    string
	TokenID     string
	TokenSecret string
	Node        string
	Insecure    bool
	TLS         TLS
	IaC         IaC

	// Sources maps a field name to the layer that won it: "flag --endpoint",
	// "env PVE_API_URL", "fichier" or "défaut". A layered configuration that
	// cannot say where a value came from is a configuration you debug by
	// guessing.
	Sources map[string]string
}

// layer is one candidate value and the name of the layer that produced it.
type layer struct {
	value  string
	source string
}

// pick applies the precedence of PRD §7.1: flags > environment > file >
// defaults. The first layer that provides a value wins.
//
// A flag counts as provided when it was explicitly set, not when it is
// non-empty: `--endpoint ""` is a deliberate choice, and must beat the file
// like any other flag. Hence Changed() rather than a string test.
func pick(fl *pflag.FlagSet, flagName, envName, fileValue, def string) layer {
	if fl != nil && flagName != "" && fl.Changed(flagName) {
		v, err := fl.GetString(flagName)
		if err == nil {
			return layer{v, "flag --" + flagName}
		}
	}
	if envName != "" {
		if v, ok := os.LookupEnv(envName); ok && v != "" {
			return layer{v, "env " + envName}
		}
	}
	if fileValue != "" {
		return layer{fileValue, "fichier"}
	}
	return layer{def, "défaut"}
}

// Resolve collapses flags, environment and file into the configuration a
// command runs with. fl may be nil, which skips the flag layer.
func Resolve(fl *pflag.FlagSet, f *File) (*Effective, error) {
	e := &Effective{Sources: map[string]string{}}

	name := pick(fl, "context", "", f.CurrentContext, "")
	e.ContextName, e.Sources["contexte"] = name.value, name.source

	// A context named but absent is almost always a typo. Falling back to an
	// empty context would produce a confusing "endpoint manquant" three
	// stories later, far from the cause.
	c := f.Contexts[e.ContextName]
	if c == nil {
		if e.ContextName != "" && len(f.Contexts) > 0 {
			known := make([]string, 0, len(f.Contexts))
			for k := range f.Contexts {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("contexte %q introuvable ; contextes disponibles : %s",
				e.ContextName, strings.Join(known, ", "))
		}
		c = &Context{}
	}

	set := func(key string, l layer) string {
		e.Sources[key] = l.source
		return l.value
	}
	e.Endpoint = set("endpoint", pick(fl, "endpoint", EnvEndpoint, c.Endpoint, ""))
	e.TokenID = set("token_id", pick(fl, "token-id", EnvTokenID, c.TokenID, ""))
	e.Node = set("node", pick(fl, "node", "", c.Node, ""))
	e.TLS.Fingerprint = set("tls.fingerprint", pick(fl, "", "", c.TLS.Fingerprint, ""))
	e.TLS.CAFile = set("tls.ca_file", pick(fl, "", "", c.TLS.CAFile, ""))

	// --terraform-dir and --ansible-dir are declared by the `iac` commands only,
	// so pick() is handed a flag name that most command lines do not have. It
	// tolerates that: an absent flag is simply a layer that does not answer.
	e.IaC.TerraformDir = set("iac.terraform_dir", pick(fl, "terraform-dir", "", c.IaC.TerraformDir, ""))
	e.IaC.AnsibleDir = set("iac.ansible_dir", pick(fl, "ansible-dir", "", c.IaC.AnsibleDir, ""))
	e.IaC.ManagedTag = set("iac.managed_tag", pick(fl, "", "", c.IaC.ManagedTag, DefaultManagedTag))

	if fl != nil && fl.Changed("insecure") {
		b, _ := fl.GetBool("insecure")
		e.Insecure, e.Sources["insecure"] = b, "flag --insecure"
	} else if v, ok := os.LookupEnv(EnvInsecure); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s=%q n'est pas un booléen (0/1, true/false)", EnvInsecure, v)
		}
		e.Insecure, e.Sources["insecure"] = b, "env "+EnvInsecure
	} else if c.Insecure {
		e.Insecure, e.Sources["insecure"] = true, "fichier"
	} else {
		e.Sources["insecure"] = "défaut"
	}

	// The token secret has exactly one source: the environment.
	//
	// Not the file — Load refuses it there. Not a flag either: a flag value is
	// visible in `ps` to every user of the machine, and lands in the shell
	// history. This omission is the design, not a gap to fill later.
	e.TokenSecret = os.Getenv(EnvTokenSecret)

	return e, nil
}
