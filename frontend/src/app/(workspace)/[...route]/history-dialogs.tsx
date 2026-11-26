"use client";

import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import Link from "next/link";
import { WorkspaceDialog } from "@/app/workspace-dialog";
import { ChatError, chatApi, readChat, type Dialogue } from "./chat-api";
import { ChatExportDialog } from "./chat-export";
import { companyIdentifier } from "./company-data";
import "./history-dialogs.css";

export type HistoryAction = { kind: "rename" | "delete" | "export"; dialogue: Dialogue };

function dialogueLabel({ title, registration, created_at }: Dialogue) {
  return title || (registration ? companyIdentifier(registration) : new Date(created_at).toLocaleString("ru-RU", {
    dateStyle: "short", timeStyle: "short",
  }));
}

export function DialogueRow({ dialogue, active, onNavigate, onAction }: {
  dialogue: Dialogue;
  active: boolean;
  onNavigate: () => void;
  onAction: (action: HistoryAction) => void;
}) {
  const id = useId();
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const label = dialogueLabel(dialogue);

  function positionMenu() {
    const anchor = trigger.current?.getBoundingClientRect();
    const panel = menu.current;
    if (!anchor || !panel) return;
    panel.style.right = `${Math.max(8, window.innerWidth - anchor.right)}px`;
    const above = anchor.bottom >= window.innerHeight / 2;
    panel.style.top = above ? "auto" : `${anchor.bottom + 4}px`;
    panel.style.bottom = above ? `${window.innerHeight - anchor.top + 4}px` : "auto";
  }

  function select(kind: HistoryAction["kind"]) {
    menu.current?.hidePopover();
    trigger.current?.focus();
    onAction({ kind, dialogue });
  }

  return (
    <li className="history-row" data-active={active || undefined}>
      <Link className="history-row-link" href={`/chat/${dialogue.id}`} prefetch={false} aria-current={active ? "page" : undefined}
        title={label} onNavigate={onNavigate}>{label}</Link>
      <button className="history-row-trigger" type="button" ref={trigger} popoverTarget={id} onClick={positionMenu}
        aria-label={`Действия с чатом «${label}»`} title="Действия с чатом">
        <svg aria-hidden="true" viewBox="0 0 20 20" width="20" height="20" fill="currentColor">
          <circle cx="4" cy="10" r="1.5" /><circle cx="10" cy="10" r="1.5" /><circle cx="16" cy="10" r="1.5" />
        </svg>
      </button>
      <div className="history-menu" id={id} ref={menu} popover="auto" role="group" aria-label="Действия с чатом">
        <button type="button" className="button button-text" onClick={() => select("rename")}>Переименовать</button>
        <button type="button" className="button button-text" onClick={() => select("export")}>Экспорт</button>
        <button type="button" className="button button-text history-danger" onClick={() => select("delete")}>Удалить</button>
      </div>
    </li>
  );
}

type SearchResult = {
  key: string;
  dialogues: Dialogue[];
  next?: string;
  status: "ready" | "error" | "loading-more" | "error-more";
};

export function HistorySearchDialog({ open, revision, onClose, onNavigate }: {
  open: boolean; revision: number; onClose: () => void; onNavigate: () => void;
}) {
  return <WorkspaceDialog open={open} onClose={onClose} title="Поиск по истории"
    description="Ищите по названиям чатов, регистрационным номерам, вопросам и кратким ответам.">
    {open && <HistorySearch revision={revision} onNavigate={onNavigate} />}
  </WorkspaceDialog>;
}

