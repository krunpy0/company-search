import { isIP } from "node:net";

export function forwardedClientIP(headers: Pick<Headers, "get">) {
  // Only enable behind a proxy that overwrites X-Real-IP and is the sole ingress to Next.js.
  if (process.env.COMPANY_TRUST_PROXY_CLIENT_IP !== "true") return;
  const ip = headers.get("x-real-ip");
  if (ip && isIP(ip) !== 0) return ip;
}
