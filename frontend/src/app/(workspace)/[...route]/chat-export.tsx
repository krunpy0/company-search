"use client";

import { useEffect, useRef, useState } from "react";
import { WorkspaceDialog } from "@/app/workspace-dialog";
import { readChatPage, type Turn } from "./chat-api";
import { collectChatHistory } from "./chat-history";
import { chatMarkdown } from "./chat-markdown";

type ExportKind = "answer" | "dialogue";

function download(name: string, markdown: string) {
  const url = URL.createObjectURL(new Blob([markdown], { type: "text/markdown;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function ChatExportDialog({ open, onClose, dialogueId, turn }: {
  open: boolean;
  onClose: () => void;
  dialogueId: string;
  turn?: Turn;
}) {
  const [state, setState] = useState<{ kind: ExportKind; status: "loading" | "error" }>();
  const request = useRef<AbortController>(null);

  useEffect(() => () => {
    request.current?.abort();
    request.current = null;
  }, [dialogueId, open]);

  async function exportMarkdown(kind: ExportKind) {
    if (request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setState({ kind, status: "loading" });
    try {
      if (kind === "answer" && turn) {
        download(`report-${dialogueId}-${turn.id}.md`, chatMarkdown("Отчёт о компании", [turn]));
      } else {
        const result = await collectChatHistory((before) => readChatPage(dialogueId, before, controller.signal));
        if (controller.signal.aborted) return;
        download(`dialogue-${dialogueId}.md`, chatMarkdown(result.dialogue.title || "Диалог", result.messages));
      }
      setState(undefined);
      onClose();
    } catch {
      if (!controller.signal.aborted) setState({ kind, status: "error" });
    } finally {
      if (request.current === controller) request.current = null;
    }
  }

  function close() {
    request.current?.abort();
    setState(undefined);
    onClose();
  }

  return <WorkspaceDialog open={open} onClose={close} title="Экспорт в Markdown"
    description="Файл включает сохранённые вопросы, ответы, источники и даты актуальности.">
    {state?.status === "loading" && <p role="status">Загружаем историю диалога…</p>}
    {state?.status === "error" && <p role="alert" className="workspace-error">
      {state.kind === "dialogue" ? "Не удалось выгрузить диалог целиком." : "Не удалось скачать отчёт."}
    </p>}
    <div className="workspace-dialog-actions">
      {turn && <button type="button" className="button button-outline" disabled={state?.status === "loading"}
        onClick={() => void exportMarkdown("answer")}>Скачать отчёт</button>}
      <button type="button" className="button button-primary" disabled={state?.status === "loading"}
        data-dialog-autofocus onClick={() => void exportMarkdown("dialogue")}>
        {state?.status === "error" && state.kind === "dialogue" ? "Повторить выгрузку" : "Скачать диалог"}
      </button>
    </div>
  </WorkspaceDialog>;
}
