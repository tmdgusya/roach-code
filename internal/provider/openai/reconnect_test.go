package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"roach-code/internal/provider"
)

// TestStreamTreatsCleanEOFWithoutDoneAsCut reproduces the half-streamed
// tool-call bug (issue #3953 upstream): a proxy that idle-closes the SSE
// connection with a clean FIN ends the scan with no error, which used to commit
// the turn as complete — including half-streamed tool-call arguments that then
// 400 on every replay. With the sawDone/finish_reason guard the cut now surfaces
// as a stream error instead of committing truncated args.
func TestStreamTreatsCleanEOFWithoutDoneAsCut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A clean close: keep-alive comment only, no [DONE], no finish_reason.
		_, _ = io.WriteString(w, ": keep-alive\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "deepseek", BaseURL: srv.URL, Model: "deepseek-v4", APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var streamErr error
	for chunk := range ch {
		if chunk.Type == provider.ChunkError {
			streamErr = chunk.Err
		}
	}
	if streamErr == nil {
		t.Fatal("clean EOF without [DONE]/finish_reason should surface as a stream error, not a completed turn")
	}
	if !errors.Is(streamErr, io.ErrUnexpectedEOF) {
		t.Errorf("stream error = %v, want one wrapping io.ErrUnexpectedEOF", streamErr)
	}
}

// TestStreamDropsPartialToolCallOnCleanEOF is the post-output half of #3953:
// the connection dies mid-tool-call after the call's start was forwarded. The
// partial arguments must never surface as a ChunkToolCall — the cut surfaces as
// a stream error so the truncated args are never committed to history.
func TestStreamDropsPartialToolCallOnCleanEOF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A tool-call delta with truncated arguments, then clean close (no [DONE]).
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\": \\\"ls\"}}]}}]}\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "deepseek", BaseURL: srv.URL, Model: "deepseek-v4", APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var gotToolCall bool
	var streamErr error
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkToolCall:
			gotToolCall = true
		case provider.ChunkError:
			streamErr = chunk.Err
		}
	}
	if gotToolCall {
		t.Error("partial tool call surfaced as a complete ChunkToolCall — truncated args must never be committed")
	}
	if streamErr == nil || !errors.Is(streamErr, io.ErrUnexpectedEOF) {
		t.Errorf("stream error = %v, want one wrapping io.ErrUnexpectedEOF", streamErr)
	}
}

// TestStreamAcceptsFinishReasonWithoutDone keeps gateways that omit the [DONE]
// sentinel working: a finish_reason marks the turn complete on its own.
func TestStreamAcceptsFinishReasonWithoutDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "deepseek", BaseURL: srv.URL, Model: "deepseek-v4", APIKey: "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var text strings.Builder
	for chunk := range ch {
		if chunk.Type == provider.ChunkError {
			t.Fatalf("finish_reason without [DONE] should complete cleanly: %v", chunk.Err)
		}
		if chunk.Type == provider.ChunkText {
			text.WriteString(chunk.Text)
		}
	}
	if text.String() != "hello" {
		t.Errorf("text = %q, want %q", text.String(), "hello")
	}
}
