import Link from "next/link";
import { redirect } from "next/navigation";
import { backend } from "@/lib/backend";
import type { Readable } from "openapi-typescript-helpers";
import type { components } from "@/lib/api";
import { companyIdentifier, companyPath, companyLabels, isCompanyId, isRegistration, taxLabels } from "./company-data";
import "./companies.css";

type Schema = { [K in keyof components["schemas"]]: Readable<components["schemas"][K]> };
type Query = Record<string, string | string[] | undefined>;
type Api = ReturnType<typeof backend>;

async function companyRequest<T>(request: Promise<{
  data?: T;
  error?: components["schemas"]["Error"];
  response: Response;
}>) {
  const result = await request.catch(() => undefined);
  if (result?.response.status === 401) redirect("/login?expired=1");
  return result;
}

const availability: Record<Schema["Availability"], string> = {
  found: "Есть сведения",
  not_published: "Сведения не опубликованы",
  not_loaded: "Набор данных ещё не загружен",
  not_applicable: "Этот показатель неприменим",
  unavailable: "Сведения временно недоступны",
  identity_conflict: "Сведения скрыты из-за расхождения идентификаторов",
};

const datasets: Record<string, string> = {
  rsmp: "Реестр МСП", revexp: "Доходы и расходы", sschr: "Численность работников",
  paytax: "Уплаченные налоги", debtam: "Налоговая задолженность", snr: "Налоговые режимы",
};

const regimes = { eshn: "ЕСХН", usn: "УСН", ausn: "АУСН", srp: "СРП", psn: "ПСН", npd: "НПД" };

const warningText: Record<string, string> = {
  user_external_api_budget_exhausted: "Ваш дневной лимит запросов к DaData исчерпан.",
  provider_budget_exhausted: "Общий дневной лимит запросов к DaData исчерпан.",
  provider_rate_limited: "DaData ограничила частоту запросов. Повторите поиск позже.",
  provider_unavailable: "DaData временно недоступна. Повторите запрос позже.",
  provider_identity_conflict: "Идентификаторы в источниках расходятся. Часть сведений DaData скрыта.",
  provider_name_differs: "Названия в источниках различаются.",
  provider_okved_differs: "Коды ОКВЭД в источниках различаются.",
  ambiguous_taxpayer_registration: "Часть сведений не удалось связать с конкретной регистрацией.",
};

function single(value: Query[string]) {
  return typeof value === "string" ? value : undefined;
}

function date(value?: string | null) {
  if (!value) return "дата не указана";
  const parsed = new Date(value);
  return Number.isNaN(parsed.valueOf()) ? value : new Intl.DateTimeFormat("ru-RU", { timeZone: "UTC" }).format(parsed);
}

function money(value?: string | null) {
  if (value == null) return "Нет данных";
  const [integer, fraction] = value.split(".");
  return `${integer.replace(/\B(?=(\d{3})+(?!\d))/g, "\u00a0")}${fraction ? `,${fraction}` : ""}\u00a0₽`;
}

