// Package config loads Roach Code's runtime configuration from TOML. Resolution order:
// flag > project ./roach-code.toml > user ~/.config/roach-code/config.toml >
// legacy user ~/roach-code.toml > built-in defaults.
// Secrets come from the environment via api_key_env and are never stored in
// config files.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"roach-code/internal/netclient"
	"roach-code/internal/provider"
)

// Config is Roach Code's runtime configuration.
type Config struct {
	DefaultModel string            `toml:"default_model"`
	Language     string            `toml:"language"` // ui/model language tag (e.g. "zh"); empty = auto-detect from $LANG / $ROACH_LANG
	UI           UIConfig          `toml:"ui"`
	Agent        AgentConfig       `toml:"agent"`
	Providers    []ProviderEntry   `toml:"providers"`
	Tools        ToolsConfig       `toml:"tools"`
	Permissions  PermissionsConfig `toml:"permissions"`
	Sandbox      SandboxConfig     `toml:"sandbox"`
	Network      NetworkConfig     `toml:"network"`
	Plugins      []PluginEntry     `toml:"plugins"`
	Skills       SkillsConfig      `toml:"skills"`
	Codegraph    CodegraphConfig   `toml:"codegraph"`
	Statusline   StatuslineConfig  `toml:"statusline"`
	LSP          LSPConfig         `toml:"lsp"`
	Workflow     WorkflowConfig    `toml:"workflow"`
}

// WorkflowConfig bounds the `run_workflow` tool's dynamic workflows. The caps
// are enforced by the runtime, not the model's script. Zero values fall back to
// safe defaults (see Default), so an absent [workflow] block still works. The
// limits are deliberately lower than Claude Code's 16/1000 — roach-code drives
// cheaper OpenAI-compatible models, so the right ceiling is a local choice.
type WorkflowConfig struct {
	MaxConcurrent int   `toml:"max_concurrent"` // simultaneous sub-agents; default 8
	MaxAgents     int   `toml:"max_agents"`     // total agent() calls per run; default 200
	MaxTokens     int64 `toml:"max_tokens"`     // output-token ceiling per run; 0 = no ceiling
}

// UIConfig controls presentation-only settings. Theme affects CLI rendering; the
// desktop frontend keeps its own browser-local theme setting.
type UIConfig struct {
	Theme      string `toml:"theme"`       // auto|dark|light; empty resolves to auto
	ThemeStyle string `toml:"theme_style"` // graphite|ember|aurora|midnight|sandstone|porcelain|linen|glacier
}

// UITheme normalizes ui.theme to a supported value.
func (c *Config) UITheme() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.Theme)) {
	case "dark":
		return "dark"
	case "light":
		return "light"
	default:
		return "auto"
	}
}

// UIThemeStyle normalizes ui.theme_style. Empty means "pick the default style
// for the resolved light/dark shell".
func (c *Config) UIThemeStyle() string {
	switch strings.ToLower(strings.TrimSpace(c.UI.ThemeStyle)) {
	case "graphite", "ember", "aurora", "midnight", "sandstone", "porcelain", "linen", "glacier":
		return strings.ToLower(strings.TrimSpace(c.UI.ThemeStyle))
	default:
		return ""
	}
}

// LSPConfig governs the optional Language Server Protocol tools (lsp_definition,
// lsp_references, lsp_hover, lsp_diagnostics). Enabled defaults to true; the
// servers themselves are never bundled — each resolves on PATH and the tool
// returns an install hint when it is missing, so the capability is dormant until
// the user installs a server. Servers overrides or extends the built-in language
// → server map, keyed by language id (e.g. "go", "rust", "python").
type LSPConfig struct {
	Enabled bool                 `toml:"enabled"`
	Servers map[string]LSPServer `toml:"servers"`
}

// LSPServer overrides a built-in language's server or, when keyed by a new
// language, adds one. An empty field falls back to the built-in default for that
// language; Extensions is required when adding a language the built-ins don't
// cover (e.g. ".ex" for Elixir) so files route to it.
type LSPServer struct {
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	LanguageID  string            `toml:"language_id"`
	Extensions  []string          `toml:"extensions"`
	InstallHint string            `toml:"install_hint"`
}

// StatuslineConfig configures a custom status line. Command, when set, is run at
// startup and after each turn; its first line of stdout replaces the built-in
// status data row. A JSON payload (model, context tokens, cwd) is fed on stdin.
type StatuslineConfig struct {
	Command string `toml:"command"`
}

