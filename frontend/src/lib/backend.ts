import createClient from "openapi-fetch";
import { headers } from "next/headers";
import type { paths } from "./api";
import { forwardedClientIP } from "./client-ip";

export const sessionCookie = "company_session";

export function backendOrigin() {
  return process.env.COMPANY_API_URL ?? "http://127.0.0.1:8080";
}

export function backend(token?: string, timeout = 8000) {
  return createClient<paths>({
    baseUrl: backendOrigin(),
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    fetch: async (request) => {
      const ip = forwardedClientIP(await headers());
      if (ip) request.headers.set("X-Forwarded-For", ip);
      return fetch(request, { cache: "no-store" });
    },
    redirect: "error",
    signal: AbortSignal.timeout(timeout),
  });
}
