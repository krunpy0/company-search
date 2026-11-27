import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import ts from "typescript";

function loadTestModule(file) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
  });
  return import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);
}

const [{ readJSON }, { forwardedClientIP }] = await Promise.all([
  loadTestModule("./request-body.ts"), loadTestModule("./client-ip.ts"),
]);

test("parses JSON at the byte limit and cancels an oversized stream", async () => {
  const url = "http://localhost/";
  const data = '{"text":"я"}';
  const size = Buffer.byteLength(data);
  assert.deepEqual(await readJSON(new Request(url, { method: "POST", body: data }), size), { text: "я" });
  assert.deepEqual(await readJSON(new Request(url, { method: "POST", body: `\uFEFF${data}` }), size + 3), { text: "я" });
  assert.equal(await readJSON(new Request(url, { method: "POST", body: "{" }), size), null);

  let canceled = false;
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(Buffer.from(data.slice(0, 4)));
      controller.enqueue(Buffer.from(data.slice(4)));
    },
    cancel() { canceled = true; },
  });
  const request = new Request(url, { method: "POST", body: stream, duplex: "half" });
  assert.equal(await readJSON(request, size - 1), undefined);
  assert.equal(canceled, true);
});

test("only forwards a single address from the configured ingress header", (t) => {
  const key = "COMPANY_TRUST_PROXY_CLIENT_IP";
  const previous = process.env[key];
  t.after(() => {
    if (previous === undefined) delete process.env[key];
    else process.env[key] = previous;
  });

  const headers = new Headers({ "X-Real-IP": "192.0.2.1", "X-Forwarded-For": "192.0.2.2" });
  delete process.env[key];
  assert.equal(forwardedClientIP(headers), undefined);
  process.env[key] = "true";
  assert.equal(forwardedClientIP(headers), "192.0.2.1");
  headers.set("X-Real-IP", "2001:db8::1");
  assert.equal(forwardedClientIP(headers), "2001:db8::1");
  headers.append("X-Real-IP", "192.0.2.2");
  assert.equal(forwardedClientIP(headers), undefined);
  headers.delete("X-Real-IP");
  assert.equal(forwardedClientIP(headers), undefined);
});
