package cli

import "testing"

func TestUseVirtualInputCursor(t *testing.T) {
	cases := []struct {
		name        string
		termProgram string
		override    string
		want        bool
	}{
		{"warp defaults to virtual", "WarpTerminal", "", true},
		{"other terminal keeps real cursor", "WezTerm", "", false},
		{"empty term keeps real cursor", "", "", false},
		{"override on forces virtual", "WezTerm", "1", true},
		{"override true forces virtual", "", "true", true},
		{"override off forces real on warp", "WarpTerminal", "0", false},
		{"override false forces real on warp", "WarpTerminal", "false", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", c.termProgram)
			t.Setenv("ROACH_VIRTUAL_CURSOR", c.override)
			if got := useVirtualInputCursor(); got != c.want {
				t.Errorf("useVirtualInputCursor() = %v, want %v (TERM_PROGRAM=%q override=%q)",
					got, c.want, c.termProgram, c.override)
			}
		})
	}
}
