import { NextResponse, type NextRequest } from "next/server";
import { appOrigin } from "./lib/app-origin";
import { sessionCookie } from "./lib/backend";

const landingSeenCookie = "landing_seen";
const landingSeenMaxAge = 60 * 60 * 24 * 365;
const noStore = "private, no-store";

function isPrefetch(request: NextRequest) {
  return request.headers.has("next-router-prefetch") ||
    request.headers.has("next-router-segment-prefetch") ||
    request.headers.get("purpose") === "prefetch";
}

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl;
  if (pathname !== "/" && pathname !== "/landing") {
    return NextResponse.next();
  }
  if (request.method !== "GET" && request.method !== "HEAD") {
    return NextResponse.next();
  }

  const seen = request.cookies.get(landingSeenCookie)?.value === "1";
  if (pathname === "/" && seen) {
    const destination = request.cookies.has(sessionCookie) ? "/chat" : "/chat?start=1";
    const origin = appOrigin(request);
    const response = NextResponse.redirect(new URL(destination, origin ?? request.url));
    response.headers.set("Cache-Control", noStore);
    return response;
  }

  if (seen || isPrefetch(request)) {
    const response = NextResponse.next();
    if (pathname === "/") response.headers.set("Cache-Control", noStore);
    return response;
  }

  const origin = appOrigin(request);
  const secure = origin ? origin.startsWith("https://") : request.nextUrl.protocol === "https:";
  const response = NextResponse.next();
  response.cookies.set(landingSeenCookie, "1", {
    path: "/",
    httpOnly: true,
    sameSite: "lax",
    secure,
    maxAge: landingSeenMaxAge,
  });
  response.headers.set("Cache-Control", noStore);
  return response;
}

export const config = {
  matcher: [
    {
      source: "/",
      has: [{ type: "cookie", key: "landing_seen", value: "1" }],
    },
    {
      source: "/",
      missing: [
        { type: "cookie", key: "landing_seen", value: "1" },
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "next-router-segment-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
    {
      source: "/landing",
      missing: [
        { type: "cookie", key: "landing_seen", value: "1" },
        { type: "header", key: "next-router-prefetch" },
        { type: "header", key: "next-router-segment-prefetch" },
        { type: "header", key: "purpose", value: "prefetch" },
      ],
    },
  ],
};
