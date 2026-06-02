import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { CapabilitiesView, MCPServerInput, ServerView, SkillRootView, SkillView } from "../lib/types";
import { ResizableDrawer } from "./ResizableDrawer";

// CapabilitiesPanel is the desktop MCP & Skills drawer — the GUI counterpart to
// the CLI's /mcp + /skill connector panel:
// each server shows a connected/failed dot, transport, and tool/prompt/resource
// counts, with add / remove / retry; skills list their scope and run mode.
type CapTab = "servers" | "skills";

export function CapabilitiesPanel({
  onClose,
}: {
  onClose: () => void;
}) {
  const t = useT();
  const [view, setView] = useState<CapabilitiesView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [tab, setTab] = useState<CapTab>("servers");
  const [skillQuery, setSkillQuery] = useState("");
  const [expandedSkills, setExpandedSkills] = useState<Set<string>>(() => new Set());
  const [expandedErrors, setExpandedErrors] = useState<Set<string>>(() => new Set());
  const [expandedServers, setExpandedServers] = useState<Set<string>>(() => new Set());

  const reload = async () =>
    setView(await app.Capabilities().catch(() => ({ servers: [], skills: [], skillRoots: [] })));
  useEffect(() => {
    void reload();
  }, []);

  // mutate runs an MCP edit, re-reads the snapshot, and surfaces any failure as an
  // inline banner (a connect error, a missing binary, a bad URL).
  const mutate = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await fn();
      await reload();
      return true;
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const summary = useMemo(() => {
    if (!view) return "";
    return t("caps.summary", {
      connected: view.servers.filter((s) => s.status === "connected").length,
      failed: view.servers.filter((s) => s.status === "failed").length,
      skills: view.skills.length,
    });
  }, [view, t]);

  const filteredSkills = useMemo(() => {
    if (!view) return [];
    const q = skillQuery.trim().toLowerCase();
    if (!q) return view.skills;
    return view.skills.filter((sk) => {
      const text = [sk.name, `/${sk.name}`, sk.description, sk.scope, sk.runAs].join(" ").toLowerCase();
      return text.includes(q);
    });
  }, [view, skillQuery]);

  const serverGroups = useMemo(() => {
    const servers = view?.servers ?? [];
    return {
      failed: servers.filter((s) => s.status === "failed"),
      active: servers.filter((s) => s.status !== "failed"),
    };
  }, [view]);

  const toggleSkill = useCallback((name: string) => {
    setExpandedSkills((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleError = useCallback((name: string) => {
    setExpandedErrors((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  const toggleServer = useCallback((name: string) => {
    setExpandedServers((prev) => {
      const next = new Set(prev);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });
  }, []);

  return (
    <ResizableDrawer onClose={onClose} subtle>
        <header className="drawer__head">
          <div>
            <div className="drawer__title">{t("caps.title")}</div>
            {view && <div className="drawer__summary">{summary}</div>}
          </div>
          <button className="chip" onClick={onClose} title={t("common.close")}>
            ✕
          </button>
        </header>

        {!view ? (
          <div className="empty">{t("caps.loading")}</div>
        ) : (
          <div className="drawer__body">
            {err && <div className="banner banner--error">{err}</div>}

            <div className="cap-tabs" role="tablist" aria-label={t("caps.title")}>
              <button
                className={`cap-tab${tab === "servers" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "servers"}
                onClick={() => setTab("servers")}
              >
                {t("caps.connectorsTab")}
              </button>
              <button
                className={`cap-tab${tab === "skills" ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={tab === "skills"}
                onClick={() => setTab("skills")}
              >
                {t("caps.skillsTab")}
              </button>
            </div>

            {tab === "servers" ? (
              <section className="mem-section">
                <div className="mem-section__actions">
                  {!adding && (
                    <button className="btn btn--small" disabled={busy} onClick={() => setAdding(true)}>
                      {t("caps.addServer")}
                    </button>
                  )}
                </div>
                {serverGroups.failed.length > 0 && (
                  <FailedServersNotice
                    servers={serverGroups.failed}
                    expanded={expandedErrors}
                    onToggle={toggleError}
                    onRetry={(name) => void mutate(() => app.RetryMCPServer(name))}
                    confirming={confirming}
                    onConfirm={setConfirming}
                    onCancelConfirm={() => setConfirming(null)}
                    onRemove={(name) => mutate(() => app.RemoveMCPServer(name)).then(() => setConfirming(null))}
                    busy={busy}
                  />
                )}
                {view.servers.length === 0 && !adding && (
                  <div className="mem-empty">{t("caps.noServers")}</div>
                )}
                <ServerGroup
                  busy={busy}
                  servers={serverGroups.active}
                  expanded={expandedServers}
                  confirming={confirming}
                  onConfirm={setConfirming}
                  onCancelConfirm={() => setConfirming(null)}
                  onRemove={(name) => mutate(() => app.RemoveMCPServer(name)).then(() => setConfirming(null))}
                  onRetry={(name) => void mutate(() => app.RetryMCPServer(name))}
                  onToggle={(name, on) => void mutate(() => app.SetMCPServerEnabled(name, on))}
                  onToggleDetails={toggleServer}
                />
                {adding ? (
                  <AddServerForm busy={busy} onCancel={() => setAdding(false)} onAdd={async (input) => (await mutate(() => app.AddMCPServer(input))) && setAdding(false)} />
                ) : null}
              </section>
            ) : (
              <section className="mem-section">
                <div className="cap-search">
                  <input
                    className="mem-input"
                    type="search"
                    placeholder={t("caps.searchSkills")}
                    value={skillQuery}
                    onChange={(e) => setSkillQuery(e.target.value)}
                  />
                </div>
                <SkillSources
                  roots={view.skillRoots ?? []}
                  busy={busy}
                  onAdd={() => mutate(async () => {
                    const path = await app.PickSkillFolder();
                    if (path) await app.AddSkillPath(path);
                  })}
                  onRefresh={() => mutate(() => app.RefreshSkills())}
                  onRemove={(path) => mutate(() => app.RemoveSkillPath(path))}
                />
                {view.skills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkills")}</div>
                ) : filteredSkills.length === 0 ? (
                  <div className="mem-empty">{t("caps.noSkillMatches")}</div>
                ) : (
                  <div className="cap-skills">
                    {filteredSkills.map((sk) => (
                      <SkillRow
                        key={sk.name}
                        skill={sk}
                        expanded={expandedSkills.has(sk.name)}
                        onToggle={() => toggleSkill(sk.name)}
                      />
                    ))}
                  </div>
                )}
              </section>
            )}
          </div>
        )}
    </ResizableDrawer>
  );
}

function SkillSources({
  roots,
  busy,
  onAdd,
  onRefresh,
  onRemove,
}: {
  roots: SkillRootView[];
  busy: boolean;
  onAdd: () => void;
  onRefresh: () => void;
  onRemove: (path: string) => void;
}) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const [showDiagnostics, setShowDiagnostics] = useState(false);
  const visibleRoots = roots.filter((root) => root.skills > 0 || root.configured || Boolean(root.warning));
  const hiddenRoots = roots.filter((root) => !visibleRoots.includes(root));
  const shownRoots = showDiagnostics ? roots : visibleRoots;
  const active = roots.filter((root) => root.skills > 0).length;
  const missing = roots.filter((root) => root.status === "missing").length;
  const empty = roots.filter((root) => root.status === "ok" && root.skills === 0).length;
  return (
    <div className={`cap-sources${expanded ? " cap-sources--expanded" : ""}`}>
      <div className="cap-sources__head">
        <div className="cap-sources__copy">
          <div className="cap-sources__title">{t("caps.sources")}</div>
          <div className="cap-sources__summary">{t("caps.sourcesSummary", { active, missing, empty })}</div>
        </div>
        <div className="cap-sources__actions">
          <button className="btn btn--small" type="button" onClick={() => setExpanded((v) => !v)} aria-expanded={expanded}>
            {expanded ? t("common.collapse") : t("caps.manageSkillSources")}
          </button>
        </div>
      </div>
      {!expanded && hiddenRoots.length > 0 && (
        <button
          className="cap-diagnostics cap-diagnostics--compact"
          type="button"
          onClick={() => {
            setExpanded(true);
            setShowDiagnostics(true);
          }}
        >
          {t("caps.showDiagnostics", { count: hiddenRoots.length })}
        </button>
      )}
      {expanded && (
        <>
          <div className="cap-sources__manage">
            <button className="btn btn--small" disabled={busy} onClick={onRefresh}>
              {t("caps.refreshSkills")}
            </button>
            <button className="btn btn--small" disabled={busy} onClick={onAdd}>
              {t("caps.addSkillFolder")}
            </button>
          </div>
          {shownRoots.length === 0 ? (
            <div className="mem-empty">{t("caps.noSkillRoots")}</div>
          ) : (
            <div className="cap-source-list">
              {shownRoots.map((root) => (
                <div className={`cap-source cap-source--${skillRootTone(root)}`} key={`${root.scope}:${root.priority}:${root.dir}`}>
                  <span className={`cap-dot cap-dot--${skillRootDot(root)}`} />
                  <div className="cap-source__text">
                    <div className="cap-source__label">{skillRootLabel(root, t)}</div>
                    <div className="cap-source__path" title={root.dir}>{root.dir}</div>
                    <div className="cap-source__meta">
                      <span>{skillRootStatus(root, t)}</span>
                      <span>{t("caps.skillRootCount", { skills: root.skills })}</span>
                      {root.configured && <span>{t("caps.skillRootConfigured")}</span>}
                    </div>
                    {root.warning && <div className="cap-source__warning">{root.warning}</div>}
                  </div>
                  {root.scope === "custom" && root.configured && (
                    <button className="btn btn--small" disabled={busy} onClick={() => onRemove(root.dir)} title={t("caps.skillRootRemove")}>
                      ✕
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
          {hiddenRoots.length > 0 && (
            <button className="cap-diagnostics" type="button" onClick={() => setShowDiagnostics((v) => !v)}>
              {showDiagnostics ? t("caps.hideDiagnostics") : t("caps.showDiagnostics", { count: hiddenRoots.length })}
            </button>
          )}
        </>
      )}
    </div>
  );
}

function skillRootTone(root: SkillRootView): "active" | "empty" | "problem" {
  if (root.warning || root.status === "inactive" || root.status === "unreadable") return "problem";
  if (root.skills > 0) return "active";
  return "empty";
}

function skillRootDot(root: SkillRootView): "connected" | "disabled" | "failed" {
  const tone = skillRootTone(root);
  if (tone === "active") return "connected";
  if (tone === "empty") return "disabled";
  return "failed";
}

function skillRootStatus(root: SkillRootView, t: ReturnType<typeof useT>): string {
  if (root.status === "ok" && root.skills > 0) return t("caps.skillRootActive");
  if (root.status === "ok") return t("caps.skillRootEmpty");
  return root.status;
}

function skillRootLabel(root: SkillRootView, t: ReturnType<typeof useT>): string {
  const parts = root.dir.split(/[\\/]/).filter(Boolean);
  const shortPath = parts.length >= 2 ? `${parts[parts.length - 2]}/${parts[parts.length - 1]}` : root.dir;
  return `${skillScopeLabel(root.scope, t)} · ${shortPath}`;
}

function ServerGroup({
  servers,
  expanded,
  busy,
  confirming,
  onConfirm,
  onCancelConfirm,
  onRemove,
  onRetry,
  onToggle,
  onToggleDetails,
}: {
  servers: ServerView[];
  expanded: Set<string>;
  busy: boolean;
  confirming: string | null;
  onConfirm: (name: string) => void;
  onCancelConfirm: () => void;
  onRemove: (name: string) => void;
  onRetry: (name: string) => void;
  onToggle: (name: string, on: boolean) => void;
  onToggleDetails: (name: string) => void;
}) {
  if (servers.length === 0) return null;
  return (
    <div className="cap-server-group">
      {servers.map((s) => (
        <ServerRow
          key={s.name}
          s={s}
          expanded={expanded.has(s.name)}
          busy={busy}
          confirming={confirming === s.name}
          onConfirm={() => onConfirm(s.name)}
          onCancelConfirm={onCancelConfirm}
          onRemove={() => onRemove(s.name)}
          onRetry={() => onRetry(s.name)}
          onToggle={(on) => onToggle(s.name, on)}
          onToggleDetails={() => onToggleDetails(s.name)}
        />
      ))}
    </div>
  );
}

function FailedServersNotice({
  servers,
  expanded,
  busy,
  confirming,
  onToggle,
  onRetry,
  onConfirm,
  onCancelConfirm,
  onRemove,
}: {
  servers: ServerView[];
  expanded: Set<string>;
  busy: boolean;
  confirming: string | null;
  onToggle: (name: string) => void;
  onRetry: (name: string) => void;
  onConfirm: (name: string) => void;
  onCancelConfirm: () => void;
  onRemove: (name: string) => void;
}) {
  const t = useT();
  return (
    <div className="cap-failures" role="status">
      <div className="cap-failures__head">
        <div>
          <div className="cap-failures__title">{t("caps.failureTitle", { failed: servers.length })}</div>
          <div className="cap-failures__hint">{t("caps.failureHint")}</div>
        </div>
      </div>
      <div className="cap-failures__list">
        {servers.map((s) => {
          const open = expanded.has(s.name);
          const error = s.error || t("caps.failed");
          return (
            <div className="cap-failure" key={s.name}>
              <div className="cap-failure__main">
                <span className="cap-dot cap-dot--failed" />
                <div className="cap-failure__text">
                  <div className="cap-failure__name">{s.name}</div>
                  <div className="cap-failure__summary">{summarizeServerError(error)}</div>
                </div>
              </div>
              <div className="cap-failure__actions">
                {confirming === s.name ? (
                  <>
                    <button className="btn btn--small" disabled={busy} onClick={() => onRemove(s.name)}>
                      {t("caps.confirmRemove")}
                    </button>
                    <button className="btn btn--small" disabled={busy} onClick={onCancelConfirm}>
                      {t("common.cancel")}
                    </button>
                  </>
                ) : (
                  <>
                    <button className="btn btn--small" disabled={busy} onClick={() => onRetry(s.name)}>
                      {t("caps.retry")}
                    </button>
                    <button className="btn btn--small" onClick={() => void navigator.clipboard?.writeText(error)}>
                      {t("common.copy")}
                    </button>
                    <button className="btn btn--small" onClick={() => onToggle(s.name)} aria-expanded={open}>
                      {open ? t("common.collapse") : t("caps.showLog")}
                    </button>
                    <button className="btn btn--small" disabled={busy} onClick={() => onConfirm(s.name)} title={t("caps.remove")}>
                      ✕
                    </button>
                  </>
                )}
              </div>
              {open && <pre className="cap-failure__log">{error}</pre>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ServerRow({
  s,
  expanded,
  busy,
  confirming,
  onConfirm,
  onCancelConfirm,
  onRemove,
  onRetry,
  onToggle,
  onToggleDetails,
}: {
  s: ServerView;
  expanded: boolean;
  busy: boolean;
  confirming: boolean;
  onConfirm: () => void;
  onCancelConfirm: () => void;
  onRemove: () => void;
  onRetry: () => void;
  onToggle: (on: boolean) => void;
  onToggleDetails: () => void;
}) {
  const t = useT();
  const actionLabel = serverActionLabel(s, t);
  const tools = s.toolList ?? [];
  const hasTools = tools.length > 0;
  const sub =
    s.status === "failed"
      ? s.error || t("caps.failed")
      : s.status === "disabled"
        ? t("caps.disabled")
        : t("caps.counts", { tools: s.tools, prompts: s.prompts, resources: s.resources });
  return (
    <div className={`cap-server-entry${s.status === "disabled" ? " cap-server-entry--disabled" : ""}`}>
      <div className={`cap-row${s.status === "disabled" ? " cap-row--disabled" : ""}`} title={s.error || undefined}>
        <button
          className="cap-disclosure"
          disabled={!hasTools}
          aria-expanded={hasTools ? expanded : undefined}
          onClick={onToggleDetails}
          title={hasTools ? (expanded ? t("caps.collapseTools") : t("caps.expandTools")) : t("caps.noToolDetails")}
        >
          {hasTools ? (expanded ? "⌄" : "›") : ""}
        </button>
        <span className={`cap-dot cap-dot--${s.status}`} />
        <div className="cap-row__text">
          <div className="cap-row__head">
            <span className="cap-row__name">{s.name}</span>
            <span className="cap-row__transport">{s.transport}</span>
          </div>
          <div className="cap-row__sub">{sub}</div>
        </div>
        <div className="cap-row__actions">
          {confirming ? (
            <>
              <button className="btn btn--small" disabled={busy} onClick={onRemove}>
                {t("caps.confirmRemove")}
              </button>
              <button className="btn btn--small" disabled={busy} onClick={onCancelConfirm}>
                {t("common.cancel")}
              </button>
            </>
          ) : (
            <>
              {s.status === "failed" ? (
                <button className="btn btn--small" disabled={busy} onClick={onRetry}>
                  {actionLabel}
                </button>
              ) : (
                <label className="cap-switch" title={s.status === "connected" ? t("caps.disable") : t("caps.enable")}>
                  <input
                    type="checkbox"
                    checked={s.status === "connected"}
                    disabled={busy}
                    onChange={(e) => onToggle(e.target.checked)}
                  />
                  <span className="cap-switch__track" />
                </label>
              )}
              <button className="btn btn--small" disabled={busy} onClick={onConfirm} title={t("caps.remove")}>
                ✕
              </button>
            </>
          )}
        </div>
      </div>
      {hasTools && expanded && (
        <div className="cap-tool-list">
          <div className="cap-tool-list__title">{t("caps.tools")}</div>
          {tools.map((tool) => (
            <div className="cap-tool" key={tool.name}>
              <div className="cap-tool__name">{tool.name}</div>
              {tool.description && <div className="cap-tool__desc">{tool.description}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function summarizeServerError(error: string): string {
  const normalized = error.replace(/\s+/g, " ").trim();
  const plugin = normalized.match(/plugin "([^"]+)"/i)?.[1];
  const npmCode = normalized.match(/\bnpm error code ([A-Z0-9_]+)/i)?.[1];
  const errno = normalized.match(/\berrno (-?\d+)/i)?.[1];
  const reason = npmCode
    ? `npm ${npmCode}${errno ? ` (${errno})` : ""}`
    : normalized.split(/(?:\.\s+|\n)/)[0];
  const summary = plugin ? `${plugin}: ${reason}` : reason;
  return summary.length > 180 ? `${summary.slice(0, 176).trim()}…` : summary;
}

function serverActionLabel(s: ServerView, t: ReturnType<typeof useT>): string {
  const err = (s.error || "").toLowerCase();
  if (err.includes("401") || err.includes("unauthorized")) return t("caps.reauthorize");
  if (
    err.includes("command not found") ||
    err.includes("executable file not found") ||
    err.includes("no such file") ||
    err.includes("enoent")
  ) {
    return t("caps.checkCommand");
  }
  return t("caps.retry");
}

function SkillRow({
  skill,
  expanded,
  onToggle,
}: {
  skill: SkillView;
  expanded: boolean;
  onToggle: () => void;
}) {
  const t = useT();
  const summary = summarizeSkillDescription(skill.description);
  const canExpand = summary !== skill.description;
  return (
    <button
      className={`cap-skill-card${expanded ? " cap-skill-card--expanded" : ""}${canExpand ? " cap-skill-card--expandable" : ""}`}
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      title={skill.description}
    >
      <div className="cap-skill-card__head">
        <span className="cap-skill-card__icon">/</span>
        <span className="cap-skill-card__main">
          <span className="cap-skill-card__command">{skill.name}</span>
          <span className="cap-skill-card__badges">
            <span className={`cap-skill-badge cap-skill-badge--${skill.scope}`}>{skillScopeLabel(skill.scope, t)}</span>
            {skill.runAs === "subagent" && <span className="cap-skill-badge cap-skill-badge--run">{t("caps.subagent")}</span>}
          </span>
        </span>
      </div>
      <div className="cap-skill-card__desc">{expanded ? skill.description : summary}</div>
      {canExpand && <div className="cap-skill-card__more">{expanded ? t("common.collapse") : t("common.expand")}</div>}
    </button>
  );
}

function skillScopeLabel(scope: string, t: ReturnType<typeof useT>): string {
  switch (scope) {
    case "builtin":
      return t("caps.skillScopeBuiltin");
    case "project":
      return t("caps.skillScopeProject");
    case "custom":
      return t("caps.skillScopeCustom");
    case "global":
      return t("caps.skillScopeGlobal");
    default:
      return scope;
  }
}

function summarizeSkillDescription(description: string): string {
  const normalized = description.replace(/\s+/g, " ").trim();
  if (normalized.length <= 132) return normalized;
  const sentence = normalized.match(/^.{48,132}?[。.!?；;，,]/u)?.[0]?.trim();
  if (sentence && sentence.length >= 48) return sentence.replace(/[。.!?；;，,]$/u, "");
  return `${normalized.slice(0, 128).trim()}…`;
}

function AddServerForm({
  busy,
  onCancel,
  onAdd,
}: {
  busy: boolean;
  onCancel: () => void;
  onAdd: (input: MCPServerInput) => void;
}) {
  const t = useT();
  const [name, setName] = useState("");
  const [transport, setTransport] = useState("stdio");
  const [command, setCommand] = useState("");
  const [url, setUrl] = useState("");
  const [env, setEnv] = useState("");

  const isStdio = transport === "stdio";
  const ready = name.trim() !== "" && (isStdio ? command.trim() !== "" : url.trim() !== "");

  const submit = () => {
    const parts = command.trim().split(/\s+/).filter(Boolean);
    const envMap: Record<string, string> = {};
    for (const line of env.split("\n")) {
      const eq = line.indexOf("=");
      if (eq > 0) envMap[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
    onAdd({
      name: name.trim(),
      transport,
      command: isStdio ? (parts[0] ?? "") : "",
      args: isStdio ? parts.slice(1) : [],
      url: isStdio ? "" : url.trim(),
      env: envMap,
    });
  };

  return (
    <div className="prov-card prov-card--edit">
      <input className="mem-input" placeholder={t("caps.namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} />
      <label className="set-label">{t("caps.transport")}</label>
      <select className="mem-select" value={transport} onChange={(e) => setTransport(e.target.value)}>
        <option value="stdio">stdio</option>
        <option value="http">http</option>
        <option value="sse">sse</option>
      </select>
      {isStdio ? (
        <input className="mem-input" placeholder={t("caps.commandPlaceholder")} value={command} onChange={(e) => setCommand(e.target.value)} />
      ) : (
        <input className="mem-input" placeholder={t("caps.urlPlaceholder")} value={url} onChange={(e) => setUrl(e.target.value)} />
      )}
      <label className="set-label">{t("caps.envLabel")}</label>
      <textarea className="mem-textarea" value={env} onChange={(e) => setEnv(e.target.value)} placeholder={t("caps.envPlaceholder")} spellCheck={false} />
      <div className="prov-card__actions">
        <button className="btn btn--small" onClick={onCancel} disabled={busy}>
          {t("common.cancel")}
        </button>
        <button className="btn btn--primary btn--small" onClick={submit} disabled={busy || !ready}>
          {t("caps.add")}
        </button>
      </div>
    </div>
  );
}
