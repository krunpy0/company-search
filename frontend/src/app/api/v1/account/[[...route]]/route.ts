import { NextRequest, NextResponse } from "next/server";
import { appOrigin } from "@/lib/app-origin";
import { backendOrigin, sessionCookie } from "@/lib/backend";

const cacheHeaders = { "Cache-Control": "private, no-store" };
const fail = (status: number, code: string) => Response.json({ error: { code } }, { status, headers: cacheHeaders });

type Context = RouteContext<"/api/v1/account/[[...route]]">;

async function readBody(stream: ReadableStream<Uint8Array>, limit: number) {
  const chunks: Uint8Array[] = [];
  let size = 0;
  const reader = stream.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return Buffer.concat(chunks, size);
      size += value.length;
      if (size > limit) {
        await reader.cancel();
        return;
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
}

async function forward(request: NextRequest, context: Context) {
  const segments = (await context.params).route ?? [];
  const section = segments[0];
  const method = request.method;
  const allowed = segments.length === 0 ? method === "DELETE" : segments.length === 1 && (
    (section === "profile" && (method === "GET" || method === "PATCH")) ||
    (section === "avatar" && (method === "GET" || method === "PUT" || method === "DELETE")) ||
    (section === "password" && method === "POST")
  );
  if (!allowed) return fail(404, "not_found");
  const origin = appOrigin(request);
  if (method !== "GET" && (!origin || request.headers.get("origin") !== origin)) return fail(403, "invalid_origin");
  const token = request.cookies.get(sessionCookie)?.value;
  if (!token) return fail(401, "invalid_session");

  const headers = new Headers({ Authorization: `Bearer ${token}` });
  const upload = method === "PUT";
  try {
    let body: Buffer<ArrayBuffer> | undefined;
    if (method === "PATCH" || method === "POST" || upload || !section) {
      if (!request.body) return fail(400, upload ? "invalid_avatar" : "invalid_json");
      body = await readBody(request.body, upload ? 2 * 1024 * 1024 : 4096);
      if (!body) return fail(413, upload ? "avatar_too_large" : "payload_too_large");
      headers.set("Content-Type", upload ? request.headers.get("Content-Type") ?? "" : "application/json");
    }
    const response = await fetch(`${backendOrigin().replace(/\/$/, "")}/v1/account${section ? `/${section}` : ""}`, {
      method, headers, body, cache: "no-store", redirect: "error", signal: AbortSignal.timeout(8000),
    });
    const responseHeaders = new Headers(cacheHeaders);
    for (const name of ["Content-Type", "Retry-After", "X-Content-Type-Options"]) {
      const value = response.headers.get(name);
      if (value) responseHeaders.set(name, value);
    }
    const result = new NextResponse(response.body, { status: response.status, headers: responseHeaders });
    if (!section && response.status === 204) {
      result.cookies.set(sessionCookie, "", {
        path: "/", httpOnly: true, sameSite: "lax", secure: origin?.startsWith("https://"), maxAge: 0,
      });
    }
    return result;
  } catch {
    return fail(503, "unavailable");
  }
}

export { forward as GET, forward as PATCH, forward as POST, forward as PUT, forward as DELETE };
