import type { ReactNode } from "react";
import Link from "next/link";
import { productName } from "./product";
import "./auth.css";

export function AuthScreen({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="auth-screen">
      <Link className="brand" href="/landing" prefetch={false} aria-label={`${productName}, на лендинг`}>{productName}</Link>
      <section className="auth-card" aria-labelledby="auth-title">
        <h1 id="auth-title">{title}</h1>
        {children}
      </section>
    </main>
  );
}
