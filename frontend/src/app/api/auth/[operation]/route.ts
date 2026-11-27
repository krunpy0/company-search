import { NextRequest, NextResponse } from "next/server";
import { appOrigin } from "@/lib/app-origin";
import { passwordFits } from "@/lib/auth-feedback";
import { backend, sessionCookie } from "@/lib/backend";
import type { components } from "@/lib/api";
import { readJSON } from "@/lib/request-body";
import { workspaceSession } from "@/lib/workspace-session";

const noStore = { "Cache-Control": "no-store" };

function failure(status: number, code: string, retryAfter?: string | null) {
  const headers = new Headers(noStore);
  if (retryAfter) headers.set("Retry-After", retryAfter);
  return NextResponse.json({ code }, { status, headers });
}

function credentials(value: unknown): components["schemas"]["Credentials"] | undefined {
  const email = emailInput(value);
  if (!email || !value || typeof value !== "object" || !("password" in value)) return;
  if (typeof value.password !== "string" || !passwordFits(value.password)) return;
  return { email, password: value.password };
}

function emailInput(value: unknown) {
  if (!value || typeof value !== "object" || !("email" in value) || typeof value.email !== "string") return;
  const email = value.email.trim();
  if (new TextEncoder().encode(email).length <= 254 && /^[^\s@]+@[^\s@]+$/.test(email)) return email;
}

function resetCredentials(value: unknown) {
  if (!value || typeof value !== "object" || !("token" in value) || !("password" in value)) return;
  const { token, password } = value;
  if (typeof token !== "string" || !/^[\w-]{43}$/.test(token)) return;
  if (typeof password !== "string" || !passwordFits(password)) return;
  return { token, password };
}

function backendFailure(response: Response, code?: string) {
  if (response.status === 400 && code === "invalid_reset_token") return failure(400, code);
  if (response.status === 400) return failure(400, "invalid_input");
  if (response.status === 401 && code === "invalid_credentials") return failure(401, code);
  if (response.status === 409 && code === "email_in_use") return failure(409, code);
  if (response.status === 429) return failure(429, "busy", response.headers.get("Retry-After"));
  return failure(503, "unavailable");
}

export async function GET(_request: NextRequest, { params }: RouteContext<"/api/auth/[operation]">) {
  if ((await params).operation !== "session") return failure(404, "not_found");
  const session = await workspaceSession();
  if (session === "unavailable") return failure(503, "unavailable");
  if (session !== "active") return failure(401, "invalid_session");
  return new NextResponse(null, { status: 204, headers: noStore });
}

export async function POST(request: NextRequest, { params }: RouteContext<"/api/auth/[operation]">) {
  const { operation } = await params;
  if (operation !== "login" && operation !== "register" && operation !== "logout" &&
      operation !== "request-password-reset" && operation !== "confirm-password-reset") {
    return failure(404, "not_found");
  }

  const origin = appOrigin(request);
  if (!origin || request.headers.get("origin") !== origin) return failure(403, "invalid_origin");

  const cookieOptions = {
    path: "/",
    httpOnly: true,
    sameSite: "lax" as const,
    secure: new URL(origin).protocol === "https:",
  };

  try {
    if (operation === "logout") {
      const token = request.cookies.get(sessionCookie)?.value;
      if (token) {
        const { response } = await backend(token).POST("/v1/auth/logout");
        if (response.status !== 204 && response.status !== 401) return backendFailure(response);
      }
      const result = new NextResponse(null, { status: 204, headers: noStore });
      result.cookies.set(sessionCookie, "", { ...cookieOptions, maxAge: 0 });
      return result;
    }

    const input = await readJSON(request, 4096);
    if (input === undefined) return failure(413, "payload_too_large");

    if (operation === "request-password-reset") {
      const email = emailInput(input);
      if (!email) return failure(400, "invalid_input");
      const { response, error } = await backend().POST("/v1/auth/password-reset/request", { body: { email } });
      if (response.status !== 202) return backendFailure(response, error?.error?.code);
      return new NextResponse(null, { status: 202, headers: noStore });
    }

    if (operation === "confirm-password-reset") {
      const body = resetCredentials(input);
      if (!body) return failure(400, "invalid_input");
      const { response, error } = await backend().POST("/v1/auth/password-reset/confirm", { body });
      if (response.status !== 204) return backendFailure(response, error?.error?.code);
      const result = new NextResponse(null, { status: 204, headers: noStore });
      result.cookies.set(sessionCookie, "", { ...cookieOptions, maxAge: 0 });
      return result;
    }

    const body = credentials(input);
    if (!body) return failure(400, "invalid_input");

    if (operation === "register") {
      const { response, data, error } = await backend().POST("/v1/auth/register", { body });
      if (response.status !== 201 || typeof data?.user_id !== "string") {
        return backendFailure(response, error?.error?.code);
      }
      return new NextResponse(null, { status: 204, headers: noStore });
    }

    const { response, data, error } = await backend().POST("/v1/auth/login", { body });
    if (response.status !== 200 || typeof data?.access_token !== "string" || data.token_type !== "Bearer") {
      return backendFailure(response, error?.error?.code);
    }
    const expires = new Date(data.expires_at);
    if (!(expires.getTime() > Date.now()) || !/^[\w-]{43}$/.test(data.access_token)) {
      return failure(503, "unavailable");
    }
    const result = new NextResponse(null, { status: 204, headers: noStore });
    result.cookies.set(sessionCookie, data.access_token, { ...cookieOptions, expires });
    return result;
  } catch {
    return failure(503, "unavailable");
  }
}
