"use client";

import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { useRouter } from "next/navigation";
import { questionDraftKey } from "./product";

const templates = [
  ["Оценка рисков", "Оцени риски работы с компанией "],
  ["Анализ конкурента", "Проанализируй конкурента "],
  ["Проверка бизнеса", "Проверь компанию "],
];

export function Arrow({ direction = "up" }: { direction?: "up" | "right" }) {
  return (
    <svg className={direction === "right" ? "arrow-right" : undefined} aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
      <path d="M12 20V4m-8 8 8-8 8 8" />
    </svg>
  );
}

export function NavMenu() {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) return;
    function handleOutside(event: PointerEvent) {
      if (event.target instanceof Node && !root.current?.contains(event.target)) {
        setOpen(false);
      }
    }
    function handleEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpen(false);
        root.current?.querySelector("button")?.focus();
      }
    }
    document.addEventListener("pointerdown", handleOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("pointerdown", handleOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, [open]);

  return (
    <div ref={root} className="nav-menu">
      <button
        type="button"
        className="nav-menu-toggle"
        aria-label={open ? "Закрыть меню разделов" : "Открыть меню разделов"}
        aria-expanded={open}
        aria-controls={menuId}
        onClick={() => setOpen((value) => !value)}
      >
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
          {open ? <path d="M6 6l12 12M18 6 6 18" /> : <path d="M4 7h16M4 12h16M4 17h16" />}
        </svg>
      </button>
      {open && (
        <div id={menuId} className="nav-menu-panel">
          <a href="#features" onClick={() => setOpen(false)}>Возможности</a>
          <a href="#pricing" onClick={() => setOpen(false)}>Тарифы</a>
          <a href="#business" onClick={() => setOpen(false)}>Для бизнеса</a>
        </div>
      )}
    </div>
  );
}

export function NoticeButton({ children, title, message, variant = "text" }: {
  children: ReactNode;
  title: string;
  message: string;
  variant?: "text" | "primary" | "outline";
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  return (
    <>
      <button type="button" className={`button button-${variant}`} onClick={() => dialog.current?.showModal()}>
        {children}
      </button>
      <dialog ref={dialog} className="notice" aria-labelledby={titleId}>
        <h2 id={titleId}>{title}</h2>
        <p>{message}</p>
        <form method="dialog">
          <button className="button button-outline">Закрыть</button>
        </form>
      </dialog>
    </>
  );
}

export function QuestionForm() {
  const router = useRouter();
  const field = useRef<HTMLTextAreaElement>(null);
  const [feedback, setFeedback] = useState("");

  useEffect(() => {
    if (!field.current || field.current.value) return;
    try {
      field.current.value = sessionStorage.getItem(questionDraftKey) ?? "";
    } catch {
      // The form remains usable when browser storage is unavailable.
    }
  }, []);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const question = field.current?.value.trim();
    if (!question) {
      setFeedback("Введите вопрос о компании.");
      field.current?.focus();
      return;
    }

    try {
      sessionStorage.setItem(questionDraftKey, question);
      router.push("/chat?start=1");
    } catch {
      setFeedback("Браузер не смог сохранить черновик. Вопрос остаётся в поле ввода.");
    }
  }

  return (
    <>
      <form className="question" onSubmit={submit}>
        <textarea
          ref={field}
          id="question"
          className="field"
          aria-label="Вопрос о компании"
          aria-describedby="question-feedback"
          maxLength={4000}
          placeholder="Введите ИНН, ОГРН, название компании или ФИО предпринимателя..."
          onChange={() => setFeedback("")}
        />
        <button className="button button-primary send" aria-label="Отправить вопрос"><Arrow /></button>
      </form>
      <p id="question-feedback" className="status error" role="status">{feedback}</p>
      <div className="templates">
        <p>Не знаешь с чего начать? Попробуй эти шаблоны:</p>
        <div>
          {templates.map(([label, question]) => (
            <button key={label} type="button" className="button button-primary" onClick={() => {
              if (!field.current) return;
              field.current.value = question;
              field.current.focus();
              setFeedback("");
            }}>{label}</button>
          ))}
        </div>
      </div>
    </>
  );
}
