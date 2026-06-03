package cli

import (
	"testing"
)

// TestResetToolStreamBufClearsOnlyBufferFields verifies resetToolStreamBuf zeroes
// the three bounded-tail buffer fields (toolTail / toolPartial / toolLineCount)
// while leaving the slot identity (toolStreamIdx / toolStreamID) untouched — the
// helper's documented contract is that call sites manage identity, so the reset
// must not clobber it.
func TestResetToolStreamBufClearsOnlyBufferFields(t *testing.T) {
	m := &chatTUI{
		toolTail:      []string{"x", "y"},
		toolPartial:   "p",
		toolLineCount: 3,
		toolStreamIdx: 7,
		toolStreamID:  "abc",
	}

	m.resetToolStreamBuf()

	if len(m.toolTail) != 0 {
		t.Errorf("toolTail should be emptied, got len=%d (%v)", len(m.toolTail), m.toolTail)
	}
	if m.toolPartial != "" {
		t.Errorf("toolPartial should be cleared, got %q", m.toolPartial)
	}
	if m.toolLineCount != 0 {
		t.Errorf("toolLineCount should be reset to 0, got %d", m.toolLineCount)
	}
	// Identity must survive the buffer reset.
	if m.toolStreamIdx != 7 {
		t.Errorf("toolStreamIdx must be untouched, want 7 got %d", m.toolStreamIdx)
	}
	if m.toolStreamID != "abc" {
		t.Errorf("toolStreamID must be untouched, want %q got %q", "abc", m.toolStreamID)
	}
}

// TestConnectorLineMatchesPrimitiveExpression locks connectorLine to its original
// definition: connectorLine(text, width) == surfaceWrap(connectorBlock([]string{text}), width)
// byte-for-byte, so the dedup helper can never silently drift from the expression
// it factored out. The RHS is recomputed inline from the same same-package
// primitives at several widths and inner texts.
func TestConnectorLineMatchesPrimitiveExpression(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
	}{
		{name: "FiveLinesWidth80", text: dim("5 lines"), width: 80},
		{name: "WorkingWidth40", text: dim("working · 3s"), width: 40},
		{name: "ManyLinesWidth120", text: dim("128 lines"), width: 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := connectorLine(tc.text, tc.width)
			want := surfaceWrap(connectorBlock([]string{tc.text}), tc.width)
			if got != want {
				t.Errorf("connectorLine(%q, %d) = %q, want %q (must equal surfaceWrap(connectorBlock([]string{text}), width))",
					tc.text, tc.width, got, want)
			}
		})
	}
}
