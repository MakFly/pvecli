package pve

// authHeader builds the single header that authenticates an API token.
//
//	Authorization: PVEAPIToken=<user>@<realm>!<tokenname>=<secret>
//
// Verified against the node running the lab (PVE 9.2.2), not from memory:
//
//   - PVE/APIServer/Formatter.pm:83 extracts the value with
//     /(?:^|\s)PVEAPIToken(?:=| )([^;]*)/ — hence the "PVEAPIToken=" prefix and
//     the ban on ';' inside the value.
//   - PVE/AccessControl.pm:493 then splits it with /^(.*)=(.*)$/. The first
//     group is greedy, so the split happens on the LAST '=': a token name may
//     contain '=', the secret may not. PVE secrets are UUIDs, so this never
//     bites in practice — but it is why the secret goes last.
//
// No CSRFPreventionToken is sent, and that is not an oversight:
// PVE/HTTPServer.pm:85 routes an api_token straight to verify_token() and never
// looks at the CSRF header. CSRF protection exists to defend cookie-carried
// tickets, which a browser attaches automatically; a token is only ever sent by
// a client that meant to send it.
func authHeader(tokenID, secret string) string {
	return "PVEAPIToken=" + tokenID + "=" + secret
}