function SourceLink({ url, children }: { url?: string; children: React.ReactNode }) {
  if (!url || !/^https?:\/\//i.test(url)) return <>{children}</>;
  return <a className="company-link" href={url}>{children}</a>;
}

function Warnings({ warnings, coverage }: { warnings: Schema["Warning"][]; coverage?: Schema["Response"]["coverage"] }) {
  const messages = new Set(warnings.map(({ code }) => warningText[code] ?? "Часть сведений недоступна."));
  if (coverage?.dadata === "unavailable" && !warnings.some(({ source }) => source === "dadata")) {
    messages.add(warningText.provider_unavailable);
  }
  if (coverage?.local === "not_loaded") messages.add("Локальные наборы данных ещё не загружены.");
  return messages.size > 0 && <ul className="company-notice" aria-label="Ограничения данных">
    {[...messages].map((message) => <li key={message}>{message}</li>)}
  </ul>;
}

function Failure({ error, retry }: { error?: Schema["Error"]; retry: string }) {
  const code = error?.error.code;
  const candidates = error?.error.candidates?.filter(isCompanyId) ?? [];
  const title = code === "company_not_found" ? "Компания не найдена"
    : code === "ambiguous_identity" ? "Выберите регистрацию"
    : code === "invalid_query" ? "Проверьте название или идентификатор"
    : code === "invalid_cursor" ? "Страница выдачи устарела"
    : code === "provider_unavailable" ? "Внешний источник недоступен"
    : "Не удалось загрузить данные";

  return <div className="company-empty">
    <h2>{title}</h2>
    <Warnings warnings={error?.warnings ?? []} />
    {candidates.length > 0 && <ul className="company-list">
      {candidates.map((id) => <li key={id}><Link href={companyPath(id)} prefetch={false}>{companyIdentifier(id)}</Link></li>)}
    </ul>}
    {(code === "invalid_cursor" || (!code || !["company_not_found", "invalid_query", "ambiguous_identity"].includes(code))) &&
      <a className="button button-outline" href={retry}>{code === "invalid_cursor" ? "К первой странице" : "Повторить"}</a>}
  </div>;
}

function searchPath(q: string, cursor?: string) {
  const params = new URLSearchParams({ q });
  if (cursor) params.set("cursor", cursor);
  return `/companies?${params}`;
}

export async function CompanySearch({ api, query }: { api: Api; query: Query }) {
  const q = single(query.q)?.trim() ?? "";
  const cursor = single(query.cursor);
  const result = q ? await companyRequest(api.GET("/v1/companies", {
    params: { query: { q, limit: 20, cursor } },
  })) : undefined;
  const data = result?.data;
  const external = data?.external_suggestions?.filter(({ id }) => !data.items.some((item) => item.id === id)) ?? [];

  return <div className="company-content">
    <header>
      <h1>Поиск компаний</h1>
      <p>Введите название, ФИО предпринимателя, ИНН, ОГРН или ОГРНИП.</p>
    </header>
    <form action="/companies" className="company-search" role="search">
      <input className="field" type="search" name="q" aria-label="Название или идентификатор компании"
        placeholder="Например, 7707083893" defaultValue={q} required maxLength={200} />
      <button className="button button-primary" type="submit">Найти</button>
    </form>
    {q && !data && <Failure error={result?.error} retry={searchPath(q, result?.error?.error.code === "invalid_cursor" ? undefined : cursor)} />}
    {data && <>
      <Warnings warnings={data.warnings} coverage={data.coverage} />
      {data.items.length === 0 && external.length === 0
        ? <div className="company-empty"><h2>{data.coverage.dadata === "unavailable" ? "В локальной базе совпадений нет" : "Компания не найдена"}</h2>
          <p>Проверьте запрос или попробуйте поиск по номеру регистрации.</p></div>
        : <section className="company-section">
          <h2>{cursor ? "Следующие результаты" : "Результаты поиска"}</h2>
          <ul className="company-list">
            {data.items.map((item) => <CompanyResult key={item.id} company={item} name={item.name?.value ?? item.dadata?.name} external={!item.name && Boolean(item.dadata)} />)}
            {external.map((item) => <CompanyResult key={item.id} company={item} name={item.name} external />)}
          </ul>
        </section>}
      {(cursor || data.next_cursor) && <nav aria-label="Страницы результатов" className="company-actions">
        {cursor && <Link className="button button-outline" href={searchPath(q)} prefetch={false}>Первая страница</Link>}
        {data.next_cursor && <Link className="button button-outline" href={searchPath(q, data.next_cursor)} prefetch={false}>Следующая страница</Link>}
      </nav>}
    </>}
  </div>;
}

function CompanyResult({ company, name, external }: {
  company: Pick<Schema["Identity"], "id" | "kind" | "inn" | "registration_number">;
  name?: string;
  external: boolean;
}) {
  return <li>
    <Link href={companyPath(company.id)} prefetch={false}>
      <strong>{name ?? (company.kind === "entrepreneur" ? "Индивидуальный предприниматель" : "Юридическое лицо")}</strong>
      <span className="company-meta">
        <span>ИНН {company.inn}</span>
        {company.registration_number && <span>{companyIdentifier(`reg:${company.registration_number}`)}</span>}
        {external && <span>DaData</span>}
      </span>
    </Link>
  </li>;
}

function Evidence({ evidence, sources }: { evidence: Schema["Evidence"]; sources: Schema["Response"]["sources"] }) {
  const source = sources[evidence.source_ref];
  return <span className="company-evidence">
    <SourceLink url={source?.url}>{source ? `${source.publisher} · ${datasets[source.dataset] ?? source.dataset}` : evidence.source_ref}</SourceLink>
    <span>Данные на {date(evidence.as_of)}</span>
  </span>;
}

function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return <dl className="company-facts">
    {rows.map(([label, content]) => <div key={label}><dt>{label}</dt><dd>{content ?? "Нет данных"}</dd></div>)}
  </dl>;
}

