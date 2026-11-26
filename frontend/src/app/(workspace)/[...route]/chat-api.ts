import createClient from "openapi-fetch";
import type { Readable } from "openapi-typescript-helpers";
import type { components, paths } from "@/lib/api";

type ChatSchemas = components["schemas"];
export type Dialogue = Readable<ChatSchemas["ChatDialogue"]>;
export type Turn = Readable<ChatSchemas["ChatTurn"]>;
export type ChatHistory = Readable<ChatSchemas["ChatHistory"]>;

export const chatApi = createClient<paths>({
  baseUrl: "/api",
  cache: "no-store",
  redirect: "error",
});

export class ChatError extends Error {
  constructor(readonly code: string, readonly status: number, readonly requestId?: string) {
    super(code);
  }
}

export async function readChat<T>(request: Promise<{
  response: Response;
  data?: T;
  error?: { error?: { code?: string; request_id?: string } };
}>) {
  const result = await request;
  if (result.response.status === 401) window.location.replace("/login?expired=1");
  if (!result.data) {
    throw new ChatError(result.error?.error?.code ?? "unavailable", result.response.status, result.error?.error?.request_id);
  }
  return result.data;
}

export function readChatPage(id: string, before_id?: string, signal?: AbortSignal) {
  return readChat(chatApi.GET("/v1/chat/dialogues/{id}/messages", {
    params: { path: { id }, query: { before_id } }, signal,
  }));
}

