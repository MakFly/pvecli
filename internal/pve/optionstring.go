package pve

import (
	"sort"
	"strings"
)

// OptionString is PVE's ubiquitous "positional,key=value,key=value" format.
//
//	virtio0: local-lvm:vm-100-disk-0,size=20G,cache=writeback
//	net0:    virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0,firewall=1
//	agent:   1,fstrim_cloned_disks=1
//
// It turns up in every guest configuration, and understanding it is what makes
// writing a configuration possible at all (PVX-026). Displaying these strings
// raw would be showing the operator the same puzzle the API showed us.
//
// Note the asymmetry: the leading element may be positional (a volume id) or
// may itself be a key=value pair (net0's `virtio=…`). Parsing has to tolerate
// both, which is exactly the kind of detail no schema documents.
type OptionString struct {
	// Value is the leading positional element, empty when there is none.
	Value string
	// Opts holds the key=value pairs, in no particular order.
	Opts map[string]string
}

// ParseOptionString splits a PVE option string.
func ParseOptionString(s string) OptionString {
	out := OptionString{Opts: map[string]string{}}
	if s == "" {
		return out
	}

	for i, part := range strings.Split(s, ",") {
		key, value, isPair := strings.Cut(part, "=")
		switch {
		case i == 0 && !isPair:
			out.Value = part
		case isPair:
			out.Opts[key] = value
		default:
			// A bare element after the first: rare, but PVE accepts flags with
			// no value. Record it so nothing is silently dropped.
			out.Opts[part] = ""
		}
	}
	return out
}

// Get returns an option, or the empty string.
func (o OptionString) Get(key string) string { return o.Opts[key] }

// Keys returns the option names, sorted, so rendering is stable.
func (o OptionString) Keys() []string {
	keys := make([]string, 0, len(o.Opts))
	for k := range o.Opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// String rebuilds the option string. Round-tripping matters: PVX-026 modifies
// one option of an existing value and writes the whole thing back.
func (o OptionString) String() string {
	parts := make([]string, 0, len(o.Opts)+1)
	if o.Value != "" {
		parts = append(parts, o.Value)
	}
	for _, k := range o.Keys() {
		if v := o.Opts[k]; v != "" {
			parts = append(parts, k+"="+v)
		} else {
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, ",")
}