function Profile({ data }: { data: Schema["Profile"] }) {
  const { company, dadata, headcounts, sections, sources } = data;
  return <>
    <section className="company-section">
      <h2>Регистрация</h2>
      <Facts rows={[
        ["Тип", company.kind === "entrepreneur" ? "Индивидуальный предприниматель" : "Юридическое лицо"],
        ["ИНН", company.inn],
        [company.kind === "entrepreneur" ? "ОГРНИП" : "ОГРН", company.registration_number],
        ["Наименование", company.name && <>{company.name.value}<Evidence evidence={company.name} sources={sources} /></>],
      ]} />
      {company.registration_evidence && <Evidence evidence={company.registration_evidence} sources={sources} />}
      {sections.registration.state !== "found" && !dadata && <p>{availability[sections.registration.state]}</p>}
    </section>
    {dadata && <section className="company-section">
      <h2>Сведения DaData</h2>
      <Facts rows={[
        ["Наименование", dadata.name], ["Статус", ({ ACTIVE: "Действующая", LIQUIDATING: "Ликвидируется", LIQUIDATED: "Ликвидирована", REORGANIZING: "Реорганизуется", BANKRUPT: "Банкротство" })[dadata.status ?? ""] ?? dadata.status],
        ["Дата регистрации", dadata.registered_on && date(dadata.registered_on)], ["КПП", dadata.kpp],
        ["ОКВЭД", dadata.okved], ["Адрес", dadata.address], ["Город", dadata.residence_city],
        ["Руководитель", dadata.manager && [dadata.manager.name, dadata.manager.title, dadata.manager.started_on && `с ${date(dadata.manager.started_on)}`].filter(Boolean).join(" · ")],
        ["Последнее изменение", dadata.last_changed_on && date(dadata.last_changed_on)],
      ].filter((row): row is [string, string] => typeof row[1] === "string")} />
      <p className="company-meta"><SourceLink url={dadata.source_url}>DaData</SourceLink> Получено {date(dadata.retrieved_at)} · Дата актуальности не указана</p>
    </section>}
    <section className="company-section">
      <h2>Реестр МСП</h2>
      {company.sme ? <>
        <Facts rows={[
          ["Категория", { "1": "Микропредприятие", "2": "Малое предприятие", "3": "Среднее предприятие" }[company.sme.category]],
          ["Включение в реестр", date(company.sme.included_on)], ["Код региона", company.sme.region_code], ["ОКВЭД", company.sme.okved],
        ]} />
        <Evidence evidence={company.sme} sources={sources} />
      </> : <p>{availability[sections.sme.state]}</p>}
    </section>
    <section className="company-section">
      <h2>Численность работников</h2>
      {headcounts.length ? <ul className="company-records">
        {headcounts.map((item, index) => <li key={index}><strong>{item.year} · {item.employees} чел.</strong><Evidence evidence={item} sources={sources} /></li>)}
      </ul> : <p>{availability[sections.headcount.state]}</p>}
    </section>
  </>;
}

function TaxRecords({ data, next }: { data: Schema["MoneyTaxes"] | Schema["TaxRegimes"]; next: (offset: number) => string }) {
  return <>
    {data.state !== "found" && <p>{availability[data.state]}</p>}
    <ul className="company-records">
      {data.items.map((item, index) => <li key={index}>
        {"codes" in item ? <strong>{item.codes.length ? item.codes.map((code) => regimes[code]).join(", ") : "Спецрежимы не указаны в документе"}</strong> :
          <><strong>{item.tax_name}{item.year ? ` · ${item.year}` : ""}</strong>
            {item.debt ? <Facts rows={[
              ["Всего", money(item.debt.total)], ["Недоимка", money(item.debt.principal)],
              ["Пени", money(item.debt.penalties)], ["Штрафы", money(item.debt.fines)],
            ]} /> : <p>{money(item.amount)}</p>}</>}
        <Evidence evidence={item} sources={data.sources} />
      </li>)}
    </ul>
    {"next_offset" in data && data.next_offset != null &&
      <Link className="button button-outline" href={next(data.next_offset)} prefetch={false}>Следующая страница</Link>}
  </>;
}

function Sources({ data }: { data: Schema["Observations"] }) {
  const refs = new Set(data.observations.map(({ evidence }) => evidence.source_ref));
  const sources = Object.entries(data.sources).filter(([ref]) => refs.has(ref));
  return <>
    {data.dadata && <p className="company-meta"><SourceLink url={data.dadata.source_url}>DaData</SourceLink> Получено {date(data.dadata.retrieved_at)} · Дата актуальности не указана</p>}
    {sources.length > 0 && <ul className="company-records">
      {sources.map(([ref, source]) => <li key={ref}>
        <strong><SourceLink url={source.url}>{source.publisher} · {datasets[source.dataset] ?? source.dataset}</SourceLink></strong>
        <p className="company-meta">Опубликовано {date(source.published_on)} · Период {source.period}</p>
        <SourceLink url={source.download_url}>Скачать выгрузку</SourceLink>
      </li>)}
    </ul>}
    {data.observations.length ? <section className="company-section">
      <h2>Наблюдения</h2>
      <ul className="company-records">{data.observations.map(({ section, year, evidence }, index) => <li key={index}>
        <strong>{{ sme: "МСП", financials: "Финансы", headcount: "Численность", regimes: "Налоговый режим" }[section]}{year ? ` · ${year}` : ""}</strong>
        <Evidence evidence={evidence} sources={data.sources} />
        <span className="company-meta">{evidence.file}</span>
      </li>)}</ul>
    </section> : !data.dadata && <p>Для этой компании источники пока не найдены.</p>}
  </>;
}

