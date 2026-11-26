import type { Viewport } from "next";
import { Suspense } from "react";
import { cookies } from "next/headers";
import { notFound, redirect } from "next/navigation";
import { connection } from "next/server";
import { SystemUnavailable } from "@/app/system-unavailable";
import { backend, sessionCookie } from "@/lib/backend";
import { isChatId } from "@/lib/chat-contract";
import { workspaceSession } from "@/lib/workspace-session";
import { CompanyCard, CompanySearch } from "./companies";
import { isCompanyId, isRegistration } from "./company-data";
import { Workspace } from "./workspace";

export const workspaceViewport: Viewport = { interactiveWidget: "resizes-content" };

type WorkspacePage = {
  section: "chat" | "companies";
  params: Promise<{ route?: string[] }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
};

export async function ProtectedWorkspace({ section, params, searchParams }: WorkspacePage) {
  await connection();
  const [{ route = [] }, query] = await Promise.all([params, searchParams]);
  const [rawId, tab] = route;
  const id = rawId?.replace(/%3a/i, ":");
  const companyTab = tab === "financials" || tab === "taxes" || tab === "sources" ? tab : undefined;
  if (section === "companies") {
    if (route.length > 2 || (id && !isCompanyId(id)) || (tab && !companyTab)) notFound();
  } else if (route.length > 1 || (id && !isChatId(id))) {
    notFound();
  }

  const session = await workspaceSession();
  if (session === "missing" || session === "expired") {
    redirect(query.start === "1" && session === "missing" ? "/login" : "/login?expired=1");
  }
  if (session === "unavailable") return <SystemUnavailable />;

  if (section === "companies") {
    const token = (await cookies()).get(sessionCookie)?.value;
    const api = backend(token);
    return <Workspace>
      <Suspense key={JSON.stringify([route, query])} fallback={<div className="company-content" role="status">Загружаем данные…</div>}>
        {id ? <CompanyCard api={api} id={id} section={companyTab ?? "overview"} query={query} /> : <CompanySearch api={api} query={query} />}
      </Suspense>
    </Workspace>;
  }

  const company = typeof query.company === "string" && isRegistration(query.company) ? query.company : undefined;
  return <Workspace key={id ?? company ?? "new-chat"} chat={{ id, company }} />;
}

