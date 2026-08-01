package pve

import "testing"

// withFirewallFlag doit poser firewall=1/0 sur net0 sans abîmer le reste de
// l'option, et remplacer le drapeau existant plutôt que d'en ajouter un second.
func TestWithFirewallFlag(t *testing.T) {
	cases := []struct {
		name string
		net  string
		on   bool
		want string
	}{
		{
			name: "ajoute le drapeau absent",
			net:  "name=eth0,bridge=vmbr0,ip=192.168.1.221/24",
			on:   true,
			want: "name=eth0,bridge=vmbr0,ip=192.168.1.221/24,firewall=1",
		},
		{
			name: "remplace firewall=0 par 1 sans bouger le reste",
			net:  "name=eth0,firewall=0,bridge=vmbr0",
			on:   true,
			want: "name=eth0,firewall=1,bridge=vmbr0",
		},
		{
			name: "déjà à 1, inchangé",
			net:  "name=eth0,bridge=vmbr0,firewall=1",
			on:   true,
			want: "name=eth0,bridge=vmbr0,firewall=1",
		},
		{
			name: "coupe le drapeau",
			net:  "name=eth0,firewall=1,bridge=vmbr0",
			on:   false,
			want: "name=eth0,firewall=0,bridge=vmbr0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withFirewallFlag(tc.net, tc.on); got != tc.want {
				t.Errorf("withFirewallFlag(%q, %v) = %q, voulu %q", tc.net, tc.on, got, tc.want)
			}
		})
	}
}
