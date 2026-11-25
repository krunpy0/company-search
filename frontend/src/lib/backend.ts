import createClient from "openapi-fetch";
import type { paths } from "./api";

export const sessionCookie = "company_session";

export function backend(token?: string, timeout = 8000) {
  return createClient<paths>({
    baseUrl: process.env.COMPANY_API_URL ?? "http://127.0.0.1:8080",
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    fetch: (request) => fetch(request, { cache: "no-store" }),
    redirect: "error",
    signal: AbortSignal.timeout(timeout),
  });
}
