export async function readBody(stream: ReadableStream<Uint8Array>, limit: number) {
  const chunks: Uint8Array[] = [];
  let size = 0;
  const reader = stream.getReader();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) return Buffer.concat(chunks, size);
      size += value.length;
      if (size > limit) {
        void reader.cancel().catch(() => {});
        return;
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
}

export async function readJSON(request: Request, limit: number): Promise<unknown> {
  if (!request.body) return null;
  try {
    const body = await readBody(request.body, limit);
    if (!body) return undefined;
    return JSON.parse(new TextDecoder().decode(body));
  } catch {
    return null;
  }
}
