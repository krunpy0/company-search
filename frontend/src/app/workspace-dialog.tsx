"use client";

import { useId, useLayoutEffect, useRef, type ReactNode } from "react";
import "./workspace-dialog.css";

type WorkspaceDialogProps = {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  className?: string;
  children: ReactNode;
};

export function WorkspaceDialog({ open, onClose, title, description, className, children }: WorkspaceDialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const backdropPress = useRef(false);
  const titleId = useId();
  const descriptionId = useId();

  useLayoutEffect(() => {
    const element = dialog.current;
    if (!open || !element) return;
    element.showModal();
    element.querySelector<HTMLElement>("[data-dialog-autofocus]")?.focus();
    return () => element.close();
  }, [open]);

  function isOutsideDialog(clientX: number, clientY: number) {
    const rect = dialog.current?.getBoundingClientRect();
    if (!rect) return false;
    return clientX < rect.left || clientX > rect.right || clientY < rect.top || clientY > rect.bottom;
  }

  return (
    <dialog
      ref={dialog}
      className={className ? `workspace-dialog ${className}` : "workspace-dialog"}
      aria-labelledby={titleId}
      aria-describedby={description ? descriptionId : undefined}
      onPointerDown={(event) => {
        backdropPress.current = event.target === event.currentTarget && isOutsideDialog(event.clientX, event.clientY);
      }}
      onPointerCancel={() => {
        backdropPress.current = false;
      }}
      onClick={(event) => {
        const shouldClose = backdropPress.current;
        backdropPress.current = false;
        if (shouldClose && event.target === event.currentTarget && isOutsideDialog(event.clientX, event.clientY)) {
          onClose();
        }
      }}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <div className="workspace-dialog-heading">
        <h2 id={titleId}>{title}</h2>
        <button type="button" className="workspace-dialog-close" aria-label="Закрыть окно" onClick={onClose}>
          <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
            <path d="m6 6 12 12M18 6 6 18" />
          </svg>
        </button>
      </div>
      {description && <p className="workspace-dialog-description" id={descriptionId}>{description}</p>}
      <div className="workspace-dialog-content">{children}</div>
    </dialog>
  );
}
