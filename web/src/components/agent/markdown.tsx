// Markdown 轻量渲染 + 助手回复打字机（原样迁自 WebAgentPage）。
import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { useI18n } from "../../lib/i18n";
import { agentCopy } from "./copy";

const TYPEWRITER_FRAME_MS = 22;

const TYPEWRITER_MIN_CHARS = 2;

const TYPEWRITER_MAX_CHARS = 24;

type TableAlign = "left" | "center" | "right";

type MarkdownBlock =
  | { type: "heading"; level: 1 | 2 | 3; text: string; key: string }
  | { type: "paragraph"; lines: string[]; key: string }
  | { type: "quote"; lines: string[]; key: string }
  | { type: "list"; ordered: boolean; items: Array<{ text: string; checked?: boolean }>; key: string }
  | { type: "code"; language: string; code: string; key: string }
  | { type: "table"; header: string[]; align: TableAlign[]; rows: string[][]; key: string }
  | { type: "hr"; key: string };

export function MarkdownLite({ content, live = false }: { content: string; live?: boolean }) {
  const blocks = useMemo(() => parseMarkdownLite(content), [content]);
  return (
    <div className={live ? "agent-markdown live-markdown" : "agent-markdown"}>
      {blocks.map((block) => renderMarkdownBlock(block))}
    </div>
  );
}

export function StreamingAssistantContent({
  content,
  animate,
  live,
  final,
  onComplete
}: {
  content: string;
  animate: boolean;
  live: boolean;
  final: boolean;
  onComplete?: () => void;
}) {
  const visibleContent = useTypewriterText(content, animate, final, onComplete);
  return <MarkdownLite content={animate ? visibleContent : content} live={live} />;
}

function useTypewriterText(content: string, enabled: boolean, final: boolean, onComplete?: () => void) {
  const reducedMotion = usePrefersReducedMotion();
  const animate = enabled && !reducedMotion;
  const [visibleContent, setVisibleContent] = useState(() => (animate ? "" : content));
  const visibleRef = useRef(animate ? "" : content);
  const targetRef = useRef(content);
  const finalRef = useRef(final);
  const onCompleteRef = useRef(onComplete);
  const completedTargetRef = useRef("");
  onCompleteRef.current = onComplete;

  useEffect(() => {
    targetRef.current = content;
    finalRef.current = final;
    if (!animate) {
      visibleRef.current = content;
      setVisibleContent(content);
      if (final && content && completedTargetRef.current !== content) {
        completedTargetRef.current = content;
        onCompleteRef.current?.();
      }
      return;
    }
    if (!content.startsWith(visibleRef.current)) {
      const prefix = commonPrefix(visibleRef.current, content);
      visibleRef.current = prefix;
      setVisibleContent(prefix);
    }
  }, [animate, content, final]);

  useEffect(() => {
    if (!animate) {
      return;
    }
    let cancelled = false;
    let timer: number | undefined;
    const tick = () => {
      if (cancelled) {
        return;
      }
      const target = targetRef.current;
      const current = visibleRef.current;
      if (current.length >= target.length) {
        if (finalRef.current && target && completedTargetRef.current !== target) {
          completedTargetRef.current = target;
          onCompleteRef.current?.();
        }
        timer = window.setTimeout(tick, TYPEWRITER_FRAME_MS);
        return;
      }
      const remaining = target.length - current.length;
      const step = Math.min(TYPEWRITER_MAX_CHARS, Math.max(TYPEWRITER_MIN_CHARS, Math.ceil(remaining / 18)));
      const next = target.slice(0, current.length + step);
      visibleRef.current = next;
      setVisibleContent(next);
      timer = window.setTimeout(tick, TYPEWRITER_FRAME_MS);
    };
    timer = window.setTimeout(tick, TYPEWRITER_FRAME_MS);
    return () => {
      cancelled = true;
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
    };
  }, [animate]);

  return visibleContent;
}

