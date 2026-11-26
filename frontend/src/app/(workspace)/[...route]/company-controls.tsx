"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import Link from "next/link";

function titleKey(id: string) {
  return `company-title:${id}`;
}

function subscribeEmpty() {
  return () => {};
}

export function CompanyResultLink({
  href,
  id,
  name,
  children,
}: {
  href: string;
  id: string;
  name?: string;
  children: React.ReactNode;
}) {
  return (
    <Link
      href={href}
      prefetch={false}
      onClick={() => {
        if (!name) return;
        try { sessionStorage.setItem(titleKey(id), name); } catch { /* Storage may be disabled. */ }
      }}
    >
      {children}
    </Link>
  );
}

export function CompanyTitleHeading({
  id,
  serverTitle,
  identifier,
}: {
  id: string;
  serverTitle: string;
  identifier: string;
}) {
  const hasServerTitle = serverTitle !== identifier;

  useEffect(() => {
    if (!hasServerTitle) return;
    try { sessionStorage.setItem(titleKey(id), serverTitle); } catch { /* Storage may be disabled. */ }
  }, [hasServerTitle, id, serverTitle]);

  const cachedTitle = useSyncExternalStore(
    subscribeEmpty,
    () => {
      try { return sessionStorage.getItem(titleKey(id)); } catch { return null; }
    },
    () => null,
  );

  const title = hasServerTitle ? serverTitle : cachedTitle || serverTitle;

  return (
    <div>
      <h1>{title}</h1>
      {title !== identifier && <p>{identifier}</p>}
    </div>
  );
}

export function CopyValue({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy() {
    window.clearTimeout(timer.current);
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      timer.current = window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return (
    <span className="company-copy-row">
      <span>{value}</span>
      <button
        type="button"
        className="company-copy-btn"
        aria-label={copied ? `${label}: скопировано` : `Скопировать ${label}: ${value}`}
        title={copied ? "Скопировано" : `Скопировать ${label}`}
        data-copied={copied || undefined}
        onClick={() => void copy()}
      >
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          {copied
            ? <path d="M5 12.5 9.5 17 19 7" />
            : <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></>}
        </svg>
        {copied && <span className="company-copy-label">Скопировано</span>}
      </button>
    </span>
  );
}
