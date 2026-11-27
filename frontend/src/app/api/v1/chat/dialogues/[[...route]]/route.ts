import { NextRequest } from "next/server";
import { appOrigin } from "@/lib/app-origin";
import { backend, sessionCookie } from "@/lib/backend";
import { isChatId, isChatMessageId, isChatRegistration } from "@/lib/chat-contract";
import { readJSON } from "@/lib/request-body";

const headers = { "Cache-Control": "private, no-store" };
const fail = (status: number, code: string) => Response.json({ error: { code } }, { status, headers });

function upstream(result: { response: Response; data?: unknown; error?: unknown }) {
  if (result.response.status === 204) return new Response(null, { status: 204, headers });
  return Response.json(result.data ?? result.error ?? { error: { code: "unavailable" } }, {
    status: result.response.status,
    headers,
  });
}

function target(route?: string[]) {
  if (!route?.length) return { kind: "dialogues" } as const;
  const [id, section] = route;
  if (route.length === 2 && section === "messages" && isChatId(id)) return { kind: "messages", id } as const;
}

type Context = RouteContext<"/api/v1/chat/dialogues/[[...route]]">;

export async function GET(request: NextRequest, context: Context) {
  const token = request.cookies.get(sessionCookie)?.value;
  if (!token) return fail(401, "invalid_session");
  const segments = (await context.params).route;
  const search = segments?.length === 1 && segments[0] === "search";
  const route = target(segments);
  if (!route && !search) return fail(404, "not_found");
  const rawBefore = request.nextUrl.searchParams.get("before_id");
  if (rawBefore !== null && !isChatId(rawBefore)) return fail(400, "invalid_input");
  const query = { limit: 20, before_id: rawBefore ?? undefined };

  try {
    const api = backend(token);
    if (search) {
      const q = request.nextUrl.searchParams.get("q")?.trim();
      if (!q) return fail(400, "invalid_search");
      return upstream(await api.GET("/v1/chat/dialogues/search", { params: { query: { ...query, q } } }));
    }
    if (!route) return fail(404, "not_found");
    return upstream(route.kind === "dialogues"
      ? await api.GET("/v1/chat/dialogues", { params: { query } })
      : await api.GET("/v1/chat/dialogues/{id}/messages", { params: { path: { id: route.id }, query } }));
  } catch {
    return fail(503, "unavailable");
  }
}

export async function PATCH(request: NextRequest, context: Context) {
  const origin = appOrigin(request);
  if (!origin || request.headers.get("origin") !== origin) return fail(403, "invalid_origin");
  const token = request.cookies.get(sessionCookie)?.value;
  if (!token) return fail(401, "invalid_session");
  const segments = (await context.params).route;
  if (segments?.length !== 1 || !isChatId(segments[0])) return fail(404, "not_found");
  try {
    const body = await readJSON(request, 32 * 1024);
    if (body === undefined) return fail(413, "payload_too_large");
    if (!body || typeof body !== "object" || !("title" in body) || typeof body.title !== "string") {
      return fail(400, "invalid_title");
    }
    return upstream(await backend(token).PATCH("/v1/chat/dialogues/{id}", {
      params: { path: { id: segments[0] } }, body: { title: body.title },
    }));
  } catch {
    return fail(503, "unavailable");
  }
}

export async function DELETE(request: NextRequest, context: Context) {
  const origin = appOrigin(request);
  if (!origin || request.headers.get("origin") !== origin) return fail(403, "invalid_origin");
  const token = request.cookies.get(sessionCookie)?.value;
  if (!token) return fail(401, "invalid_session");
  const segments = (await context.params).route;
  const api = backend(token);
  try {
    if (!segments?.length) return upstream(await api.DELETE("/v1/chat/dialogues"));
    if (segments.length !== 1 || !isChatId(segments[0])) return fail(404, "not_found");
    return upstream(await api.DELETE("/v1/chat/dialogues/{id}", { params: { path: { id: segments[0] } } }));
  } catch {
    return fail(503, "unavailable");
  }
}

export async function POST(request: NextRequest, context: Context) {
  const origin = appOrigin(request);
  if (!origin || request.headers.get("origin") !== origin) {
    return fail(403, "invalid_origin");
  }
  const token = request.cookies.get(sessionCookie)?.value;
  if (!token) return fail(401, "invalid_session");
  const route = target((await context.params).route);
  if (!route) return fail(404, "not_found");

  try {
    const body = await readJSON(request, 32 * 1024);
    if (body === undefined) return fail(413, "payload_too_large");
    if (!body || typeof body !== "object" || Array.isArray(body)) return fail(400, "invalid_input");
    if (route.kind === "dialogues") {
      const registration = "registration" in body ? body.registration : undefined;
      if (registration !== undefined && (typeof registration !== "string" || !isChatRegistration(registration))) {
        return fail(400, "invalid_registration");
      }
      return upstream(await backend(token).POST("/v1/chat/dialogues", { body: { registration } }));
    }

    const content = "content" in body && typeof body.content === "string" ? body.content.trim() : "";
    if (!content || content.length > 4000 || [...content].length > 2000 || content.includes("\0")) {
      return fail(400, "invalid_message");
    }
    const clientMessageId = "client_message_id" in body ? body.client_message_id : undefined;
    if (clientMessageId !== undefined && !isChatMessageId(clientMessageId)) {
      return fail(400, "invalid_message_id");
    }
    return upstream(await backend(token, 130_000).POST("/v1/chat/dialogues/{id}/messages", {
      params: { path: { id: route.id } }, body: { content, client_message_id: clientMessageId },
    }));
  } catch {
    return fail(503, "unavailable");
  }
}

