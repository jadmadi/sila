#!/usr/bin/env node
import fs from "node:fs";

let input = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  input += chunk;
});
process.stdin.on("end", () => {
  const logPath = process.env.FAKE_SILA_LOG || "";
  if (logPath) {
    fs.appendFileSync(
      logPath,
      JSON.stringify({ argv: process.argv.slice(2), stdin: input }) + "\n"
    );
  }
  const mode = process.env.FAKE_SILA_MODE || "allow";
  if (mode === "deny") {
    process.stdout.write(
      JSON.stringify({
        decision: "deny",
        reason: "goal contract denied tool",
        reason_code: "CLAIM_MISSING",
        recovery: "sila goals claim demo-goal",
      }) + "\n"
    );
  } else if (mode === "warn") {
    process.stdout.write(
      JSON.stringify({
        decision: "warn",
        reason: "goal contract advisory warning",
        additionalContext: "Remember active goal: demo-goal",
        text: "Re-anchor: active goal demo-goal",
      }) + "\n"
    );
  } else if (mode === "malformed") {
    process.stdout.write("not-valid-json{{{[\n");
  } else {
    process.stdout.write(
      JSON.stringify({
        decision: "allow",
        additionalContext: "Allowed by Sila",
        text: "Compaction re-anchor context",
      }) + "\n"
    );
  }
});
