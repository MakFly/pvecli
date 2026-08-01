package pve

import "strings"

// endpoint is one API path this client knows how to call, in the
// {placeholder} form that docs/API-MAP.md uses.
//
// Paths are never written inline at the call site. That is not style: it is
// what makes the rule of PRD §6.3 — "no endpoint written from memory" —
// checkable by a test instead of by good intentions. TestNoInlineEndpoint
// fails on any string literal handed to Get/Post, and TestAPIMapCoverage fails
// on any endpoint below that API-MAP.md does not document.
type endpoint struct {
	Method  string
	Pattern string
}

// greedyPlaceholder names the placeholders that swallow the rest of the path,
// slashes included.
//
// {volume} is the only one: a volid is written "local:iso/debian.iso" and PVE
// splits it itself, so the slash must reach the node as a slash. Escaping it
// to %2F earns « unable to parse directory volume name 'iso%2Fdebian.iso' » —
// a 500 that blames the name rather than the encoding, which is exactly why
// it took a real round trip against the lab to find (PVX-051).
//
// The rule lives with the placeholder rather than with the endpoint because
// that is where it is true: any endpoint taking a {volume} needs it.
var greedyPlaceholder = map[string]bool{"{volume}": true, "{cidr}": true}

var (
	epVersion     = endpoint{"GET", "/version"}
	epNodes       = endpoint{"GET", "/nodes"}
	epNodeStatus  = endpoint{"GET", "/nodes/{node}/status"}
	epClusterStat = endpoint{"GET", "/cluster/status"}
	epPermissions = endpoint{"GET", "/access/permissions"}
	epClusterRes  = endpoint{"GET", "/cluster/resources"}

	epUsers       = endpoint{"GET", "/access/users"}
	epUserCreate  = endpoint{"POST", "/access/users"}
	epUser        = endpoint{"GET", "/access/users/{userid}"}
	epRoles       = endpoint{"GET", "/access/roles"}
	epRole        = endpoint{"GET", "/access/roles/{roleid}"}
	epACL         = endpoint{"GET", "/access/acl"}
	epACLUpdate   = endpoint{"PUT", "/access/acl"}
	epTokens      = endpoint{"GET", "/access/users/{userid}/token"}
	epTokenCreate = endpoint{"POST", "/access/users/{userid}/token/{tokenid}"}
	epTokenDelete = endpoint{"DELETE", "/access/users/{userid}/token/{tokenid}"}
	epToken       = endpoint{"GET", "/access/users/{userid}/token/{tokenid}"}

	epPools      = endpoint{"GET", "/pools"}
	epPoolCreate = endpoint{"POST", "/pools"}
	epPoolUpdate = endpoint{"PUT", "/pools"}
	epPoolDelete = endpoint{"DELETE", "/pools"}

	epQemuList     = endpoint{"GET", "/nodes/{node}/qemu"}
	epQemuCreate   = endpoint{"POST", "/nodes/{node}/qemu"}
	epQemuDelete   = endpoint{"DELETE", "/nodes/{node}/qemu/{vmid}"}
	epQemuUpdate   = endpoint{"PUT", "/nodes/{node}/qemu/{vmid}/config"}
	epQemuClone    = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/clone"}
	epQemuTemplate = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/template"}

	epQemuMigratePre = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/migrate"}
	epQemuMigrate    = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/migrate"}
	epLXCMigratePre  = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/migrate"}
	epLXCMigrate     = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/migrate"}

	epQemuSnapshots    = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/snapshot"}
	epQemuSnapCreate   = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/snapshot"}
	epQemuSnapRollback = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/snapshot/{name}/rollback"}
	epQemuSnapDelete   = endpoint{"DELETE", "/nodes/{node}/qemu/{vmid}/snapshot/{name}"}
	epQemuAgentIfaces  = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/agent/network-get-interfaces"}
	epQemuAgentExec    = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/agent/exec"}
	epQemuAgentStatus  = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/agent/exec-status"}

	epLXCSnapshots    = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/snapshot"}
	epLXCSnapCreate   = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/snapshot"}
	epLXCSnapRollback = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/snapshot/{name}/rollback"}
	epLXCSnapDelete   = endpoint{"DELETE", "/nodes/{node}/lxc/{vmid}/snapshot/{name}"}
	epQemuConfig      = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/config"}
	epQemuStatus      = endpoint{"GET", "/nodes/{node}/qemu/{vmid}/status/current"}
	epQemuAction      = endpoint{"POST", "/nodes/{node}/qemu/{vmid}/status/{action}"}

	epLXCList   = endpoint{"GET", "/nodes/{node}/lxc"}
	epLXCConfig = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/config"}
	epLXCStatus = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/status/current"}
	epLXCAction = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/status/{action}"}

	epLXCCreate = endpoint{"POST", "/nodes/{node}/lxc"}
	epLXCDelete = endpoint{"DELETE", "/nodes/{node}/lxc/{vmid}"}
	epLXCUpdate = endpoint{"PUT", "/nodes/{node}/lxc/{vmid}/config"}
	epLXCClone  = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/clone"}

	// Un conteneur n'a pas d'agent : PVE n'expose aucun « exec » REST pour LXC
	// comme il le fait pour QEMU. Le seul canal vers l'intérieur passe par la
	// console — termproxy fabrique un ticket + un port, vncwebsocket ouvre le
	// PTY par-dessus. Voir internal/pve/lxc_exec.go et stories/BACKLOG.md (PVX-074).
	epLXCTermproxy    = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/termproxy"}
	epLXCVNCWebsocket = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/vncwebsocket"}

	// Firewall PVE, la best practice sur Proxmox : le filtrage vit à
	// l'hyperviseur, par-guest, piloté par l'API — pas dans l'invité. Il ne
	// prend effet que si le firewall datacenter est activé ET la NIC porte
	// « firewall=1 » (posée via le config du guest). Voir internal/pve/firewall.go.
	epClusterFwOptions = endpoint{"GET", "/cluster/firewall/options"}
	epLXCFwOptions     = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/firewall/options"}
	epLXCFwOptionsSet  = endpoint{"PUT", "/nodes/{node}/lxc/{vmid}/firewall/options"}
	epLXCFwRules       = endpoint{"GET", "/nodes/{node}/lxc/{vmid}/firewall/rules"}
	epLXCFwRuleCreate  = endpoint{"POST", "/nodes/{node}/lxc/{vmid}/firewall/rules"}
	epLXCFwRuleDelete  = endpoint{"DELETE", "/nodes/{node}/lxc/{vmid}/firewall/rules/{pos}"}
	epClusterIPSets    = endpoint{"GET", "/cluster/firewall/ipset"}
	epClusterIPSetNew  = endpoint{"POST", "/cluster/firewall/ipset"}
	epClusterIPSet     = endpoint{"GET", "/cluster/firewall/ipset/{name}"}
	epClusterIPSetAdd  = endpoint{"POST", "/cluster/firewall/ipset/{name}"}
	epClusterIPSetDel  = endpoint{"DELETE", "/cluster/firewall/ipset/{name}/{cidr}"}

	epNodeStorage    = endpoint{"GET", "/nodes/{node}/storage"}
	epStorageContent = endpoint{"GET", "/nodes/{node}/storage/{storage}/content"}
	epStorageDownURL = endpoint{"POST", "/nodes/{node}/storage/{storage}/download-url"}
	epStorageUpload  = endpoint{"POST", "/nodes/{node}/storage/{storage}/upload"}
	epStorageVolume  = endpoint{"DELETE", "/nodes/{node}/storage/{storage}/content/{volume}"}

	epNetwork       = endpoint{"GET", "/nodes/{node}/network"}
	epNetworkIface  = endpoint{"GET", "/nodes/{node}/network/{iface}"}
	epNetworkApply  = endpoint{"PUT", "/nodes/{node}/network"}
	epNetworkRevert = endpoint{"DELETE", "/nodes/{node}/network"}

	epVZDump = endpoint{"POST", "/nodes/{node}/vzdump"}

	epTasks      = endpoint{"GET", "/nodes/{node}/tasks"}
	epTaskStatus = endpoint{"GET", "/nodes/{node}/tasks/{upid}/status"}
	epTaskLog    = endpoint{"GET", "/nodes/{node}/tasks/{upid}/log"}
)