// CodegraphConfig governs the built-in CodeGraph MCP server — symbol/call-graph
// code intelligence (tree-sitter + SQLite) that gives the agent codegraph_*
// search / context / explore / trace / node tools. Enabled defaults to true; set
// enabled = false to drop those tools and fall back to grep/glob. AutoInstall
// (default true) lets roach-code fetch the CodeGraph runtime into its cache on first
// use; set false to require an explicit `roach-code codegraph install` (e.g. for
// air-gapped or headless runs). Path overrides binary resolution; empty resolves
// the cache, then a `codegraph` on PATH, then a bundle beside the executable.
type CodegraphConfig struct {
	Enabled     bool   `toml:"enabled"`
	AutoInstall bool   `toml:"auto_install"`
	Path        string `toml:"path"`
}

// NetworkConfig controls ordinary outbound HTTP traffic such as model providers,
// wallet-balance lookups, updater checks, and CodeGraph downloads. It intentionally
// does not apply to web_fetch, which keeps its own SSRF-guarded dialer.
type NetworkConfig struct {
	// ProxyMode is "auto" (default; environment proxy for now), "env", "custom",
	// or "off". auto leaves room for OS proxy detection later without changing the
	// config shape.
	ProxyMode string `toml:"proxy_mode"`
	// ProxyURL is an advanced custom override such as "socks5://127.0.0.1:7890".
	// When set and proxy_mode = "custom", it wins over the structured proxy table.
	ProxyURL string `toml:"proxy_url"`
	// NoProxy is honored for custom proxies. Env/auto modes use NO_PROXY from the
	// process environment instead.
	NoProxy string             `toml:"no_proxy"`
	Proxy   NetworkProxyConfig `toml:"proxy"`
}

