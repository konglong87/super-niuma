import { CircleHelp, Send } from "lucide-react";
import { useEffect, useState, type FormEvent, type JSX } from "react";
import { ApiError, resolveAgentTaskQuestion } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import type { AgentUserQuestion, IdentityConfig } from "../../lib/types";

type UserQuestionCardProps = {
  identity?: IdentityConfig;
  question: AgentUserQuestion;
  prompt: string;
};

export function UserQuestionCard({ identity, question, prompt }: UserQuestionCardProps): JSX.Element {
  const { t } = useI18n();
  const [choice, setChoice] = useState("");
  const [customAnswer, setCustomAnswer] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submittedAnswer, setSubmittedAnswer] = useState("");
  const [error, setError] = useState("");
  const [closedByConflict, setClosedByConflict] = useState(false);
  const [expired, setExpired] = useState(() => question.status === "pending" && isExpired(question.expiresAt));
  const answer = customAnswer.trim() || choice.trim();
  const effectiveAnswer = question.answer || submittedAnswer;
  const conflictClosed = closedByConflict && question.status === "pending" && !effectiveAnswer;
  const actionable = question.status === "pending" && Boolean(identity && question.requestID) && !effectiveAnswer && !conflictClosed && !expired;
  const displayedQuestion: AgentUserQuestion = expired && question.status === "pending" ? { ...question, status: "expired" } : conflictClosed ? { ...question, status: "unavailable" } : question;

  useEffect(() => {
    if (question.status !== "pending" || !question.expiresAt) {
      setExpired(false);
      return;
    }
    const expiresAt = Date.parse(question.expiresAt);
    if (!Number.isFinite(expiresAt)) {
      setExpired(false);
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const update = () => {
      const remaining = expiresAt - Date.now();
      if (remaining <= 0) {
        setExpired(true);
        return;
      }
      setExpired(false);
      timer = setTimeout(update, Math.min(remaining, 2_147_483_647));
    };
    update();
    return () => clearTimeout(timer);
  }, [question.expiresAt, question.status]);

  async function submit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (!actionable || !identity || !question.requestID || !answer || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      const resolved = await resolveAgentTaskQuestion(identity, question.taskID, question.requestID, answer);
      setSubmittedAnswer(resolved.answer);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) setClosedByConflict(true);
      setError(questionError(cause, t));
    } finally {
      setSubmitting(false);
    }
  }

  return <article className="agent-user-question" data-question-status={effectiveAnswer ? "answered" : displayedQuestion.status}>
    <header><CircleHelp aria-hidden="true" size={18} /><div><span>{t("webui2.question.label")}</span><strong>{prompt}</strong></div></header>
    {actionable || conflictClosed ? <form aria-disabled={conflictClosed || undefined} onSubmit={(event) => void submit(event)}>
      {question.choices.length ? <fieldset disabled={submitting || conflictClosed}><legend>{t("webui2.question.options")}</legend>{question.choices.map((option) => <label key={option}><input checked={choice === option} name={`question-${question.taskID}-${question.requestID}`} onChange={() => { setChoice(option); setCustomAnswer(""); setError(""); }} type="radio" value={option} /><span>{option}</span></label>)}</fieldset> : null}
      <label className="agent-user-question-custom"><span>{t("webui2.question.custom")}</span><textarea disabled={submitting || conflictClosed} onChange={(event) => { setCustomAnswer(event.target.value); setChoice(""); setError(""); }} placeholder={t("webui2.question.customPlaceholder")} rows={2} value={customAnswer} /></label>
      <div className="agent-user-question-actions"><button disabled={!actionable || !answer || submitting} type="submit"><Send aria-hidden="true" size={14} />{submitting ? t("webui2.question.submitting") : t("webui2.question.submit")}</button></div>
    </form> : <QuestionResult answer={effectiveAnswer} question={displayedQuestion} />}
    {error ? <p className="agent-user-question-error" role="alert">{error}</p> : null}
  </article>;
}

function isExpired(value?: string): boolean {
  if (!value) return false;
  const expiresAt = Date.parse(value);
  return Number.isFinite(expiresAt) && expiresAt <= Date.now();
}

function QuestionResult({ answer, question }: { answer: string; question: AgentUserQuestion }): JSX.Element {
  const { t } = useI18n();
  if (answer) return <p className="agent-user-question-result"><span>{t("webui2.question.answered")}</span><strong>{answer}</strong></p>;
  return <div className="agent-user-question-closed">
    {question.choices.length ? <ul>{question.choices.map((choice) => <li key={choice}>{choice}</li>)}</ul> : null}
    <p>{t(`webui2.question.status.${question.status}`)}</p>
  </div>;
}

function questionError(error: unknown, t: (key: string) => string): string {
  if (error instanceof ApiError) {
    if (error.status === 400) return t("webui2.question.error.invalid");
    if (error.status === 403) return t("webui2.question.error.forbidden");
    if (error.status === 404) return t("webui2.question.error.notFound");
    if (error.status === 409) return t("webui2.question.error.conflict");
  }
  return t("webui2.question.error.network");
}
