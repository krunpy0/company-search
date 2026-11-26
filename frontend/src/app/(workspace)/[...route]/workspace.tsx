"use client";

import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import Link from "next/link";
import { FeedbackDialog } from "@/app/feedback-dialog";
import { authMessage, authMessages } from "@/lib/auth-feedback";
import { productName, questionDraftKey } from "../../product";
import { avatarUrl, readProfile, type AccountProfile } from "./account-data";
import { AccountSettingsDialog } from "./account-settings";
import { Chat } from "./chat";
import { chatApi, readChat, type Dialogue } from "./chat-api";
import { DialogueRow, HistoryActionDialog, HistorySearchDialog, type HistoryAction } from "./history-dialogs";
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

const defaultSidebarWidth = 263;
const minSidebarWidth = 200;
const maxSidebarWidth = 420;
const collapseSidebarThreshold = 135;
const sidebarWidthKey = "workspace.sidebarWidth";

function clampSidebarWidth(value: number) {
  return Math.round(Math.min(maxSidebarWidth, Math.max(minSidebarWidth, value)));
}

function Sidebar({ close, history, profile, avatarVersion, onNavigate, onSearch, onSettings, onFeedback }: {
  close: () => void;
  history: React.ReactNode;
  profile?: AccountProfile;
  avatarVersion: number;
  onNavigate: () => void;
  onSearch: () => void;
  onSettings: () => void;
  onFeedback: () => void;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const profileMenu = useRef<HTMLDetailsElement>(null);
  const profileLabel = profile?.display_name || profile?.email || "Аккаунт";
  const profileInitial = profile
    ? (Array.from(profile.display_name || profile.email)[0] ?? "?").toLocaleUpperCase("ru")
    : undefined;

  useEffect(() => {
    function handleOutside(event: PointerEvent) {
      const details = profileMenu.current;
      if (details?.open && event.target instanceof Node && !details.contains(event.target)) {
        details.open = false;
      }
    }
    function handleEscape(event: KeyboardEvent) {
      const details = profileMenu.current;
      if (event.key === "Escape" && details?.open) {
        details.open = false;
        details.querySelector("summary")?.focus();
      }
    }
    document.addEventListener("pointerdown", handleOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("pointerdown", handleOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, []);

  function closeProfileMenu() {
    if (profileMenu.current) profileMenu.current.open = false;
  }

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
        <button className="workspace-icon workspace-search" type="button" aria-label="Поиск по истории" title="Поиск по истории"
          aria-haspopup="dialog" onClick={onSearch}><Icon name="search" /></button>
        <button className="workspace-icon" aria-label="Скрыть боковую панель" onClick={close}><Icon name="panel" /></button>
      </div>
      <Link className="button workspace-new" href="/chat" prefetch={false} onNavigate={onNavigate}><Icon name="plus" />Новый чат</Link>
      <p className="workspace-history">Ваши чаты</p>
      {history}
      <details className="workspace-profile" ref={profileMenu}>
        <summary>
          <span className="workspace-avatar">
            {profile?.has_avatar
              // eslint-disable-next-line @next/next/no-img-element
              ? <img src={avatarVersion ? `${avatarUrl}?v=${avatarVersion}` : avatarUrl} alt="" />
              : profileInitial ? <span aria-hidden="true">{profileInitial}</span> : <Icon name="account" />}
          </span>
          <span className="workspace-profile-name" title={profileLabel}>{profileLabel}</span>
        </summary>
        <div className="workspace-profile-actions">
          <button type="button" className="button button-text" aria-haspopup="dialog" onClick={() => { closeProfileMenu(); onSettings(); }}>Настройки</button>
          <Link href="/help" className="button button-text" onNavigate={() => { closeProfileMenu(); onNavigate(); }}>Помощь</Link>
          <button type="button" className="button button-text" aria-haspopup="dialog" onClick={() => { closeProfileMenu(); onFeedback(); }}>Обратная связь</button>
          <button type="button" className="button button-text" disabled={pending} onClick={logout}>{pending ? "Выходим…" : "Выйти"}</button>
          {error && <p className="workspace-error" role="alert">{error}</p>}
        </div>
      </details>
    </>
  );
}

function mergeDialogues(current: Dialogue[], loaded: Dialogue[], edits: ReadonlyMap<string, string | null>) {
  const byId = new Map(current.map((dialogue) => [dialogue.id, dialogue]));
  for (const dialogue of loaded) {
    const title = edits.get(dialogue.id);
    if (title === null) continue;
    byId.set(dialogue.id, { ...dialogue, title: title ?? dialogue.title ?? byId.get(dialogue.id)?.title });
  }
  return [...byId.values()].sort((a, b) => b.id.length - a.id.length || b.id.localeCompare(a.id));
}

export function Workspace({ chat, children }: {
  chat?: { id?: string; company?: string };
  children?: React.ReactNode;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(defaultSidebarWidth);
  const [resizing, setResizing] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const drawer = useRef<HTMLDialogElement>(null);
  const desktopToggle = useRef<HTMLButtonElement>(null);
  const resizingRef = useRef(false);
  const lastValidWidthRef = useRef(defaultSidebarWidth);
  const [dialogues, setDialogues] = useState<Dialogue[]>([]);
  const [next, setNext] = useState<string>();
  const [listError, setListError] = useState(false);
  const [listing, setListing] = useState(true);
  const [createdId, setCreatedId] = useState<string>();
  const [historyRevision, setHistoryRevision] = useState(0);
  const [profile, setProfile] = useState<AccountProfile>();
  const [avatarVersion, setAvatarVersion] = useState(0);
  const [searchOpen, setSearchOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [feedbackOpen, setFeedbackOpen] = useState(false);
  const [action, setAction] = useState<HistoryAction>();
  const listRequest = useRef<AbortController>(null);
  const titleEdits = useRef(new Map<string, string | null>());

  useEffect(() => {
    try {
      const saved = Number(localStorage.getItem(sidebarWidthKey));
      if (Number.isFinite(saved) && saved >= minSidebarWidth) {
        const clamped = clampSidebarWidth(saved);
        lastValidWidthRef.current = clamped;
        queueMicrotask(() => setSidebarWidth(clamped));
      }
    } catch { /* Storage may be disabled. */ }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void readProfile({ signal: controller.signal }).then((loaded) => {
      if (!controller.signal.aborted) setProfile(loaded);
    }).catch(() => { /* Sidebar falls back to generic account label if profile fails. */ });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    listRequest.current = controller;
    void readChat(chatApi.GET("/v1/chat/dialogues", { signal: controller.signal })).then((data) => {
      if (controller.signal.aborted) return;
      const edits = new Map(titleEdits.current);
      setDialogues((current) => mergeDialogues(current, data.dialogues, edits));
      setNext(data.next_before_id);
    }).catch(() => {
      if (!controller.signal.aborted) setListError(true);
    }).finally(() => {
      if (listRequest.current !== controller) return;
      listRequest.current = null;
      setListing(false);
    });
    return () => {
      listRequest.current?.abort();
      listRequest.current = null;
    };
  }, []);

  async function loadDialogues() {
    if (listRequest.current) return;
    const controller = new AbortController();
    listRequest.current = controller;
    setListing(true);
    setListError(false);
    try {
      const data = await readChat(chatApi.GET("/v1/chat/dialogues", { params: { query: { before_id: next } }, signal: controller.signal }));
      if (controller.signal.aborted) return;
      const edits = new Map(titleEdits.current);
      setDialogues((current) => mergeDialogues(current, data.dialogues, edits));
      setNext(data.next_before_id);
    } catch {
      if (!controller.signal.aborted) setListError(true);
    } finally {
      if (listRequest.current === controller) {
        listRequest.current = null;
        setListing(false);
      }
    }
  }

  function addDialogue(dialogue: Dialogue) {
    const edits = new Map(titleEdits.current);
    const title = edits.get(dialogue.id);
    if (title === null) return;
    setCreatedId(dialogue.id);
    setDialogues((current) => mergeDialogues(current, [dialogue], edits));
    setHistoryRevision((revision) => revision + 1);
  }

  const currentId = chat?.id ?? createdId;
  const closeDrawer = () => drawer.current?.close();

  function renameDialogue(dialogue: Dialogue) {
    if (dialogue.title) titleEdits.current.set(dialogue.id, dialogue.title);
    setDialogues((current) => current.map((item) => item.id === dialogue.id ? dialogue : item));
    setHistoryRevision((revision) => revision + 1);
  }

  function deleteDialogue(id: string) {
    titleEdits.current.set(id, null);
    setDialogues((current) => current.filter((item) => item.id !== id));
    setHistoryRevision((revision) => revision + 1);
    if (id === currentId) {
      window.location.replace("/chat");
      return;
    }
    window.requestAnimationFrame(() => {
      const sidebar = drawer.current?.open ? drawer.current : root.current?.querySelector("aside");
      sidebar?.querySelector<HTMLButtonElement>(".workspace-search")?.focus();
    });
  }

  function clearHistory() {
    if (currentId) {
      window.location.replace("/chat");
      return;
    }
    listRequest.current?.abort();
    listRequest.current = null;
    titleEdits.current.clear();
    setDialogues([]);
    setNext(undefined);
    setListing(false);
    setListError(false);
    setHistoryRevision((revision) => revision + 1);
  }

  const history = <div className="workspace-dialogues" onScroll={(event) => {
    event.currentTarget.querySelector<HTMLElement>(".history-menu:popover-open")?.hidePopover();
  }}>
    <ul>{dialogues.map((dialogue) => <DialogueRow key={dialogue.id} dialogue={dialogue} active={currentId === dialogue.id}
      onNavigate={closeDrawer} onAction={setAction} />)}</ul>
    {listing && !dialogues.length && <p className="workspace-history" role="status">Загружаем чаты…</p>}
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

  useEffect(() => {
    const mobile = window.matchMedia("(max-width: 47.9375rem)");
    const syncDrawer = () => { if (!mobile.matches) drawer.current?.close(); };
    mobile.addEventListener("change", syncDrawer);
    return () => mobile.removeEventListener("change", syncDrawer);
  }, []);

  function saveSidebarWidth(width: number) {
    try { localStorage.setItem(sidebarWidthKey, String(width)); } catch { /* Storage may be disabled. */ }
  }

  function startResize(event: ReactPointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    lastValidWidthRef.current = sidebarWidth;
    resizingRef.current = true;
    setResizing(true);
  }

  function moveResize(event: ReactPointerEvent<HTMLDivElement>) {
    if (!resizingRef.current) return;
    const left = root.current?.getBoundingClientRect().left ?? 0;
    const rawWidth = event.clientX - left;
    if (rawWidth < collapseSidebarThreshold) {
      resizingRef.current = false;
      setResizing(false);
      if (event.currentTarget.hasPointerCapture(event.pointerId)) {
        event.currentTarget.releasePointerCapture(event.pointerId);
      }
      setSidebarWidth(lastValidWidthRef.current);
      setCollapsed(true);
      window.requestAnimationFrame(() => desktopToggle.current?.focus());
      return;
    }
    const nextWidth = clampSidebarWidth(rawWidth);
    lastValidWidthRef.current = nextWidth;
    setSidebarWidth(nextWidth);
  }

  function finishResize(event: ReactPointerEvent<HTMLDivElement>) {
    if (!resizingRef.current) return;
    resizingRef.current = false;
    setResizing(false);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    saveSidebarWidth(lastValidWidthRef.current);
  }

  function resizeKey(event: ReactKeyboardEvent<HTMLDivElement>) {
    let nextWidth: number | undefined;
    if (event.key === "ArrowLeft") nextWidth = clampSidebarWidth(sidebarWidth - 16);
    else if (event.key === "ArrowRight") nextWidth = clampSidebarWidth(sidebarWidth + 16);
    else if (event.key === "Home") nextWidth = minSidebarWidth;
    else if (event.key === "End") nextWidth = maxSidebarWidth;
    if (nextWidth === undefined) return;
    event.preventDefault();
    lastValidWidthRef.current = nextWidth;
    setSidebarWidth(nextWidth);
    saveSidebarWidth(nextWidth);
  }

  const sidebar = {
    history, profile, avatarVersion, onNavigate: closeDrawer, onSearch: () => setSearchOpen(true),
    onSettings: () => setSettingsOpen(true), onFeedback: () => setFeedbackOpen(true),
  };

  return (
    <div ref={root} className={`workspace${collapsed ? " workspace-collapsed" : ""}`}
      data-resizing={resizing || undefined}
      style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}>
      {!collapsed && <aside className="workspace-sidebar" aria-label="Боковая панель">
        <Sidebar {...sidebar} close={() => {
          setCollapsed(true);
          window.requestAnimationFrame(() => desktopToggle.current?.focus());
        }} />
        <div className="workspace-resizer" role="separator" aria-orientation="vertical"
          aria-label="Изменить ширину боковой панели" aria-valuemin={minSidebarWidth}
          aria-valuemax={maxSidebarWidth} aria-valuenow={sidebarWidth} tabIndex={0}
          onPointerDown={startResize} onPointerMove={moveResize} onPointerUp={finishResize}
          onPointerCancel={finishResize} onKeyDown={resizeKey}
          onDoubleClick={() => {
            lastValidWidthRef.current = defaultSidebarWidth;
            setSidebarWidth(defaultSidebarWidth);
            saveSidebarWidth(defaultSidebarWidth);
          }} />
      </aside>}
      <dialog ref={drawer} className="workspace-drawer" aria-label="Боковая панель">
        <div className="workspace-sidebar"><Sidebar {...sidebar} close={closeDrawer} /></div>
      </dialog>
      <main className={children ? "workspace-main workspace-main-data" : "workspace-main"}>
        <button ref={desktopToggle} className="workspace-icon workspace-open workspace-open-desktop" aria-label="Показать боковую панель" onClick={() => {
          setCollapsed(false);
          window.requestAnimationFrame(() => root.current?.querySelector<HTMLButtonElement>("aside .workspace-search")?.focus());
        }}><Icon name="panel" /></button>
        <button className="workspace-icon workspace-open workspace-open-mobile" aria-label="Показать боковую панель" onClick={() => drawer.current?.showModal()}><Icon name="panel" /></button>
        {chat ? <Chat initialId={chat.id} company={chat.company} onDialogue={addDialogue} /> : children}
      </main>
      <HistorySearchDialog open={searchOpen} revision={historyRevision} onClose={() => setSearchOpen(false)} onNavigate={() => {
        setSearchOpen(false);
        closeDrawer();
      }} />
      {action && <HistoryActionDialog action={action} onClose={() => setAction(undefined)} onRename={renameDialogue} onDelete={deleteDialogue} />}
      <FeedbackDialog open={feedbackOpen} onClose={() => setFeedbackOpen(false)} />
      <AccountSettingsDialog open={settingsOpen} onClose={() => setSettingsOpen(false)} onHistoryCleared={clearHistory}
        onProfileUpdate={(nextProfile) => {
          setProfile(nextProfile);
          setAvatarVersion(Date.now());
        }} />
    </div>
  );
}