// NetworkProxyConfig is the structured custom-proxy editor shape. Password is
// optional and supports ${VAR} expansion, so users can avoid storing it literally.
type NetworkProxyConfig struct {
	Type     string `toml:"type"` // http|https|socks5|socks5h
	Server   string `toml:"server"`
	Port     int    `toml:"port"`
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// NetworkProxySpec returns the expanded proxy settings used by netclient.
func (c *Config) NetworkProxySpec() netclient.ProxySpec {
	return netclient.ProxySpec{
		Mode:     c.Network.ProxyMode,
		URL:      ExpandVars(c.Network.ProxyURL),
		NoProxy:  ExpandVars(c.Network.NoProxy),
		Type:     c.Network.Proxy.Type,
		Server:   ExpandVars(c.Network.Proxy.Server),
		Port:     c.Network.Proxy.Port,
		Username: ExpandVars(c.Network.Proxy.Username),
		Password: ExpandVars(c.Network.Proxy.Password),
	}
}

// NetworkProxyMode normalizes network.proxy_mode to a known value.
func (c *Config) NetworkProxyMode() string {
	return netclient.NormalizeMode(c.Network.ProxyMode)
}

// SkillsConfig configures skill discovery. Paths adds extra "custom"-scope skill
// roots — each a directory of SKILL.md / <name>.md playbooks — scanned between
// the project roots (.roach-code/.agents/.claude under the workspace) and the
// global roots (the same three under the home dir). ~ and relative paths and
// ${VAR} expansion are supported.
type SkillsConfig struct {
	Paths []string `toml:"paths"`
}

// SkillCustomPaths returns the configured custom skill roots with ${VAR}
// expanded; empty entries are dropped.
func (c *Config) SkillCustomPaths() []string {
	var out []string
	for _, p := range c.Skills.Paths {
		if p = ExpandVars(p); strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

// SandboxConfig bounds the blast radius of tool calls (Phase 0: file-writer
// confinement). WorkspaceRoot is the directory the built-in file writers
// (write_file / edit_file / multi_edit) may modify; empty means the current
// working directory, so writes stay inside the project by default. AllowWrite
// lists extra directories writers may also touch (e.g. a sibling repo or a temp
// dir). Both support ${VAR} / ${VAR:-default} expansion. Reads are unrestricted;
// confining `bash` is Phase 1 (OS-level sandbox).
type SandboxConfig struct {
	WorkspaceRoot string   `toml:"workspace_root"`
	AllowWrite    []string `toml:"allow_write"`
	// Bash is the OS-sandbox mode for the bash tool: "enforce" (default) jails
	// each command, "off" runs it unconfined. Phase 1; macOS only for now, with
	// a graceful fallback elsewhere (see internal/sandbox).
	Bash string `toml:"bash"`
	// Network allows network egress from inside the bash sandbox. Defaults true
	// so module/package downloads keep working; the boundary is then writes.
	Network bool `toml:"network"`
}

// WriteRoots returns the directories file-writer tools may modify: the
// workspace root (defaulting to the current working directory when unset) plus
// any AllowWrite extras, with ${VAR} expanded. The roots are returned as given
// (relative or absolute); the confiner resolves them to absolute, symlink-free
// paths. The result is always non-empty, so confinement is on by default.
func (c *Config) WriteRoots() []string {
	root := ExpandVars(c.Sandbox.WorkspaceRoot)
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		} else {
			root = "."
		}
	}
	roots := []string{root}
	for _, d := range c.Sandbox.AllowWrite {
		if d = ExpandVars(d); d != "" {
			roots = append(roots, d)
		}
	}
	return roots
}

// BashMode normalises the bash-sandbox mode: only an explicit "off" disables
// it; empty or any other value resolves to "enforce", so the sandbox is on by
// default and fails safe.
func (c *Config) BashMode() string {
	if c.Sandbox.Bash == "off" {
		return "off"
	}
	return "enforce"
}

// AgentConfig configures the harness loop. PlannerModel is optional: when set
// to another provider's name it enables two-model collaboration, where the
// planner handles low-frequency planning in its own session (kept separate so
// each model's prompt prefix stays cache-stable). SubagentModel is the optional
// default for runAs=subagent skills; SubagentModels overrides it per skill name.
type AgentConfig struct {
	SystemPrompt     string            `toml:"system_prompt"`
	SystemPromptFile string            `toml:"system_prompt_file"`
	MaxSteps         int               `toml:"max_steps"` // tool-call rounds per turn; 0 = unlimited
	Temperature      float64           `toml:"temperature"`
	PlannerModel     string            `toml:"planner_model"`
	SubagentModel    string            `toml:"subagent_model"`
	SubagentModels   map[string]string `toml:"subagent_models"`
	// OutputStyle selects a persona/tone block folded into the system prompt at
	// startup (a built-in like "explanatory"/"learning"/"concise", or a custom
	// .roach-code/output-styles/<name>.md). Empty = the unmodified prompt.
	OutputStyle string `toml:"output_style"`
}

// ProviderEntry declares a model provider instance. ContextWindow is the model's
// token budget; the harness compacts older history as a turn's prompt approaches
// it (see agent compaction). 0 disables compaction for the instance.
type ProviderEntry struct {
	Name          string            `toml:"name"`
	Kind          string            `toml:"kind"`
	BaseURL       string            `toml:"base_url"`
	Model         string            `toml:"model"`      // a single model (back-compat)
	Models        []string          `toml:"models"`     // a vendor's model list (one base_url/key, many models)
	ModelsURL     string            `toml:"models_url"` // auto-fetch models from this URL on startup
	Default       string            `toml:"default"`    // default model when Models is set (else Models[0])
	APIKeyEnv     string            `toml:"api_key_env"`
	BalanceURL    string            `toml:"balance_url"` // optional; a provider-specific wallet-balance endpoint (DeepSeek: https://api.deepseek.com/user/balance). Empty = no balance readout.
	ContextWindow int               `toml:"context_window"`
	Price         *provider.Pricing `toml:"price"`
	// Thinking / Effort are provider-kind-specific knobs forwarded to the provider
	// via Config.Extra. The anthropic provider reads Thinking="adaptive" to enable
	// extended thinking and Effort ("low".."max") to tune depth. The
	// openai-compatible provider forwards Effort as reasoning_effort for
	// thinking-capable models; DeepSeek accepts high|max.
	// Empty = provider default.
	Thinking string `toml:"thinking"`
	Effort   string `toml:"effort"`
	// Auth selects the authentication method for OAuth-capable kinds
	// (openai-responses/codex): "oauth" uses ChatGPT-subscription login
	// (run `roach-code codex login`; no api_key_env needed), "apikey" uses the
	// api_key_env, and "" auto-detects (api key if set, else an existing login).
	Auth string `toml:"auth"`
}

// ModelList returns the models this provider exposes: the explicit `models` list,
// or the single `model` as a one-element list (back-compat). Empty if neither set.
func (e *ProviderEntry) ModelList() []string {
	if len(e.Models) > 0 {
		return e.Models
	}
	if e.Model != "" {
		return []string{e.Model}
	}
	return nil
}

// DefaultModel returns the provider's default model: the explicit `default`, else
// the first of ModelList.
func (e *ProviderEntry) DefaultModel() string {
	if e.Default != "" {
		return e.Default
	}
	if l := e.ModelList(); len(l) > 0 {
		return l[0]
	}
	return ""
}

// HasModel reports whether m is one of the provider's models.
func (e *ProviderEntry) HasModel(m string) bool {
	for _, x := range e.ModelList() {
		if x == m {
			return true
		}
	}
	return false
}

// ToolsConfig selects which built-in tools are enabled. Empty means all of them.
type ToolsConfig struct {
	Enabled []string     `toml:"enabled"`
	Search  SearchConfig `toml:"search"`
}

// SearchConfig tunes the grep tool's engine. Engine is "auto" (default — use
// ripgrep when it's on PATH, else the native Go scanner), "native" (always Go),
// or "rg" (require ripgrep; warn at startup and fall back to native if absent).
// RgPath optionally points at a specific ripgrep binary instead of a PATH lookup.
type SearchConfig struct {
	Engine string `toml:"engine"`
	RgPath string `toml:"rg_path"`
}

// PermissionsConfig declares the per-call permission policy (see
// internal/permission). Mode is the fallback decision for writer tools when no
// rule matches ("ask" | "allow" | "deny"; default "ask"); read-only tools always
// fall back to allow. Allow/Ask/Deny are rule lists of the form "ToolName" or
// "ToolName(glob)". Precedence: deny > ask > allow > fallback.
type PermissionsConfig struct {
	Mode  string   `toml:"mode"`
	Allow []string `toml:"allow"`
	Ask   []string `toml:"ask"`
	Deny  []string `toml:"deny"`
}

// PluginEntry declares an external MCP server. Type selects the transport:
// "stdio" (default) launches Command/Args/Env as a subprocess; "http"
// (a.k.a. streamable-http) and "sse" connect to a remote URL with optional
// static Headers. String fields support ${VAR} / ${VAR:-default} expansion so
// secrets (bearer tokens, keys) come from the environment, not the file. The
// fields mirror the mcpServers spec, so entries can come from either
// roach-code.toml's [[plugins]] or a project-root .mcp.json (see loadMCPJSON).
type PluginEntry struct {
	Name    string            `toml:"name"`
	Type    string            `toml:"type"` // "stdio" (default) | "http" | "sse"
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
	Headers map[string]string `toml:"headers"`
	// AutoStart controls whether the server connects during session startup.
	// Nil preserves historical behavior: configured servers start automatically.
	AutoStart *bool `toml:"auto_start"`
	// Tier selects how aggressively the server is connected at boot:
	//   "eager"      — blocks startup until the handshake completes; required for
	//                  servers whose tools the system prompt depends on.
	//   "lazy"       — registers placeholder tools immediately (from on-disk
	//                  schema cache when available) and only spawns the real
	//                  subprocess on first model use. Default for user plugins.
	//   "background" — placeholder + spawn fired at boot but not waited on;
	//                  swap happens once the spawn finishes.
	// Empty defaults to "lazy" so adding a plugin never slows the next launch.
	Tier string `toml:"tier"`
}

func (e PluginEntry) ShouldAutoStart() bool {
	return e.AutoStart == nil || *e.AutoStart
}

// ResolvedTier returns the normalized tier ("eager"|"lazy"|"background") with
// the project default applied. Unknown values fall back to "lazy" so a typo
// never forces a slow boot.
func (e PluginEntry) ResolvedTier() string {
	switch strings.ToLower(strings.TrimSpace(e.Tier)) {
	case "eager":
		return "eager"
	case "background":
		return "background"
	default:
		return "lazy"
	}
}

func (c *Config) AutoStartPlugins() []PluginEntry {
	out := make([]PluginEntry, 0, len(c.Plugins))
	for _, p := range c.Plugins {
		if p.ShouldAutoStart() {
			out = append(out, p)
		}
	}
	return out
}

// DefaultSystemPrompt is used when config provides none.
const DefaultSystemPrompt = `You are Roach Code, a coding agent focused on executing code tasks.
Use the provided tools to read and write files and run shell commands.
Principles: understand the request before acting; verify with tools instead of
guessing; keep changes minimal and correct; briefly summarize what you did.
When the request leaves a real choice to the user — which approach or library,
the scope, or a consequential or ambiguous decision — call the ask tool to offer
2-4 concrete options rather than guessing or burying the question in prose. Skip
it when there's an obvious default; don't ask just to confirm.
For multi-step work, track progress with the todo_write tool: lay out the steps,
keep exactly one in_progress, and flip each to completed as you finish it — update
the list as you go, not just at the end.`

// AgentOperatingGuide is the durable operating discipline folded into every
// system prompt (after the persona, before language/memory/skills). It is static
// English text, so it rides the cache-stable prefix and costs nothing per turn.
// It encodes how to USE the tools well — the behavioural layer that turns a raw
// model into a reliable coding agent — and lives in code, not the editable toml
// prompt, so it stays consistent and can't be lost to a stray edit. It augments
// (never repeats) the persona prompt, so a custom system_prompt still gets it.
const AgentOperatingGuide = `## Operating guide

Investigate before acting. Read the relevant code with the dedicated tools
(read_file, grep, glob, ls) until you understand how the area actually works;
never edit a file you have not read. Match the conventions of the surrounding
code — its naming, structure, error handling, and comment density — instead of
imposing your own style.

Verify before you claim. After any change that can be checked, run the build and
the tests with bash and read the real output. Never report a task as done,
working, or fixed unless you have verified it; if a step failed or was skipped,
say so plainly with the evidence. A confident but wrong "done" is worse than an
honest "this part is unverified".

Use the tools precisely. Prefer the dedicated tools over their shell equivalents
(grep / read_file / ls / glob / edit_file over shell grep/cat/ls/find/sed) — they
behave the same on every OS. read_file before edit_file, and target the exact
line numbers it reports. Independent read-only lookups may be issued together;
keep calls that write, or that depend on an earlier result, in order.

Stay in scope. Make the smallest correct change that satisfies the request. Do
not refactor, rename, reformat, or "improve" code you were not asked to touch,
and do not add abstraction the task does not need. If you spot a real adjacent
problem, mention it rather than silently changing it.

Decide, or ask — don't guess. When the request leaves a genuine, consequential
choice (which approach or library, the scope, an ambiguous requirement), use the
ask tool to offer 2-4 concrete options. When there is an obvious default, take it
and say what you did; never ask just to confirm.

Track multi-step work with todo_write: lay out the steps, keep exactly one
in_progress, and flip each to completed as you finish — update as you go, not at
the end.

Communicate tersely and truthfully. Lead with the result; skip preamble and
filler. Reference code as file:line. State what you changed and what you actually
verified, and flag anything you could not.

Act safely. Refuse genuinely destructive or unauthorized actions. For changes
that are hard to reverse or reach outside the workspace (deleting data, network
calls with side effects, pushing or publishing), confirm intent first unless the
user has clearly authorized it.`

// LanguagePolicy is the auto fallback appended to the system prompt when no
// concrete UI language is resolved. It is static English text, so it stays part
// of the cache-stable prefix and avoids per-turn language injection.
const LanguagePolicy = `Reply in the same language the user is using in their most recent message: ` +
	`if they write in Chinese answer in Chinese, in English answer in English, and switch ` +
	`whenever they switch. Let this also guide the language you think in. Always keep code, ` +
	`identifiers, file paths, shell commands, and technical terms in their original form — never translate them.`

// Default returns the built-in default configuration (DeepSeek + MiMo presets).
func Default() *Config {
	return &Config{
		DefaultModel: "deepseek-flash",
		UI:           UIConfig{Theme: "auto"},
		Agent: AgentConfig{
			SystemPrompt: DefaultSystemPrompt,
			// 0 = no step cap: the agent loops until the model gives a final answer,
			// the user cancels, or the provider errors. Context stays bounded by
			// compaction, not by a round count. Set a positive agent.max_steps only
			// if you want a hard guard against runaway.
			MaxSteps: 0,
		},
		// Mode "ask" with no rules keeps `roach-code run` autonomous (no TTY → ask
		// resolves to allow) while `roach-code chat` prompts before writers. Users add
		// deny/allow rules to harden or quiet specific tools.
		Permissions: PermissionsConfig{Mode: "ask"},
		// Sandbox on by default: bash is jailed (macOS), network allowed so
		// builds/downloads work. Set bash = "off" to disable. Network=true here
		// so an absent [sandbox] in a user's file keeps egress (zero value would
		// wrongly deny it).
		Sandbox: SandboxConfig{Bash: "enforce", Network: true},
		// CodeGraph code-intelligence on by default: when it resolves it is injected
		// as a built-in MCP server, and AutoInstall fetches it into the cache on
		// first use. Set enabled = false to opt out, or auto_install = false to
		// require an explicit `roach-code codegraph install`.
		Codegraph: CodegraphConfig{Enabled: true, AutoInstall: true},
		// LSP tools on by default, but dormant until a language server is on PATH;
		// a missing server yields an install hint rather than an error.
		LSP:     LSPConfig{Enabled: true},
		Network: NetworkConfig{ProxyMode: netclient.ModeAuto},
		// Dynamic-workflow caps (run_workflow). Conservative defaults for the
		// cheaper models roach-code typically drives; raise in [workflow] if needed.
		Workflow: WorkflowConfig{MaxConcurrent: 8, MaxAgents: 200},
		Providers: []ProviderEntry{
			{Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}},
			{Name: "deepseek-pro", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-pro", APIKeyEnv: "DEEPSEEK_API_KEY", BalanceURL: "https://api.deepseek.com/user/balance", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.025, Input: 3, Output: 6, Currency: "¥"}},
			{Name: "mimo-pro", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.025, Input: 3, Output: 6, Currency: "¥"}},
			{Name: "mimo-flash", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Model: "mimo-v2.5", APIKeyEnv: "MIMO_API_KEY", ContextWindow: 1_000_000, Price: &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}},
			// MiniMax M2 — OpenAI-compatible global endpoint; <think>…</think> chain-of-thought
			// is peeled into reasoning by the openai provider (see internal/provider/openai/think.go).
			// Prices are USD defaults; override in roach-code.toml if your plan differs.
			{Name: "minimax-m3", Kind: "openai", BaseURL: "https://api.minimax.io/v1", Model: "MiniMax-M3", APIKeyEnv: "MINIMAX_API_KEY", ContextWindow: 200_000, Price: &provider.Pricing{CacheHit: 0.03, Input: 0.3, Output: 1.2, Currency: "$"}},
			// GLM (Z.ai / Zhipu) — OpenAI-compatible. thinking="enabled" turns on GLM's
			// reasoning (the openai provider emits the thinking field for non-DeepSeek
			// hosts only when this is set). Coding-plan users may prefer the dedicated
			// coding endpoint (https://api.z.ai/api/coding/paas/v4).
			// Coding-plan keys are scoped to the coding endpoint (general /api/paas/v4
			// returns 429 for them); roach-code is a coding agent, so default there.
			{Name: "glm-5.1", Kind: "openai", BaseURL: "https://api.z.ai/api/coding/paas/v4", Model: "glm-5.1", APIKeyEnv: "ZAI_API_KEY", ContextWindow: 200_000, Thinking: "enabled", Price: &provider.Pricing{CacheHit: 0.11, Input: 0.6, Output: 2.2, Currency: "$"}},
			// OpenAI Codex — the "openai-responses" kind speaks the Responses API
			// (/responses), not chat completions. API-key auth via OPENAI_API_KEY for
			// now (ChatGPT-subscription OAuth is planned). effort: low|medium|high.
			// Codex twice, one per auth method, so /model can offer both: "codex"
			// (ChatGPT subscription via OAuth) and "codex-api" (OPENAI_API_KEY billing).
			// Each shows only when its auth is available (login vs key).
			{Name: "codex", Kind: "openai-responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.5", APIKeyEnv: "OPENAI_API_KEY", Auth: "oauth", ContextWindow: 272_000, Effort: "medium", Price: &provider.Pricing{CacheHit: 0.125, Input: 1.25, Output: 10, Currency: "$"}},
			{Name: "codex-api", Kind: "openai-responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-5.5", APIKeyEnv: "OPENAI_API_KEY", Auth: "apikey", ContextWindow: 272_000, Effort: "medium", Price: &provider.Pricing{CacheHit: 0.125, Input: 1.25, Output: 10, Currency: "$"}},
		},
	}
}

