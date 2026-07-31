package output

import "testing"

func TestBytes(t *testing.T) {
	tests := map[int64]string{
		0:           "0 B",
		512:         "512 B",
		1024:        "1.0 KiB",
		33041162240: "30.8 GiB", // les 32 Go du lab, tels que PVE les renvoie
	}
	for in, want := range tests {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestUptime(t *testing.T) {
	tests := map[int64]string{
		0:      "—",
		90:     "1m",
		8542:   "2h 22m",
		200000: "2j 7h",
	}
	for in, want := range tests {
		if got := Uptime(in); got != want {
			t.Errorf("Uptime(%d) = %q, want %q", in, got, want)
		}
	}
}

// PVE reports CPU usage as a ratio. Reading it as a percentage is a factor-100
// mistake that looks perfectly plausible on an idle node.
func TestRatio(t *testing.T) {
	if got := Ratio(0.00142309120158396); got != "0.1 %" {
		t.Errorf("Ratio() = %q, want %q", got, "0.1 %")
	}
	if got := Ratio(1); got != "100.0 %" {
		t.Errorf("Ratio(1) = %q, want %q", got, "100.0 %")
	}
}
