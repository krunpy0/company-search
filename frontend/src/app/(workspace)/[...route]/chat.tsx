"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { FeedbackDialog } from "@/app/feedback-dialog";
import { WorkspaceDialog } from "@/app/workspace-dialog";
import { newChatMessageId } from "@/lib/chat-contract";
import { Arrow } from "../../landing-controls";
import { questionDraftKey } from "../../product";
import { companyIdentifier, companyPath } from "./company-data";
import { chatApi, ChatError, readChat, readChatPage, type ChatHistory, type Dialogue, type Turn } from "./chat-api";
import { Answer } from "./chat-answer";
import { ChatExportDialog } from "./chat-export";
import { collectChatHistory } from "./chat-history";

type Props = { initialId?: string; company?: string; onDialogue: (dialogue: Dialogue) => void; onNewChat?: () => void };
type Attempt = { messageId: string } & ({ kind: "question"; draft: string } | { kind: "candidate"; registration: string });
type Recovery = { attempt: Attempt; id: string; boundary?: string };
type Failure = {
  code: string;
  requestId?: string;
} & ({ kind: "retry"; attempt: Attempt } | { kind: "check" | "checked"; recovery: Recovery } | { kind: "notice" });
type History = { kind: "loading" } | { kind: "ready" } | { kind: "missing" } | { kind: "error"; requestId?: string };
type Overlay = { kind: "quota" } | { kind: "export" | "report"; turn: Turn } | { kind: "technical"; requestId: string; dialogueText?: string };

const templates = [
  ["Оценка рисков", "Оцени риски работы с компанией "],
  ["Анализ конкурента", "Проанализируй конкурента "],
  ["Проверка бизнеса", "Проверь компанию "],
];

const companyTemplates = [
  ["Оценка рисков", "Оцени риски работы с этой компанией"],
  ["Финансы и налоги", "Покажи доходы, расходы и налоги этой компании"],
  ["Проверка бизнеса", "Сделай общую проверку этой компании"],
];

const messageBudget = "Дневной лимит сообщений исчерпан. Попробуйте после полуночи по Москве.";
const failureMessages: Record<string, string> = {
  message_budget_exhausted: messageBudget,
  token_budget_exhausted: "Дневной лимит токенов исчерпан. Попробуйте после полуночи по Москве.",
  model_unavailable: "Модель чата временно недоступна. Можно повторить отправку.",
  turn_in_progress: "В этом диалоге уже готовится ответ. Проверьте историю перед повтором.",
  busy: "Чат сейчас занят. Попробуйте отправить сообщение чуть позже.",
  checked: "История обновлена. Ответ на это сообщение пока не найден. Вы можете повторить отправку.",
  invalid_message: "Введите вопрос длиной до 2000 символов.",
  dialogue_not_found: "Диалог не найден.",
  registration_selection_required: "Выберите регистрацию из актуального списка.",
  history_check_failed: "Не удалось проверить историю. Повторите проверку, чтобы узнать, сохранился ли ответ.",
  unavailable: "Не удалось отправить сообщение. Проверьте историю перед повтором.",
  create_unavailable: "Не удалось создать диалог. Вопрос сохранён в поле ввода.",
  history_unavailable: "Не удалось обновить диалог перед отправкой. Попробуйте ещё раз.",
};

function submissionText(attempt: Attempt) {
  return attempt.kind === "question"
    ? attempt.draft.trim().replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "")
    : attempt.registration;
}

function registrationLabel(value: string) {
  return companyIdentifier(`reg:${value.replace(/^reg:/, "")}`);
}

function missing(error: unknown) {
  return error instanceof ChatError && error.status === 404;
}

export function Chat(props: Props) {
  return <ChatThread key={props.initialId ?? props.company ?? "new"} {...props} />;
}