// Load builds the configuration: defaults, then user config, then project
// config, then any MCP servers from the project's .mcp.json. The user-global
// ~/.env is loaded first so api_key_env can resolve.
func Load() (*Config, error) {
	loadDotEnv()
	cfg := Default()

	var tomlSources []string
	if uc := existingUserConfigPath(); uc != "" {
		tomlSources = append(tomlSources, uc)
	} else if hc := homeConfigPath(); hc != "" {
		tomlSources = append(tomlSources, hc)
	}
	tomlSources = append(tomlSources, "roach-code.toml")
	for _, path := range tomlSources {
		if err := mergeFile(cfg, path); err != nil {
			return nil, err
		}
	}
	// toml.DecodeFile replaces [[plugins]] wholesale, so cfg.Plugins now holds
	// only the last file's. Re-merge by name across all sources (later wins) so a
	// project roach-code.toml doesn't drop the global config's MCP servers.
	plugins, err := mergeTOMLPlugins(tomlSources)
	if err != nil {
		return nil, err
	}
	cfg.Plugins = plugins

	// The project .mcp.json is read last and merged into
	// [[plugins]], so a server configured for Claude works here unchanged.
	// roach-code.toml wins on a name collision (see mergeMCPJSON).
	entries, err := loadMCPJSON(mcpJSONFile)
	if err != nil {
		return nil, err
	}
	cfg.mergeMCPJSON(entries)
	normalizeLegacyEffort(cfg)
	return cfg, nil
}

