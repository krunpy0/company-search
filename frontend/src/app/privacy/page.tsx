import type { Metadata } from "next";
import Link from "next/link";
import { SystemScreen } from "../system-screen";

const title = "Политика конфиденциальности";
export const metadata: Metadata = { title };

export default function PrivacyPage() {
  return (
    <SystemScreen title={title}>
      <p>Политика конфиденциальности пока не опубликована.</p>
      <Link className="button button-outline" href="/help#documents">К списку документов</Link>
    </SystemScreen>
  );
}
