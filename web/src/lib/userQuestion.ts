export function questionChoices(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return [...new Set(value.flatMap((item) => {
    const label = typeof item === "string" ? item : record(item).label;
    return typeof label === "string" && label.trim() ? [label.trim()] : [];
  }))];
}

export function parseUserQuestionInput(value: unknown): { question: string; choices: string[] } | undefined {
  let parsed = value;
  if (typeof value === "string") {
    try { parsed = JSON.parse(value) as unknown; } catch { return undefined; }
  }
  const payload = record(parsed);
  const first = Array.isArray(payload.questions) ? record(payload.questions[0]) : payload;
  if (typeof first.question !== "string" || !first.question.trim()) return undefined;
  return { question: first.question.trim(), choices: questionChoices(first.choices ?? first.options) };
}

export type UserQuestionToolCandidate = {
  active: boolean;
  prompt: string;
  taskID: number;
  toolID: string;
};

export function claimUserQuestionToolID(candidates: UserQuestionToolCandidate[], taskID: number, prompt: string, explicitToolID = ""): string | undefined {
  const active = [...candidates].reverse().filter((candidate) => candidate.active);
  const normalizedPrompt = prompt.trim();
  const candidate = explicitToolID
    ? active.find((item) => item.toolID === explicitToolID)
    : active.find((item) => item.taskID === taskID && item.prompt === normalizedPrompt)
      ?? active.find((item) => item.taskID === taskID)
      ?? active.find((item) => item.prompt === normalizedPrompt);
  if (candidate) candidate.active = false;
  return explicitToolID || candidate?.toolID;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}