function usePrefersReducedMotion() {
  const [reduced, setReduced] = useState(prefersReducedMotion);

  useEffect(() => {
    if (typeof window.matchMedia !== "function") {
      return;
    }
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReduced(media.matches);
    update();
    media.addEventListener?.("change", update);
    return () => media.removeEventListener?.("change", update);
  }, []);

  return reduced;
}

function prefersReducedMotion() {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function parseMarkdownLite(content: string): MarkdownBlock[] {
  const lines = normalizeMarkdownLiteInput(content).split("\n");
  const blocks: MarkdownBlock[] = [];
  let paragraph: string[] = [];
  let quote: string[] = [];
  let list: MarkdownBlock | null = null;
  let code: { language: string; lines: string[] } | null = null;
  let table: string[] | null = null;

  const nextKey = () => `md-${blocks.length}`;
  const flushParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ type: "paragraph", lines: paragraph, key: nextKey() });
      paragraph = [];
    }
  };
  const flushQuote = () => {
    if (quote.length > 0) {
      blocks.push({ type: "quote", lines: quote, key: nextKey() });
      quote = [];
    }
  };
  const flushList = () => {
    if (list) {
      blocks.push(list);
      list = null;
    }
  };
  const flushTable = () => {
    if (!table) {
      return;
    }
    const parsed = parseTableBlock(table);
    if (parsed) {
      blocks.push({ ...parsed, key: nextKey() });
    } else {
      // 不是合法表格(缺分隔行等):按普通段落兜底,不丢内容。
      paragraph.push(...table);
    }
    table = null;
  };
  const flushTextBlocks = () => {
    flushTable();
    flushParagraph();
    flushQuote();
    flushList();
  };

  for (const rawLine of lines) {
    const line = rawLine.replace(/\s+$/, "");
    const fence = line.match(/^```([\w-]*)\s*$/);
    const inlineFence = !fence ? line.match(/^```(.+?)```$/) : null;
    const looseOpeningFence = !fence && !inlineFence ? line.match(/^```(\S.*)$/) : null;
    if (inlineFence) {
      const split = splitLooseFenceBody(inlineFence[1]);
      flushTextBlocks();
      blocks.push({ type: "code", language: split.language, code: split.code, key: nextKey() });
      continue;
    }
    if (fence || looseOpeningFence) {
      const split = looseOpeningFence ? splitLooseFenceBody(looseOpeningFence[1]) : { language: fence?.[1] || "", code: "" };
      if (code) {
        if (looseOpeningFence) {
          code.lines.push(split.code);
        }
        blocks.push({ type: "code", language: code.language, code: code.lines.join("\n"), key: nextKey() });
        code = null;
      } else {
        flushTextBlocks();
        code = { language: split.language, lines: split.code ? [split.code] : [] };
      }
      continue;
    }
    if (code) {
      code.lines.push(rawLine);
      continue;
    }
    if (line.trim() === "") {
      flushTextBlocks();
      continue;
    }
    if (line.trim().startsWith("|")) {
      flushParagraph();
      flushQuote();
      flushList();
      table = table ?? [];
      table.push(line.trim());
      continue;
    }
    if (table) {
      flushTable();
    }
    if (/^(-{3,}|\*{3,})$/.test(line.trim())) {
      flushTextBlocks();
      blocks.push({ type: "hr", key: nextKey() });
      continue;
    }
    const heading = line.match(/^(#{1,3})\s*(\S.*)$/);
    if (heading) {
      flushTextBlocks();
      blocks.push({ type: "heading", level: heading[1].length as 1 | 2 | 3, text: heading[2], key: nextKey() });
      continue;
    }
    const quoteMatch = line.match(/^>\s?(.*)$/);
    if (quoteMatch) {
      flushParagraph();
      flushList();
      quote.push(quoteMatch[1]);
      continue;
    }
    const listMatch = line.match(/^(\d+[.)]|[-*+])\s+(\[[ xX]\]\s+)?(.+)$/);
    if (listMatch) {
      flushParagraph();
      flushQuote();
      const ordered = /^\d/.test(listMatch[1]);
      if (list?.type !== "list" || list.ordered !== ordered) {
        flushList();
        list = { type: "list", ordered, items: [], key: nextKey() };
      }
      if (list.type === "list") {
        const marker = listMatch[2] || "";
        const checked = marker ? /\[[xX]\]/.test(marker) : undefined;
        list.items.push({ text: listMatch[3], checked });
      }
      continue;
    }
    flushQuote();
    flushList();
    paragraph.push(line);
  }
  if (code) {
    blocks.push({ type: "code", language: code.language, code: code.lines.join("\n"), key: nextKey() });
  }
  flushTextBlocks();
  return blocks.length > 0 ? blocks : [{ type: "paragraph", lines: [content], key: "md-empty" }];
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim());
}