// normalizeLegacyEffort migrates the retired DeepSeek effort="off" (the old
// /thinking off that disabled thinking) to the provider default, so a config
// written by an older version keeps loading instead of erroring on a value the
// provider no longer accepts.
func normalizeLegacyEffort(c *Config) {
	for i := range c.Providers {
		if strings.EqualFold(strings.TrimSpace(c.Providers[i].Effort), "off") {
			c.Providers[i].Effort = ""
		}
	}
}

// mergeTOMLPlugins merges [[plugins]] across TOML sources by name (later source wins).
func mergeTOMLPlugins(paths []string) ([]PluginEntry, error) {
	var merged []PluginEntry
	index := map[string]int{}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		var f Config
		if _, err := toml.DecodeFile(path, &f); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
		for _, p := range f.Plugins {
			if i, ok := index[p.Name]; ok {
				merged[i] = p
				continue
			}
			index[p.Name] = len(merged)
			merged = append(merged, p)
		}
	}
	return merged, nil
}

// LoadForEdit returns a config to seed the `roach-code setup` wizard when reconfiguring:
// the built-in defaults with the file at path (if present) decoded on top, so a
// reconfigure preserves the user's existing providers and agent settings instead
// of resetting to defaults. ~/.env is loaded so api_key_env resolution works
// while the wizard decides which keys are still missing.
func LoadForEdit(path string) *Config {
	loadDotEnv()
	cfg := Default()
	if err := mergeFile(cfg, path); err != nil {
		slog.Warn("config: load for edit failed, using defaults", "path", path, "err", err)
	}
	return cfg
}

