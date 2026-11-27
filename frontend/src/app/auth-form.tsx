"use client";

import { useState } from "react";
import type { FormEvent, ReactNode } from "react";
import Link from "next/link";
import { authMessage, authMessages, passwordFits } from "@/lib/auth-feedback";
import { AuthScreen } from "./auth-screen";

export function AuthForm({ mode, children }: { mode: "register" | "login"; children?: ReactNode }) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const registering = mode === "register";
  const title = registering ? "Создать аккаунт" : "Войти в аккаунт";

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;

    const fields = new FormData(event.currentTarget);
    const email = String(fields.get("email") ?? "").trim();
    const password = String(fields.get("password") ?? "");
    if (!passwordFits(password)) {
      setError(authMessages.invalid_input);
      event.currentTarget.querySelector<HTMLInputElement>("#auth-password")?.focus();
      return;
    }

    setError("");
    setPending(true);
    try {
      const response = await fetch(`/api/auth/${mode}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (response.status === 204) {
        window.location.replace(registering ? "/login?registered=1" : "/chat");
        return;
      }
      setError(authMessage(await response.json()));
    } catch {
      setError(authMessages.unavailable);
    }
    setPending(false);
  }

  return (
    <AuthScreen title={title}>
      {children}
      <form method="post" action={`/api/auth/${mode}`} onSubmit={submit}>
        <div className="auth-field">
          <label htmlFor="auth-email">Email</label>
          <input id="auth-email" className="field" type="email" name="email" autoComplete="email" maxLength={254} required />
        </div>
        <div className="auth-field">
          <label htmlFor="auth-password">Пароль</label>
          <input id="auth-password" className="field" type="password" name="password" autoComplete={registering ? "new-password" : "current-password"} aria-describedby={registering ? "auth-password-hint" : undefined} required />
          {registering && <p id="auth-password-hint">Для пароля из латинских букв и цифр — от 12 до 72 символов.</p>}
        </div>
        {error && <p className="auth-error" role="alert">{error}</p>}
        <button className="button button-primary" disabled={pending}>{pending ? "Подождите…" : title}</button>
      </form>
      {!registering && <p className="auth-switch"><Link href="/reset-password">Забыли пароль?</Link></p>}
      <p className="auth-switch">
        {registering ? "Уже есть аккаунт?" : "Нет аккаунта?"}{" "}
        <Link href={registering ? "/login" : "/register"}>{registering ? "Войти" : "Зарегистрироваться"}</Link>
      </p>
      {registering && <p className="auth-switch">
        <Link href="/privacy">Политика конфиденциальности</Link> · <Link href="/terms">Пользовательское соглашение</Link>
      </p>}
    </AuthScreen>
  );
}
