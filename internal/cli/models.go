package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"roach-code/internal/config"
)

// modelsCommand implements `roach-code models`:
//
//	models                       list configured providers and their models
//	models refresh [provider]    re-fetch each provider's GET /models list and
//	                             write the updated models=[...] back to the
//	                             source TOML (--dry-run previews only)
//
// Refresh edits only the models/default lines of each [[providers]] block, so
// comments, auth modes, and every other field are preserved.
func modelsCommand(args []string) int {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "ls":
		return modelsList()
	case "refresh", "update", "sync":
		return modelsRefresh(args)
	default:
		fmt.Fprintln(os.Stderr, "usage: roach-code models [list | refresh [provider] [--dry-run]]")
		return 2
	}
}

func modelsList() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return 1
	}
	if len(cfg.Providers) == 0 {
		fmt.Println("No providers configured. Run `roach-code setup`.")
		return 0
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		status := dim("(no key — set " + p.APIKeyEnv + ")")
		if p.Configured() {
			status = green("✓ ready")
		}
		fmt.Printf("%s  %s  %s\n", bold(p.Name), dim(p.Kind), status)
		def := p.DefaultModel()
		for _, m := range p.ModelList() {
			marker := "  "
			if m == def {
				marker = green("→ ")
			}
			fmt.Printf("    %s%s\n", marker, m)
		}
	}
	fmt.Println()
	fmt.Println(dim("Refresh from the provider's API:  roach-code models refresh [provider]"))
	return 0
}

func modelsRefresh(args []string) int {
	fs := flag.NewFlagSet("models refresh", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing the file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	only := ""
	if fs.NArg() > 0 {
		only = fs.Arg(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: %v\n", err)
		return 1
	}
	path := config.SourcePath()
	if path == "" {
		fmt.Fprintln(os.Stderr, "models: no roach-code.toml found. Run `roach-code setup` first.")
		return 1
	}

	updates := map[string]config.ModelsUpdate{}
	matched := false
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if only != "" && p.Name != only {
			continue
		}
		matched = true

		// OAuth / Responses providers don't expose the OpenAI /models list (the
		// ChatGPT-account model is pinned), so leave codex entries alone.
		if p.Kind == "openai-responses" || p.Kind == "codex" {
			fmt.Printf("%s  %s\n", bold(p.Name), dim("skipped — OAuth/Responses provider, model is pinned"))
			continue
		}
		if p.APIKey() == "" {
			fmt.Printf("%s  %s\n", bold(p.Name), dim("skipped — no API key (set "+p.APIKeyEnv+")"))
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		fetched, err := p.FetchModels(ctx)
		cancel()
		if err != nil {
			fmt.Printf("%s  %s\n", bold(p.Name), dim("skipped — "+flattenErr(err.Error())))
			continue
		}
		if len(fetched) == 0 {
			fmt.Printf("%s  %s\n", bold(p.Name), dim("skipped — provider returned no models"))
			continue
		}

		current := p.ModelList()
		added, removed := diffModels(current, fetched)
		def := p.DefaultModel()
		if def == "" || !containsStr(fetched, def) {
			def = fetched[0]
		}

		fmt.Printf("%s  %s\n", bold(p.Name), dim(fmt.Sprintf("(%s)", p.Kind)))
		if len(added) == 0 && len(removed) == 0 {
			fmt.Printf("    %s\n", dim(fmt.Sprintf("already up to date (%d models)", len(fetched))))
			continue
		}
		for _, m := range added {
			fmt.Printf("    %s %s\n", green("+"), m)
		}
		for _, m := range removed {
			fmt.Printf("    %s %s\n", dim("-"), dim(m))
		}
		fmt.Printf("    %s\n", dim(fmt.Sprintf("%d models, default %q", len(fetched), def)))
		updates[p.Name] = config.ModelsUpdate{Models: fetched, Default: def}
	}

	if only != "" && !matched {
		fmt.Fprintf(os.Stderr, "models: no provider named %q (see `roach-code models`)\n", only)
		return 1
	}
	if len(updates) == 0 {
		fmt.Println(dim("Nothing to update."))
		return 0
	}
	if *dryRun {
		fmt.Println()
		fmt.Println(dim("dry-run — no file written. Re-run without --dry-run to apply."))
		return 0
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "models: read %s: %v\n", path, err)
		return 1
	}
	out, missing := config.RewriteProviderModels(string(src), updates)
	if len(missing) > 0 {
		// Shouldn't happen — every update came from a loaded provider — but warn
		// rather than silently skip.
		fmt.Fprintf(os.Stderr, "models: could not locate in %s: %s\n", path, strings.Join(missing, ", "))
	}
	backup := path + ".bak"
	if err := os.WriteFile(backup, src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "models: write backup %s: %v\n", backup, err)
		return 1
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "models: write %s: %v\n", path, err)
		return 1
	}
	fmt.Println()
	fmt.Printf("%s %s\n", green("✓"), fmt.Sprintf("updated %d provider(s) in %s", len(updates), path))
	fmt.Println(dim("backup: " + backup))
	return 0
}

// diffModels returns models present in fetched but not current (added) and in
// current but not fetched (removed), each sorted.
func diffModels(current, fetched []string) (added, removed []string) {
	cur := map[string]bool{}
	for _, m := range current {
		cur[m] = true
	}
	got := map[string]bool{}
	for _, m := range fetched {
		got[m] = true
		if !cur[m] {
			added = append(added, m)
		}
	}
	for _, m := range current {
		if !got[m] {
			removed = append(removed, m)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// flattenErr collapses a multi-line error into a single trimmed line for compact
// status output.
func flattenErr(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}
