export const companyLabels = {
  overview: "Обзор",
  financials: "Финансы",
  taxes: "Налоги",
  sources: "Источники",
};

export const taxLabels = {
  regimes: "Режимы",
  payments: "Уплачено",
  debts: "Задолженность",
};

export function isRegistration(id: string) {
  return /^reg:(?:\d{13}|\d{15})$/.test(id);
}

export function isCompanyId(id: string) {
  return isRegistration(id) || /^inn:(?:\d{10}|\d{12})$/.test(id);
}

export function companyPath(id: string) {
  return `/companies/${encodeURIComponent(id)}`;
}

export function companyIdentifier(id: string) {
  const number = id.slice(4);
  const label = id.startsWith("inn:") ? "ИНН" : number.length === 15 ? "ОГРНИП" : "ОГРН";
  return `${label} ${number}`;
}