// mergeFile decodes a TOML file onto cfg if it exists. An absent file is not an error.
func mergeFile(cfg *Config, path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return fmt.Errorf("config %s: %w", path, err)
	}
	return nil
}

func userConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "roach-code", "config.toml")
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".config", "roach-code", "config.toml")
}

func osUserConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code", "config.toml")
}

func userConfigCandidates() []string {
	var paths []string
	if p := userConfigPath(); p != "" {
		paths = append(paths, p)
	}
	if p := osUserConfigPath(); p != "" && !containsString(paths, p) {
		paths = append(paths, p)
	}
	return paths
}

func existingUserConfigPath() string {
	for _, p := range userConfigCandidates() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func homeConfigPath() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code.toml")
}

// UserConfigPath is the primary user-global config file (~/.config/roach-code/config.toml),
// or "" when the home/user config dir can't be resolved.
func UserConfigPath() string { return userConfigPath() }

// ArchiveDir is where compacted conversation history is archived for
// traceability (one timestamped .jsonl per compaction). Empty if the user config
// directory cannot be resolved, in which case archiving is skipped.
func ArchiveDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code", "archive")
}

// SessionDir is where chat sessions are persisted (one .jsonl per session).
// Used by `roach-code chat --continue` / `--resume` to find the recent ones. Empty
// if the user config dir can't be resolved — sessions then aren't saved.
func SessionDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code", "sessions")
}

