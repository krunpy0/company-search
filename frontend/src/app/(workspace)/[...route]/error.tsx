"use client";

import { SystemUnavailable } from "@/app/system-unavailable";

export default function WorkspaceError({ retry }: { retry: () => void }) {
  return <SystemUnavailable retry={retry} />;
}