function HistorySearch({ revision, onNavigate }: { revision: number; onNavigate: () => void }) {
  const id = useId();
  const [input, setInput] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<SearchResult>();
  const more = useRef<AbortController>(null);
  const query = input.trim();
  const key = JSON.stringify([query, revision, attempt]);
  const page = result?.key === key ? result : undefined;

  useEffect(() => {
    if (!query) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void readChat(chatApi.GET("/v1/chat/dialogues/search", {
        params: { query: { q: query } }, signal: controller.signal,
      })).then(({ dialogues, next_before_id }) => {
        if (!controller.signal.aborted) setResult({ key, dialogues, next: next_before_id, status: "ready" });
      }).catch(() => {
        if (!controller.signal.aborted) setResult({ key, dialogues: [], status: "error" });
      });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
      more.current?.abort();
      more.current = null;
    };
  }, [key, query]);

  async function loadMore() {
    if (!page?.next || more.current) return;
    const controller = new AbortController();
    more.current = controller;
    setResult({ ...page, status: "loading-more" });
    try {
      const { dialogues, next_before_id } = await readChat(chatApi.GET("/v1/chat/dialogues/search", {
        params: { query: { q: query, before_id: page.next } }, signal: controller.signal,
      }));
      if (!controller.signal.aborted) setResult({
        key, dialogues: [...page.dialogues, ...dialogues], next: next_before_id, status: "ready",
      });
    } catch {
      if (!controller.signal.aborted) setResult({ ...page, status: "error-more" });
    } finally {
      if (more.current === controller) more.current = null;
    }
  }

  return <div className="history-search">
    <label htmlFor={id}>Запрос</label>
    <input id={id} className="field history-input" type="search" value={input} maxLength={200}
      onChange={(event) => setInput(event.target.value)} data-dialog-autofocus />
    <p className="history-search-status" role="status">
      {query && !page && "Ищем чаты…"}
      {page?.status === "ready" && (page.dialogues.length ? `Показано чатов: ${page.dialogues.length}.` : "Чаты по этому запросу не найдены.")}
      {page?.status === "error" && "Не удалось выполнить поиск."}
      {page?.status === "error-more" && "Не удалось загрузить следующие результаты."}
    </p>
    {page?.status === "error" && <button type="button" className="button button-outline" onClick={() => setAttempt((value) => value + 1)}>Повторить поиск</button>}
    {Boolean(page?.dialogues.length) && <ul className="history-results" aria-label="Найденные чаты">
      {page?.dialogues.map((dialogue) => <li key={dialogue.id}>
        <Link href={`/chat/${dialogue.id}`} prefetch={false} onNavigate={onNavigate}>{dialogueLabel(dialogue)}</Link>
      </li>)}
    </ul>}
    {page?.next && <button type="button" className="button button-outline" onClick={() => void loadMore()} disabled={page.status === "loading-more"}>
      {page.status === "loading-more" ? "Загружаем…" : page.status === "error-more" ? "Повторить" : "Ещё результаты"}
    </button>}
  </div>;
}

export function HistoryActionDialog({ action, onClose, onRename, onDelete }: {
  action: HistoryAction;
  onClose: () => void;
  onRename: (dialogue: Dialogue) => void;
  onDelete: (id: string) => void;
}) {
  const id = useId();
  const [title, setTitle] = useState(action.dialogue.title ?? "");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const saving = useRef(false);
  const renaming = action.kind === "rename";
  const trimmedTitle = title.trim();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving.current || (renaming && !trimmedTitle)) return;
    saving.current = true;
    setPending(true);
    setError("");
    const path = { id: action.dialogue.id };
    try {
      if (renaming) {
        onRename(await readChat(chatApi.PATCH("/v1/chat/dialogues/{id}", { params: { path }, body: { title: trimmedTitle } })));
      } else {
        const { response } = await chatApi.DELETE("/v1/chat/dialogues/{id}", { params: { path } });
        if (response.status === 401) window.location.replace("/login?expired=1");
        if (response.status !== 204 && response.status !== 404) throw new Error("unavailable");
        onDelete(path.id);
      }
      onClose();
    } catch (failure) {
      setError(failure instanceof ChatError && failure.status === 404
        ? "Чат больше недоступен."
        : renaming ? "Не удалось сохранить название. Попробуйте ещё раз." : "Не удалось удалить чат. Попробуйте ещё раз.");
    } finally {
      saving.current = false;
      setPending(false);
    }
  }

  if (action.kind === "export") return <ChatExportDialog open onClose={onClose} dialogueId={action.dialogue.id} />;

  return <WorkspaceDialog open onClose={() => { if (!saving.current) onClose(); }} title={renaming ? "Переименовать чат" : "Удалить чат?"}
    description={renaming ? undefined : `Чат «${dialogueLabel(action.dialogue)}» и его сообщения будут удалены. Восстановить их нельзя.`}>
    <form onSubmit={submit} className="history-edit">
      {renaming && <>
        <label htmlFor={id}>Название чата</label>
        <input id={id} className="field history-input" value={title} maxLength={80} required disabled={pending}
          onChange={(event) => setTitle(event.target.value)} data-dialog-autofocus />
      </>}
      {error && <p className="workspace-error" role="alert">{error}</p>}
      <div className="workspace-dialog-actions">
        <button type="button" className="button button-outline" disabled={pending} onClick={onClose} data-dialog-autofocus={renaming ? undefined : ""}>Отмена</button>
        <button type="submit" className="button button-primary" disabled={pending || (renaming && !trimmedTitle)}>
          {renaming ? pending ? "Сохраняем…" : "Сохранить" : pending ? "Удаляем…" : "Удалить чат"}
        </button>
      </div>
    </form>
  </WorkspaceDialog>;
}
