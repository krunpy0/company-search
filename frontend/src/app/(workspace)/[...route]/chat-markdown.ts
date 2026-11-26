import type { Turn } from "./chat-api";

function sourceUrl(value: unknown) {
  if (typeof value !== "string") return;
  try {
    const url = new URL(value);
    if (url.protocol === "https:" || url.protocol === "http:") return url.href;
  } catch {
    // A source without a valid HTTP URL remains visible as text.
  }
}

export function answerSources(answer: Turn["answer"]) {
  const sources = new Map<string, { label: string; url?: string; dates: Set<string> }>();
  for (const record of answer.records ?? []) {
    const { source_ref: ref, source, as_of: date } = record;
    let item = sources.get(ref);
    if (!item) {
      const publisher = source.publisher ?? source.provider ?? source.source;
      item = { label: typeof publisher === "string" && publisher !== ref ? `${publisher} · ${ref}` : ref, dates: new Set() };
      sources.set(ref, item);
    }
    item.url ??= sourceUrl(source.url ?? source.source_url);
    if (date) item.dates.add(date);
  }
  return [...sources].map(([ref, { label, url, dates }]) => ({ ref, label, url, dates: [...dates].sort() }));
}

function escapeMarkdown(value: string) {
  return value.replace(/[\\`*_\[\]]/g, "\\$&")
    .replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/^(\s*)([#+-]|\d+[.)])(?=\s)/gm, "$1\\$2");
}

export function chatMarkdown(title: string, turns: readonly Turn[]) {
  const sections = turns.map((turn) => {
    const sources = answerSources(turn.answer);
    const lines = [
      "## Вопрос",
      escapeMarkdown(turn.question),
      "## Ответ",
      `Сохранён ${turn.created_at}`,
      turn.answer.summary.replace(/[\\&<>\[\]]/g, (char) => `&#${char.charCodeAt(0)};`),
    ];
    if (turn.answer.partial) lines.push("Частичный ответ");
    if (sources.length) {
      lines.push("### Источники", sources.map(({ label, url, dates }) => {
        const date = dates.length ? `данные на ${dates.map(escapeMarkdown).join(", ")}` : "дата актуальности не указана";
        return `- ${escapeMarkdown(label)}${url ? ` <${url}>` : ""} (${date})`;
      }).join("\n"));
    }
    return lines.join("\n\n");
  });
  return [`# ${escapeMarkdown(title)}`, ...sections].join("\n\n") + "\n";
}
