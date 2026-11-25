import { cookies } from "next/headers";
import { backend, sessionCookie } from "./backend";

export async function workspaceSession() {
  const token = (await cookies()).get(sessionCookie)?.value;
  if (!token) return "missing";

  try {
    const { response } = await backend(token).GET("/v1/chat/dialogues", {
      params: { query: { limit: 1 } },
    });
    if (response.status === 401) return "expired";
    return response.ok ? "active" : "unavailable";
  } catch {
    return "unavailable";
  }
}
