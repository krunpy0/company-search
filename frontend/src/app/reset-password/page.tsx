"use client";

import { useState, useSyncExternalStore, type FormEvent } from "react";
import Link from "next/link";
import { authMessage, passwordFits } from "@/lib/auth-feedback";
import { AuthScreen } from "../auth-screen";

function subscribeToHash(onChange: () => void) {
  window.addEventListener("hashchange", onChange);
  return () => window.removeEventListener("hashchange", onChange);
}

function readToken() {
  return new URLSearchParams(window.location.hash.slice(1)).get("token");
}

const resetUnavailable = "Восстановление пароля временно недоступно. Попробуйте позже.";

type ResetState =
  | { kind: "idle" | "pending" | "sent" | "invalid-link" }
  | { kind: "error"; message: string };

export default function ResetPasswordPage() {
  const token = useSyncExternalStore(subscribeToHash, readToken, () => null);
  const [state, setState] = useState<ResetState>({ kind: "idle" });
  const confirming = token !== null;
  const action = confirming ? "/api/auth/confirm-password-reset" : "/api/auth/request-password-reset";
  const invalidLink = state.kind === "invalid-link" || (confirming && !/^[\w-]{43}$/.test(token));

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state.kind === "pending" || invalidLink) return;

    const form = event.currentTarget;
    const fields = new FormData(form);
    const password = String(fields.get("password") ?? "");
    if (confirming && !passwordFits(password)) {
      setState({ kind: "error", message: "Для пароля из латинских букв и цифр нужно от 12 до 72 символов." });
      form.querySelector<HTMLInputElement>("#reset-password")?.focus();
      return;
    }
    if (confirming && password !== fields.get("confirmation")) {
      setState({ kind: "error", message: "Пароли не совпадают." });
      form.querySelector<HTMLInputElement>("#reset-confirmation")?.focus();
      return;
    }

    setState({ kind: "pending" });
    try {
      const response = await fetch(action, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(confirming ? { token, password } : { email: String(fields.get("email") ?? "").trim() }),
      });
      if (confirming && response.status === 204) {
        window.location.replace("/login?reset=1");
        return;
      }
      if (!confirming && response.status === 202) {
        setState({ kind: "sent" });
        return;
      }
      const error: unknown = await response.json();
      if (confirming && typeof error === "object" && error && "code" in error && error.code === "invalid_reset_token") {
        setState({ kind: "invalid-link" });
        return;
      }
      const message = response.status === 429 ? authMessage(error)
        : response.status === 400 && !confirming ? "Проверьте адрес электронной почты."
        : resetUnavailable;
      setState({ kind: "error", message });
    } catch {
      setState({ kind: "error", message: resetUnavailable });
    }
  }

  return (
    <AuthScreen title={confirming ? "Установить новый пароль" : "Восстановление пароля"}>
      {invalidLink ? (
        <>
          <p className="auth-notice" role="alert">Ссылка недействительна или срок её действия истёк.</p>
          <p className="auth-switch">
            <Link href="/reset-password" onNavigate={(event) => {
              event.preventDefault();
              setState({ kind: "idle" });
              window.location.replace("/reset-password");
            }}>Запросить новую ссылку</Link>
          </p>
        </>
      ) : state.kind === "sent" ? (
        <p className="auth-notice" role="status">
          Если на этот email зарегистрирован аккаунт, мы отправим ссылку. Проверьте входящие и папку «Спам».
        </p>
      ) : (
        <form method="post" action={action} onSubmit={submit} key={action}>
          {confirming ? (
            <>
              <div className="auth-field">
                <label htmlFor="reset-password">Новый пароль</label>
                <input className="field" id="reset-password" name="password" type="password" autoComplete="new-password" aria-describedby="reset-password-hint" required />
                <p id="reset-password-hint">Для пароля из латинских букв и цифр — от 12 до 72 символов.</p>
              </div>
              <div className="auth-field">
                <label htmlFor="reset-confirmation">Повторите пароль</label>
                <input className="field" id="reset-confirmation" name="confirmation" type="password" autoComplete="new-password" required />
              </div>
            </>
          ) : (
            <div className="auth-field">
              <label htmlFor="reset-email">Email</label>
              <input className="field" id="reset-email" name="email" type="email" autoComplete="email" maxLength={254} required />
            </div>
          )}
          {state.kind === "error" && <p className="auth-error" role="alert">{state.message}</p>}
          <button className="button button-primary" disabled={state.kind === "pending"}>
            {state.kind === "pending" ? "Подождите…" : confirming ? "Сохранить пароль" : "Отправить ссылку"}
          </button>
        </form>
      )}
      <p className="auth-switch"><Link href="/login">Вернуться ко входу</Link></p>
    </AuthScreen>
  );
}
