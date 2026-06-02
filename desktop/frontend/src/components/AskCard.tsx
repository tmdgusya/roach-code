import { useState } from "react";
import { useT } from "../lib/i18n";
import type { QuestionAnswer, WireAsk, WireAskQuestion } from "../lib/types";

// AskCard renders the `ask` tool's question(s) as structured choice cards: each
// question shows its options (radio for single-select, checkbox for multi),
// a free-text field, and Submit gathers the picks. "Just chat" dismisses without
// choosing. The card is modal — the turn is blocked until it's answered.
export function AskCard({
  ask,
  onAnswer,
  onDismiss,
}: {
  ask: WireAsk;
  onAnswer: (id: string, answers: QuestionAnswer[]) => void;
  onDismiss: () => void;
}) {
  const t = useT();
  // Per-question state: selected option labels, and an optional typed answer.
  const [sel, setSel] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});

  const toggle = (q: WireAskQuestion, label: string) => {
    setCustom((c) => ({ ...c, [q.id]: "" }));
    setSel((s) => {
      const cur = s[q.id] ?? [];
      if (q.multi) {
        return { ...s, [q.id]: cur.includes(label) ? cur.filter((x) => x !== label) : [...cur, label] };
      }
      return { ...s, [q.id]: [label] };
    });
  };

  const setTyped = (q: WireAskQuestion, text: string) => {
    setCustom((c) => ({ ...c, [q.id]: text }));
    if (text.trim()) setSel((s) => ({ ...s, [q.id]: [] }));
  };

  const answered = (q: WireAskQuestion) =>
    (sel[q.id]?.length ?? 0) > 0 || (custom[q.id]?.trim() ?? "") !== "";
  const allAnswered = ask.questions.every(answered);

  const submit = () => {
    onAnswer(
      ask.id,
      ask.questions.map((q) => ({
        questionId: q.id,
        selected: custom[q.id]?.trim() ? [custom[q.id].trim()] : (sel[q.id] ?? []),
      })),
    );
  };

  return (
    <div className="modal-backdrop">
      <div className="modal modal--ask">
        {ask.questions.map((q) => (
          <div className="ask-q" key={q.id}>
            {q.header && <div className="ask-q__header">{q.header}</div>}
            <div className="ask-q__prompt">{q.prompt}</div>
            <div className="ask-q__options">
              {q.options.map((o) => {
                const on = (sel[q.id] ?? []).includes(o.label);
                return (
                  <button
                    key={o.label}
                    className={`ask-opt ${on ? "ask-opt--on" : ""}`}
                    onClick={() => toggle(q, o.label)}
                  >
                    <span className="ask-opt__mark">
                      {q.multi ? (on ? "☑" : "☐") : on ? "●" : "○"}
                    </span>
                    <span className="ask-opt__body">
                      <span className="ask-opt__label">{o.label}</span>
                      {o.description && <span className="ask-opt__desc">{o.description}</span>}
                    </span>
                  </button>
                );
              })}
            </div>
            <input
              className="ask-q__custom"
              placeholder={t("ask.customPlaceholder")}
              value={custom[q.id] ?? ""}
              onChange={(e) => setTyped(q, e.target.value)}
            />
          </div>
        ))}
        <div className="modal__actions">
          <button className="btn" onClick={onDismiss}>
            {t("ask.justChat")}
          </button>
          <button className="btn btn--primary" onClick={submit} disabled={!allAnswered}>
            {t("common.submit")}
          </button>
        </div>
      </div>
    </div>
  );
}