export async function CompanyCard({ api, id, section, query }: {
  api: Api;
  id: string;
  section: keyof typeof companyLabels;
  query: Query;
}) {
  const base = companyPath(id);
  const params = { path: { id } };
  const requestedTax = single(query.tax);
  const tax = requestedTax === "payments" || requestedTax === "debts" ? requestedTax : "regimes";
  const rawOffset = single(query.offset);
  const offset = rawOffset && /^\d{1,7}$/.test(rawOffset) && Number(rawOffset) <= 1000000 ? Number(rawOffset) : undefined;
  const taxPath = (kind: keyof typeof taxLabels, page?: number) => `${base}/taxes?tax=${kind}${page ? `&offset=${page}` : ""}`;
  let content: React.ReactNode;
  let data: (Schema["Response"] & { id: string }) | undefined;
  let title = companyIdentifier(id);

  switch (section) {
    case "overview": {
      const result = await companyRequest(api.GET("/v1/companies/{id}", { params }));
      data = result?.data;
      if (result?.data) {
        title = result.data.company.name?.value ?? result.data.dadata?.name ?? title;
      }
      content = result?.data ? <Profile data={result.data} /> : <Failure error={result?.error} retry={base} />;
      break;
    }
    case "financials": {
      const result = await companyRequest(api.GET("/v1/companies/{id}/financials", { params }));
      const financials = result?.data;
      data = financials;
      content = financials ? <section className="company-section">
        <h2>Доходы и расходы</h2>
        {financials.state !== "found" && <p>{availability[financials.state]}</p>}
        <ul className="company-records">{financials.items.map((item, index) => <li key={index}>
          <strong>{item.year} · {item.display_name}</strong>
          <Facts rows={[["Доходы", money(item.income)], ["Расходы", money(item.expense)]]} />
          <Evidence evidence={item} sources={financials.sources} />
        </li>)}</ul>
      </section> : <Failure error={result?.error} retry={`${base}/financials`} />;
      break;
    }
    case "taxes": {
      const result = await companyRequest(api.GET("/v1/companies/{id}/taxes", {
        params: { ...params, query: tax === "regimes" ? { section: tax } : { section: tax, limit: 20, offset } },
      }));
      data = result?.data;
      content = <section className="company-section">
        <h2>Налоги</h2>
        <nav className="company-actions" aria-label="Виды налоговых сведений">
          {Object.entries(taxLabels).map(([key, label]) => <Link key={key} className="button button-outline"
            href={`${base}/taxes?tax=${key}`} prefetch={false} aria-current={tax === key ? "page" : undefined}>{label}</Link>)}
        </nav>
        {offset && tax !== "regimes" ? <Link className="company-link" href={taxPath(tax)} prefetch={false}>Первая страница</Link> : null}
        {result?.data ? <TaxRecords data={result.data} next={(page) => taxPath(tax, page)} /> :
          <Failure error={result?.error} retry={taxPath(tax, offset)} />}
      </section>;
      break;
    }
    case "sources": {
      const result = await companyRequest(api.GET("/v1/companies/{id}/sources", { params }));
      const sources = result?.data;
      data = sources;
      content = sources ? <section className="company-section"><h2>Источники</h2><Sources data={sources} /></section> :
        <Failure error={result?.error} retry={`${base}/sources`} />;
      break;
    }
  }

  return <div className="company-content">
    <Link className="company-back" href="/companies" prefetch={false}>К поиску компаний</Link>
    <header className="company-heading">
      <div>
        <h1>{title}</h1>
        {title !== companyIdentifier(id) && <p>{companyIdentifier(id)}</p>}
      </div>
      {data && isRegistration(data.id) &&
        <Link className="button button-primary" href={`/chat?company=${encodeURIComponent(data.id)}`} prefetch={false}>Спросить в чате</Link>}
    </header>
    <nav className="company-tabs" aria-label="Разделы карточки">
      {Object.entries(companyLabels).map(([key, label]) => <Link key={key} prefetch={false}
        href={key === "overview" ? base : `${base}/${key}`} aria-current={section === key ? "page" : undefined}>{label}</Link>)}
    </nav>
    {data && <Warnings warnings={data.warnings} coverage={data.coverage} />}
    {content}
  </div>;
}

