package cli

import (
	"fmt"
	"math"
	"strings"
)

// thinkGlyphs is the breathing pulse cycled in the live "thinking" indicator — a
// star drawing a breath in and out rather than a spinning block. It is a
// palindrome so the pulse eases at both ends instead of snapping back.
//
// Every glyph here is deliberately an EMOJI-SAFE asterisk: codepoints with a
// Unicode emoji-presentation variant (e.g. U+2733 ✳, U+2734 ✴) get substituted by
// the font for a fixed-colour emoji — a green star on Windows Terminal — which
// ignores our accent colour and breaks the shimmer. The ones below (U+2722,
// U+2736, U+2737, U+273B, U+273D) have no emoji form and always render as
// monochrome text. Advanced by shimmerPhase.
var thinkGlyphs = []string{"·", "✢", "✷", "✶", "✽", "✻", "✽", "✶", "✷", "✢"}

// thinkGlyph picks the current pulse frame. Phase is halved so the star breathes
// at a calm ~5 fps even though the shimmer wave underneath moves every tick.
func thinkGlyph(phase int) string {
	n := len(thinkGlyphs)
	if n == 0 {
		return "*"
	}
	i := (phase / 2) % n
	if i < 0 {
		i += n
	}
	return thinkGlyphs[i]
}

// lampBreath is the single shared "lamp" heartbeat — a cosine on a 2s period
// (20 frames at the 100ms spinner tick) returning 0..1. The thinking star's
// brightness rides it so the agent has one calm, continuous pulse of light that
// any future ambient element can phase-lock to (coherence, not a pile of
// independent animations). The modulo is guarded so a negative phase can't index
// out of the cycle.
func lampBreath(phase int) float64 {
	return 0.5 * (1 + math.Cos(2*math.Pi*float64(((phase%20)+20)%20)/20))
}

// shimmer paints readable TEXT with a soft brightness wave that sweeps left→right
// as phase advances: a bright brand-coloured crest travelling over a dim-but-
// legible base, the way Claude Code shimmers its "thinking…" verb. The crest is a
// single Gaussian highlight (never a row of bands), and because the trough is the
// theme's legible faint colour no glyph is ever lost — unlike a hard block
// decoration. Pure chrome: under NO_COLOR / a non-tty it returns s unchanged.
func shimmer(s string, phase int) string {
	if !colorEnabled {
		return s
	}
	r := []rune(s)
	n := len(r)
	if n == 0 {
		return s
	}
	base := activeCLITheme.faint   // legible trough
	crest := activeCLITheme.accent // brand-bright crest
	// The crest travels the word plus a short tail of blank padding, so there's a
	// brief dim lull between sweeps rather than a hard wrap.
	period := float64(n + 6)
	center := math.Mod(math.Mod(float64(phase), period)+period, period)
	const sigma = 1.8 // crest half-width, in runes — tight enough to read as a glint
	var b strings.Builder
	for i, ch := range r {
		d := float64(i) - center
		w := math.Exp(-(d * d) / (2 * sigma * sigma)) // single Gaussian crest, 0..1
		b.WriteString(themeFg(mixColor(base, crest, w), string(ch)))
	}
	return b.String()
}

// gradient paints s with a STATIC smooth sweep through the given theme colours —
// a fixed brand sheen for the startup wordmark/banner, no animation. Adjacent
// stops are linearly interpolated so a wide banner reads as one continuous ramp
// rather than three flat bands. strong=true keeps every glyph bold (applied per
// rune so an SGR reset can't drop boldness early). Under NO_COLOR / a non-tty it
// returns s unchanged.
func gradient(s string, strong bool, cols ...cliColor) string {
	if !colorEnabled || len(cols) == 0 {
		return s
	}
	r := []rune(s)
	n := len(r)
	if n == 0 {
		return s
	}
	stops := float64(len(cols) - 1)
	denom := float64(n - 1)
	if denom <= 0 {
		denom = 1
	}
	var b strings.Builder
	for i, ch := range r {
		col := cols[0]
		if stops > 0 {
			pos := float64(i) / denom * stops
			lo := int(pos)
			if lo >= len(cols)-1 {
				lo = len(cols) - 2
			}
			col = mixColor(cols[lo], cols[lo+1], pos-float64(lo))
		}
		span := themeFg(col, string(ch))
		if strong {
			span = bold(span)
		}
		b.WriteString(span)
	}
	return b.String()
}

// mixColor linearly interpolates between two theme colours at t∈[0,1], producing
// a truecolor span when the terminal supports it (smooth ramp) and degrading to
// the nearer endpoint's 256-colour index otherwise (a crisp two-tone wave that's
// still clearly on the text). It's the shared engine under shimmer and gradient.
func mixColor(a, b cliColor, t float64) cliColor {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab, okA := parseHexColor(a.hex)
	br, bg, bb, okB := parseHexColor(b.hex)
	if !okA || !okB {
		if t >= 0.5 {
			return b
		}
		return a
	}
	mix := func(x, y int64) int64 { return x + int64(float64(y-x)*t+0.5) }
	xt := a.xterm
	if t >= 0.5 {
		xt = b.xterm
	}
	return cliColor{
		hex:   fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb)),
		xterm: xt,
	}
}

