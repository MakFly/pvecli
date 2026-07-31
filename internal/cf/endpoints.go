package cf

import "strings"

// endpoint is one Cloudflare API path this client knows how to call, in the
// {placeholder} form docs/CF-API-MAP.md uses.
//
// Same rule as the PVE client: paths are never written inline at the call site,
// so "no endpoint written from memory" is checkable by a test rather than by
// good intentions.
type endpoint struct {
	Method  string
	Pattern string
}

var (
	epTokenVerify = endpoint{"GET", "/user/tokens/verify"}

	epTunnels      = endpoint{"GET", "/accounts/{account}/cfd_tunnel"}
	epTunnelCreate = endpoint{"POST", "/accounts/{account}/cfd_tunnel"}
	epTunnelDelete = endpoint{"DELETE", "/accounts/{account}/cfd_tunnel/{tunnel}"}
	epTunnelToken  = endpoint{"GET", "/accounts/{account}/cfd_tunnel/{tunnel}/token"}
	epTunnelConfig = endpoint{"GET", "/accounts/{account}/cfd_tunnel/{tunnel}/configurations"}
	epTunnelSetCfg = endpoint{"PUT", "/accounts/{account}/cfd_tunnel/{tunnel}/configurations"}

	epZones         = endpoint{"GET", "/zones"}
	epDNSRecords    = endpoint{"GET", "/zones/{zone}/dns_records"}
	epDNSCreate     = endpoint{"POST", "/zones/{zone}/dns_records"}
	epDNSUpdate     = endpoint{"PUT", "/zones/{zone}/dns_records/{record}"}
	epDNSRecordDrop = endpoint{"DELETE", "/zones/{zone}/dns_records/{record}"}
)

// AllEndpoints is what the coverage test walks. An endpoint absent from this
// list is an endpoint no test can prove is documented.
var AllEndpoints = []endpoint{
	epTokenVerify,
	epTunnels, epTunnelCreate, epTunnelDelete, epTunnelToken,
	epTunnelConfig, epTunnelSetCfg,
	epZones, epDNSRecords, epDNSCreate, epDNSUpdate, epDNSRecordDrop,
}

// path substitutes the placeholders, in order, with the given values.
func (e endpoint) path(values ...string) string {
	out := e.Pattern
	for _, v := range values {
		start := strings.Index(out, "{")
		if start < 0 {
			break
		}
		end := strings.Index(out[start:], "}")
		if end < 0 {
			break
		}
		out = out[:start] + v + out[start+end+1:]
	}
	return out
}