// CacheDir is the per-user cache root for derived/regenerable artefacts: MCP
// handshake snapshots, plugin startup-latency telemetry. Lives beside the
// existing dirs (UserConfigDir/roach-code/...) so the whole roach-code state tree
// shares one root the user can wipe in a single rm. Empty when the OS dir is
// unavailable — callers must tolerate that (caching is best-effort).
func CacheDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code", "cache")
}

// MemoryUserDir returns the roach-code user config root (…/roach-code), under which
// the user-global ROACH-CODE.md and the per-project auto-memory store live. Empty
// when the user config dir can't be resolved, which disables user-scoped memory.
func MemoryUserDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "roach-code")
}

// ConventionDirs are the parent directories scanned for agent assets (skills,
// commands), in canonical-first order. .roach-code is ours; .agents / .agent /
// .claude let users drop in assets authored for other agent tools without moving
// files. Shared so skills (internal/skill) and commands (CommandDirs) discover
// the same set. Note: hooks are NOT scanned across these — a .claude/settings.json
// uses a different hook schema that can't be parsed as ours, so hooks stay in
// .roach-code/settings.json (see internal/hook).
var ConventionDirs = []string{".roach-code", ".agents", ".agent", ".claude"}

// conventionSubdirsAsc joins sub under each ConventionDir of base, in ascending
// priority (reverse of ConventionDirs) so the canonical .roach-code ends up the
// highest-priority entry — command.Load lets a later directory win on a clash.
func conventionSubdirsAsc(base, sub string) []string {
	out := make([]string, 0, len(ConventionDirs))
	for i := len(ConventionDirs) - 1; i >= 0; i-- {
		out = append(out, filepath.Join(base, ConventionDirs[i], sub))
	}
	return out
}

// CommandDirs returns the directories scanned for custom slash commands, lowest
// priority first, so a later (more specific) directory overrides an earlier one
// on a name clash. Order: home-dir convention dirs (~/.claude/commands … ~/.roach-code/commands),
// the legacy XDG user dir (~/.config/roach-code/commands), then the project's
// convention dirs (.claude/commands … .roach-code/commands). Scanning the .claude /
// .agents / .agent dirs lets commands authored for other agent tools (same .md +
// frontmatter format) work here unchanged.
func CommandDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, conventionSubdirsAsc(home, "commands")...)
	}
	if dir, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(dir, "roach-code", "commands")) // legacy XDG user dir
	}
	dirs = append(dirs, conventionSubdirsAsc(".", "commands")...)
	return dirs
}

// SourcePath returns the highest-priority config file that exists, or "" if none.
func SourcePath() string {
	if _, err := os.Stat("roach-code.toml"); err == nil {
		return "roach-code.toml"
	}
	if uc := existingUserConfigPath(); uc != "" {
		return uc
	}
	if hc := homeConfigPath(); hc != "" {
		if _, err := os.Stat(hc); err == nil {
			return hc
		}
	}
	return ""
}

// WriteFile writes the configuration to path as annotated TOML.
func (c *Config) WriteFile(path string) error {
	return os.WriteFile(path, []byte(RenderTOML(c)), 0o644)
}

// Provider returns the named provider entry.
func (c *Config) Provider(name string) (*ProviderEntry, bool) {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i], true
		}
	}
	return nil, false
}

