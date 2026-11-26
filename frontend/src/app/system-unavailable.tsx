"use client";

import Link from "next/link";
import { SystemScreen } from "./system-screen";

export function SystemUnavailable({ retry }: { retry?: () => void }) {
  return (
    <SystemScreen title="Сервис временно недоступен">
      <p>Не удалось загрузить страницу. Попробуйте ещё раз.</p>
      <div className="system-actions">
        <button type="button" className="button button-primary" onClick={retry ?? (() => window.location.reload())}>Повторить</button>
        <Link className="button button-outline" href="/help">Помощь</Link>
      </div>
    </SystemScreen>
  );
}
