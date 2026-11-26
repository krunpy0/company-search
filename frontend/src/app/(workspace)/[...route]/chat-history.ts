import type { ChatHistory, Turn } from "./chat-api";

function after(id: string, boundary: string) {
  return id.length === boundary.length ? id > boundary : id.length > boundary.length;
}

export async function collectChatHistory(readPage: (before?: string) => Promise<ChatHistory>, boundary?: string) {
  const first = await readPage();
  const messages: Turn[] = [];
  let page = first;
  let before: string | undefined;

  for (;;) {
    for (const message of page.messages) {
      const previous = messages.at(-1)?.id ?? before;
      if (previous && !after(previous, message.id)) throw new Error("invalid_history_order");
      if (boundary && !after(message.id, boundary)) return { ...first, messages: messages.reverse() };
      messages.push(message);
    }

    before = page.next_before_id;
    if (!before) return { ...first, messages: messages.reverse() };
    if (before !== page.messages.at(-1)?.id) throw new Error("invalid_history_cursor");
    page = await readPage(before);
    if (page.dialogue.id !== first.dialogue.id || !page.messages.length) throw new Error("incomplete_history");
  }
}