// AllEndpoints is what the API-MAP coverage test walks.
var AllEndpoints = []endpoint{
	epVersion,
	epNodes,
	epNodeStatus,
	epClusterStat,
	epPermissions,
	epClusterRes,
	epUsers,
	epUserCreate,
	epUser,
	epRoles,
	epRole,
	epACL,
	epACLUpdate,
	epTokens,
	epToken,
	epTokenCreate,
	epTokenDelete,
	epPools,
	epPoolCreate,
	epPoolUpdate,
	epPoolDelete,
	epQemuList,
	epQemuCreate,
	epQemuDelete,
	epQemuUpdate,
	epQemuClone,
	epQemuTemplate,
	epQemuMigratePre,
	epQemuMigrate,
	epLXCMigratePre,
	epLXCMigrate,
	epQemuSnapshots,
	epQemuSnapCreate,
	epQemuSnapRollback,
	epQemuSnapDelete,
	epQemuAgentIfaces,
	epQemuAgentExec,
	epTicket,
	epQemuAgentStatus,
	epLXCSnapshots,
	epLXCSnapCreate,
	epLXCSnapRollback,
	epLXCSnapDelete,
	epQemuConfig,
	epQemuStatus,
	epQemuAction,
	epLXCList,
	epLXCConfig,
	epLXCStatus,
	epLXCAction,
	epLXCCreate,
	epLXCDelete,
	epLXCUpdate,
	epLXCClone,
	epLXCTermproxy,
	epLXCVNCWebsocket,
	epClusterFwOptions,
	epLXCFwOptions,
	epLXCFwOptionsSet,
	epLXCFwRules,
	epLXCFwRuleCreate,
	epLXCFwRuleDelete,
	epClusterIPSets,
	epClusterIPSetNew,
	epClusterIPSet,
	epClusterIPSetAdd,
	epClusterIPSetDel,
	epNodeStorage,
	epStorageContent,
	epStorageDownURL,
	epStorageUpload,
	epStorageVolume,
	epNetwork,
	epNetworkIface,
	epNetworkApply,
	epNetworkRevert,
	epVZDump,
	epTasks,
	epTaskStatus,
	epTaskLog,
}

