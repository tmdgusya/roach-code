import { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronUp, PauseCircle } from "lucide-react";
import { useT } from "../lib/i18n";
import type { WireApproval } from "../lib/types";

export function ApprovalModal({
  approval,
  onAnswer,
  onRevisePlan,
  onExitPlan,
}: {
  approval: WireApproval;
  onAnswer: (allow: boolean, session: boolean) => void;
  onRevisePlan?: (text: string) => void;
  onExitPlan?: () => void;
}) {
  const t = useT();
  const [revisionOpen, setRevisionOpen] = useState(false);
  const [revisionText, setRevisionText] = useState("");
  const [detailsOpen, setDetailsOpen] = useState(false);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const isPlanApproval = approval.tool === "exit_plan_mode";
  const subject = approval.subject.trim();

  const choosePlanAction = (key: string) => {
    if (key === "1") setRevisionOpen((open) => !open);
    else if (key === "2") onAnswer(true, false);
    else if (key === "3" || key === "Escape") (onExitPlan ?? (() => onAnswer(false, false)))();
  };

  const chooseToolAction = (key: string) => {
    if (key === "1") onAnswer(true, false);
    else if (key === "2") onAnswer(true, true);
    else if (key === "3" || key === "Escape") onAnswer(false, false);
  };

  useEffect(() => {
    cardRef.current?.focus();
    setRevisionOpen(false);
    setRevisionText("");
    setDetailsOpen(false);
  }, [approval.id]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const tag = target?.tagName.toLowerCase();
      if (tag === "input" || tag === "textarea" || target?.isContentEditable) return;
      if (event.key !== "1" && event.key !== "2" && event.key !== "3" && event.key !== "Escape") return;
      event.preventDefault();
      if (isPlanApproval) choosePlanAction(event.key);
      else chooseToolAction(event.key);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [isPlanApproval, onAnswer, onExitPlan]);

  useEffect(() => {
    if (revisionOpen) inputRef.current?.focus();
  }, [revisionOpen]);

  const submitRevision = () => {
    const text = revisionText.trim();
    if (!text) {
      inputRef.current?.focus();
      return;
    }
    onRevisePlan?.(text);
  };

  const choice = (key: string, label: string, onClick: () => void, primary = false) => (
    <button className={`approval-action${primary ? " approval-action--primary" : ""}`} onClick={onClick}>
      <span className="approval-action__key">{key}</span>
      <span className="approval-action__label">{label}</span>
    </button>
  );

  // The plan is already shown above as the assistant's reply; this is just the gate.
  if (isPlanApproval) {
    return (
      <div className="approval-shelf" aria-live="polite">
        <div
          ref={cardRef}
          className="approval-shelf__bar"
          role="dialog"
          aria-modal="false"
          aria-labelledby="plan-approval-title"
          tabIndex={-1}
        >
          <div className="approval-shelf__summary">
            <PauseCircle size={16} aria-hidden="true" />
            <div className="approval-shelf__copy">
              <div id="plan-approval-title" className="approval-shelf__title">
                {t("approval.planReady")}
              </div>
              <div className="approval-shelf__meta">{t("approval.planReadyHint")}</div>
            </div>
          </div>
          <div className="approval-shelf__actions">
            {choice("1", t("approval.revisePlan"), () => setRevisionOpen((open) => !open))}
            {choice("2", t("approval.startExecution"), () => onAnswer(true, false), true)}
            {choice("3", t("approval.exitPlan"), () => (onExitPlan ?? (() => onAnswer(false, false)))())}
          </div>
        </div>
        {revisionOpen && (
          <div className="approval-shelf__panel plan-revision">
            <textarea
              ref={inputRef}
              className="plan-revision__input"
              value={revisionText}
              rows={3}
              placeholder={t("approval.revisePlanPlaceholder")}
              onChange={(event) => setRevisionText(event.target.value)}
              onKeyDown={(event) => {
                if ((event.metaKey || event.ctrlKey) && event.key === "Enter") submitRevision();
                event.stopPropagation();
              }}
            />
            <div className="plan-revision__actions">
              <button className="btn" onClick={() => setRevisionOpen(false)}>
                {t("common.cancel")}
              </button>
              <button className="btn btn--primary" onClick={submitRevision}>
                {t("approval.sendRevision")}
              </button>
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="approval-shelf" aria-live="polite">
      <div
        ref={cardRef}
        className="approval-shelf__bar"
        role="dialog"
        aria-modal="false"
        aria-labelledby="tool-approval-title"
        tabIndex={-1}
      >
        <div className="approval-shelf__summary">
          <PauseCircle size={16} aria-hidden="true" />
          <div className="approval-shelf__copy">
            <div id="tool-approval-title" className="approval-shelf__title">
              {t("approval.toolPending")}
            </div>
            <div className="approval-shelf__meta">
              <span className="tool__name">{approval.tool}</span>
              {subject && <span className="approval-shelf__subject"> · {subject}</span>}
            </div>
          </div>
        </div>
        <div className="approval-shelf__actions">
          {subject && (
            <button className="approval-detail-toggle" onClick={() => setDetailsOpen((open) => !open)}>
              <span>{detailsOpen ? t("approval.hideDetails") : t("approval.details")}</span>
              {detailsOpen ? <ChevronUp size={14} aria-hidden="true" /> : <ChevronDown size={14} aria-hidden="true" />}
            </button>
          )}
          {choice("1", t("approval.allowOnce"), () => onAnswer(true, false), true)}
          {choice("2", t("approval.allowSession"), () => onAnswer(true, true))}
          {choice("3", t("approval.deny"), () => onAnswer(false, false))}
        </div>
      </div>
      {detailsOpen && subject && (
        <div className="approval-shelf__panel">
          <pre className="approval-subject">{subject}</pre>
        </div>
      )}
    </div>
  );
}
