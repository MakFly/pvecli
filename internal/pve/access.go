package pve

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// The authorisation model of PVE, in three objects that are easy to confuse:
//
//   - a PRIVILEGE is an atom: VM.PowerMgmt, Datastore.Audit.
//   - a ROLE is a named bundle of privileges: PVEVMAdmin, PVEAuditor.
//   - an ACL is a triplet (path, identity, role), optionally propagating down
//     the path tree.
//
// Effective privileges are read at a path, never at an object. Until that is
// clear, every 403 stays a mystery — which is the whole point of PVX-033.

// User is one entry of GET /access/users.
type User struct {
	UserID  string `json:"userid"`
	Comment string `json:"comment,omitempty"`
	Email   string `json:"email,omitempty"`
	Enable  int    `json:"enable"`
	// Expire is seconds since the epoch, 0 meaning "never".
	Expire    int64  `json:"expire"`
	RealmType string `json:"realm-type,omitempty"`
}

// Users lists the accounts known to the node.
//
// GET /access/users
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out []User
	if err := c.get(ctx, epUsers, nil, nil, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

// Role is one entry of GET /access/roles.
type Role struct {
	RoleID string `json:"roleid"`
	// Privs is a COMMA-separated string here — and the same information comes
	// back as a map from GET /access/roles/{roleid}. One endpoint, two shapes,
	// which is why Privileges() exists rather than a raw field read.
	Privs string `json:"privs,omitempty"`
	// Special marks a built-in role, which cannot be edited.
	Special int `json:"special,omitempty"`
}

// Privileges splits the comma-separated privilege list.
func (r Role) Privileges() []string {
	if r.Privs == "" {
		return nil
	}
	return strings.Split(r.Privs, ",")
}

// IsBuiltin reports whether the role ships with PVE.
func (r Role) IsBuiltin() bool { return r.Special == 1 }

// Roles lists the roles defined on the node.
//
// GET /access/roles
func (c *Client) Roles(ctx context.Context) ([]Role, error) {
	var out []Role
	if err := c.get(ctx, epRoles, nil, nil, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RoleID < out[j].RoleID })
	return out, nil
}

// RolePrivileges reads one role's privileges.
//
// The detail endpoint answers a MAP of privilege→1, not the comma-separated
// string the index uses. Same information, two schemas, and assuming the index
// shape here earns a decoding error rather than a 404.
//
// GET /access/roles/{roleid}
func (c *Client) RolePrivileges(ctx context.Context, roleID string) ([]string, error) {
	var out map[string]int
	if err := c.get(ctx, epRole, []string{roleID}, nil, &out); err != nil {
		return nil, err
	}
	privs := make([]string, 0, len(out))
	for priv, granted := range out {
		if granted == 1 {
			privs = append(privs, priv)
		}
	}
	sort.Strings(privs)
	return privs, nil
}

// ACLEntry is one line of GET /access/acl: the (path, identity, role) triplet.
type ACLEntry struct {
	Path   string `json:"path"`
	RoleID string `json:"roleid"`
	// Type is "user", "token" or "group".
	Type string `json:"type"`
	// UGID is the identity the entry applies to.
	UGID      string `json:"ugid"`
	Propagate int    `json:"propagate"`
}

// ACL lists the access control entries of the node.
//
// GET /access/acl
func (c *Client) ACL(ctx context.Context) ([]ACLEntry, error) {
	var out []ACLEntry
	if err := c.get(ctx, epACL, nil, nil, &out); err != nil {
		return nil, err
	}
	sortACL(out)
	return out, nil
}

// sortACL orders entries the way an operator reads them: by path from the most
// general to the most specific, then by identity.
func sortACL(entries []ACLEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if di, dj := pathDepth(entries[i].Path), pathDepth(entries[j].Path); di != dj {
			return di < dj
		}
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].UGID < entries[j].UGID
	})
}

// pathDepth counts the segments of an ACL path. "/" is the root and comes
// first; "/vms/120" is more specific than "/vms".
func pathDepth(path string) int {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "/") + 1
}

// ACLChange is one modification of the access control list.
//
// The API names its parameters in the PLURAL — roles, users, tokens, groups —
// because one call can carry several. pvecli exposes one at a time: an ACL
// change that touches four identities at once is a change nobody can review.
type ACLChange struct {
	Path      string
	Role      string
	User      string
	Token     string
	Group     string
	Propagate bool
	// Delete removes the entry instead of adding it. It is a boolean parameter
	// of the same endpoint, not a separate verb.
	Delete bool
}

// Values renders the change as the payload that will be sent.
func (a ACLChange) Values() url.Values {
	v := url.Values{}
	v.Set("path", a.Path)
	v.Set("roles", a.Role)
	if a.User != "" {
		v.Set("users", a.User)
	}
	if a.Token != "" {
		v.Set("tokens", a.Token)
	}
	if a.Group != "" {
		v.Set("groups", a.Group)
	}
	if a.Propagate {
		v.Set("propagate", "1")
	} else {
		// The schema defaults propagate to 1. Staying silent here would grant a
		// propagating ACL to an operator who did not ask for one.
		v.Set("propagate", "0")
	}
	if a.Delete {
		v.Set("delete", "1")
	}
	return v
}

