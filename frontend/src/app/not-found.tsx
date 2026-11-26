import Link from "next/link";
import { SystemScreen } from "./system-screen";

export default function NotFound() {
  return (
    <SystemScreen title="Страница не найдена">
      <p>Проверьте адрес или начните с главной страницы.</p>
      <div className="system-actions">
        <Link className="button button-primary" href="/" prefetch={false}>На главную</Link>
        <Link className="button button-outline" href="/help">Помощь</Link>
      </div>
    </SystemScreen>
  );
}
