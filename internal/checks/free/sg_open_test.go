package free

import (
	"strings"
	"testing"
)

func TestOpenPortHits(t *testing.T) {
	cases := []struct {
		from, to int32
		all      bool
		want     []string // substrings that must appear
		wantNone bool
	}{
		{22, 22, false, []string{"SSH"}, false},
		{80, 80, false, nil, true},                          // plain HTTP is fine
		{443, 443, false, nil, true},                        // HTTPS is fine
		{0, 65535, false, []string{"SSH", "RDP", "MySQL"}, false},
		{3300, 3400, false, []string{"MySQL", "RDP"}, false}, // range covers 3306 and 3389
		{0, 0, true, []string{"SSH", "PostgreSQL"}, false},   // allPorts
	}
	for _, c := range cases {
		got := openPortHits(c.from, c.to, c.all)
		joined := strings.Join(got, ",")
		if c.wantNone && len(got) != 0 {
			t.Errorf("ports %d-%d: expected no hits, got %v", c.from, c.to, got)
		}
		for _, w := range c.want {
			if !strings.Contains(joined, w) {
				t.Errorf("ports %d-%d: expected %q in %v", c.from, c.to, w, got)
			}
		}
	}
}
