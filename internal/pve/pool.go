package pve

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Pool is one resource pool.
//
// A pool is first and foremost an ACL path: /pool/<id>. Grouping guests is
// what it looks like; carrying permissions is what it is for.
type Pool struct {
	PoolID  string `json:"poolid"`
	Comment string `json:"comment,omitempty"`

	// Members is filled only when a single pool was asked for.
	Members []PoolMember `json:"members,omitempty"`
}

// PoolMember is one guest or storage inside a pool.
type PoolMember struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Node    string `json:"node,omitempty"`
	VMID    int    `json:"vmid,omitempty"`
	Storage string `json:"storage,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
}

// Label names a member the way an operator refers to it.
func (m PoolMember) Label() string {
	if m.VMID != 0 {
		if m.Name != "" {
			return fmt.Sprintf("%d (%s)", m.VMID, m.Name)
		}
		return strconv.Itoa(m.VMID)
	}
	return firstNonBlank(m.Storage, m.ID)
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Pools lists the pools the caller can audit.
//
// GET /pools
func (c *Client) Pools(ctx context.Context) ([]Pool, error) {
	var out []Pool
	if err := c.get(ctx, epPools, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Pool reads one pool with its members.
//
// GET /pools?poolid=… — and note the answer is an ARRAY of one element, not an
// object: the handler pushes a single entry onto the same list the index
// builds (PVE/API2/Pool.pm, `push @$res, $pool_info`). Decoding it as an
// object earns a type error that reads like a broken endpoint.
//
// The older GET /pools/{poolid} still answers, and PVE 9 marks it deprecated
// in its own description: « no support for nested pools ». A pool id may now
// contain a slash (parent/child), which is exactly what a path segment cannot
// carry — hence the move to a parameter.
func (c *Client) Pool(ctx context.Context, poolid string) (*Pool, error) {
	var out []Pool
	query := url.Values{"poolid": {poolid}}
	if err := c.get(ctx, epPools, nil, query, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("le pool %q n'existe pas", poolid)
	}
	return &out[0], nil
}

// CreatePool creates an empty pool. Synchronous: no UPID.
//
// POST /pools
func (c *Client) CreatePool(ctx context.Context, poolid, comment string) error {
	return c.post(ctx, epPoolCreate, nil, PoolCreateValues(poolid, comment), nil)
}

// PoolCreateValues renders the creation payload, so --dry-run and the request
// cannot drift apart.
func PoolCreateValues(poolid, comment string) url.Values {
	v := url.Values{"poolid": {poolid}}
	if comment != "" {
		v.Set("comment", comment)
	}
	return v
}

// DeletePool removes an EMPTY pool. PVE refuses a pool that still holds a
// guest, a storage or a sub-pool — there is no force flag on the endpoint
// (PVE/API2/Pool.pm, delete_pool: « You can only delete empty pools »).
//
// DELETE /pools?poolid=…
func (c *Client) DeletePool(ctx context.Context, poolid string) error {
	return c.del(ctx, epPoolDelete, nil, url.Values{"poolid": {poolid}}, nil)
}

// PoolChange adds members to a pool, or takes them out.
type PoolChange struct {
	PoolID  string
	VMs     []int
	Storage []string
	Comment string

	// Delete removes the listed members instead of adding them.
	Delete bool
	// AllowMove takes a guest out of the pool it is already in. Without it,
	// PVE refuses rather than silently moving a guest someone else grouped.
	AllowMove bool
}

// Values renders the update payload.
func (p PoolChange) Values() url.Values {
	v := url.Values{"poolid": {p.PoolID}}
	if len(p.VMs) > 0 {
		ids := make([]string, len(p.VMs))
		for i, vmid := range p.VMs {
			ids[i] = strconv.Itoa(vmid)
		}
		v.Set("vms", strings.Join(ids, ","))
	}
	if len(p.Storage) > 0 {
		v.Set("storage", strings.Join(p.Storage, ","))
	}
	if p.Comment != "" {
		v.Set("comment", p.Comment)
	}
	if p.Delete {
		v.Set("delete", "1")
	}
	if p.AllowMove {
		v.Set("allow-move", "1")
	}
	return v
}

// UpdatePool adds or removes members. Synchronous.
//
// PUT /pools
func (c *Client) UpdatePool(ctx context.Context, change PoolChange) error {
	return c.post(ctx, epPoolUpdate, nil, change.Values(), nil)
}

// PoolPath renders the pool endpoint, for --dry-run. It takes no argument:
// in PVE 9 the pool id is a parameter, not a path segment.
func PoolPath() string { return epPools.Pattern }

// PoolACLPath is the ACL path a pool answers to — the reason pools exist.
func PoolACLPath(poolid string) string { return "/pool/" + poolid }
