"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { Arrow } from "../../landing-controls";
import { questionDraftKey } from "../../product";
import { companyIdentifier, companyPath } from "./company-data";
import { chatApi, readChat, type Dialogue, type Turn } from "./chat-api";
import { Answer } from "./chat-answer";

function questionError(error: unknown) {
  if (error instanceof Error && error.message === "message_budget_exhausted") {
    return "Дневной лимит сообщений исчерпан. Попробуйте после полуночи по Москве.";
  }
  if (error instanceof Error && error.message === "invalid_message") return "Введите вопрос длиной до 2000 символов.";
  if (error instanceof Error && error.message === "model_unavailable") return "Модель чата временно недоступна. Вопрос сохранён в поле ввода.";
  if (error instanceof Error && error.message === "turn_in_progress") return "Ответ на предыдущий вопрос ещё готовится.";
  return "Не удалось получить ответ. Проверьте историю диалога перед повторной отправкой.";
}

export function Chat({ initialId, company, onDialogue }: {
  initialId?: string;
  company?: string;
  onDialogue: (dialogue: Dialogue) => void;
}) {
  const [dialogue, setDialogue] = useState<Dialogue>();
  const [turns, setTurns] = useState<Turn[]>([]);
  const [older, setOlder] = useState<string>();
  const [history, setHistory] = useState<"loading" | "ready" | "missing" | "error">(initialId ? "loading" : "ready");
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [historyError, setHistoryError] = useState(false);
  const [pending, setPending] = useState("");
  const [error, setError] = useState("");
  const active = useRef(false);
  const sending = useRef(false);
  const titleLookup = useRef<AbortController>(null);
  const paging = useRef(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const scrollArea = useRef<HTMLDivElement>(null);
  const scrollToLatest = useRef(false);
  const failedDraft = useRef("");
  const id = dialogue?.id ?? initialId;
  const inThread = Boolean(id || pending);

  useEffect(() => {
    active.current = true;
    const controller = new AbortController();
    if (initialId) {
      void readChat(chatApi.GET("/v1/chat/dialogues/{id}/messages", {
        params: { path: { id: initialId } }, signal: controller.signal,
      })).then((data) => {
        if (controller.signal.aborted) return;
        scrollToLatest.current = true;
        setDialogue(data.dialogue);
        setTurns([...data.messages].reverse());
        setOlder(data.next_before_id);
        setHistory("ready");
      }).catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setHistory(error instanceof Error && (error.message === "dialogue_not_found" || error.message === "not_found") ? "missing" : "error");
      });
    } else {
      try {
        if (field.current) field.current.value = sessionStorage.getItem(questionDraftKey) ?? "";
      } catch {
        // The composer is available without browser storage.
      }
    }
    return () => {
      active.current = false;
      controller.abort();
      titleLookup.current?.abort();
    };
  }, [initialId]);

  useEffect(() => {
    const area = scrollArea.current;
    if (scrollToLatest.current && area) {
      area.scrollTop = area.scrollHeight;
      scrollToLatest.current = false;
    }
    if (failedDraft.current && !pending && field.current) {
      field.current.value = failedDraft.current;
      failedDraft.current = "";
    }
  }, [inThread, pending, turns]);

  async function loadOlder() {
    if (!id || !older || paging.current) return;
    paging.current = true;
    setLoadingOlder(true);
    setHistoryError(false);
    try {
      const page = await readChat(chatApi.GET("/v1/chat/dialogues/{id}/messages", {
        params: { path: { id }, query: { before_id: older } },
      }));
      setTurns((current) => [...page.messages].reverse().concat(current));
      setOlder(page.next_before_id);
    } catch {
      setHistoryError(true);
    } finally {
      paging.current = false;
      setLoadingOlder(false);
    }
  }

  async function refreshTitle(id: string, controller: AbortController) {
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(50_000)]);
    while (!signal.aborted) {
      try {
        const { dialogue: latest } = await readChat(chatApi.GET("/v1/chat/dialogues/{id}/messages", {
          params: { path: { id } }, signal,
        }));
        if (signal.aborted) return;
        if (latest.title) {
          setDialogue(latest);
          onDialogue(latest);
          return;
        }
      } catch {
        // A transient history error can clear before the title is ready.
      }
      if (!signal.aborted) await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  }

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const content = field.current?.value.trim() ?? "";
    if (!content || sending.current || history !== "ready") return;
    if ([...content].length > 2000) {
      setError("Введите вопрос длиной до 2000 символов.");
      return;
    }

    sending.current = true;
    scrollToLatest.current = true;
    setPending(content);
    setError("");
    if (field.current) field.current.value = "";
    try {
      let target = id;
      if (!target) {
        const created = await readChat(chatApi.POST("/v1/chat/dialogues", { body: { registration: company } }));
        target = created.id;
        if (active.current) {
          setDialogue(created);
          onDialogue(created);
        }
      }
      const response = readChat(chatApi.POST("/v1/chat/dialogues/{id}/messages", {
        params: { path: { id: target } }, body: { content },
      }));
      if (active.current && !turns.length && !dialogue?.title && !titleLookup.current) {
        const controller = new AbortController();
        titleLookup.current = controller;
        void refreshTitle(target, controller).finally(() => {
          if (titleLookup.current === controller) titleLookup.current = null;
        });
      }
      const turn = await response;
      if (!initialId) {
        try {
          if (sessionStorage.getItem(questionDraftKey)?.trim() === content) sessionStorage.removeItem(questionDraftKey);
        } catch {
          // The completed turn is already saved by the API.
        }
      }
      if (active.current) {
        scrollToLatest.current = true;
        setTurns((current) => [...current, turn]);
        if (!initialId) {
          // Keep the current chat mounted while its title is still being fetched.
          window.history.replaceState(null, "", `/chat/${target}`);
        }
      }
    } catch (error) {
      if (active.current) {
        failedDraft.current = content;
        setError(questionError(error));
      }
    } finally {
      sending.current = false;
      if (active.current) setPending("");
    }
  }

  const composer = <form className="workspace-question" onSubmit={send}>
    <textarea ref={field} className="field" aria-label="Вопрос о компании" maxLength={4000}
      placeholder={inThread ? "Ответить…" : "Введите ИНН, ОГРН, название компании или ФИО предпринимателя..."}
      required disabled={Boolean(pending)}
      onChange={(event) => {
        if (inThread) return;
        try { sessionStorage.setItem(questionDraftKey, event.currentTarget.value); } catch { /* Storage may be disabled. */ }
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
          event.preventDefault();
          event.currentTarget.form?.requestSubmit();
        }
      }} />
    <button type="button" className="button workspace-attach" aria-label="Вложения пока недоступны" title="Вложения пока недоступны" disabled>
      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round"><path d="M12 5v14M5 12h14" /></svg>
    </button>
    <button className="button send workspace-send" aria-label="Отправить вопрос" disabled={Boolean(pending)}><Arrow /></button>
  </form>;

  if (!inThread) return <div className="workspace-intro">
    <h1>Какую компанию изучим?</h1>
    <p><Link href="/companies" prefetch={false}>Найдите компанию</Link>, проверьте конкурента или задайте вопрос.</p>
    {company && <p className="workspace-selected">Выбрана регистрация <Link href={companyPath(company)} prefetch={false}>{companyIdentifier(company)}</Link></p>}
    {composer}
    {error && <p className="workspace-error" role="alert">{error}</p>}
  </div>;

  if (history !== "ready") return <div className="workspace-state">
    {history === "loading" ? <p role="status">Загружаем диалог…</p> : <>
      <h1>{history === "missing" ? "Диалог не найден" : "Не удалось загрузить диалог"}</h1>
      {history === "missing"
        ? <Link className="button button-outline" href="/chat" prefetch={false}>К новому чату</Link>
        : <a className="button button-outline" href={`/chat/${id}`}>Повторить</a>}
    </>}
  </div>;

  return <section className="chat-thread" aria-label="Диалог">
    <div className="chat-scroll" ref={scrollArea}>
      <div className="chat-column chat-turns">
        {older && <button type="button" className="button button-text chat-older" onClick={loadOlder} disabled={loadingOlder}>
          {loadingOlder ? "Загружаем…" : "Показать предыдущие сообщения"}
        </button>}
        {historyError && <p className="workspace-error" role="alert">Не удалось загрузить предыдущие сообщения.</p>}
        {!turns.length && !pending && !error &&
          <p className="chat-empty">Задайте первый вопрос{dialogue?.registration ? ` о компании с ${companyIdentifier(dialogue.registration)}` : ""}.</p>}
        {turns.map((turn) => <article className="chat-turn" key={turn.id}>
          <p className="chat-question">{turn.question}</p>
          <Answer answer={turn.answer} />
        </article>)}
        {pending && <div className="chat-turn"><p className="chat-question">{pending}</p><p className="chat-wait" role="status">Готовим ответ…</p></div>}
      </div>
    </div>
    <footer className="chat-column chat-footer">
      {error && <p className="workspace-error" role="alert">{error}</p>}
      {composer}
    </footer>
  </section>;
}