// Identity returns the ugid this change applies to, for the pre-read diff.
func (a ACLChange) Identity() string {
	switch {
	case a.Token != "":
		return a.Token
	case a.Group != "":
		return a.Group
	default:
		return a.User
	}
}

// SetACL adds or removes an access control entry.
//
// PUT /access/acl
func (c *Client) SetACL(ctx context.Context, change ACLChange) error {
	return c.post(ctx, epACLUpdate, nil, change.Values(), nil)
}

// Token is one entry of GET /access/users/{userid}/token.
//
// The secret is not in it, and cannot be: PVE hashes it and returns the plain
// value exactly once, at creation.
type Token struct {
	TokenID string `json:"tokenid"`
	Comment string `json:"comment,omitempty"`
	// Expire is seconds since the epoch, 0 meaning "never".
	Expire flexInt `json:"expire"`
	// PrivSep restricts the token to its OWN ACLs. With privsep=1 the effective
	// privileges are the INTERSECTION of the token's and its user's — which is
	// the cause of most inexplicable 403s in a homelab.
	PrivSep flexInt `json:"privsep"`
}

// flexInt is an integer PVE answers as a number from one endpoint and as a
// string from another.
//
// Observed on the lab: GET /access/users/{u}/token returns {"privsep":1,
// "expire":1785621600}, while the POST that creates the token returns
// {"privsep":"1","expire":"1785608487"}. Same fields, same resource, two JSON
// types. A strict int decode fails on the POST — and that failure happens
// AFTER the write, so it destroyed a secret that is returned exactly once.
type flexInt int64

func (f *flexInt) UnmarshalJSON(raw []byte) error {
	text := strings.Trim(string(raw), `"`)
	if text == "" || text == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("entier attendu, reçu %s", raw)
	}
	*f = flexInt(v)
	return nil
}

// Int returns the value as a plain integer.
func (f flexInt) Int() int64 { return int64(f) }

// Separated reports whether this token has privilege separation on.
func (t Token) Separated() bool { return t.PrivSep == 1 }

// Tokens lists a user's API tokens.
//
// GET /access/users/{userid}/token
func (c *Client) Tokens(ctx context.Context, userID string) ([]Token, error) {
	var out []Token
	if err := c.get(ctx, epTokens, []string{userID}, nil, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TokenID < out[j].TokenID })
	return out, nil
}

// TokenInfo reads one token's metadata — never its secret.
//
// GET /access/users/{userid}/token/{tokenid}
func (c *Client) TokenInfo(ctx context.Context, userID, tokenID string) (*Token, error) {
	var out Token
	if err := c.get(ctx, epToken, []string{userID, tokenID}, nil, &out); err != nil {
		return nil, err
	}
	out.TokenID = tokenID
	return &out, nil
}

// TokenOptions are the parameters of a token creation.
type TokenOptions struct {
	Comment string
	// Expire is seconds since the epoch. PVE takes an integer, not a date:
	// the conversion happens in the CLI, and --dry-run shows the result.
	Expire int64
	// Separated asks for privsep=1, the safe setting.
	Separated bool
}

// Values renders the options as the payload that will be sent.
func (o TokenOptions) Values() url.Values {
	v := url.Values{}
	if o.Comment != "" {
		v.Set("comment", o.Comment)
	}
	// 0 is a meaningful value here — "never expires" — so it is always sent.
	v.Set("expire", strconv.FormatInt(o.Expire, 10))
	if o.Separated {
		v.Set("privsep", "1")
	} else {
		v.Set("privsep", "0")
	}
	return v
}

// NewToken is what POST /access/users/{userid}/token/{tokenid} answers.
type NewToken struct {
	// FullTokenID is "user@realm!name", the value that goes in PVE_API_TOKEN_ID.
	FullTokenID string `json:"full-tokenid"`
	// Value is the secret, returned ONCE and never retrievable again.
	Value string `json:"value"`
	Info  Token  `json:"info"`
}

// CreateToken issues a new API token and returns its secret.
//
// POST /access/users/{userid}/token/{tokenid}
func (c *Client) CreateToken(ctx context.Context, userID, tokenID string, o TokenOptions) (*NewToken, error) {
	var out NewToken
	if err := c.post(ctx, epTokenCreate, []string{userID, tokenID}, o.Values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteToken revokes an API token.
//
// DELETE /access/users/{userid}/token/{tokenid}
func (c *Client) DeleteToken(ctx context.Context, userID, tokenID string) error {
	return c.del(ctx, epTokenDelete, []string{userID, tokenID}, nil, nil)
}

// SplitTokenID cuts a full token id into its user and token parts.
//
// "automation@pve!pvectl" → ("automation@pve", "pvectl", true). The realm
// contains no '!', so the first one is the separator — unlike the SECRET, whose
// '=' separator is the LAST one (see docs/API-MAP.md).
func SplitTokenID(full string) (user, token string, ok bool) {
	user, token, ok = strings.Cut(full, "!")
	if !ok {
		return full, "", false
	}
	return user, token, true
}

// ACLPath renders the ACL path, for --dry-run.
func ACLPath() string { return epACLUpdate.Pattern }

// TokenPath renders a token path, for --dry-run.
func TokenPath(userID, tokenID string) string {
	return epTokenCreate.Path(userID, tokenID)
}
