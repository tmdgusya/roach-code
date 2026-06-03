import { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronUp, PauseCircle } from "lucide-react";
import { useT } from "../lib/i18n";
import type { WireApproval } from "../lib/types";

export function ApprovalModal({
  approval,
  onAnswer,
}: {
  approval: WireApproval;
  onAnswer: (allow: boolean, session: boolean) => void;
}) {
  const t = useT();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const subject = approval.subject.trim();

  const chooseToolAction = (key: string) => {
    if (key === "1") onAnswer(true, false);
    else if (key === "2") onAnswer(true, true);
    else if (key === "3" || key === "Escape") onAnswer(false, false);
  };

  useEffect(() => {
    cardRef.current?.focus();
    setDetailsOpen(false);
  }, [approval.id]);

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const tag = target?.tagName.toLowerCase();
      if (tag === "input" || tag === "textarea" || target?.isContentEditable) return;
      if (event.key !== "1" && event.key !== "2" && event.key !== "3" && event.key !== "Escape") return;
      event.preventDefault();
      chooseToolAction(event.key);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onAnswer]);

  const choice = (key: string, label: string, onClick: () => void, primary = false) => (
    <button className={`approval-action${primary ? " approval-action--primary" : ""}`} onClick={onClick}>
      <span className="approval-action__key">{key}</span>
      <span className="approval-action__label">{label}</span>
    </button>
  );

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
