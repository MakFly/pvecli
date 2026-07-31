package output

import "sort"

// Entry is one line of a connection block: how to reach one thing on one host.
type Entry struct {
	Key   string `json:"key" yaml:"key"`
	Value string `json:"value" yaml:"value"`
	// Secret entries never carry their value here. Value holds the reference
	// that lets the operator fetch it, so the whole structure stays safe to
	// render, to pipe and to paste.
	Secret bool `json:"secret" yaml:"secret"`
}

// Connection is everything needed to reach one host after a run.
type Connection struct {
	Host    string  `json:"host" yaml:"host"`
	IP      string  `json:"ip" yaml:"ip"`
	User    string  `json:"user" yaml:"user"`
	Entries []Entry `json:"entries" yaml:"entries"`
}

// ConnectRows renders connection blocks as table rows.
//
// Rows rather than a free-form block printed with Println, because a command
// whose real result only exists on stderr cannot be scripted: `-o json` has to
// carry the same information. The whole point of the block is to answer "and
// now, how do I get in?" — an answer that only a human can read is half an
// answer.
func ConnectRows(conns []Connection) Rows {
	rows := Rows{Headers: []string{"HÔTE", "ACCÈS", "VALEUR"}}
	for _, c := range conns {
		if c.IP != "" {
			rows.Cells = append(rows.Cells, []string{c.Host, "ip", c.IP})
			if c.User != "" {
				rows.Cells = append(rows.Cells, []string{c.Host, "ssh", "ssh " + c.User + "@" + c.IP})
			}
		}
		entries := append([]Entry(nil), c.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
		for _, e := range entries {
			rows.Cells = append(rows.Cells, []string{c.Host, e.Key, e.Value})
		}
	}
	return rows
}
