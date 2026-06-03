package cli

import (
	"strings"
	"testing"

	"roach-code/internal/control"
)

// TestRewriteAnswerBlockOverwritesInPlace proves rewriteAnswerBlock overwrites
// exactly transcript[answerIdx], flags the transcript dirty for a viewport
// re-feed, and leaves the surrounding committed lines byte-for-byte untouched.
func TestRewriteAnswerBlockOverwritesInPlace(t *testing.T) {
	cases := []struct {
		name      string
		answerIdx int
		block     string
		want      []string
	}{
		{
			name:      "middle slot",
			answerIdx: 1,
			block:     "NEW",
			want:      []string{"a", "NEW", "c"},
		},
		{
			name:      "first slot",
			answerIdx: 0,
			block:     "Z",
			want:      []string{"Z", "b", "c"},
		},
		{
			name:      "last slot",
			answerIdx: 2,
			block:     "tail",
			want:      []string{"a", "b", "tail"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &chatTUI{
				transcript: []string{"a", "b", "c"},
				answerIdx:  tc.answerIdx,
			}
			m.rewriteAnswerBlock(tc.block)

			if got := m.transcript[tc.answerIdx]; got != tc.block {
				t.Errorf("transcript[%d] = %q, want %q", tc.answerIdx, got, tc.block)
			}
			if !m.transcriptDirty {
				t.Errorf("rewriteAnswerBlock must set transcriptDirty for a viewport re-feed")
			}
			for i := range tc.want {
				if m.transcript[i] != tc.want[i] {
					t.Errorf("transcript[%d] = %q, want %q (neighbors must be untouched)", i, m.transcript[i], tc.want[i])
				}
			}
		})
	}
}

// TestRewriteAnswerBlockDoesNotMirrorPendingCommit documents the deliberate
// NON-equivalence to replaceTranscriptLine (chat_transcript.go): that helper
// mirrors an in-place rewrite into *pendingCommit when the slot has not been
// flushed by finalize yet, but rewriteAnswerBlock intentionally does NOT — the
// answer slot's pendingCommit handling differs, and only the transcript write +
// transcriptDirty re-feed is shared. We seed pendingCommit so that the rewritten
// answer slot WOULD fall inside the pending window (firstPending = len(transcript)
// - len(*pendingCommit)); replaceTranscriptLine would overwrite it, but
// rewriteAnswerBlock must leave *pendingCommit byte-for-byte unchanged.
func TestRewriteAnswerBlockDoesNotMirrorPendingCommit(t *testing.T) {
	// transcript has 3 lines; a 3-element pendingCommit means firstPending == 0,
	// so EVERY index (including answerIdx) sits inside the unflushed window — the
	// exact condition under which replaceTranscriptLine would mirror.
	commit := []string{"a", "old", "c"}
	m := &chatTUI{
		transcript:    []string{"a", "old", "c"},
		answerIdx:     1,
		pendingCommit: &commit,
	}

	m.rewriteAnswerBlock("NEW")

	if m.transcript[1] != "NEW" {
		t.Fatalf("transcript[1] = %q, want %q", m.transcript[1], "NEW")
	}
	// The contrast: pendingCommit is the queue finalize flushes to scrollback. A
	// mirror would have rewritten (*pendingCommit)[1] to "NEW"; rewriteAnswerBlock
	// must NOT, so the queued block stays the pre-rewrite value.
	if got := (*m.pendingCommit); !equalStrings(got, []string{"a", "old", "c"}) {
		t.Errorf("rewriteAnswerBlock leaked into *pendingCommit: got %v, want %v (it must NOT mirror like replaceTranscriptLine)", got, []string{"a", "old", "c"})
	}
}

// TestRewriteAnswerBlockVsReplaceTranscriptLine pins the divergence directly:
// the same starting state, rewritten through each helper, differs only in whether
// *pendingCommit is mirrored. This is the regression guard for the comment in
// chat_answer.go that the answer-block write is the PLAIN transcript write.
func TestRewriteAnswerBlockVsReplaceTranscriptLine(t *testing.T) {
	newM := func() (*chatTUI, *[]string) {
		commit := []string{"a", "old", "c"}
		return &chatTUI{
			transcript:    []string{"a", "old", "c"},
			answerIdx:     1,
			pendingCommit: &commit,
		}, &commit
	}

	// rewriteAnswerBlock: transcript changes, pendingCommit does NOT.
	mRewrite, rewriteCommit := newM()
	mRewrite.rewriteAnswerBlock("NEW")
	if mRewrite.transcript[1] != "NEW" {
		t.Fatalf("rewrite transcript[1] = %q, want NEW", mRewrite.transcript[1])
	}
	if (*rewriteCommit)[1] != "old" {
		t.Errorf("rewriteAnswerBlock must leave (*pendingCommit)[1]=%q == old", (*rewriteCommit)[1])
	}

	// replaceTranscriptLine: transcript AND pendingCommit both change (firstPending==0).
	mReplace, replaceCommit := newM()
	mReplace.replaceTranscriptLine(1, "NEW")
	if mReplace.transcript[1] != "NEW" {
		t.Fatalf("replace transcript[1] = %q, want NEW", mReplace.transcript[1])
	}
	if (*replaceCommit)[1] != "NEW" {
		t.Errorf("replaceTranscriptLine should mirror into (*pendingCommit)[1], got %q", (*replaceCommit)[1])
	}
}

