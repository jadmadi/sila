import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const bridgePath = process.argv[2];
const fakeSilaPath = process.argv[3];
const logPath = process.argv[4];

const mod = await import(pathToFileURL(path.resolve(bridgePath)).href);
const { SilaBridge, buildToolShape, buildSignal } = mod;

// 1. Test buildToolShape privacy: sorted keys + types only, never values/paths/secrets
const shape = buildToolShape({
  secretToken: "sk-live-SUPER-SECRET-12345",
  filePath: "/etc/shadow",
  count: 42,
  flags: ["a", "b"],
  emptyVal: null,
});
assert.equal(
  shape,
  "count:number,emptyVal:null,filePath:string,flags:array,secretToken:string"
);
assert.equal(shape.includes("sk-live"), false);
assert.equal(shape.includes("/etc/shadow"), false);

// 2. Test buildSignal with missing session_id -> unbound
const unboundSig = buildSignal({}, "tool-before", "tool.execute.before", { tool: "Bash" }, { args: { cmd: "ls /secret" } });
assert.equal(unboundSig.session_id, "unbound");
assert.equal(unboundSig.tool_name, "Bash");
assert.equal(unboundSig.tool_shape, "cmd:string");
assert.equal(JSON.stringify(unboundSig).includes("/secret"), false);

// 3. Setup plugin instance & test double-load protection
const ctx = {
  sessionID: "sess-bridge-1",
  directory: "/workspace/demo",
  project: { id: "proj-demo" },
};
const hooks1 = await SilaBridge(ctx);
const hooks2 = await SilaBridge(ctx);
assert.equal(typeof hooks1.event, "function");
assert.deepEqual(hooks2, {}, "second load on same ctx must be a no-op");

process.env.SILA_HOOK_BIN = fakeSilaPath;
process.env.FAKE_SILA_LOG = logPath;

// 4. Lifecycle: session.created -> session-start
// Freeze the clock so the dedupe window cannot straddle a second boundary.
const realNow = Date.now;
Date.now = () => 1762300000000;
process.env.FAKE_SILA_MODE = "allow";
await hooks1.event({
  event: {
    type: "session.created",
    properties: { sessionID: "sess-bridge-1" },
  },
});

// 5. Dedupe: second identical session.created in the same second is suppressed
const countAfterFirst = fs.readFileSync(logPath, "utf8").trim().split("\n").length;
await hooks1.event({
  event: {
    type: "session.created",
    properties: { sessionID: "sess-bridge-1" },
  },
});
const countAfterDuplicate = fs.readFileSync(logPath, "utf8").trim().split("\n").length;
assert.equal(countAfterDuplicate, countAfterFirst, "dedupe must suppress duplicate event within same second");
Date.now = realNow;

// 6. tool.execute.before allow -> attaches bounded additionalContext
const toolOutAllow = {};
await hooks1["tool.execute.before"](
  { tool: "Edit", sessionID: "sess-bridge-1", args: { file: "secret.txt", content: "top-secret-value" } },
  toolOutAllow
);
assert.equal(toolOutAllow.additionalContext, "Allowed by Sila");

// 7. tool.execute.before deny -> throws Error with reason + recovery
process.env.FAKE_SILA_MODE = "deny";
let threwDeny = false;
try {
  await hooks1["tool.execute.before"](
    { tool: "Write", sessionID: "sess-bridge-1", args: { path: "foo.go" } },
    {}
  );
} catch (err) {
  threwDeny = true;
  assert.match(String(err.message), /goal contract denied tool/);
  assert.match(String(err.message), /sila goals claim demo-goal/);
}
assert.equal(threwDeny, true, "tool.execute.before must throw on core deny");

// 8. tool.execute.after on deny -> advisory only, NEVER throws
await hooks1["tool.execute.after"](
  { tool: "Write", sessionID: "sess-bridge-1", args: { path: "foo.go" } },
  {}
);

// 9. session.idle on deny -> never throws, returns unsupported decision
const idleRes = await hooks1.event({
  event: {
    type: "session.idle",
    properties: { sessionID: "sess-bridge-1" },
  },
});
assert.equal(idleRes.decision, "unsupported");

// 10. experimental.session.compacting on warn -> injects context into output.context
process.env.FAKE_SILA_MODE = "warn";
const compactOut = { context: [] };
await hooks1["experimental.session.compacting"](
  { sessionID: "sess-bridge-1" },
  compactOut
);
assert.equal(compactOut.context.length, 1);
assert.match(compactOut.context[0], /Re-anchor: active goal demo-goal/);

// 11. session.error without session_id -> returns unsupported without calling sila
const unboundCtx = {};
const unboundHooks = await SilaBridge(unboundCtx);
const errRes = await unboundHooks.event({
  event: { type: "session.error", properties: { error_category: "crash" } },
});
assert.equal(errRes.decision, "unsupported");
assert.equal(errRes.reason_code, "HARNESS_UNSUPPORTED");

// 12. Missing sila binary -> fails open without throwing
process.env.SILA_HOOK_BIN = "/nonexistent/sila-missing-bin";
await hooks1["tool.execute.before"](
  { tool: "Read", sessionID: "sess-bridge-2", args: { file: "README.md" } },
  {}
);

// Verify recorded signals in logPath never contain raw secret values
const loggedLines = fs.readFileSync(logPath, "utf8").trim().split("\n");
assert.equal(loggedLines.length >= 5, true);
for (const line of loggedLines) {
  assert.equal(line.includes("top-secret-value"), false, "stdin signal must never leak argument values");
  assert.equal(line.includes("secret.txt"), false, "stdin signal must never leak file paths");
  const entry = JSON.parse(line);
  const sig = JSON.parse(entry.stdin);
  assert.equal(sig.schema_version, "sila/harness-signal/v1");
}

console.log("BRIDGE_RUNNER_OK");
