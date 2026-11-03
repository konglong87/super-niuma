const ASCII_LETTER = /[A-Za-z]/;
const ASCII_WORD = /[A-Za-z0-9]/;
const CJK = /\p{Script=Han}/u;

/**
 * Appends a streamed text delta without manufacturing whitespace inside code
 * or between CJK characters. Providers occasionally omit a boundary space
 * between English word chunks, so add one only for the narrow safe case.
 */
export function appendStreamText(current: string, incoming: string): string {
  if (!current || !incoming || isInsideMarkdownCode(current)) {
    return current + incoming;
  }
  const left = current.at(-1) || "";
  const right = incoming.at(0) || "";
  if (needsBoundarySpace(current, left, right)) {
    return `${current} ${incoming}`;
  }
  return current + incoming;
}

function needsBoundarySpace(current: string, left: string, right: string): boolean {
  if (ASCII_LETTER.test(left) && ASCII_LETTER.test(right)) {
    return !(isLowercase(left) && isUppercase(right) && endsWithCamelCaseToken(current));
  }
  if (ASCII_LETTER.test(left) && /\d/.test(right) && endsWithUppercaseAcronym(current)) {
    return true;
  }
  if (CJK.test(left) && ASCII_WORD.test(right)) {
    return true;
  }
  if (ASCII_WORD.test(left) && CJK.test(right)) {
    return true;
  }
  if (/[.!?,;:)]|["'”》）】]/u.test(left) && (ASCII_WORD.test(right) || CJK.test(right))) {
    return !(left === "." && /\d/.test(right));
  }
  return false;
}

function isInsideMarkdownCode(value: string): boolean {
  let open = false;
  for (const line of value.split(/\r?\n/)) {
    if (/^\s*```/.test(line)) {
      open = !open;
    }
  }
  if (open) {
    return true;
  }
  let inlineBackticks = 0;
  for (let index = 0; index < value.length; index += 1) {
    if (value[index] === "`" && value[index - 1] !== "\\") {
      inlineBackticks += 1;
    }
  }
  return inlineBackticks % 2 === 1;
}

function isLowercase(value: string): boolean {
  return value === value.toLowerCase() && value !== value.toUpperCase();
}

function isUppercase(value: string): boolean {
  return value === value.toUpperCase() && value !== value.toLowerCase();
}

function endsWithCamelCaseToken(value: string): boolean {
  const token = value.match(/[A-Za-z]+$/)?.[0] || "";
  return token.length > 1 && /^[a-z]/.test(token) && /[A-Z]/.test(token);
}

function endsWithUppercaseAcronym(value: string): boolean {
  const token = value.match(/[A-Za-z]+$/)?.[0] || "";
  return token.length > 1 && token === token.toUpperCase();
}
