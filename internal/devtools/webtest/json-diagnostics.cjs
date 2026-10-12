// Browser-test processes only. Preserve the parser's original stack and bounded
// input when a protocol failure would otherwise become a stackless test error.
// Every exception is rethrown; monitoring never handles an uncaught exception.
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const parse = JSON.parse;
const stringify = JSON.stringify;
const directory = process.env.WEBTEST_JSON_DIAGNOSTICS || path.join(__dirname, "test-results", "json-diagnostics");
let records = 0;
let latestFailure;
let fatalRecorded = false;

function record(value, fatal = false) {
  if (!fatal && records++ >= 16) return;
  try {
    fs.mkdirSync(directory, { recursive: true });
    fs.appendFileSync(path.join(directory, `node-${process.pid}.jsonl`), stringify({
      time: new Date().toISOString(), pid: process.pid, ...value,
    }) + "\n", { mode: 0o600 });
  } catch { /* Diagnostic I/O must not replace the original failure. */ }
}

JSON.parse = function (...args) {
  try {
    return Reflect.apply(parse, this, args);
  } catch (error) {
    try {
      const input = args[0];
      // Do not coerce a caller's object again: toString may have side effects.
      const details = typeof input === "string" ? {
        chars: input.length,
        bytes: Buffer.byteLength(input),
        sha256: crypto.createHash("sha256").update(input).digest("hex"),
        inputHead: input.slice(0, 1024),
        inputTail: input.length > 1024 ? input.slice(-1024) : "",
      } : { inputType: typeof input };
      const entry = { kind: "json-parse-failure", ...details, stack: errorStack(error) };
      latestFailure = { error, entry };
      record(entry);
    } catch { /* A caller can throw any value, including an object with hostile getters. */ }
    throw error;
  }
};

function errorStack(error) {
  return (error instanceof Error ? String(error.stack) : typeof error === "string" ? error : typeof error).slice(0, 8192);
}

process.on("uncaughtExceptionMonitor", error => {
  if (fatalRecorded) return;
  fatalRecorded = true;
  try {
    // Reserve one fatal record even if expected caught failures filled the cap.
    record({ kind: "uncaught-exception", stack: errorStack(error),
      parseFailure: latestFailure?.error === error ? latestFailure.entry : undefined,
    }, true);
  } catch {}
});
