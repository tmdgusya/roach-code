package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
	"roach-code/internal/textarea"
)

// wvt models a Warp-like terminal: it renders CJK at width 1 (the mismatch that
// makes the app's width-2 column math drift) and supports ED (ESC[2J) so we can
// verify the full-repaint workaround. If a full repaint lays the line out
// sequentially from column 0, no gaps appear; an incremental mid-line diff drifts.
type wvt struct {
	w, h int
	g    [][]rune
	x, y int
}

func newWVT(w, h int) *wvt {
	g := make([][]rune, h)
	for i := range g {
		g[i] = make([]rune, w)
		for j := range g[i] {
			g[i][j] = ' '
		}
	}
	return &wvt{w: w, h: h, g: g}
}
func (t *wvt) cw(r rune) int { // Warp's (buggy) width: CJK as 1
	if uniseg.StringWidth(string(r)) == 2 {
		return 1
	}
	return uniseg.StringWidth(string(r))
}
func (t *wvt) put(r rune) {
	if t.x >= t.w || t.x < 0 {
		return
	}
	t.g[t.y][t.x] = r
	t.x += t.cw(r)
}
func (t *wvt) ich(n int) {
	row := t.g[t.y]
	for i := t.w - 1; i >= t.x+n; i-- {
		row[i] = row[i-n]
	}
	for i := t.x; i < t.x+n && i < t.w; i++ {
		row[i] = ' '
	}
}
func (t *wvt) dch(n int) {
	row := t.g[t.y]
	for i := t.x; i < t.w-n; i++ {
		row[i] = row[i+n]
	}
	for i := t.w - n; i < t.w; i++ {
		if i >= 0 {
			row[i] = ' '
		}
	}
}
func (t *wvt) elr() {
	for i := t.x; i < t.w; i++ {
		t.g[t.y][i] = ' '
	}
}
func (t *wvt) ell() {
	for i := 0; i <= t.x && i < t.w; i++ {
		t.g[t.y][i] = ' '
	}
}
func (t *wvt) elall() {
	for i := 0; i < t.w; i++ {
		t.g[t.y][i] = ' '
	}
}
func (t *wvt) ed(mode int) { // erase display
	switch mode {
	case 2, 3:
		for y := 0; y < t.h; y++ {
			for x := 0; x < t.w; x++ {
				t.g[y][x] = ' '
			}
		}
	case 0:
		t.elr()
		for y := t.y + 1; y < t.h; y++ {
			for x := 0; x < t.w; x++ {
				t.g[y][x] = ' '
			}
		}
	case 1:
		t.ell()
		for y := 0; y < t.y; y++ {
			for x := 0; x < t.w; x++ {
				t.g[y][x] = ' '
			}
		}
	}
}
func wai(s string, d int) int {
	if s == "" {
		return d
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return d
		}
		n = n*10 + int(c-'0')
	}
	return n
}
func wcl(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
func (t *wvt) feed(b []byte) {
	rs := []rune(string(b))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\x1b':
			if i+1 < len(rs) && rs[i+1] == '[' {
				j := i + 2
				var priv byte
				if j < len(rs) && (rs[j] == '<' || rs[j] == '=' || rs[j] == '>' || rs[j] == '?') {
					priv = byte(rs[j])
					j++
				}
				var p strings.Builder
				for j < len(rs) && (rs[j] == ';' || (rs[j] >= '0' && rs[j] <= '9')) {
					p.WriteRune(rs[j])
					j++
				}
				for j < len(rs) && rs[j] >= ' ' && rs[j] <= '/' {
					j++
				}
				if j < len(rs) {
					if priv == 0 {
						t.csi(rs[j], p.String())
					}
					i = j
					continue
				}
			} else if i+1 < len(rs) && rs[i+1] == ']' {
				j := i + 2
				for j < len(rs) {
					if rs[j] == '\a' {
						break
					}
					if rs[j] == '\x1b' && j+1 < len(rs) && rs[j+1] == '\\' {
						j++
						break
					}
					j++
				}
				i = j
				continue
			} else if i+1 < len(rs) && rs[i+1] == 'M' {
				if t.y > 0 {
					t.y--
				}
				i++
				continue
			}
			i++
		case r == '\r':
			t.x = 0
		case r == '\n':
			t.y++
			if t.y >= t.h {
				t.y = t.h - 1
			}
		case r == '\b':
			if t.x > 0 {
				t.x--
			}
		case r == '\a':
		default:
			t.put(r)
		}
	}
}
func (t *wvt) csi(f rune, p string) {
	if strings.HasPrefix(p, "?") {
		return
	}
	switch f {
	case 'H', 'f':
		ps := strings.Split(p, ";")
		row := wai(ps[0], 1)
		col := 1
		if len(ps) > 1 {
			col = wai(ps[1], 1)
		}
		t.y = wcl(row-1, 0, t.h-1)
		t.x = wcl(col-1, 0, t.w-1)
	case 'A':
		t.y = wcl(t.y-wai(p, 1), 0, t.h-1)
	case 'B':
		t.y = wcl(t.y+wai(p, 1), 0, t.h-1)
	case 'C':
		t.x = wcl(t.x+wai(p, 1), 0, t.w-1)
	case 'D':
		t.x = wcl(t.x-wai(p, 1), 0, t.w-1)
	case 'G':
		t.x = wcl(wai(p, 1)-1, 0, t.w-1)
	case 'd':
		t.y = wcl(wai(p, 1)-1, 0, t.h-1)
	case '@':
		t.ich(wai(p, 1))
	case 'P':
		t.dch(wai(p, 1))
	case 'K':
		switch wai(p, 0) {
		case 0:
			t.elr()
		case 1:
			t.ell()
		case 2:
			t.elall()
		}
	case 'J':
		t.ed(wai(p, 0))
	}
}
func (t *wvt) row(y int) string {
	var b strings.Builder
	for _, r := range t.g[y] {
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

// wmodel mirrors roach-code's composer; `workaround` toggles the ClearScreen fix.
type wmodel struct {
	ti         textarea.Model
	w          int
	workaround bool
	prevWide   bool
}

func newWModel(w int, workaround bool) wmodel {
	ti := textarea.New()
	ti.Prompt = ""
	ti.SetHeight(1)
	ti.ShowLineNumbers = false
	ti.DynamicHeight = true
	ti.MaxHeight = 6
	ti.SetVirtualCursor(false)
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"))
	ti.SetWidth(w - 4)
	ti.Focus()
	return wmodel{ti: ti, w: w, workaround: workaround}
}
func (m wmodel) Init() tea.Cmd { return textarea.Blink }
func (m wmodel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var c tea.Cmd
	m.ti, c = m.ti.Update(msg)
	cmds := []tea.Cmd{c}
	if _, ok := msg.(tea.KeyPressMsg); ok && m.workaround {
		wide := hasWideRune(m.ti.Value())
		if wide || m.prevWide {
			cmds = append(cmds, tea.ClearScreen)
		}
		m.prevWide = wide
	}
	return m, tea.Batch(cmds...)
}
func (m wmodel) View() tea.View {
	box := lipgloss.NewStyle().Border(lipgloss.ThickBorder(), true, false, true, false).PaddingLeft(1).Width(m.w).Render(m.ti.View())
	v := tea.NewView("L1\nL2\nL3\n" + box)
	v.AltScreen = true
	if cur := m.ti.Cursor(); cur != nil {
		cur.X += 1
		cur.Y += 4
		v.Cursor = cur
	}
	return v
}

func runWarpSim(workaround bool) string {
	const w = 40
	var out bytes.Buffer
	p := tea.NewProgram(newWModel(w, workaround), tea.WithOutput(&out), tea.WithInput(nil), tea.WithoutSignals(),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithoutCatchPanics())
	done := make(chan struct{})
	go func() { _, _ = p.Run(); close(done) }()
	send := func(msg tea.Msg) { p.Send(msg); time.Sleep(15 * time.Millisecond) }
	time.Sleep(50 * time.Millisecond)
	send(tea.WindowSizeMsg{Width: w, Height: 12})
	for _, r := range "가나다라마" {
		send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	for i := 0; i < 3; i++ {
		send(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	send(tea.KeyPressMsg{Code: 'X', Text: "X"})
	time.Sleep(40 * time.Millisecond)
	p.Quit()
	<-done
	term := newWVT(w, 12)
	term.feed(out.Bytes())
	// the box content row: find row between the ━ borders
	var content strings.Builder
	inBox := false
	for y := 0; y < 12; y++ {
		r := term.row(y)
		isBorder := r != "" && strings.Trim(r, "━") == ""
		if isBorder {
			if inBox {
				break
			}
			inBox = true
			continue
		}
		if inBox {
			content.WriteString(r)
		}
	}
	return content.String()
}

func TestForceInputRepaintEnv(t *testing.T) {
	cases := []struct {
		name              string
		term, override    string
		wideNow, widePrev bool
		want              bool
	}{
		{"warp wide-now repaints", "WarpTerminal", "", true, false, true},
		{"warp wide-prev repaints (delete transition)", "WarpTerminal", "", false, true, true},
		{"warp ascii does not repaint", "WarpTerminal", "", false, false, false},
		{"non-warp never repaints", "WezTerm", "", true, true, false},
		{"override on forces repaint anywhere", "WezTerm", "1", false, false, true},
		{"override off disables on warp", "WarpTerminal", "0", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", c.term)
			t.Setenv("ROACH_FULL_REPAINT", c.override)
			if got := forceInputRepaint(c.wideNow, c.widePrev); got != c.want {
				t.Errorf("forceInputRepaint(%v,%v) = %v, want %v", c.wideNow, c.widePrev, got, c.want)
			}
		})
	}
}

func TestHasWideRune(t *testing.T) {
	for _, c := range []struct {
		s    string
		want bool
	}{{"abcd", false}, {"", false}, {"가", true}, {"abc가d", true}, {"ㅋㅋ", true}, {"界", true}} {
		if got := hasWideRune(c.s); got != c.want {
			t.Errorf("hasWideRune(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestWarpSimRepaintWorkaround(t *testing.T) {
	off := strings.TrimSpace(runWarpSim(false))
	on := strings.TrimSpace(runWarpSim(true))
	t.Logf("workaround OFF: %q", off)
	t.Logf("workaround ON : %q", on)
	// Type 가나다라마, move the caret left 3 (between 나 and 다), insert X.
	want := "가나X다라마"
	// Without the workaround, Warp's width-1 incremental render drifts (gaps and a
	// duplicated trailing glyph) — this documents the reproduced bug.
	if off == want {
		t.Fatalf("expected the Warp-sim to reproduce corruption without the workaround, but it was clean (%q) — the model no longer reproduces", off)
	}
	// With the workaround (force full repaint on wide-char edits), the line is
	// redrawn fresh and renders the correct glyphs in order with no gaps/ghosts.
	if on != want {
		t.Errorf("workaround did not produce clean output: got %q want %q", on, want)
	}
}
