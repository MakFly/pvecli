package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/MakFly/pvecli/internal/iac"
	"github.com/MakFly/pvecli/internal/pve"
)

// suggestIDHelp is the flag description shared by `vm declare` et
// `lxc declare` : --suggest-id est le SEUL chemin par lequel ces deux
// commandes construisent un pve.Client. Le reste de « declare » est
// entièrement hors-ligne, et c'est ce texte-là qui prévient l'opérateur que
// cette option-ci, elle, parle au nœud -- y compris sous --dry-run, qui pour
// tout le reste de la commande ne touche jamais le réseau.
const suggestIDHelp = "demande au nœud le prochain vmid libre (contacte l'API, y compris avec --dry-run — declare est hors-ligne par défaut sinon)"

// resolveSuggestedID transforme --suggest-id en un vmid proposé, ou refuse
// avant toute construction de client. Les trois refus miroitent le
// précédent d'internal/iac/declare.go (« --ip dhcp » + « --gateway ») :
// deux sources contradictoires pour un même champ ne se départagent pas
// silencieusement, elles s'arrêtent.
//
// vmidGiven est f.Changed("vmid") côté appelant -- --vmid et --suggest-id
// sont deux sources pour le même vmid, jamais une question de priorité.
//
// C'est le seul point d'appel de newClient() pour tout le chemin de
// création de « declare » : audit-le ici, nulle part ailleurs.
//
// decl est la déclaration DÉJÀ lue par l'appelant (pré-lecture) : /cluster/nextid
// ne connaît que ce qui existe sur le cluster, jamais ce que cette même
// commande est en train d'écrire à côté. Deux « declare --suggest-id »
// d'affilée, avant tout « iac apply », recevraient donc deux fois le même id
// si on s'arrêtait à la réponse du nœud -- silencieusement, puisque le nœud
// répond 200 dans les deux cas. Le nœud est donc consulté une fois, puis
// l'id est avancé LOCALEMENT (aucun appel réseau supplémentaire) tant qu'il
// est déjà tenu par un VM ou un LXC de cette même déclaration.
func resolveSuggestedID(cmd *cobra.Command, name string, suggestID, vmidGiven, remove, exists bool, decl *iac.Declaration) (int, error) {
	if !suggestID {
		return 0, nil
	}
	switch {
	case vmidGiven:
		return 0, &exitError{code: pve.ExitUsage,
			msg: "--vmid et --suggest-id posent tous les deux le même champ : choisis-en un seul"}
	case remove:
		return 0, &exitError{code: pve.ExitUsage,
			msg: "--suggest-id avec --remove n'a pas de sens : --remove ne crée rien à numéroter"}
	case exists:
		return 0, &exitError{code: pve.ExitUsage, msg: fmt.Sprintf(
			"« %s » est déjà dans la déclaration : --suggest-id ne renumérote pas un guest existant, ce n'est pas ce que fait cette option",
			name)}
	}

	client, err := newClient(cmd)
	if err != nil {
		return 0, err
	}
	proposed, err := client.NextID(cmd.Context())
	if err != nil {
		return 0, err
	}

	id := proposed
	for vmidClaimedLocally(decl, id) {
		id++
	}

	errW := cmd.ErrOrStderr()
	switch id {
	case proposed:
		// Le seul effet visible d'un appel réseau que « declare » fait
		// d'ordinaire jamais : l'opérateur ne doit jamais le découvrir après
		// coup, donc il est annoncé ici, avant que le vmid ne réapparaisse
		// dans le plan comme un champ « + » ordinaire.
		_, _ = fmt.Fprintf(errW, "nœud contacté (GET /cluster/nextid) : id proposé %d\n", id)
	default:
		// Le nœud ne voit que le cluster, pas la déclaration locale qu'on est
		// en train d'écrire à côté : sans cet ajustement, deux « declare
		// --suggest-id » de suite recevraient le même id et ne s'en
		// apercevraient qu'au « iac apply ». L'ajustement est dit en clair,
		// et sa limite aussi -- un id trouvé par incrément local n'a, lui,
		// jamais été confirmé par le nœud.
		_, _ = fmt.Fprintf(errW,
			"nœud contacté (GET /cluster/nextid) : id proposé %d, déjà revendiqué dans la déclaration locale — retenu %d à la place "+
				"(non revalidé par le nœud, il le sera au prochain « pvecli iac apply »)\n",
			proposed, id)
	}
	return id, nil
}

// vmidClaimedLocally dit si un vmid est déjà tenu par un VM ou un LXC de
// cette déclaration -- les deux espaces partagent le même compteur PVE, donc
// une collision côté LXC est tout aussi réelle qu'une collision côté VM.
func vmidClaimedLocally(decl *iac.Declaration, vmid int) bool {
	if decl == nil {
		return false
	}
	for _, vm := range decl.VMs {
		if vm.VMID == vmid {
			return true
		}
	}
	for _, ct := range decl.LXCs {
		if ct.VMID == vmid {
			return true
		}
	}
	return false
}
