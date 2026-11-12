#!/usr/bin/env node
// Fixture driver for the OpenCode 2 plugin definition.
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const bridgePath = process.argv[2];
const fakeSilaPath = process.argv[3];
const logPath = process.argv[4];

process.env.SILA_HOOK_BIN = fakeSilaPath;
process.env.FAKE_SILA_LOG = logPath;

const mod = await import(pathToFileURL(path.resolve(bridgePath)).href);
const { buildToolShape, buildSignal } = mod;

// 1. Tool shape stays privacy-safe: sorted keys and types only.
const shape = buildToolShape({
  secretToken: "sk-live-SUPER-SECRET-12345",
  filePath: "/etc/shadow",
  count: 42,
  flags: ["a", "b"],
  emptyVal: null,
});
assert.equal(shape, "count:number,emptyVal:null,filePath:string,flags:array,secretToken:string");
assert.equal(shape.includes("sk-live"), false);
assert.equal(shape.includes("/etc/shadow"), false);

// 2. Signal builder reads the V2 location and event shape.
const sig = buildSignal(
  { location: { directory: "/tmp/project", project: { id: "proj-v2" } } },
  "tool-before",
  "execute.before",
  { tool: "Write", sessionID: "sess-v2-1", input: { file: "secret.txt", content: "top-secret-value" } },
  { args: { file: "secret.txt", content: "top-secret-value" } },
);
assert.equal(sig.session_id, "sess-v2-1");
assert.equal(sig.project_id, "proj-v2");
assert.equal(sig.cwd, "/tmp/project");
assert.equal(sig.tool_name, "Write");
assert.equal(sig.hook_event_name, "execute.before");
assert.equal(sig.tool_shape, "content:string,file:string");
assert.equal(JSON.stringify(sig).includes("top-secret-value"), false);

// 3. The default export is a V2 definition.
assert.equal(typeof mod.default, "object", "opencode default export must be an object");
assert.equal(mod.default.id, "sila-bridge");
assert.equal(typeof mod.default.setup, "function");
assert.equal("server" in mod.default, false, "OpenCode artifact must not carry a V1 server entrypoint");

// 4. setup registers the V2 hooks and the event subscription.
const registeredTools = new Map();
const registeredSessions = new Map();
let subscribedTypes = null;
const ctx = {
  location: { directory: "/tmp/project", project: { id: "proj-v2" } },
  tool: {
    hook: async (name, fn) => {
      registeredTools.set(name, fn);
      return { dispose: async () => {} };
    },
  },
  session: {
    hook: async (name, fn) => {
      registeredSessions.set(name, fn);
      return { dispose: async () => {} };
    },
  },
  event: {
    subscribe: async function* (opts) {
      subscribedTypes = opts.types;
      yield { type: "session.created", data: { sessionID: "sess-v2-1" } };
    },
  },
};
const cleanup = await mod.default.setup(ctx);
const waitForLog = async (deadline) => {
  while (Date.now() < deadline) {
    if (fs.existsSync(logPath)) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
};
await waitForLog(Date.now() + 3000);

assert.deepEqual([...registeredTools.keys()].sort(), ["execute.after", "execute.before"]);
assert.deepEqual([...registeredSessions.keys()].sort(), ["compaction", "experimental.idle"]);
assert.deepEqual(subscribedTypes, ["session.created"]);
assert.equal(typeof cleanup, "function", "setup must return the subscription cleanup");

// 5. Session start is translated from the event stream.
const afterStart = fs.readFileSync(logPath, "utf8").trim().split("\n");
assert.equal(afterStart.length >= 1, true, "session.created must emit one hook call");
const startEntry = JSON.parse(afterStart[0]);
assert.equal(startEntry.argv.includes("--event=session-start"), true);

// 6. Deny throws with the reason and recovery.
process.env.FAKE_SILA_MODE = "deny";
let denied = false;
try {
  await registeredTools.get("execute.before")({
    tool: "Write",
    sessionID: "sess-v2-1",
    input: { path: "foo.go" },
  });
} catch (err) {
  denied = true;
  assert.match(String(err.message), /goal contract denied tool/);
  assert.match(String(err.message), /sila goals claim demo-goal/);
}
assert.equal(denied, true, "execute.before must throw on core deny");

// 7. Idle on deny warns and never throws.
await registeredSessions.get("experimental.idle")({ sessionID: "sess-v2-1", background: [] });

// 8. Compaction on warn appends the re-anchor to event.system.
process.env.FAKE_SILA_MODE = "warn";
const compact = { sessionID: "sess-v2-1", system: [], messages: [] };
await registeredSessions.get("compaction")(compact);
assert.equal(compact.system.length, 1);
assert.equal(compact.system[0].type, "text");
assert.match(compact.system[0].text, /Re-anchor: active goal demo-goal/);

// 9. Post-tool failure is advisory only.
process.env.FAKE_SILA_MODE = "deny";
await registeredTools.get("execute.after")({
  tool: "Write",
  sessionID: "sess-v2-1",
  input: { path: "foo.go" },
  status: "error",
  error: { message: "boom" },
});

// 10. Missing hook binary fails open without throwing.
process.env.SILA_HOOK_BIN = "/nonexistent/sila-missing-bin";
await registeredTools.get("execute.before")({ tool: "Read", sessionID: "sess-v2-2", input: {} });

await cleanup();

// 11. Recorded signals never contain values or paths from tool arguments.
const lines = fs.readFileSync(logPath, "utf8").trim().split("\n");
assert.equal(lines.length >= 3, true);
for (const line of lines) {
  assert.equal(line.includes("top-secret-value"), false, "stdin signal must never leak argument values");
  assert.equal(line.includes("secret.txt"), false, "stdin signal must never leak file paths");
  const entry = JSON.parse(line);
  assert.equal(entry.argv.includes("--agent=opencode"), true);
  const signal = JSON.parse(entry.stdin);
  assert.equal(signal.schema_version, "sila/harness-signal/v1");
}

console.log("BRIDGE_V2_RUNNER_OK");
