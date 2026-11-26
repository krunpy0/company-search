import { useEffect, useRef, useState } from "react";
import type { Turn } from "./chat-api";
import { answerSources } from "./chat-markdown";

function ActionIcon({ name }: { name: "copy" | "check" | "sources" | "export" | "report" }) {
  const shapes = {
    copy: <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
    check: <path d="M5 12.5 9.5 17 19 7" />,
    sources: <><path d="M4 6h16M4 12h16M4 18h10" /></>,
    export: <><path d="M12 3v12m0 0-4-4m4 4 4-4" /><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" /></>,
    report: <><path d="M4 21V4m0 0h11l-2 4 2 4H4" /></>,
  };
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
    {shapes[name]}
  </svg>;
}

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

export function Answer({ answer, onExport, onReport }: { answer: Turn["answer"]; onExport: () => void; onReport: () => void }) {
  const sources = answerSources(answer);
  const sourceDetails = useRef<HTMLDetailsElement>(null);
  const copyTimer = useRef<number>(undefined);
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copyStatus, setCopyStatus] = useState("");

  useEffect(() => () => window.clearTimeout(copyTimer.current), []);

  async function copy() {
    window.clearTimeout(copyTimer.current);
    try {
      await navigator.clipboard.writeText(answer.summary);
      setCopied(true);
      setCopyStatus("Ответ скопирован.");
      copyTimer.current = window.setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
      setCopyStatus("Не удалось скопировать ответ.");
    }
  }

  function toggleSources() {
    const details = sourceDetails.current;
    if (!details) return;
    const next = !details.open;
    details.open = next;
    setSourcesOpen(next);
    if (next) details.querySelector("summary")?.focus();
  }

  return <div className="chat-answer">
    <Summary text={answer.summary} />
    {answer.partial && <p className="chat-partial">Частичный ответ</p>}
    {sources.length > 0 && <details className="chat-sources" ref={sourceDetails}
      onToggle={(event) => setSourcesOpen(event.currentTarget.open)}>
      <summary>Источники ({sources.length})</summary>
      <ul>{sources.map(({ ref, label, url, dates }) => <li key={ref}>
        {url ? <a href={url}>{label}</a> : <span>{label}</span>}
        <span>{dates.length ? `Данные на ${dates.map((date) => /^\d{4}-\d{2}-\d{2}$/.test(date) ? date.split("-").reverse().join(".") : date).join(", ")}` : "Дата актуальности не указана"}</span>
      </li>)}</ul>
    </details>}
    <div className="chat-answer-actions" role="group" aria-label="Действия с ответом">
      <button className="chat-answer-action" type="button" aria-label="Копировать ответ" title="Копировать ответ"
        data-copied={copied || undefined} onClick={() => void copy()}>
        <ActionIcon name={copied ? "check" : "copy"} />
      </button>
      {sources.length > 0 && <button className="chat-answer-action" type="button"
        aria-label={sourcesOpen ? "Скрыть источники" : "Показать источники"}
        title={sourcesOpen ? "Скрыть источники" : "Показать источники"}
        aria-expanded={sourcesOpen} onClick={toggleSources}>
        <ActionIcon name="sources" />
      </button>}
      <button className="chat-answer-action" type="button" aria-label="Экспортировать" title="Экспортировать" onClick={onExport}>
        <ActionIcon name="export" />
      </button>
      <button className="chat-answer-action chat-answer-action-danger" type="button"
        aria-label="Пожаловаться на ответ" title="Пожаловаться на ответ" onClick={onReport}>
        <ActionIcon name="report" />
      </button>
      {copyStatus && <span className="chat-copy-status" data-copied={copied || undefined} role="status">{copyStatus}</span>}
    </div>
  </div>;
}