// ResolveModel resolves a model reference to a provider entry whose Model is the
// selected model string (a copy, so the config's lists stay intact). It accepts:
//   - "provider/model" — that exact model under that provider;
//   - a provider name   — the provider's default model;
//   - a bare model name — the (first) provider that lists it.
//
// The returned entry is ready to build a provider from (NewProvider reads .Model),
// so a single "vendor with many models" entry yields one instance per model
// without duplicating base_url/api_key_env. Single-`model` entries still resolve
// by provider name, keeping older configs working unchanged.
func (c *Config) ResolveModel(ref string) (*ProviderEntry, bool) {
	if ref == "" {
		return nil, false
	}
	// "provider/model"
	if prov, model, ok := strings.Cut(ref, "/"); ok {
		if e, found := c.Provider(prov); found && e.HasModel(model) {
			cp := *e
			cp.Model = model
			return &cp, true
		}
	}
	// a provider name → its default model
	if e, found := c.Provider(ref); found {
		cp := *e
		cp.Model = e.DefaultModel()
		return &cp, true
	}
	// a bare model name → the provider that lists it
	for i := range c.Providers {
		if c.Providers[i].HasModel(ref) {
			cp := c.Providers[i]
			cp.Model = ref
			return &cp, true
		}
	}
	return nil, false
}

// APIKey resolves the entry's API key from its api_key_env.
func (e *ProviderEntry) APIKey() string {
	if e.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(e.APIKeyEnv)
}

// Configured reports whether the provider is usable, so pickers (e.g. /model)
// can filter on it. Resolves through the auth method: an api-key provider needs
// its api_key_env set; an OAuth (ChatGPT) provider needs a stored login.
func (e *ProviderEntry) Configured() bool {
	if e.AuthMode() == AuthOAuth {
		return codexLoggedIn()
	}
	return e.APIKey() != ""
}

// codexLoggedIn reports whether a stored ChatGPT-OAuth credential exists. The
// path mirrors internal/provider/openairesponses.authPath (os.UserConfigDir +
// roach-code/codex-auth.json); checked directly here to avoid importing the
// provider package from config.
func codexLoggedIn() bool {
	dir, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "roach-code", "codex-auth.json"))
	return err == nil && fi.Size() > 50
}

// OAuthCapable reports whether the provider kind *can* authenticate without an
// env API key. The openai-responses/codex kind supports ChatGPT-subscription
// OAuth. (Whether it actually does is AuthMode.)
func (e *ProviderEntry) OAuthCapable() bool {
	return e.Kind == "openai-responses" || e.Kind == "codex"
}

// Auth method values.
const (
	AuthOAuth  = "oauth"
	AuthAPIKey = "apikey"
)

// AuthMode resolves the effective authentication method: AuthOAuth or AuthAPIKey.
// Non-OAuth-capable kinds are always api-key. For OAuth-capable kinds the explicit
// `auth` setting wins ("oauth"/"chatgpt" → OAuth, "apikey"/"key" → api key);
// empty auto-detects (api key if its env is set, otherwise OAuth).
func (e *ProviderEntry) AuthMode() string {
	if !e.OAuthCapable() {
		return AuthAPIKey
	}
	switch strings.ToLower(strings.TrimSpace(e.Auth)) {
	case "oauth", "chatgpt":
		return AuthOAuth
	case "apikey", "api_key", "key":
		return AuthAPIKey
	default:
		if e.APIKey() != "" {
			return AuthAPIKey
		}
		return AuthOAuth
	}
}

// ResolveSystemPrompt returns the system prompt, reading system_prompt_file if set.
func (c *Config) ResolveSystemPrompt() (string, error) {
	if c.Agent.SystemPromptFile != "" {
		b, err := os.ReadFile(c.Agent.SystemPromptFile)
		if err != nil {
			return "", fmt.Errorf("system_prompt_file: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	if strings.TrimSpace(c.Agent.SystemPrompt) == "" {
		return DefaultSystemPrompt, nil
	}
	return c.Agent.SystemPrompt, nil
}

// Validate checks that the selected model's provider is usable.
func (c *Config) Validate(model string) error {
	e, ok := c.ResolveModel(model)
	if !ok {
		return fmt.Errorf("unknown model %q (configured: %s)", model, c.providerNames())
	}
	if e.Kind == "" {
		return fmt.Errorf("provider %q: kind is required", model)
	}
	if e.BaseURL == "" {
		return fmt.Errorf("provider %q: base_url is required", model)
	}
	if e.APIKey() == "" && !e.OAuthCapable() {
		return fmt.Errorf("provider %q: missing env %s", model, e.APIKeyEnv)
	}
	return nil
}

func (c *Config) providerNames() string {
	names := make([]string, len(c.Providers))
	for i, p := range c.Providers {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}