// TestCommitPendingStripsGoalStatusSentinel proves commitPending drops a trailing
// GOAL_STATUS sentinel from the DISPLAYED answer (via control.StripGoalVerdict)
// before it reaches the visible transcript, while keeping the real answer body.
// It uses the package's full test chatTUI (renderer wired) — see
// newTestChatTUI in chat_render_test.go.
func TestCommitPendingStripsGoalStatusSentinel(t *testing.T) {
	cases := []struct {
		name       string
		answer     string
		wantBody   string // substring that must survive into the transcript
		wantAbsent string // substring that must NOT appear in the transcript
	}{
		{
			name:       "met verdict stripped",
			answer:     "Done implementing the feature.\n\nGOAL_STATUS: met — the tests pass",
			wantBody:   "Done implementing the feature.",
			wantAbsent: "GOAL_STATUS",
		},
		{
			name:       "unmet verdict stripped",
			answer:     "Made progress.\n\nGOAL_STATUS: unmet — wire the renderer",
			wantBody:   "Made progress.",
			wantAbsent: "GOAL_STATUS",
		},
		{
			name:       "no sentinel passes through",
			answer:     "Plain answer with no verdict.",
			wantBody:   "Plain answer with no verdict.",
			wantAbsent: "GOAL_STATUS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestChatTUI()
			m.pending.WriteString(tc.answer)

			m.commitPending()

			joined := strings.Join(m.transcript, "\n")
			if !strings.Contains(joined, tc.wantBody) {
				t.Errorf("answer body %q missing from committed transcript:\n%s", tc.wantBody, joined)
			}
			if strings.Contains(joined, tc.wantAbsent) {
				t.Errorf("sentinel %q must be stripped from the visible transcript:\n%s", tc.wantAbsent, joined)
			}
			// State resets after a commit (mirrors TestStreamAnswerFlushesCompletedParagraphs).
			if m.pending.Len() != 0 || m.answerIdx != -1 {
				t.Errorf("answer state should reset after commitPending: pending=%d answerIdx=%d", m.pending.Len(), m.answerIdx)
			}
		})
	}
}

// TestCommitPendingEmptyAfterStrip proves a pending buffer that is ONLY a
// GOAL_STATUS sentinel collapses to nothing: StripGoalVerdict yields "" so
// commitPending commits no answer block and still resets streaming state.
func TestCommitPendingEmptyAfterStrip(t *testing.T) {
	m := newTestChatTUI()
	m.pending.WriteString("GOAL_STATUS: met — all done")

	before := len(m.transcript)
	m.commitPending()

	if len(m.transcript) != before {
		t.Errorf("a sentinel-only answer must commit no transcript block: len %d -> %d", before, len(m.transcript))
	}
	if m.pending.Len() != 0 || m.answerIdx != -1 {
		t.Errorf("state should reset even on an empty-after-strip answer: pending=%d answerIdx=%d", m.pending.Len(), m.answerIdx)
	}
}

// TestStripGoalVerdictBoundary pins the StripGoalVerdict contract commitPending
// relies on, at the control-package boundary: it strips a trailing met/unmet
// sentinel, leaves text with no sentinel untouched, and (defensively) does NOT
// eat a sentinel that is followed by real content (not the final line).
func TestStripGoalVerdictBoundary(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips trailing met",
			in:   "Answer body.\n\nGOAL_STATUS: met — done",
			want: "Answer body.",
		},
		{
			name: "strips trailing unmet",
			in:   "Answer body.\nGOAL_STATUS: unmet — next step",
			want: "Answer body.",
		},
		{
			name: "no sentinel unchanged",
			in:   "Just an answer.",
			want: "Just an answer.",
		},
		{
			name: "non-final sentinel left intact",
			in:   "GOAL_STATUS: met — early\nmore text after",
			want: "GOAL_STATUS: met — early\nmore text after",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := control.StripGoalVerdict(tc.in); got != tc.want {
				t.Errorf("StripGoalVerdict(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// equalStrings is a local helper (kept private to this file to avoid colliding
// with sibling test helpers) comparing two string slices element-wise.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
