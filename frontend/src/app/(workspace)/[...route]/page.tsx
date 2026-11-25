import type { Viewport } from "next";
import { Suspense } from "react";
import { cookies } from "next/headers";
import { notFound, redirect } from "next/navigation";
import { backend, sessionCookie } from "@/lib/backend";
import { isChatId } from "@/lib/chat-contract";
import { workspaceSession } from "@/lib/workspace-session";
import { CompanyCard, CompanySearch } from "./companies";
import { isCompanyId, isRegistration } from "./company-data";
import { Workspace } from "./workspace";

export const viewport: Viewport = { interactiveWidget: "resizes-content" };

async function ProtectedWorkspace({ params, searchParams }: PageProps<"/[...route]">) {
  const [{ route }, query] = await Promise.all([params, searchParams]);
  const [section, rawId, tab] = route;
  const id = rawId?.replace(/%3a/i, ":");
  const companyTab = tab === "financials" || tab === "taxes" || tab === "sources" ? tab : undefined;
  if (section === "companies") {
    if (route.length > 3) notFound();
  } else if (section !== "chat" || route.length > 2) {
    notFound();
  }

  const session = await workspaceSession();
  if (session === "missing" || session === "expired") {
    redirect(query.start === "1" && session === "missing" ? "/login" : "/login?expired=1");
  }
  if (session === "unavailable") {
    return (
      <main className="workspace-state">
        <h1>Не удалось проверить сессию</h1>
        <p>Сервис временно недоступен.</p>
        <a className="button button-outline" href={`/${route.map(encodeURIComponent).join("/")}`}>Повторить</a>
      </main>
    );
  }

  if (section === "companies") {
    if ((id && !isCompanyId(id)) || (tab && !companyTab)) notFound();
    const token = (await cookies()).get(sessionCookie)?.value;
    const api = backend(token);
    return <Workspace>
      <Suspense key={JSON.stringify([route, query])} fallback={<div className="company-content" role="status">Загружаем данные…</div>}>
        {id ? <CompanyCard api={api} id={id} section={companyTab ?? "overview"} query={query} /> : <CompanySearch api={api} query={query} />}
      </Suspense>
    </Workspace>;
  }

  const company = typeof query.company === "string" && isRegistration(query.company) ? query.company : undefined;
  if (id && !isChatId(id)) notFound();
  return <Workspace key={id ?? company ?? "new-chat"} chat={{ id, company }} />;
}

export default function WorkspacePage(props: PageProps<"/[...route]">) {
  return <Suspense><ProtectedWorkspace {...props} /></Suspense>;
}

