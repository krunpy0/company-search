import type { Metadata } from "next";
import Link from "next/link";
import { SystemScreen } from "../system-screen";

const title = "Пользовательское соглашение";
export const metadata: Metadata = { title };

export default function TermsPage() {
  return (
    <SystemScreen title={title}>
      <p>Пользовательское соглашение пока не опубликовано.</p>
      <Link className="button button-outline" href="/help#documents">К списку документов</Link>
    </SystemScreen>
  );
}
