import { useRef, useState } from "react";
import type { Turn } from "./chat-api";
import { answerSources } from "./chat-markdown";

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
  const menu = useRef<HTMLDetailsElement>(null);
  const [copyStatus, setCopyStatus] = useState("");

  function closeMenu() {
    if (menu.current) {
      menu.current.open = false;
      menu.current.querySelector("summary")?.focus();
    }
  }

  async function copy() {
    closeMenu();
    try {
      await navigator.clipboard.writeText(answer.summary);
      setCopyStatus("Ответ скопирован.");
    } catch {
      setCopyStatus("Не удалось скопировать ответ.");
    }
  }

  return <div className="chat-answer">
    <Summary text={answer.summary} />
    {answer.partial && <p className="chat-partial">Частичный ответ</p>}
    {sources.length > 0 && <details className="chat-sources" ref={sourceDetails}>
      <summary>Источники ({sources.length})</summary>
      <ul>{sources.map(({ ref, label, url, dates }) => <li key={ref}>
        {url ? <a href={url}>{label}</a> : <span>{label}</span>}
        <span>{dates.length ? `Данные на ${dates.map((date) => /^\d{4}-\d{2}-\d{2}$/.test(date) ? date.split("-").reverse().join(".") : date).join(", ")}` : "Дата актуальности не указана"}</span>
      </li>)}</ul>
    </details>}
    <div className="chat-answer-actions">
      <details className="chat-answer-menu" ref={menu} onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) event.currentTarget.open = false;
      }} onKeyDown={(event) => {
        if (event.key === "Escape" && menu.current?.open) closeMenu();
      }}>
        <summary aria-label="Действия с ответом">
          <svg aria-hidden="true" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="1.5"/><circle cx="12" cy="12" r="1.5"/><circle cx="19" cy="12" r="1.5"/></svg>
        </summary>
        <ul>
          <li><button className="button button-text" type="button" onClick={() => void copy()}>Копировать ответ</button></li>
          {sources.length > 0 && <li><button className="button button-text" type="button" onClick={() => {
            closeMenu();
            if (sourceDetails.current) {
              sourceDetails.current.open = true;
              sourceDetails.current.querySelector("summary")?.focus();
            }
          }}>Показать источники</button></li>}
          <li><button className="button button-text" type="button" onClick={() => { closeMenu(); onExport(); }}>Экспортировать</button></li>
          <li><button className="button button-text" type="button" onClick={() => { closeMenu(); onReport(); }}>Пожаловаться на ответ</button></li>
        </ul>
      </details>
      <span role="status">{copyStatus}</span>
    </div>
  </div>;
}

