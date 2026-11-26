import { useRef, useState } from "react";
import type { Turn } from "./chat-api";
import { answerSources } from "./chat-markdown";

function MenuIcon({ name }: { name: "copy" | "sources" | "export" | "report" }) {
  const shapes = {
    copy: <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>,
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
          <li><button className="button button-text" type="button" onClick={() => void copy()}><MenuIcon name="copy" />Копировать ответ</button></li>
          {sources.length > 0 && <li><button className="button button-text" type="button" onClick={() => {
            closeMenu();
            if (sourceDetails.current) {
              sourceDetails.current.open = true;
              sourceDetails.current.querySelector("summary")?.focus();
            }
          }}><MenuIcon name="sources" />Показать источники</button></li>}
          <li><button className="button button-text" type="button" onClick={() => { closeMenu(); onExport(); }}><MenuIcon name="export" />Экспортировать</button></li>
          <li><button className="button button-text" type="button" onClick={() => { closeMenu(); onReport(); }}><MenuIcon name="report" />Пожаловаться на ответ</button></li>
        </ul>
      </details>
      <span role="status">{copyStatus}</span>
    </div>
  </div>;
}

