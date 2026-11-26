import assert from "node:assert/strict";
import { test } from "node:test";
import { collectChatHistory } from "./chat-history.ts";
import { chatMarkdown } from "./chat-markdown.ts";

const created_at = "2026-11-27T09:00:00Z";
const dialogue = { id: "3", created_at, updated_at: created_at };
const turn = (id, question = id, answer = { summary: "Ответ" }) => ({ id, question, answer, created_at });

test("exports every history page in chronological order with 64-bit IDs", async () => {
  const oldest = turn("9223372036854775799");
  const newer = turn("9223372036854775800");
  const newest = turn("9223372036854775801");
  const result = await collectChatHistory(async (before) => before
    ? { dialogue, messages: [oldest] }
    : { dialogue, messages: [newest, newer], next_before_id: newer.id });
  assert.deepEqual(result.messages, [oldest, newer, newest]);
});

test("recovery skips repeated questions at or before the persisted boundary", async () => {
  const question = "Тот же вопрос";
  const boundary = turn("99", question);
  const latest = turn("101", "Другой вопрос из второй вкладки");
  const pending = { ...dialogue, candidates: ["reg:1010101010101"] };
  const result = await collectChatHistory(async (before) => before
    ? { dialogue, messages: [boundary, turn("97", question)] }
    : { dialogue: pending, messages: [latest], next_before_id: latest.id }, boundary.id);
  assert.deepEqual(result.messages, [latest]);
  assert.deepEqual(result.dialogue.candidates, pending.candidates);
});

test("failed or inconsistent history pages reject instead of producing partial exports", async () => {
  const first = { dialogue, messages: [turn("2")], next_before_id: "2" };
  await assert.rejects(collectChatHistory(async (before) => {
    if (before) throw new Error("unavailable");
    return first;
  }), /unavailable/);
  await assert.rejects(collectChatHistory(async () => first), /invalid_history_order/);
});

test("Markdown includes the dated source, excludes unsafe URLs and escapes answer markup", () => {
  const current = turn("1", "Запрос <script>", {
    summary: "## Сводка\n![изображение](https://example.test/image)\n<img>",
    records: [
      { source_ref: "fns", source: { publisher: "ФНС", url: "https://example.test/tax" }, as_of: "2025-12-31" },
      { source_ref: "other", source: { url: "javascript:alert(1)" }, as_of: null },
    ],
  });
  const output = chatMarkdown("Диалог", [current]);
  assert.match(output, /ФНС.*<https:\/\/example\.test\/tax>.*2025-12-31/);
  assert.match(output, /other.*дата актуальности не указана/);
  assert.match(output, /## Сводка/);
  assert.doesNotMatch(output, /javascript:|!\[|<img>|<script>/);
});
