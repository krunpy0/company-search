"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { authMessage, authMessages } from "@/lib/auth-feedback";
import { productName, questionDraftKey } from "../../product";
import { WorkspaceDialog } from "../../workspace-dialog";
import { companyIdentifier } from "./company-data";
import { Chat } from "./chat";
import { chatApi, readChat, type Dialogue } from "./chat-api";
import "./chat.css";
import "./workspace.css";

function Icon({ name }: { name: "search" | "panel" | "plus" | "account" }) {
  const shapes = {
    search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 4 4" /></>,
    panel: <><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M9 3v18" /></>,
    plus: <path d="M12 5v14M5 12h14" />,
    account: <><circle cx="12" cy="8" r="3" /><path d="M6 19v-1a6 6 0 0 1 12 0v1" /></>,
  };
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
      {shapes[name]}
    </svg>
  );
}

function Sidebar({ close, history, onNewChat }: { close: () => void; history: React.ReactNode; onNewChat?: () => void }) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  async function logout() {
    setError("");
    setPending(true);
    try {
      const response = await fetch("/api/auth/logout", { method: "POST" });
      if (response.status === 204) {
        try { sessionStorage.removeItem(questionDraftKey); } catch { /* Storage may be disabled. */ }
        window.location.replace("/login");
        return;
      }
      setError(authMessage(await response.json()));
    } catch {
      setError(authMessages.unavailable);
    }
    setPending(false);
  }

  return (
    <>
      <div className="workspace-sidebar-head">
        <Link href="/">{productName}</Link>
        <button className="workspace-icon" aria-label="Поиск по истории пока недоступен" title="Поиск по истории пока недоступен" disabled><Icon name="search" /></button>
        <button className="workspace-icon" aria-label="Скрыть боковую панель" onClick={close}><Icon name="panel" /></button>
      </div>
      <Link className="button workspace-new" href="/chat" prefetch={false} onNavigate={onNewChat}><Icon name="plus" />Новый чат</Link>
      <p className="workspace-history">Ваши чаты</p>
      {history}
      <details className="workspace-profile">
        <summary><span className="workspace-avatar"><Icon name="account" /></span>Аккаунт</summary>
        <div className="workspace-profile-actions">
          <button type="button" className="button button-text" aria-haspopup="dialog" onClick={() => setSettingsOpen(true)}>Настройки</button>
          <button className="button button-text" disabled={pending} onClick={logout}>{pending ? "Выходим…" : "Выйти"}</button>
          {error && <p className="workspace-error" role="alert">{error}</p>}
        </div>
      </details>
      <WorkspaceDialog
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        title="Настройки"
        description="Настройки аккаунта пока недоступны."
      >
        <div className="workspace-dialog-actions">
          <button type="button" className="button button-outline" onClick={() => setSettingsOpen(false)}>Закрыть</button>
        </div>
      </WorkspaceDialog>
    </>
  );
}

export function Workspace({ chat, children }: {
  chat?: { id?: string; company?: string };
  children?: React.ReactNode;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const drawer = useRef<HTMLDialogElement>(null);
  const [dialogues, setDialogues] = useState<Dialogue[]>([]);
  const [next, setNext] = useState<string>();
  const [listError, setListError] = useState(false);
  const [listing, setListing] = useState(false);
  const [createdId, setCreatedId] = useState<string>();
  const listingRef = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    void readChat(chatApi.GET("/v1/chat/dialogues", { signal: controller.signal })).then((data) => {
      if (controller.signal.aborted) return;
      setDialogues((current) => {
        const loaded = data.dialogues.map((item) => {
          const local = current.find(({ id }) => id === item.id);
          return local?.title && !item.title ? local : item;
        });
        return [...current.filter((item) => !loaded.some(({ id }) => id === item.id)), ...loaded];
      });
      setNext(data.next_before_id);
    }).catch(() => {
      if (!controller.signal.aborted) setListError(true);
    });
    return () => controller.abort();
  }, []);

  async function loadDialogues() {
    if (listingRef.current) return;
    listingRef.current = true;
    setListing(true);
    setListError(false);
    try {
      const data = await readChat(chatApi.GET("/v1/chat/dialogues", { params: { query: { before_id: next } } }));
      setDialogues((current) => [...current, ...data.dialogues.filter(({ id }) => !current.some((item) => item.id === id))]);
      setNext(data.next_before_id);
    } catch {
      setListError(true);
    } finally {
      listingRef.current = false;
      setListing(false);
    }
  }

  function addDialogue(dialogue: Dialogue) {
    setCreatedId(dialogue.id);
    setDialogues((current) => [dialogue, ...current.filter(({ id }) => id !== dialogue.id)]);
  }

  const currentId = chat?.id ?? createdId;
  const history = <div className="workspace-dialogues">
    <ul>{dialogues.map(({ id, title, registration, created_at }) => <li key={id}>
      <Link href={`/chat/${id}`} prefetch={false} aria-current={currentId === id ? "page" : undefined}
        onClick={() => drawer.current?.close()}>
        {title ?? (registration ? companyIdentifier(registration) : new Date(created_at).toLocaleString("ru-RU", { dateStyle: "short", timeStyle: "short" }))}
      </Link>
    </li>)}</ul>
    {listError && <p className="workspace-error" role="status">Не удалось загрузить список чатов.</p>}
    {(next || listError) && <button type="button" className="button button-text" disabled={listing} onClick={loadDialogues}>
      {listing ? "Загружаем…" : listError ? "Повторить" : "Ещё чаты"}
    </button>}
  </div>;

  useEffect(() => {
    const controller = new AbortController();
    async function checkSession() {
      if (document.visibilityState !== "visible") return;
      try {
        const response = await fetch("/api/auth/session", { cache: "no-store", signal: controller.signal });
        if (response.status === 401) window.location.replace("/login?expired=1");
      } catch {
        // A network failure does not invalidate the current session.
      }
    }
    function recheck(event: PageTransitionEvent) {
      if (event.persisted) window.location.reload();
    }
    const interval = window.setInterval(checkSession, 60_000);
    document.addEventListener("visibilitychange", checkSession);
    window.addEventListener("pageshow", recheck);
    return () => {
      controller.abort();
      window.clearInterval(interval);
      document.removeEventListener("visibilitychange", checkSession);
      window.removeEventListener("pageshow", recheck);
    };
  }, []);

  return (
    <div className={`workspace${collapsed ? " workspace-collapsed" : ""}`}>
      {!collapsed && <aside className="workspace-sidebar" aria-label="Боковая панель"><Sidebar close={() => setCollapsed(true)} history={history} /></aside>}
      <dialog ref={drawer} className="workspace-drawer" aria-label="Боковая панель">
        <div className="workspace-sidebar"><Sidebar close={() => drawer.current?.close()} history={history} onNewChat={() => drawer.current?.close()} /></div>
      </dialog>
      <main className={children ? "workspace-main workspace-main-data" : "workspace-main"}>
        <button className="workspace-icon workspace-open workspace-open-desktop" aria-label="Показать боковую панель" onClick={() => setCollapsed(false)}><Icon name="panel" /></button>
        <button className="workspace-icon workspace-open workspace-open-mobile" aria-label="Показать боковую панель" onClick={() => drawer.current?.showModal()}><Icon name="panel" /></button>
        {chat ? <Chat initialId={chat.id} company={chat.company} onDialogue={addDialogue} /> : children}
      </main>
    </div>
  );
}
