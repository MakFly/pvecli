package cf

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Zone is a domain served by Cloudflare.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Record is one DNS entry.
type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

// Zones lists the zones the token can see.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var zones []Zone
	if err := c.do(ctx, epZones, nil, url.Values{"per_page": {"100"}}, nil, &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

// ZoneForHost finds which zone a fully-qualified name belongs to.
//
// The longest matching suffix wins: an account holding both "example.com" and
// "lab.example.com" must route "n8n.lab.example.com" through the second, and
// picking the first would create the record in a zone that does not serve it.
func (c *Client) ZoneForHost(ctx context.Context, fqdn string) (Zone, error) {
	zones, err := c.Zones(ctx)
	if err != nil {
		return Zone{}, err
	}

	var best Zone
	for _, z := range zones {
		if fqdn == z.Name || strings.HasSuffix(fqdn, "."+z.Name) {
			if len(z.Name) > len(best.Name) {
				best = z
			}
		}
	}
	if best.ID == "" {
		names := make([]string, 0, len(zones))
		for _, z := range zones {
			names = append(names, z.Name)
		}
		if len(names) == 0 {
			return Zone{}, fmt.Errorf("aucune zone Cloudflare visible avec ce jeton : %w", ErrNotFound)
		}
		return Zone{}, fmt.Errorf("« %s » n'appartient à aucune zone de ce compte : %w — zones visibles : %s",
			fqdn, ErrNotFound, strings.Join(names, ", "))
	}
	return best, nil
}

// RecordByName finds an existing record for a name, or nil.
func (c *Client) RecordByName(ctx context.Context, zoneID, name string) (*Record, error) {
	var records []Record
	q := url.Values{"name": {name}, "per_page": {"100"}}
	if err := c.do(ctx, epDNSRecords, []string{zoneID}, q, nil, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.Name == name {
			found := r
			return &found, nil
		}
	}
	return nil, nil
}

// PointAtTunnel makes a hostname resolve into a tunnel, creating or updating
// the CNAME as needed.
//
// Proxied is always true: an unproxied CNAME to cfargotunnel.com resolves to
// something no client can reach, and the symptom is a name that answers with a
// timeout rather than an error.
func (c *Client) PointAtTunnel(ctx context.Context, zone Zone, fqdn string, tunnel Tunnel) (Record, error) {
	existing, err := c.RecordByName(ctx, zone.ID, fqdn)
	if err != nil {
		return Record{}, err
	}

	body := map[string]any{
		"type":    "CNAME",
		"name":    fqdn,
		"content": tunnel.CNAME(),
		"proxied": true,
		"comment": "pvecli — tunnel " + tunnel.Name,
	}

	var out Record
	if existing == nil {
		if err := c.do(ctx, epDNSCreate, []string{zone.ID}, nil, body, &out); err != nil {
			return Record{}, err
		}
		return out, nil
	}

	// Refusing to overwrite an unrelated record: a name already serving
	// something else is a collision the operator has to resolve, not a
	// formality this tool gets to decide.
	if existing.Type != "CNAME" || (existing.Content != tunnel.CNAME() && !strings.HasSuffix(existing.Content, "."+TunnelDomain)) {
		return Record{}, fmt.Errorf(
			"« %s » existe déjà en %s vers %s.\nSupprime-le, ou choisis un autre nom — pvecli ne remplace pas un enregistrement qu'il n'a pas posé",
			fqdn, existing.Type, existing.Content)
	}
	if err := c.do(ctx, epDNSUpdate, []string{zone.ID, existing.ID}, nil, body, &out); err != nil {
		return Record{}, err
	}
	return out, nil
}

// DeleteRecord removes a DNS entry.
func (c *Client) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	return c.do(ctx, epDNSRecordDrop, []string{zoneID, recordID}, nil, nil, nil)
}