function ChatThread({ initialId, company, onDialogue, onNewChat }: Props) {
  const [dialogue, setDialogue] = useState<Dialogue>();
  const [turns, setTurns] = useState<Turn[]>([]);
  const [older, setOlder] = useState<string>();
  const [history, setHistory] = useState<History>(initialId ? { kind: "loading" } : { kind: "ready" });
  const [historyRetry, setHistoryRetry] = useState(0);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [historyError, setHistoryError] = useState(false);
  const [draft, setDraft] = useState("");
  const [pending, setPending] = useState<string>();
  const [checking, setChecking] = useState(false);
  const [failure, setFailure] = useState<Failure>();
  const [overlay, setOverlay] = useState<Overlay>();
  const active = useRef(false);
  const sending = useRef(false);
  const titleLookup = useRef<AbortController>(null);
  const paging = useRef(false);
  const storeDraft = useRef(!initialId);
  const scrollArea = useRef<HTMLDivElement>(null);
  const scrollToLatest = useRef(false);
  const dialogueRef = useRef<Dialogue>(undefined);
  const id = dialogue?.id ?? initialId;
  const inThread = Boolean(id || pending);
  const candidates = dialogue?.candidates ?? [];
  const needsCheck = failure?.kind === "check" || failure?.kind === "checked";
  const busy = Boolean(pending || checking);
  const canSubmit = !busy && !needsCheck && !candidates.length && Boolean(draft.trim());
  const draftInput = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    active.current = true;
    const controller = new AbortController();
    if (initialId) {
      void readChatPage(initialId, undefined, controller.signal).then((page) => {
        if (controller.signal.aborted) return;
        scrollToLatest.current = true;
        dialogueRef.current = page.dialogue;
        setDialogue(page.dialogue);
        setTurns([...page.messages].reverse());
        setOlder(page.next_before_id);
        setHistory({ kind: "ready" });
      }).catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setHistory(missing(error) ? { kind: "missing" } : {
          kind: "error", requestId: error instanceof ChatError ? error.requestId : undefined,
        });
      });
    } else {
      queueMicrotask(() => {
        if (controller.signal.aborted) return;
        try { setDraft(sessionStorage.getItem(questionDraftKey) ?? ""); } catch { /* Storage may be disabled. */ }
      });
    }
    return () => {
      active.current = false;
      controller.abort();
      titleLookup.current?.abort();
    };
  }, [initialId, historyRetry]);

  useEffect(() => {
    const area = scrollArea.current;
    if (scrollToLatest.current && area) {
      area.scrollTop = area.scrollHeight;
      scrollToLatest.current = false;
    }
  }, [inThread, pending, turns]);

  function updateDialogue(next: Dialogue, notify = true) {
    const latest = { ...next, title: next.title ?? dialogueRef.current?.title };
    dialogueRef.current = latest;
    setDialogue(latest);
    if (notify) onDialogue(latest);
  }

  function finishAttempt(attempt: Attempt, target: string) {
    if (!active.current) return;
    if (!initialId && storeDraft.current && attempt.kind === "question") {
      storeDraft.current = false;
      try {
        if (sessionStorage.getItem(questionDraftKey) === attempt.draft) sessionStorage.removeItem(questionDraftKey);
      } catch { /* A completed turn is already saved by the API. */ }
      // Keep this composer mounted while the title is being generated.
      window.history.replaceState(null, "", `/chat/${target}`);
    }
    if (attempt.kind === "question") setDraft("");
  }

  async function loadOlder() {
    if (!id || !older || paging.current) return;
    paging.current = true;
    setLoadingOlder(true);
    setHistoryError(false);
    try {
      const page = await readChatPage(id, older);
      if (!active.current) return;
      setTurns((current) => [...page.messages].reverse().concat(current));
      setOlder(page.next_before_id);
    } catch {
      if (active.current) setHistoryError(true);
    } finally {
      paging.current = false;
      if (active.current) setLoadingOlder(false);
    }
  }

  async function refreshTitle(id: string, controller: AbortController) {
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(50_000)]);
    while (!signal.aborted) {
      try {
        const { dialogue: latest } = await readChatPage(id, undefined, signal);
        if (signal.aborted) return;
        if (latest.title && dialogueRef.current) {
          updateDialogue({ ...dialogueRef.current, title: latest.title });
          return;
        }
      } catch {
        // A transient history error can clear before the title is ready.
      }
      if (!signal.aborted) await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  }

  function applyHistory(page: ChatHistory) {
    updateDialogue(page.dialogue, Boolean(page.messages.length));
    if (page.messages.length) {
      scrollToLatest.current = true;
      setTurns((current) => {
        const ids = new Set(current.map((message) => message.id));
        return [...current, ...page.messages.filter((message) => !ids.has(message.id))];
      });
    }
  }

  function canSend(attempt: Attempt, dialogue: Dialogue) {
    return attempt.kind === "candidate"
      ? dialogue.candidates?.includes(attempt.registration)
      : !dialogue.candidates?.length;
  }

  async function reconcile(recovery: Recovery, requestId?: string, initialCode = "unavailable") {
    setChecking(true);
    try {
      const page = await collectChatHistory((before) => readChatPage(recovery.id, before), recovery.boundary);
      if (!active.current) return "stop";
      applyHistory(page);
      if (page.messages.some((message) => message.client_message_id === recovery.attempt.messageId)) {
        finishAttempt(recovery.attempt, recovery.id);
        setFailure(undefined);
        return "stop";
      }
      if (initialCode === "registration_selection_required" || !canSend(recovery.attempt, page.dialogue)) {
        setFailure({ kind: "notice", code: "registration_selection_required", requestId });
        return "stop";
      }
      const boundary = page.messages.at(-1)?.id ?? recovery.boundary;
      const code = initialCode === "turn_in_progress" ? initialCode : "checked";
      setFailure({ kind: "checked", code, recovery: { ...recovery, boundary }, requestId });
      return { boundary };
    } catch (error) {
      if (!active.current) return "stop";
      const latestId = error instanceof ChatError ? error.requestId : undefined;
      if (missing(error)) setHistory({ kind: "missing" });
      else setFailure({ kind: "check", code: "history_check_failed", recovery, requestId: latestId ?? requestId });
      return "stop";
    } finally {
      if (active.current) setChecking(false);
    }
  }

  async function send(attempt: Attempt, retry?: Extract<Failure, { kind: "check" | "checked" }>) {
    const content = submissionText(attempt);
    if (!content || sending.current || history.kind !== "ready") return;
    if ([...content].length > 2000 || content.includes("\0")) {
      setFailure({ kind: "notice", code: "invalid_message" });
      return;
    }

    sending.current = true;
    let target = id;
    let boundary = turns.at(-1)?.id;
    let started = false;

    try {
      if (retry) {
        const result = await reconcile(retry.recovery, retry.requestId);
        if (result === "stop" || retry.kind === "check") return;
        boundary = result.boundary;
      } else if (target) {
        const historyId = target;
        setChecking(true);
        const page = await collectChatHistory((before) => readChatPage(historyId, before), boundary);
        if (!active.current) return;
        applyHistory(page);
        boundary = page.messages.at(-1)?.id ?? boundary;
        if (!canSend(attempt, page.dialogue)) {
          setFailure({ kind: "notice", code: "registration_selection_required" });
          return;
        }
        setChecking(false);
      }
      scrollToLatest.current = true;
      setPending(attempt.kind === "candidate" ? registrationLabel(content) : content);
      setFailure(undefined);
      if (!target) {
        const created = await readChat(chatApi.POST("/v1/chat/dialogues", { body: { registration: company } }));
        if (!active.current) return;
        target = created.id;
        updateDialogue(created);
      }

      started = true;
      const response = readChat(chatApi.POST("/v1/chat/dialogues/{id}/messages", {
        params: { path: { id: target } }, body: { content, client_message_id: attempt.messageId },
      }));
      if (!dialogueRef.current?.title && !titleLookup.current) {
        const controller = new AbortController();
        titleLookup.current = controller;
        void refreshTitle(target, controller).finally(() => {
          if (titleLookup.current === controller) titleLookup.current = null;
        });
      }
      const turn = await response;
      finishAttempt(attempt, target);
      if (!active.current) return;
      scrollToLatest.current = true;
      setTurns((current) => current.some((item) => item.id === turn.id) ? current : [...current, turn]);
      if (dialogueRef.current) {
        updateDialogue({
          ...dialogueRef.current,
          updated_at: turn.created_at,
          candidates: turn.answer.candidates,
          registration: attempt.kind === "candidate" ? `reg:${content.replace(/^reg:/, "")}` : dialogueRef.current.registration,
        });
      }
    } catch (error) {
      if (!active.current) return;
      const code = error instanceof ChatError ? error.code : "unavailable";
      const requestId = error instanceof ChatError ? error.requestId : undefined;
      if (missing(error)) {
        setHistory({ kind: "missing" });
      } else if (code === "message_id_conflict" || code === "invalid_message_id") {
        setFailure({ kind: "notice", code: "invalid_message", requestId });
      } else if (started && target && !["invalid_message", "model_unavailable", "busy", "message_budget_exhausted", "token_budget_exhausted", "invalid_session", "invalid_origin"].includes(code)) {
        setPending(undefined);
        await reconcile({ attempt, id: target, boundary }, requestId, code);
      } else if (code !== "invalid_session") {
        const failureCode = started ? code : target ? "history_unavailable" : "create_unavailable";
        setFailure({ kind: "retry", code: failureCode, attempt, requestId });
        if (code === "message_budget_exhausted") setOverlay({ kind: "quota" });
      }
    } finally {
      sending.current = false;
      if (active.current) {
        setPending(undefined);
        setChecking(false);
      }
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!needsCheck && !candidates.length) void send({ kind: "question", draft, messageId: newChatMessageId() });
  }

  const composer = <form className="workspace-question" onSubmit={submit}>
    <textarea ref={draftInput} value={pending ? "" : draft} className="field" aria-label="Вопрос о компании" maxLength={4000}
      placeholder={candidates.length ? "Выберите регистрацию выше" : inThread ? "Ответить…" : "Введите ИНН, ОГРН, название компании или ФИО предпринимателя..."}
      required disabled={busy || needsCheck || Boolean(candidates.length)}
      onChange={(event) => {
        const value = event.currentTarget.value;
        setDraft(value);
        setFailure(undefined);
        if (storeDraft.current) {
          try { sessionStorage.setItem(questionDraftKey, value); } catch { /* Storage may be disabled. */ }
        }
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
          event.preventDefault();
          event.currentTarget.form?.requestSubmit();
        }
      }} />
    <button className="button button-primary send workspace-send" aria-label="Отправить вопрос"
      disabled={!canSubmit}><Arrow /></button>
  </form>;

  const issue = failure && <div className="chat-failure">
    <p className="workspace-error" role="alert">{failureMessages[failure.code] ?? failureMessages.unavailable}</p>
    <div className="chat-failure-actions">
      {failure.kind === "retry" && <button type="button" className="button button-outline" disabled={busy}
        onClick={() => void send(failure.attempt)}>Повторить отправку</button>}
      {(failure.kind === "check" || failure.kind === "checked") && <button type="button" className="button button-outline" disabled={busy}
        onClick={() => void send(failure.recovery.attempt, failure)}>
        {failure.kind === "check" ? "Проверить историю" : "Повторить отправку"}
      </button>}
      {failure.kind === "checked" && <button type="button" className="button button-text" disabled={busy}
        onClick={() => setFailure(undefined)}>{failure.recovery.attempt.kind === "candidate" ? "Выбрать снова" : "Оставить в черновике"}</button>}
      {failure.requestId && <button type="button" className="button button-text"
        onClick={() => {
          if (failure.requestId) setOverlay({ kind: "technical", requestId: failure.requestId, dialogueText: draft || undefined });
        }}>Сообщить о проблеме</button>}
    </div>
  </div>;

  function closeOverlay() { setOverlay(undefined); }

  return <>
    {!inThread ? <div className="workspace-intro">
      <h1>Какую компанию изучим?</h1>
      <p><Link href="/companies" prefetch={false}>Найдите компанию</Link>, проверьте конкурента или задайте вопрос.</p>
      {company && <p className="workspace-selected">Выбрана регистрация <Link href={companyPath(company)} prefetch={false}>{companyIdentifier(company)}</Link></p>}
      {composer}
      <div className="workspace-templates" aria-label="Примеры вопросов">
        {(company ? companyTemplates : templates).map(([label, text]) => (
          <button key={label} type="button" className="workspace-template-chip" disabled={busy} onClick={() => {
            setDraft(text);
            setFailure(undefined);
            if (storeDraft.current) {
              try { sessionStorage.setItem(questionDraftKey, text); } catch { /* Storage may be disabled. */ }
            }
            draftInput.current?.focus();
          }}>{label}</button>
        ))}
      </div>
      {issue}
    </div> : history.kind !== "ready" ? <div className="workspace-state">
      {history.kind === "loading" ? <p role="status">Загружаем диалог…</p> : <>
        <h1>{history.kind === "missing" ? "Диалог не найден" : "Не удалось загрузить диалог"}</h1>
        {history.kind === "missing"
          ? <Link className="button button-outline" href="/chat" prefetch={false} onNavigate={onNewChat}>К новому чату</Link>
          : <>
            <button className="button button-outline" type="button" onClick={() => {
              setHistory({ kind: "loading" });
              setHistoryRetry((value) => value + 1);
            }}>Повторить</button>
            {history.requestId && <button className="button button-text" type="button" onClick={() => {
              if (history.requestId) setOverlay({ kind: "technical", requestId: history.requestId });
            }}>Сообщить о проблеме</button>}
          </>}
      </>}
    </div> : <section className="chat-thread" aria-label="Диалог">
      <div className="chat-scroll" ref={scrollArea}>
        <div className="chat-column chat-turns">
          {older && <button type="button" className="button button-text chat-older" onClick={loadOlder} disabled={loadingOlder || busy}>
            {loadingOlder ? "Загружаем…" : historyError ? "Повторить загрузку" : "Показать предыдущие сообщения"}
          </button>}
          {historyError && <p className="workspace-error" role="alert">Не удалось загрузить предыдущие сообщения.</p>}
          {!turns.length && !pending && !failure && !checking &&
            <p className="chat-empty">Задайте первый вопрос{dialogue?.registration ? ` о компании с ${registrationLabel(dialogue.registration)}` : ""}.</p>}
          {turns.map((turn) => <article className="chat-turn" key={turn.id}>
            <p className="chat-question">{turn.question}</p>
            <Answer answer={turn.answer} onExport={() => setOverlay({ kind: "export", turn })}
              onReport={() => setOverlay({ kind: "report", turn })} />
          </article>)}
          {candidates.length > 0 && <div className="chat-candidates" role="group" aria-label="Выбор регистрации">
            <p>Выберите регистрацию, чтобы продолжить исходный вопрос.</p>
            <ul>{candidates.map((value) => <li key={value}>
              <button type="button" className="button button-outline" disabled={busy || needsCheck}
                onClick={() => void send({ kind: "candidate", registration: value, messageId: newChatMessageId() })}>{registrationLabel(value)}</button>
            </li>)}</ul>
          </div>}
          {pending && <div className="chat-turn"><p className="chat-question">{pending}</p><p className="chat-wait" role="status">Готовим ответ…</p></div>}
        </div>
      </div>
      <footer className="chat-column chat-footer">
        {checking && <p className="chat-wait" role="status">Проверяем историю диалога…</p>}
        {issue}
        {composer}
      </footer>
    </section>}
    {overlay?.kind === "quota" && <WorkspaceDialog open onClose={closeOverlay} title="Дневной лимит сообщений"
      description={messageBudget}>
      <div className="workspace-dialog-actions"><button type="button" className="button button-primary" data-dialog-autofocus onClick={closeOverlay}>Понятно</button></div>
    </WorkspaceDialog>}
    {overlay?.kind === "export" && id && <ChatExportDialog open onClose={closeOverlay} dialogueId={id} turn={overlay.turn} />}
    {overlay?.kind === "report" && <FeedbackDialog open onClose={closeOverlay} kind="answer"
      dialogueText={`${overlay.turn.question}\n\n${overlay.turn.answer.summary}`} />}
    {overlay?.kind === "technical" && <FeedbackDialog open onClose={closeOverlay} kind="technical"
      requestId={overlay.requestId} dialogueText={overlay.dialogueText} />}
  </>;
}