// Path fills the {placeholders} in order.
//
// Only the two characters that would change the structure of the path are
// escaped. Everything else is deliberately left alone, because PVE compares
// some path segments byte for byte against what it stored — a UPID above all.
//
// url.PathEscape looked like the obvious choice, and it is wrong here: it
// percent-escapes '!', so the UPID of a task started by the token
// `automation@pve!pvectl` came back as "no such task". The task had been
// created; only the polling was broken, which is the most confusing shape a
// bug can take.
func (e endpoint) Path(args ...string) string {
	out := e.Pattern
	for _, arg := range args {
		open := strings.Index(out, "{")
		if open < 0 {
			break
		}
		closing := strings.Index(out[open:], "}")
		if closing < 0 {
			break
		}
		name := out[open : open+closing+1]
		out = out[:open] + pathValue(arg, greedyPlaceholder[name]) + out[open+closing+1:]
	}
	return out
}

// pathValue escapes only what would break the path: a literal percent, and a
// slash that would invent an extra segment — unless the placeholder is one
// PVE parses itself, in which case that extra segment is the point.
func pathValue(s string, greedy bool) string {
	s = strings.ReplaceAll(s, "%", "%25")
	if greedy {
		return s
	}
	return strings.ReplaceAll(s, "/", "%2F")
}
