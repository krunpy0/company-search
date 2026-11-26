import { SystemScreen } from "@/app/system-screen";

export default function WorkspaceLoading() {
  return (
    <SystemScreen title="Загружаем приложение">
      <progress className="system-loading-track" aria-label="Загрузка приложения" />
    </SystemScreen>
  );
}
