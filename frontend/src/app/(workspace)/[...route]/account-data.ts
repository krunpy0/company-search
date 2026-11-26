import type { components } from "@/lib/api";
import { chatApi, readChat, readChatPage, type Dialogue, type Turn } from "./chat-api";
import { collectChatHistory } from "./chat-history";

export type AccountProfile = components["schemas"]["AccountProfile"];
export const avatarUrl = "/api/v1/account/avatar";
export const maxAvatarBytes = 2 * 1024 * 1024;

const errors = new Map<string, string>(Object.entries({
  incorrect_password: "Текущий пароль введён неверно.",
  email_in_use: "Этот email уже привязан к другому аккаунту.",
  invalid_profile: "Проверьте имя или адрес электронной почты.",
  invalid_password: "Пароль должен содержать от 12 до 72 байт.",
  invalid_avatar: "Выберите JPG или PNG с допустимыми размерами.",
  avatar_too_large: "Размер файла должен быть не больше 2 МБ.",
  busy: "Сервис занят. Попробуйте ещё раз.",
  invalid_session: "Сессия истекла. Войдите снова.",
}));

function hasCode(value: unknown): value is { error: { code: string } } {
  return typeof value === "object" && value !== null && "error" in value &&
    typeof value.error === "object" && value.error !== null && "code" in value.error &&
    typeof value.error.code === "string";
}

function isProfile(value: unknown): value is AccountProfile {
  return typeof value === "object" && value !== null &&
    "user_id" in value && typeof value.user_id === "string" && /^[1-9]\d*$/.test(value.user_id) &&
    "email" in value && typeof value.email === "string" &&
    "display_name" in value && typeof value.display_name === "string" &&
    "has_avatar" in value && typeof value.has_avatar === "boolean";
}

export function jsonBody(method: "PATCH" | "POST" | "DELETE", body: Record<string, string>): RequestInit {
  return { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

export async function accountRequest(url: string, status: 200 | 204, fallback: string, init?: RequestInit) {
  let response: Response;
  try {
    response = await fetch(url, { ...init, cache: "no-store", redirect: "error" });
  } catch {
    throw new Error(fallback);
  }
  if (response.status === status) return response;
  if (response.status === 401 && !init?.signal?.aborted) window.location.replace("/login?expired=1");
  const error: unknown = await response.json().catch(() => null);
  const code = response.status === 401 ? "invalid_session" : hasCode(error) ? error.error.code : "";
  throw new Error(errors.get(code) ?? fallback);
}

export async function readProfile(init?: RequestInit, url = "/api/v1/account/profile") {
  const fallback = init?.method ? "Не удалось сохранить профиль. Попробуйте ещё раз." :
    "Не удалось загрузить профиль. Попробуйте ещё раз.";
  const response = await accountRequest(url, 200, fallback, init);
  const value: unknown = await response.json().catch(() => null);
  if (!isProfile(value)) throw new Error(fallback);
  return value;
}

function avatarData(blob: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === "string") resolve(reader.result);
      else reject(new Error("invalid_avatar_data"));
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

type ExportedDialogue = { dialogue: Dialogue; messages: Turn[] };

export async function collectAccountData(signal: AbortSignal) {
  const profile = await readProfile({ signal });
  let avatar: string | null = null;
  if (profile.has_avatar) {
    const response = await accountRequest(avatarUrl, 200, "Не удалось загрузить аватар для выгрузки.", { signal });
    const blob = await response.blob();
    if (blob.type !== "image/png" || blob.size === 0) throw new Error("invalid_avatar_data");
    avatar = await avatarData(blob);
  }

  const dialogues: ExportedDialogue[] = [];
  let before_id: string | undefined;
  let lastId: string | undefined;
  do {
    const result = await readChat(chatApi.GET("/v1/chat/dialogues", { params: { query: { before_id } }, signal }));
    if (before_id && !result.dialogues.length) throw new Error("incomplete_dialogue_list");
    for (const { id } of result.dialogues) {
      if (!/^[1-9]\d*$/.test(id) || (lastId && (lastId.length < id.length ||
        (lastId.length === id.length && lastId <= id)))) throw new Error("invalid_dialogue_order");
      lastId = id;
      const { dialogue, messages } = await collectChatHistory((before) => readChatPage(id, before, signal));
      if (dialogue.id !== id) throw new Error("invalid_dialogue_id");
      dialogues.push({ dialogue, messages });
    }
    before_id = result.next_before_id;
    if (before_id && before_id !== result.dialogues.at(-1)?.id) throw new Error("invalid_dialogue_cursor");
  } while (before_id);

  signal.throwIfAborted();
  return { exported_at: new Date().toISOString(), profile, avatar, dialogues };
}

export function downloadAccountData(data: Awaited<ReturnType<typeof collectAccountData>>) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `account-data-${data.exported_at.slice(0, 10)}.json`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