function isTableSeparatorRow(line: string): boolean {
  const cells = splitTableRow(line);
  return cells.length > 0 && cells.every((cell) => /^:?-+:?$/.test(cell));
}

function parseTableBlock(lines: string[]): { type: "table"; header: string[]; align: TableAlign[]; rows: string[][] } | null {
  if (lines.length < 2 || !isTableSeparatorRow(lines[1])) {
    return null;
  }
  const header = splitTableRow(lines[0]);
  const align: TableAlign[] = splitTableRow(lines[1]).map((cell) => {
    const left = cell.startsWith(":");
    const right = cell.endsWith(":");
    if (left && right) {
      return "center";
    }
    return right ? "right" : "left";
  });
  const rows = lines
    .slice(2)
    .filter((line) => !isTableSeparatorRow(line))
    .map(splitTableRow);
  return { type: "table", header, align, rows };
}

function splitLooseFenceBody(body: string) {
  const trimmed = body.trim();
  const spaced = trimmed.match(/^([\w-]+)\s+([\s\S]+)$/);
  if (spaced) {
    return { language: spaced[1], code: spaced[2].trim() };
  }
  const knownLanguages = ["text", "txt", "go", "json", "bash", "sh", "md", "markdown", "python", "ts", "tsx", "js", "jsx"];
  for (const language of knownLanguages) {
    if (trimmed.startsWith(language) && trimmed.length > language.length) {
      const next = trimmed[language.length];
      if (next && next === next.toUpperCase() && /[A-Z0-9_]/.test(next)) {
        return { language, code: trimmed.slice(language.length).trim() };
      }
    }
  }
  return { language: "", code: trimmed };
}

function normalizeMarkdownLiteInput(content: string) {
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  const normalized: string[] = [];
  let inFence = false;

  for (const line of lines) {
    if (/^\s*```/.test(line)) {
      normalized.push(line);
      inFence = !inFence;
      continue;
    }
    if (inFence) {
      normalized.push(line);
      continue;
    }
    splitLooseHeadingLine(line).forEach((headingLine) => {
      normalized.push(...splitLooseListLine(headingLine));
    });
  }

  return normalized.join("\n");
}

function splitLooseListLine(line: string) {
  const firstMarker = line.match(/^(\d+[.)]|[-*+])\s+/);
  if (!firstMarker) {
    return [line];
  }

  const ordered = /^\d/.test(firstMarker[1]);
  const marker = ordered ? firstMarker[1].replace(/\d+/g, "") : firstMarker[1];
  const candidates = findLooseListMarkerStarts(line, firstMarker[0].length, ordered, marker);
  if (candidates.length === 0) {
    return [line];
  }

  // A single spaced hyphen is usually prose. Compact markers attached to the
  // previous item, or two or more spaced markers, provide stronger structure evidence.
  const hasAttachedMarker = candidates.some((start) => start > 0 && !/\s/.test(line[start - 1]));
  if (!hasAttachedMarker && candidates.length < 2) {
    return [line];
  }

  const starts = [0, ...candidates];
  return starts.map((start, index) => line.slice(start, starts[index + 1] ?? line.length).trim()).filter(Boolean);
}

function findLooseListMarkerStarts(line: string, from: number, ordered: boolean, marker: string) {
  const starts: number[] = [];
  let inInlineCode = false;

  for (let index = from; index < line.length; index += 1) {
    const current = line[index];
    if (current === "`") {
      inInlineCode = !inInlineCode;
      continue;
    }
    if (inInlineCode) {
      continue;
    }
    if (current === "\\") {
      index += 1;
      continue;
    }

    if (!ordered) {
      if (current === marker && /\s/.test(line[index + 1] || "")) {
        starts.push(index);
      }
      continue;
    }

    if (!/\d/.test(current)) {
      continue;
    }
    let end = index + 1;
    while (/\d/.test(line[end] || "")) {
      end += 1;
    }
    if (line[end] === marker && /\s/.test(line[end + 1] || "")) {
      starts.push(index);
      index = end;
    }
  }

  return starts;
}

