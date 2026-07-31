package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MakFly/pvectl/internal/pve"
	"github.com/spf13/cobra"
)

// completionTTL is how long a cached inventory is reused.
//
// Ten seconds: long enough that hammering Tab costs one API call, short enough
// that a VM created thirty seconds ago is offered. The number is a compromise
// with no right answer — but a completion that opens an HTTPS connection at
// every keystroke is a completion people turn off.
const completionTTL = 10 * time.Second

// completionTimeout caps how long a Tab may block.
//
// A shell that freezes for thirty seconds because a node is unreachable is
// worse than no completion at all. This budget is deliberately far below the
// client's own --timeout: the operator is waiting with a finger on the key.
const completionTimeout = 2 * time.Second

func newCompletionHelpCmd(root *cobra.Command) {
	// Cobra generates the `completion` command itself; what it does not
	// generate is the paragraph explaining where the file goes.
	root.InitDefaultCompletionCmd()
	c, _, err := root.Find([]string{"completion"})
	if err != nil {
		return
	}

	c.Short = "Génère le script de complétion du shell"
	c.Long = `Génère le script de complétion pour bash, zsh, fish ou powershell.

La complétion de cette CLI est DYNAMIQUE : elle interroge « GET /cluster/resources »
pour proposer les VMID existants avec leur nom, les nœuds, les stockages, les
pools et les tags. C'est ce qui fait qu'on cesse d'ouvrir l'interface web « juste
pour retrouver l'ID ».

  · la réponse est mise en cache ` + completionTTL.String() + ` : marteler Tab ne martèle pas l'API ;
  · si le nœud est injoignable, la complétion se tait et rend la main. Elle
    n'affiche jamais d'erreur et ne bloque jamais le shell — au pire, elle ne
    propose rien.

INSTALLATION

  zsh
    pvectl completion zsh > "${fpath[1]}/_pvectl" && exec zsh

    Si la complétion n'est pas déjà active, ajoute d'abord à ~/.zshrc :
      autoload -U compinit && compinit

  bash
    pvectl completion bash > /usr/local/etc/bash_completion.d/pvectl   # macOS
    pvectl completion bash > /etc/bash_completion.d/pvectl             # Linux

  fish
    pvectl completion fish > ~/.config/fish/completions/pvectl.fish

  powershell
    pvectl completion powershell | Out-String | Invoke-Expression`
}

// ---------------------------------------------------------------- inventory

// completionInventory is the slice of the cluster the completion needs. It is
// what gets cached, not the raw answer: a completion has no use for CPU ratios
// and reading a smaller file is the point of caching at all.
type completionInventory struct {
	Fetched  time.Time     `json:"fetched"`
	Guests   []guestChoice `json:"guests"`
	Nodes    []string      `json:"nodes"`
	Storages []string      `json:"storages"`
	Pools    []string      `json:"pools"`
	Tags     []string      `json:"tags"`
}

type guestChoice struct {
	VMID int    `json:"vmid"`
	Name string `json:"name"`
	Type string `json:"type"`
	Node string `json:"node"`
}

// completionCandidates is the single entry point of every completion function.
//
// It NEVER returns an error and never writes anything: a completion that
// reports a problem corrupts the command line the operator is typing. A node
// that is down, a missing token, an expired ACL — all of them mean the same
// thing here, which is "propose nothing".
func completionCandidates(cmd *cobra.Command, pick func(completionInventory) []string) ([]string, cobra.ShellCompDirective) {
	inv, err := loadCompletionInventory(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return pick(*inv), cobra.ShellCompDirectiveNoFileComp
}

// loadCompletionInventory serves the cache when it is fresh, and refreshes it
// otherwise.
func loadCompletionInventory(cmd *cobra.Command) (*completionInventory, error) {
	eff, err := resolveConfig(cmd)
	if err != nil {
		return nil, err
	}
	path := completionCachePath(eff.Endpoint, eff.TokenID)

	if inv, err := readCompletionCache(path); err == nil {
		return inv, nil
	}

	inv, err := fetchCompletionInventory(cmd, eff.Endpoint, eff.TokenID, eff.TokenSecret, eff.Insecure, eff.TLS.Fingerprint, eff.TLS.CAFile)
	if err != nil {
		return nil, err
	}
	writeCompletionCache(path, inv)
	return inv, nil
}

// fetchCompletionInventory builds its own client rather than reusing
// newClient.
//
// newClient prints the --insecure warning on stderr at every call (cmd/client.go).
// That line is right for a command and catastrophic for a completion: it would
// land in the middle of the prompt the operator is typing. Here stderr goes to
// io.Discard, and the trace is never enabled.
func fetchCompletionInventory(cmd *cobra.Command, endpoint, tokenID, secret string, insecure bool, fingerprint, caFile string) (*completionInventory, error) {
	client, err := pve.New(pve.Options{
		Endpoint: endpoint,
		TokenID:  tokenID,
		Secret:   secret,
		Timeout:  completionTimeout,
		Trust: pve.TrustOptions{
			Fingerprint: fingerprint,
			CAFile:      caFile,
			Insecure:    insecure,
		},
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
	defer cancel()

	resources, err := client.Resources(ctx, "")
	if err != nil {
		return nil, err
	}

	inv := &completionInventory{Fetched: time.Now()}
	seenNode, seenTag, seenPool := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for _, r := range resources {
		switch r.Type {
		case "qemu", "lxc":
			inv.Guests = append(inv.Guests, guestChoice{
				VMID: r.VMID, Name: r.Name, Type: r.Type, Node: r.Node,
			})
		case "storage":
			inv.Storages = append(inv.Storages, r.Storage)
		case "node":
			if r.Node != "" && !seenNode[r.Node] {
				seenNode[r.Node] = true
				inv.Nodes = append(inv.Nodes, r.Node)
			}
		}
		// A pool entry carries its own id in `pool`; a guest carries the pool
		// it belongs to in the SAME field. Reading both means a pool shows up
		// even when the caller cannot see the pool resource itself.
		if r.Pool != "" && !seenPool[r.Pool] {
			seenPool[r.Pool] = true
			inv.Pools = append(inv.Pools, r.Pool)
		}
		for _, tag := range strings.Split(r.Tags, ";") {
			tag = strings.TrimSpace(tag)
			if tag != "" && !seenTag[tag] {
				seenTag[tag] = true
				inv.Tags = append(inv.Tags, tag)
			}
		}
	}

	sort.Slice(inv.Guests, func(i, j int) bool { return inv.Guests[i].VMID < inv.Guests[j].VMID })
	sort.Strings(inv.Storages)
	sort.Strings(inv.Nodes)
	sort.Strings(inv.Pools)
	sort.Strings(inv.Tags)
	return inv, nil
}

// completionCachePath keys the cache by endpoint AND identity.
//
// Two contexts pointing at two clusters must not see each other's VMID, and a
// token whose ACL was narrowed must not keep offering what it can no longer
// read. Hashing keeps a token id out of a filename.
func completionCachePath(endpoint, tokenID string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + tokenID))
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "pvectl", "completion-"+hex.EncodeToString(sum[:8])+".json")
}

