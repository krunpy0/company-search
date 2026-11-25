import { Suspense } from "react";
import { AuthForm } from "../auth-form";

async function LoginNotice({ searchParams }: PageProps<"/login">) {
  const { registered, expired } = await searchParams;
  const notice = registered === "1" ? "Аккаунт создан. Войдите с указанным email и паролем."
    : expired === "1" ? "Сессия закончилась. Войдите снова. Черновик останется в этой вкладке."
    : undefined;
  return notice ? <p className="auth-notice">{notice}</p> : null;
}

export default function LoginPage(props: PageProps<"/login">) {
  return <AuthForm mode="login"><Suspense><LoginNotice {...props} /></Suspense></AuthForm>;
}