function splitLooseHeadingLine(line: string) {
  if (!line.includes("##")) {
    return [line];
  }

  const markers: Array<{ start: number; after: number; hashes: string }> = [];
  const markerPattern = /(#{2,3})(?!#)\s*(?=\S)/g;
  for (let match = markerPattern.exec(line); match !== null; match = markerPattern.exec(line)) {
    const start = match.index;
    const previous = start > 0 ? line[start - 1] : "";
    const startsLine = start === 0 || line.slice(0, start).trim() === "";
    const hasLooseHeadingContext = markers.length > 0 && (markers[0].start === line.search(/\S/) || markers.length > 1);
    if (!startsLine && !/\s/.test(previous) && !hasLooseHeadingContext) {
      continue;
    }
    markers.push({ start, after: markerPattern.lastIndex, hashes: match[1] });
  }

  if (markers.length === 0) {
    return [line];
  }

  const firstText = line.search(/\S/);
  const firstMarkerStartsLine = firstText >= 0 && markers[0].start === firstText;
  if (!firstMarkerStartsLine && markers.length < 2) {
    return [line];
  }

  const parts: string[] = [];
  let cursor = 0;
  markers.forEach((marker, index) => {
    const prefix = line.slice(cursor, marker.start).trim();
    if (prefix) {
      parts.push(prefix);
    }
    const nextStart = markers[index + 1]?.start ?? line.length;
    const segment = line.slice(marker.after, nextStart).trim();
    if (segment) {
      parts.push(...formatLooseHeadingSegment(marker.hashes, segment));
    }
    cursor = nextStart;
  });

  const trailing = line.slice(cursor).trim();
  if (trailing) {
    parts.push(trailing);
  }

  return parts.length > 0 ? parts : [line];
}

function formatLooseHeadingSegment(hashes: string, segment: string) {
  const titleAndBody = segment.match(/^(.{1,48}?)(\s*[-*+]\s+|\s*[：:]\s+)(.+)$/);
  if (!titleAndBody) {
    return [`${hashes} ${segment}`];
  }

  const title = titleAndBody[1].trim();
  const separator = titleAndBody[2].trim()[0];
  const body = titleAndBody[3].trim();
  if (!title || !body) {
    return [`${hashes} ${segment}`];
  }

  return [`${hashes} ${title}`, separator === ":" || separator === "：" ? body : `${separator} ${body}`];
}

function renderMarkdownBlock(block: MarkdownBlock) {
  if (block.type === "heading") {
    const Tag = `h${block.level}` as "h1" | "h2" | "h3";
    return <Tag key={block.key}>{renderInlineMarkdown(block.text)}</Tag>;
  }
  if (block.type === "paragraph") {
    return <p key={block.key}>{renderLines(block.lines)}</p>;
  }
  if (block.type === "quote") {
    return <blockquote key={block.key}>{renderLines(block.lines)}</blockquote>;
  }
  if (block.type === "list") {
    const Tag = block.ordered ? "ol" : "ul";
    return (
      <Tag key={block.key}>
        {block.items.map((item, index) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: markdown is re-parsed from the raw string on every change, so list items have no identity across renders and these children are stateless — position IS the identity
          <li key={`${block.key}-${index}`} className={item.checked === undefined ? undefined : "task-list-item"}>
            {item.checked === undefined ? null : <input type="checkbox" checked={item.checked} readOnly aria-label={item.checked ? "completed" : "pending"} />}
            <span>{renderInlineMarkdown(item.text)}</span>
          </li>
        ))}
      </Tag>
    );
  }
  if (block.type === "code") {
    return <MarkdownCodeBlock key={block.key} language={block.language} code={block.code} />;
  }
  if (block.type === "table") {
    return (
      <div key={block.key} className="agent-table-wrap">
        <table>
          <thead>
            <tr>
              {block.header.map((cell, index) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: table cells are re-parsed positionally from the raw markdown and hold no state; column order IS their identity
                <th key={`${block.key}-th-${index}`} style={{ textAlign: block.align[index] || "left" }}>
                  {renderInlineMarkdown(cell, `th${index}-`)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {block.rows.map((row, rowIndex) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: same as the header row — positional identity, stateless children
              <tr key={`${block.key}-tr-${rowIndex}`}>
                {block.header.map((_, colIndex) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: same as the header row — positional identity, stateless children
                  <td key={`${block.key}-tr-${rowIndex}-td-${colIndex}`} style={{ textAlign: block.align[colIndex] || "left" }}>
                    {renderInlineMarkdown(row[colIndex] ?? "", `td${rowIndex}-${colIndex}-`)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }
  return <hr key={block.key} />;
}

function MarkdownCodeBlock({ language, code }: { language: string; code: string }) {
  const { language: uiLanguage } = useI18n();
  const copy = agentCopy[uiLanguage];
  const [copied, setCopied] = useState(false);
  return (
    <div className="agent-codeblock">
      <div className="agent-codeblock-head">
        <span>{language || "text"}</span>
        <button
          type="button"
          className="agent-codeblock-copy"
          title={copied ? copy.copied : copy.copyCode}
          aria-label={copied ? copy.copied : copy.copyCode}
          onClick={() => {
            navigator.clipboard
              .writeText(code)
              .then(() => {
                setCopied(true);
                window.setTimeout(() => setCopied(false), 1500);
              })
              .catch(() => {});
          }}
        >
          {copied ? <Check size={12} /> : <Copy size={12} />}
        </button>
      </div>
      <pre data-language={language || undefined}>
        <code>{code}</code>
      </pre>
    </div>
  );
}

function renderLines(lines: string[]) {
  return lines.flatMap((line, index) => (
    // biome-ignore lint/suspicious/noArrayIndexKey: a soft line break's only identity is its position in the block
    index === 0 ? renderInlineMarkdown(line) : [<br key={`br-${index}`} />, ...renderInlineMarkdown(line, `${index}-`)]
  ));
}

function renderInlineMarkdown(text: string, keyPrefix = ""): ReactNode[] {
  const nodes: ReactNode[] = [];
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let cursor = 0;
  for (let match = pattern.exec(text); match !== null; match = pattern.exec(text)) {
    if (match.index > cursor) {
      nodes.push(text.slice(cursor, match.index));
    }
    const token = match[0];
    if (token.startsWith("`")) {
      nodes.push(<code key={`${keyPrefix}code-${match.index}`}>{token.slice(1, -1)}</code>);
    } else {
      nodes.push(<strong key={`${keyPrefix}strong-${match.index}`}>{token.slice(2, -2)}</strong>);
    }
    cursor = match.index + token.length;
  }
  if (cursor < text.length) {
    nodes.push(text.slice(cursor));
  }
  return nodes;
}

function commonPrefix(left: string, right: string) {
  const limit = Math.min(left.length, right.length);
  let index = 0;
  while (index < limit && left[index] === right[index]) {
    index += 1;
  }
  return left.slice(0, index);
}