func readCompletionCache(path string) (*completionInventory, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv completionInventory
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, err
	}
	if time.Since(inv.Fetched) > completionTTL {
		return nil, fmt.Errorf("cache expiré")
	}
	return &inv, nil
}

// writeCompletionCache is best-effort: a cache that cannot be written is a
// slower completion, not a broken one.
func writeCompletionCache(path string, inv *completionInventory) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(inv)
	if err != nil {
		return
	}
	// 0600 even though the file holds no secret: it holds the shape of an
	// infrastructure, and it is keyed by an identity.
	_ = os.WriteFile(path, raw, 0o600)
}

// ---------------------------------------------------------------- completers

// completer is the signature Cobra wants, for a flag or for an argument.
type completer func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)

// firstArgOnly restricts a completer to the FIRST positional argument.
//
// It exists because the same function serves two jobs whose notion of `args`
// differs: as a ValidArgsFunction, args holds the positional arguments already
// typed, so a non-empty args means the <vmid> slot is filled. As a flag
// completer, args holds those same positional arguments — which are none of the
// flag's business. Wrapping only the positional use is what keeps
// « vm clone 9001 --pool <Tab> » from being silenced by the vmid that precedes
// it.
func firstArgOnly(c completer) completer {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return c(cmd, args, toComplete)
	}
}

// completeGuests offers the VMID of one family — or of both — with the guest's
// name as the description, which is the whole reason to complete a number.
func completeGuests(kind pve.GuestType) completer {
	return func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return completionCandidates(cmd, func(inv completionInventory) []string {
			var out []string
			for _, g := range inv.Guests {
				if kind != "" && g.Type != string(kind) {
					continue
				}
				label := strconv.Itoa(g.VMID)
				if g.Name != "" {
					// Cobra splits a candidate on \t: what follows is shown as
					// a description and never inserted on the command line.
					label += "\t" + g.Name
				}
				out = append(out, label)
			}
			return out
		})
	}
}

func completeNodes(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionCandidates(cmd, func(inv completionInventory) []string { return inv.Nodes })
}

func completeStorages(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionCandidates(cmd, func(inv completionInventory) []string { return inv.Storages })
}

func completePools(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionCandidates(cmd, func(inv completionInventory) []string { return inv.Pools })
}

func completeTags(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completionCandidates(cmd, func(inv completionInventory) []string { return inv.Tags })
}

// ---------------------------------------------------------------- wiring

// wireCompletion walks the built tree and attaches the dynamic completers.
//
// Walking the tree rather than annotating each constructor is what keeps this
// from rotting: a command added later that takes a <vmid> is served without
// anyone remembering to wire it, because the rule is stated once — the shape of
// the Use line decides.
func wireCompletion(root *cobra.Command) {
	newCompletionHelpCmd(root)

	_ = root.RegisterFlagCompletionFunc("node", completeNodes)

	var walk func(c *cobra.Command, family pve.GuestType)
	walk = func(c *cobra.Command, family pve.GuestType) {
		switch c.Name() {
		case "vm":
			family = pve.TypeQEMU
		case "lxc":
			family = pve.TypeLXC
		}

		if c.ValidArgsFunction == nil && c.Runnable() {
			switch {
			case strings.Contains(c.Use, "<vmid>"):
				c.ValidArgsFunction = firstArgOnly(completeGuests(family))
			case strings.Contains(c.Use, "<storage>"):
				c.ValidArgsFunction = firstArgOnly(completeStorages)
			case strings.Contains(c.Use, "<poolid>"):
				c.ValidArgsFunction = firstArgOnly(completePools)
			}
		}

		for name, fn := range map[string]completer{
			"target":  completeNodes,
			"storage": completeStorages,
			"pool":    completePools,
			"tag":     completeTags,
			"tags":    completeTags,
		} {
			if c.Flags().Lookup(name) != nil {
				_ = c.RegisterFlagCompletionFunc(name, fn)
			}
		}

		for _, sub := range c.Commands() {
			walk(sub, family)
		}
	}
	walk(root, "")
}
