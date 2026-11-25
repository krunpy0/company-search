import type { Turn } from "./chat-api";

function emphasis(line: string) {
  return line.split(/(\*\*[^*]+\*\*|__[^_]+__)/g).map((part, index) =>
    index % 2 ? <strong key={index}>{part.slice(2, -2)}</strong> : part);
}

function Summary({ text }: { text: string }) {
  return text.split(/\r?\n/).map((line, index) => {
    const heading = /^#{1,6}\s+(.+)$/.exec(line);
    if (heading) return <h2 key={index}>{emphasis(heading[1])}</h2>;
    return line.trim() ? <p key={index}>{emphasis(line)}</p> : <div key={index} className="chat-paragraph-gap" />;
  });
}

function sourceUrl(value: unknown) {
  if (typeof value !== "string") return;
  try {
    const url = new URL(value);
    if (url.protocol === "https:" || url.protocol === "http:") return url.href;
  } catch {
    // A source without a valid HTTP URL remains visible as text.
  }
}

export function Answer({ answer }: { answer: Turn["answer"] }) {
  const sources = new Map<string, { label: string; url?: string; dates: Set<string> }>();
  for (const record of answer.records ?? []) {
    const { source_ref: ref, source, as_of: date } = record;
    const existing = sources.get(ref);
    if (existing) {
      if (date) existing.dates.add(date);
      continue;
    }
    const publisher = source.publisher ?? source.provider ?? source.source;
    sources.set(ref, {
      label: typeof publisher === "string" && publisher !== ref ? `${publisher} · ${ref}` : ref,
      url: sourceUrl(source.url ?? source.source_url),
      dates: new Set(date ? [date] : []),
    });
  }

  return <div className="chat-answer">
    <Summary text={answer.summary} />
    {answer.partial && <p className="chat-partial">Частичный ответ</p>}
    {sources.size > 0 && <details className="chat-sources">
      <summary>Источники ({sources.size})</summary>
      <ul>{[...sources].map(([ref, source]) => <li key={ref}>
        {source.url ? <a href={source.url}>{source.label}</a> : <span>{source.label}</span>}
        <span>{source.dates.size ? `Данные на ${[...source.dates].sort().map((date) => /^\d{4}-\d{2}-\d{2}$/.test(date) ? date.split("-").reverse().join(".") : date).join(", ")}` : "Дата актуальности не указана"}</span>
      </li>)}</ul>
    </details>}
  </div>;
}

