import { useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react";
import { questionDraftKey } from "@/app/product";
import { WorkspaceDialog } from "@/app/workspace-dialog";
import {
  accountRequest, avatarUrl, collectAccountData, downloadAccountData, jsonBody, maxAvatarBytes, readProfile,
  type AccountProfile,
} from "./account-data";
import "./account-settings.css";

export type { AccountProfile } from "./account-data";

type AccountSettingsProps = {
  open: boolean;
  onClose: () => void;
  onHistoryCleared: () => void;
  onProfileUpdate?: (profile: AccountProfile) => void;
};
type Section = "account" | "data";
type Action = "name" | "email" | "password" | "history" | "account" | "avatar";
type ProfileState = { kind: "loading" } | { kind: "error"; message: string } | { kind: "ready"; profile: AccountProfile };
type Notice = { kind: "error" | "success"; text: string };
type Mutation = { kind: "upload" | Action } | { kind: "export"; controller: AbortController };

const labels: Record<Section, string> = { account: "Аккаунт", data: "Управление данными" };
const sections: Section[] = ["account", "data"];
const fieldLabels = {
  name: "Сохранить имя",
  email: "Сохранить email",
  password: "Изменить пароль",
  history: "Удалить историю",
  account: "Удалить аккаунт",
  avatar: "Удалить аватар",
};
const destructiveText = {
  history: "Все сохранённые диалоги и сообщения будут удалены без возможности восстановления.",
  account: "Профиль, аватар и история диалогов будут удалены. Все сеансы завершатся.",
  avatar: "Ваш текущий аватар будет удалён.",
};

function validPassword(password: string) {
  const bytes = new TextEncoder().encode(password).length;
  return bytes >= 12 && bytes <= 72;
}

function leaveDeletedAccount() {
  try { sessionStorage.removeItem(questionDraftKey); } catch { /* Storage may be disabled. */ }
  window.location.replace("/register");
}

function SettingRow({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return <div className="account-setting-row">
    <div>
      <h4>{title}</h4>
      {description && <p>{description}</p>}
    </div>
    <div className="account-setting-actions">{children}</div>
  </div>;
}

export function AccountSettingsDialog({ open, onClose, onHistoryCleared, onProfileUpdate }: AccountSettingsProps) {
  return <WorkspaceDialog open={open} onClose={onClose} title="Настройки" className="account-settings">
    {open && <AccountSettingsContent onHistoryCleared={onHistoryCleared} onProfileUpdate={onProfileUpdate} />}
  </WorkspaceDialog>;
}

function AccountSettingsContent({ onHistoryCleared, onProfileUpdate }: Pick<AccountSettingsProps, "onHistoryCleared" | "onProfileUpdate">) {
  const baseId = useId();
  const tabs = useRef<HTMLDivElement>(null);
  const uploader = useRef<HTMLInputElement>(null);
  const editor = useRef<HTMLFormElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const uploadButton = useRef<HTMLButtonElement>(null);
  const request = useRef<Mutation | null>(null);
  const [section, setSection] = useState<Section>("account");
  const [profileState, setProfileState] = useState<ProfileState>({ kind: "loading" });
  const [revision, setRevision] = useState(0);
  const [avatarVersion, setAvatarVersion] = useState(0);
  const [action, setAction] = useState<Action | null>(null);
  const [pending, setPending] = useState<Mutation["kind"] | null>(null);
  const [notice, setNotice] = useState<Notice>();
  const profile = profileState.kind === "ready" ? profileState.profile : undefined;

  useEffect(() => {
    const controller = new AbortController();
    void readProfile({ signal: controller.signal }).then((profile) => {
      if (!controller.signal.aborted) setProfileState({ kind: "ready", profile });
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setProfileState({
        kind: "error", message: error instanceof Error ? error.message : "Не удалось загрузить профиль.",
      });
    });
    return () => controller.abort();
  }, [revision]);

  useLayoutEffect(() => () => {
    if (request.current?.kind === "export") request.current.controller.abort();
  }, []);

  useEffect(() => {
    if (action) editor.current?.querySelector<HTMLElement>("[data-account-autofocus]")?.focus();
  }, [action]);

  function selectSection(next: Section) {
    if (next === section || request.current) return;
    setAction(null);
    setNotice(undefined);
    setSection(next);
  }

  function tabKey(event: KeyboardEvent<HTMLButtonElement>) {
    const target = event.key === "Home" ? "account" : event.key === "End" ? "data" :
      event.key === "ArrowLeft" || event.key === "ArrowRight" ? (section === "account" ? "data" : "account") : null;
    if (!target || request.current) return;
    event.preventDefault();
    selectSection(target);
    tabs.current?.querySelector<HTMLElement>(`[data-section="${target}"]`)?.focus();
  }

  function start(action: Action, button: HTMLButtonElement) {
    if (request.current) return;
    trigger.current = button;
    setNotice(undefined);
    setAction(action);
  }

  function finishAction(resetNotice: boolean) {
    if (action) requestAnimationFrame(() => {
      const previous = trigger.current;
      if (previous?.isConnected) previous.focus();
      else uploadButton.current?.focus();
    });
    setAction(null);
    if (resetNotice) setNotice(undefined);
  }

  function updateProfile(profile: AccountProfile) {
    setProfileState({ kind: "ready", profile });
    onProfileUpdate?.(profile);
  }

  function error(text: string) {
    setNotice({ kind: "error", text });
  }

  async function perform(kind: Mutation["kind"], change: (signal?: AbortSignal) => Promise<string>, fallback: string) {
    if (request.current) return;
    const current: Mutation = kind === "export" ? { kind, controller: new AbortController() } : { kind };
    const signal = current.kind === "export" ? current.controller.signal : undefined;
    request.current = current;
    setPending(kind);
    setNotice(undefined);
    try {
      const text = await change(signal);
      if (!signal?.aborted) {
        finishAction(false);
        setNotice({ kind: "success", text });
      }
    } catch (reason) {
      if (!signal?.aborted) {
        error(kind !== "export" && reason instanceof Error ? reason.message : fallback);
        requestAnimationFrame(() => {
          const form = editor.current;
          if (form) (form.querySelector<HTMLElement>("input") ??
            form.querySelector<HTMLElement>("[data-account-autofocus]"))?.focus();
        });
      }
    } finally {
      request.current = null;
      setPending(null);
    }
  }

  function upload(file?: File) {
    if (!file || request.current) return;
    setAction(null);
    if (file.type !== "image/jpeg" && file.type !== "image/png") {
      error("Выберите изображение в формате JPG или PNG.");
      return;
    }
    if (file.size > maxAvatarBytes) {
      error("Размер файла должен быть не больше 2 МБ.");
      return;
    }
    void perform("upload", async () => {
      updateProfile(await readProfile({ method: "PUT", headers: { "Content-Type": file.type }, body: file }, avatarUrl));
      setAvatarVersion(Date.now());
      return "Аватар обновлён.";
    }, "Не удалось загрузить аватар.");
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!action || request.current) return;
    const fields = new FormData(event.currentTarget);
    const currentPassword = String(fields.get("current_password") ?? "");
    if ((action === "email" || action === "password" || action === "account") && !validPassword(currentPassword)) {
      error("Текущий пароль должен содержать от 12 до 72 байт.");
      return;
    }

    const accountUrl = "/api/v1/account";
    const profileUrl = `${accountUrl}/profile`;
    switch (action) {
      case "name": {
        const display_name = String(fields.get("display_name") ?? "").trim();
        if (Array.from(display_name).length > 80 || /\p{Cc}/u.test(display_name)) {
          error("Имя может содержать до 80 символов без управляющих знаков.");
          return;
        }
        void perform(action, async () => {
          updateProfile(await readProfile(jsonBody("PATCH", { display_name })));
          return "Имя сохранено.";
        }, "Не удалось сохранить имя.");
        break;
      }
      case "email": {
        const email = String(fields.get("email") ?? "").trim();
        if (new TextEncoder().encode(email).length > 254) {
          error("Введите адрес электронной почты длиной до 254 байт.");
          return;
        }
        void perform(action, async () => {
          updateProfile(await readProfile(jsonBody("PATCH", { email, current_password: currentPassword }), profileUrl));
          return "Email изменён.";
        }, "Не удалось изменить email.");
        break;
      }
      case "password": {
        const newPassword = String(fields.get("new_password") ?? "");
        if (!validPassword(newPassword)) {
          error("Новый пароль должен содержать от 12 до 72 байт.");
          return;
        }
        if (newPassword !== fields.get("confirm_password")) {
          error("Новый пароль и его подтверждение различаются.");
          return;
        }
        void perform(action, async () => {
          await accountRequest(`${accountUrl}/password`, 204, "Не удалось изменить пароль.",
            jsonBody("POST", { current_password: currentPassword, new_password: newPassword }));
          return "Пароль изменён. Остальные сеансы завершены.";
        }, "Не удалось изменить пароль.");
        break;
      }
      case "history":
        void perform(action, async () => {
          await accountRequest("/api/v1/chat/dialogues", 204, "Не удалось удалить историю.", { method: "DELETE" });
          onHistoryCleared();
          return "История диалогов удалена.";
        }, "Не удалось удалить историю.");
        break;
      case "account":
        void perform(action, async () => {
          await accountRequest(accountUrl, 204, "Не удалось удалить аккаунт.",
            jsonBody("DELETE", { current_password: currentPassword }));
          leaveDeletedAccount();
          return "Аккаунт удалён.";
        }, "Не удалось удалить аккаунт.");
        break;
      case "avatar":
        void perform(action, async () => {
          await accountRequest(avatarUrl, 204, "Не удалось удалить аватар.", { method: "DELETE" });
          if (profile) updateProfile({ ...profile, has_avatar: false });
          return "Аватар удалён.";
        }, "Не удалось удалить аватар.");
        break;
    }
  }

  function exportData() {
    setAction(null);
    void perform("export", async (signal) => {
      if (!signal) throw new Error("missing_export_signal");
      const data = await collectAccountData(signal);
      signal.throwIfAborted();
      downloadAccountData(data);
      return "Файл с вашими данными скачивается.";
    }, "Не удалось выгрузить все данные. Повторите попытку.");
  }

  const status = <div className="account-status">
    {pending && <p role="status">{pending === "export" ? "Собираем данные…" : "Выполняем запрос…"}</p>}
    {notice && <p data-kind={notice.kind} role={notice.kind === "error" ? "alert" : "status"}>{notice.text}</p>}
  </div>;
  const confirmation = action === "history" || action === "account" || action === "avatar";
  const form = action && <form className="account-editor" ref={editor} onSubmit={submit} key={action}>
    <h4>{confirmation ? `${fieldLabels[action]}?` : fieldLabels[action]}</h4>
    {confirmation && <p>{destructiveText[action]}</p>}
    <fieldset disabled={pending !== null}>
      {action === "name" && <label>Имя
        <input name="display_name" autoComplete="name" defaultValue={profile?.display_name} data-account-autofocus />
      </label>}
      {action === "email" && <label>Новый email
        <input name="email" type="email" autoComplete="email" required data-account-autofocus />
      </label>}
      {(action === "email" || action === "password" || action === "account") && <label>Текущий пароль
        <input name="current_password" type="password" autoComplete="current-password" required
          data-account-autofocus={action === "password" ? "" : undefined} />
      </label>}
      {action === "password" && <>
        <label>Новый пароль
          <input name="new_password" type="password" autoComplete="new-password" required
            aria-describedby={`${baseId}-password-hint`} />
        </label>
        <p id={`${baseId}-password-hint`}>От 12 до 72 байт. Один кириллический символ занимает два байта.</p>
        <label>Повторите новый пароль
          <input name="confirm_password" type="password" autoComplete="new-password" required />
        </label>
      </>}
      {status}
      <div className="account-editor-actions">
        <button type="button" className="button button-outline" onClick={() => finishAction(true)}
          data-account-autofocus={confirmation ? "" : undefined}>Отмена</button>
        <button type="submit" className="button button-primary">{fieldLabels[action]}</button>
      </div>
    </fieldset>
  </form>;

  return <>
    <div className="account-tabs" ref={tabs} role="tablist" aria-label="Разделы настроек">
      {sections.map((item) => <button key={item} type="button" role="tab" data-section={item}
        id={`${baseId}-tab-${item}`} aria-controls={`${baseId}-panel-${item}`} aria-selected={item === section}
        aria-disabled={pending !== null || undefined} tabIndex={item === section ? 0 : -1}
        data-dialog-autofocus={item === "account" ? "" : undefined} onKeyDown={tabKey}
        onClick={() => selectSection(item)}>{labels[item]}</button>)}
    </div>
    <div role="tabpanel" tabIndex={0} id={`${baseId}-panel-account`} aria-labelledby={`${baseId}-tab-account`}
      className="account-panel" hidden={section !== "account"}>
      {section === "account" && <>
        <h3>Профиль</h3>
        {profileState.kind === "loading" && <p role="status" className="account-muted">Загружаем профиль…</p>}
        {profileState.kind === "error" && <div className="account-load-error">
          <p role="alert">{profileState.message}</p>
          <button type="button" className="button button-outline" onClick={() => {
            setProfileState({ kind: "loading" });
            setRevision((value) => value + 1);
          }}>Повторить</button>
        </div>}
        {profile && <>
          <div className="account-avatar-row">
            <div className="account-avatar">
              {profile.has_avatar ?
                /* Avatar is private and fetched directly with the session cookie. */
                // eslint-disable-next-line @next/next/no-img-element
                <img src={avatarVersion ? `${avatarUrl}?v=${avatarVersion}` : avatarUrl} alt="" /> :
                <span aria-hidden="true">{(Array.from(profile.display_name || profile.email)[0] ?? "?").toLocaleUpperCase("ru")}</span>}
            </div>
            <SettingRow title="Аватар" description="JPG или PNG до 2 МБ.">
              <button ref={uploadButton} type="button" className="button button-outline" disabled={pending !== null}
                onClick={() => uploader.current?.click()}>{profile.has_avatar ? "Заменить" : "Загрузить"}</button>
              {profile.has_avatar && <button type="button" className="button button-text" disabled={pending !== null}
                onClick={(event) => start("avatar", event.currentTarget)}>Удалить аватар</button>}
            </SettingRow>
            <input type="file" hidden ref={uploader} accept="image/jpeg,image/png" onChange={(event) => {
              const file = event.currentTarget.files?.[0];
              event.currentTarget.value = "";
              upload(file);
            }} />
          </div>
          {action === "avatar" && form}
          <SettingRow title="Имя" description={profile.display_name || "Имя не указано"}>
            <button type="button" className="button button-outline" disabled={pending !== null}
              onClick={(event) => start("name", event.currentTarget)}>Изменить имя</button>
          </SettingRow>
          {action === "name" && form}
          <SettingRow title="Электронная почта" description={profile.email}>
            <button type="button" className="button button-outline" disabled={pending !== null}
              onClick={(event) => start("email", event.currentTarget)}>Изменить email</button>
          </SettingRow>
          {action === "email" && form}
          <SettingRow title="Пароль" description="После смены пароля нужно будет заново войти на других устройствах.">
            <button type="button" className="button button-outline" disabled={pending !== null}
              onClick={(event) => start("password", event.currentTarget)}>Изменить пароль</button>
          </SettingRow>
          {action === "password" && form}
          {!action && status}
        </>}
      </>}
    </div>
    <div role="tabpanel" tabIndex={0} id={`${baseId}-panel-data`} aria-labelledby={`${baseId}-tab-data`}
      className="account-panel" hidden={section !== "data"}>
      {section === "data" && <>
        <h3>Экспорт</h3>
        <SettingRow title="Скачать данные" description="Профиль, аватар и вся история диалогов в одном файле JSON. Отдельный диалог можно скачать из его меню.">
          <button type="button" className="button button-outline" disabled={pending !== null}
            onClick={exportData}>Скачать данные</button>
        </SettingRow>
        <h3>Удаление</h3>
        <SettingRow title="История диалогов" description="Удалить все диалоги и сообщения из аккаунта.">
          <button type="button" className="button account-danger" disabled={pending !== null}
            onClick={(event) => start("history", event.currentTarget)}>Удалить историю</button>
        </SettingRow>
        {action === "history" && form}
        <SettingRow title="Аккаунт" description="Удалить профиль, аватар и историю диалогов.">
          <button type="button" className="button account-danger" disabled={pending !== null}
            onClick={(event) => start("account", event.currentTarget)}>Удалить аккаунт</button>
        </SettingRow>
        {action === "account" && form}
        {!action && status}
      </>}
    </div>
  </>;
}
