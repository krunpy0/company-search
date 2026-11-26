import type { ReactNode } from "react";
import Link from "next/link";
import { productName } from "./product";
import "./system-screen.css";

export function SystemScreen({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="system-screen">
      <Link className="brand" href="/" aria-label={`${productName}, главная`}>{productName}</Link>
      <div className="system-content">
        <h1>{title}</h1>
        {children}
      </div>
    </main>
  );
}
