import type { NextRequest } from "next/server";

export function appOrigin(request: NextRequest) {
  if (process.env.COMPANY_APP_ORIGIN) return process.env.COMPANY_APP_ORIGIN;
  const host = request.headers.get("host");
  if (!host) return undefined;
  try {
    // Next may normalize the hostname in nextUrl to localhost.
    return new URL(`${request.nextUrl.protocol}//${host}`).origin;
  } catch {
    return undefined;
  }
}

