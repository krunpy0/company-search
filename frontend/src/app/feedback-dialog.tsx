"use client";

import { useId, useRef, useState, type ReactNode } from "react";
import { WorkspaceDialog } from "./workspace-dialog";
import "./feedback-dialog.css";

type FeedbackKind = "feedback" | "technical" | "answer";
type FeedbackDialogProps = {
  open: boolean;
  onClose: () => void;
  requestId?: string;
  dialogueText?: string;
  kind?: FeedbackKind;
};

const titles: Record<FeedbackKind, string> = {
  feedback: "Обратная связь",
  technical: "Сообщить о технической проблеме",
  answer: "Пожаловаться на ответ",
};

function FeedbackDraft({ kind, requestId, dialogueText }: Pick<FeedbackDialogProps, "requestId" | "dialogueText"> & { kind: FeedbackKind }) {
  const id = useId();
  const preview = useRef<HTMLTextAreaElement>(null);
  const [message, setMessage] = useState("");
  const [includeDialogue, setIncludeDialogue] = useState(false);
  const [copyResult, setCopyResult] = useState<{ report: string; success: boolean }>();
  const description = message.trim();
  const report = [
    titles[kind],
    requestId && `request_id: ${requestId}`,
    description,
    includeDialogue && dialogueText && `Текст диалога\n${dialogueText}`,
  ].filter(Boolean).join("\n\n");
  const hasContent = Boolean(requestId || description || (includeDialogue && dialogueText));

  async function copyReport() {
    setCopyResult(undefined);
    try {
      await navigator.clipboard.writeText(report);
      setCopyResult({ report, success: true });
    } catch {
      preview.current?.focus();
      preview.current?.select();
      setCopyResult({ report, success: false });
    }
  }

  return (
    <div className="feedback-draft">
      <div className="feedback-field">
        <label htmlFor={`${id}-message`}>Что произошло или что стоит улучшить?</label>
        <textarea id={`${id}-message`} className="field" rows={3} value={message} onChange={(event) => setMessage(event.target.value)} data-dialog-autofocus />
      </div>
      {dialogueText && (
        <label className="feedback-opt-in">
          <input type="checkbox" checked={includeDialogue} onChange={(event) => setIncludeDialogue(event.target.checked)} />
          <span>Включить текст диалога</span>
        </label>
      )}
      {hasContent && (
        <div className="feedback-field">
          <label htmlFor={`${id}-preview`}>Будет скопировано</label>
          <textarea ref={preview} id={`${id}-preview`} className="field feedback-preview" rows={4} readOnly value={report} />
        </div>
      )}
      <div className="feedback-actions">
        <button type="button" className="button button-primary" disabled={!hasContent} onClick={copyReport}>Скопировать обращение</button>
        <p className="feedback-copy-status" role="status">
          {copyResult?.report === report && (copyResult.success ? "Обращение скопировано." : "Скопируйте выделенный текст вручную.")}
        </p>
      </div>
    </div>
  );
}

export function FeedbackDialog({ open, onClose, requestId, dialogueText, kind = "feedback" }: FeedbackDialogProps) {
  return (
    <WorkspaceDialog open={open} onClose={onClose} title={titles[kind]} description="Канал поддержки пока недоступен. Обращение можно подготовить и скопировать." className="feedback-dialog">
      {open && <FeedbackDraft kind={kind} requestId={requestId} dialogueText={dialogueText} />}
    </WorkspaceDialog>
  );
}

export function FeedbackButton({ children, kind }: { children: ReactNode; kind?: FeedbackKind }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" className="button button-outline" onClick={() => setOpen(true)}>{children}</button>
      <FeedbackDialog open={open} onClose={() => setOpen(false)} kind={kind} />
    </>
  );
}
