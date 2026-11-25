import createClient from "openapi-fetch";
import type { Readable } from "openapi-typescript-helpers";
import type { components, paths } from "@/lib/api";

type ChatSchemas = components["schemas"];
export type Dialogue = Readable<ChatSchemas["ChatDialogue"]>;
export type Turn = Readable<ChatSchemas["ChatTurn"]>;

export const chatApi = createClient<paths>({
  baseUrl: "/api",
  cache: "no-store",
  redirect: "error",
});

export async function readChat<T>(request: Promise<{ response: Response; data?: T; error?: { error: { code: string } } }>) {
  const result = await request;
  if (result.response.status === 401) window.location.replace("/login?expired=1");
  if (!result.data) throw new Error(result.error?.error.code ?? "unavailable");
  return result.data;
}