// gradientColorAt returns the smoothly-interpolated colour at position pos∈[0,1]
// across the given stops — the per-cell engine the banner uses to paint a diagonal
// light axis (gradient() is the same idea but tied to a 1-D rune index).
func gradientColorAt(pos float64, cols ...cliColor) cliColor {
	if len(cols) == 0 {
		return activeCLITheme.accent
	}
	if len(cols) == 1 {
		return cols[0]
	}
	if pos < 0 {
		pos = 0
	}
	if pos > 1 {
		pos = 1
	}
	p := pos * float64(len(cols)-1)
	lo := int(p)
	if lo >= len(cols)-1 {
		lo = len(cols) - 2
	}
	return mixColor(cols[lo], cols[lo+1], p-float64(lo))
}

// roachHeroRows renders the ROACH wordmark with a DIAGONAL foreground gradient
// (top-left→bottom-right) so the light has one axis — but NO background fill: like
// the rest of the TUI it sits on the user's own terminal background, only the glyphs
// are coloured. (An earlier version painted a radial warm halo BEHIND the glyphs; we
// dropped it with the rest of the background fills so nothing covers the terminal's
// own colour.) Truecolor gets the per-cell diagonal ramp; otherwise the flat 1-D sweep.
func roachHeroRows(stops []cliColor) []string {
	artW := roachArtWidth()
	out := make([]string, len(roachArtRows))
	if !colorEnabled || !supportsTrueColor() {
		for y, row := range roachArtRows {
			out[y] = gradient(padRight(row, artW), true, stops...)
		}
		return out
	}
	rows := float64(len(roachArtRows))
	for y, row := range roachArtRows {
		r := []rune(row)
		var b strings.Builder
		for x := 0; x < len(r); x++ {
			fgPos := float64(x)/float64(artW)*0.6 + float64(y)/rows*0.4
			b.WriteString(fgSGR(gradientColorAt(fgPos, stops...)) + ansiBold + string(r[x]) + ansiReset)
		}
		out[y] = b.String()
	}
	return out
}

// roachHeroShimmerRows renders the ROACH wordmark with its diagonal base gradient
// PLUS a soft, slow brightness crest that travels diagonally across it as phase
// advances — the banner's ambient "glow", the static-art analogue of the thinking
// verb's shimmer(). The crest lightens the base colour toward warm off-white (capped,
// so it glows rather than blows out) with a wide gaussian, and sweeps over a period a
// little wider than the art so there's a calm lull between passes. Foreground only,
// no background.
//
// Works in BOTH truecolor AND 256-colour: mixColor degrades to the nearer endpoint's
// xterm index, so the crest reads as a moving band of brightened cells exactly the way
// the thinking shimmer() animates on a 256-colour terminal. (It used to bail to the
// STATIC gradient when truecolor wasn't detected — which is why the banner sat frozen
// while the thinking line still shimmered: thinking never bailed.) Plain only under
// NO_COLOR.
func roachHeroShimmerRows(stops []cliColor, phase int) []string {
	artW := roachArtWidth()
	if !colorEnabled {
		return roachHeroRows(stops)
	}
	rows := float64(len(roachArtRows))
	period := float64(artW) + 8
	center := math.Mod(math.Mod(float64(phase), period)+period, period)
	const sigma = 5.0 // crest half-width in cells — a clear glowing band, still soft
	out := make([]string, len(roachArtRows))
	for y, row := range roachArtRows {
		r := []rune(row)
		var b strings.Builder
		for x := 0; x < len(r); x++ {
			fgPos := float64(x)/float64(artW)*0.6 + float64(y)/rows*0.4
			base := gradientColorAt(fgPos, stops...)
			d := (float64(x) + float64(y)) - center           // mild diagonal axis
			glow := math.Exp(-(d*d)/(2*sigma*sigma)) * 0.92    // bright crest — clearly lit
			col := mixColor(base, activeCLITheme.text, glow)   // lighten toward warm off-white
			b.WriteString(fgSGR(col) + ansiBold + string(r[x]) + ansiReset)
		}
		out[y] = b.String()
	}
	return out
}

// roachArtRows is the "ROACH" wordmark in the ANSI Shadow figlet face — six rows
// of equal display width (asserted by TestRoachArtAligned). renderTUIBanner sweeps
// a gradient across each row at session start, and falls back to a compact
// wordmark when the terminal is too narrow to hold the full art.
var roachArtRows = []string{
	"██████╗  ██████╗  █████╗  ██████╗██╗  ██╗",
	"██╔══██╗██╔═══██╗██╔══██╗██╔════╝██║  ██║",
	"██████╔╝██║   ██║███████║██║     ███████║",
	"██╔══██╗██║   ██║██╔══██║██║     ██╔══██║",
	"██║  ██║╚██████╔╝██║  ██║╚██████╗██║  ██║",
	"╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝",
}

// roachArtWidth is the display width of the wordmark art, measured from the data
// so it can never drift from the rows above.
func roachArtWidth() int {
	w := 0
	for _, row := range roachArtRows {
		if rw := visibleWidth(row); rw > w {
			w = rw
		}
	}
	return w
}
