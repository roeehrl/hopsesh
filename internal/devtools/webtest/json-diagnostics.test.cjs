const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");
const preload = path.join(__dirname, "json-diagnostics.cjs");

function run(t, script, directory) {
  const dir = directory || fs.mkdtempSync(path.join(os.tmpdir(), "hopsesh-json-diagnostics-"));
  if (!directory) t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const result = spawnSync(process.execPath, ["--require", preload, "-e", script], {
    encoding: "utf8", env: { ...process.env, WEBTEST_JSON_DIAGNOSTICS: dir },
  });
  const records = fs.existsSync(dir) && fs.statSync(dir).isDirectory()
    ? fs.readdirSync(dir).flatMap(name => fs.readFileSync(path.join(dir, name), "utf8").trim().split("\n").filter(Boolean).map(line => JSON.parse(line))) : [];
  return { result, records };
}

test("a fatal parser failure retains its stack and bounded input, and still exits unsuccessfully", t => {
  const input = '{"text":"' + "x".repeat(65536);
  const { result, records } = run(t, `JSON.parse(${JSON.stringify(input)})`);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /SyntaxError/);
  assert.deepEqual(records.map(r => r.kind), ["json-parse-failure", "uncaught-exception"]);
  const record = records[0];
  assert.equal(record.bytes, Buffer.byteLength(input));
  assert.equal(record.sha256, crypto.createHash("sha256").update(input).digest("hex"));
  assert.equal(record.inputHead, input.slice(0, 1024));
  assert.equal(record.inputTail, input.slice(-1024));
  assert.match(record.stack, /\[eval\]/);
});

test("instrumentation preserves valid parsing, revivers, and one-time input coercion", t => {
  const { result, records } = run(t, `
    const assert = require('node:assert/strict');
    assert.deepEqual(JSON.parse('{"n":2}', (key,value) => key === 'n' ? value + 1 : value), {n:3});
    let coercions = 0;
    const object = {toString(){coercions++;return '{'}};
    assert.throws(() => JSON.parse(object), SyntaxError);
    assert.equal(coercions, 1);
    const sentinel = new Error('reviver failure');
    Object.defineProperty(sentinel, 'stack', {get(){throw new Error('bad stack getter')}});
    try { JSON.parse('{}', () => {throw sentinel}); assert.fail('reviver returned'); }
    catch(error){assert.equal(error, sentinel);}
  `);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(records.length, 1);
  assert.equal(records[0].inputType, "object");
  assert.equal(records[0].inputHead, undefined);
});

test("diagnostic write failures cannot mask a parser failure", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hopsesh-json-diagnostics-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, "file"); fs.writeFileSync(file, "occupied");
  const { result } = run(t, "JSON.parse('{')", file);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /SyntaxError/);
  assert.doesNotMatch(result.stderr, /EEXIST|ENOTDIR/);
});

test("repeated caught failures cannot grow an unbounded diagnostic file", t => {
  const { result, records } = run(t, "for(let i=0;i<100;i++){try{JSON.parse('{')}catch{}};");
  assert.equal(result.status, 0, result.stderr);
  assert.equal(records.length, 16);
});

test("caught failures filling the cap cannot hide the fatal input and caller", t => {
  const { result, records } = run(t, "for(let i=0;i<100;i++){try{JSON.parse('{')}catch{}};JSON.parse('[');");
  assert.equal(result.status, 1);
  assert.equal(records.length, 17);
  assert.equal(records.at(-1).kind, "uncaught-exception");
  assert.equal(records.at(-1).parseFailure.inputHead, "[");
  assert.match(records.at(-1).parseFailure.stack, /\[eval\]/);
});

test("the actual Playwright worker inherits the recorder and still fails on an uncaught parser error", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hopsesh-json-worker-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const diagnostics = path.join(dir, "diagnostics");
  const fixture = path.join(dir, "worker.spec.js");
  fs.writeFileSync(fixture, `
    const {test} = require(${JSON.stringify(require.resolve("@playwright/test"))});
    test('uncaught parser control', () => {
      process.nextTick(() => JSON.parse('{'));
      return new Promise(() => {});
    });
  `);
  const config = path.join(dir, "playwright.config.cjs");
  fs.writeFileSync(config, `module.exports = {testDir:${JSON.stringify(dir)},outputDir:${JSON.stringify(path.join(dir,"results"))},workers:1,retries:0,timeout:3000,reporter:'line'};`);
  const result = spawnSync(process.execPath, ["--require", preload, require.resolve("@playwright/test/cli"), "test", "--config", config], {
    encoding: "utf8", timeout: 15000, env: { ...process.env, WEBTEST_JSON_DIAGNOSTICS: diagnostics },
  });
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stdout + result.stderr, /SyntaxError/);
  const records = fs.readdirSync(diagnostics).flatMap(name => fs.readFileSync(path.join(diagnostics,name),"utf8").trim().split("\n").map(line => JSON.parse(line)));
  const failure = records.find(r => r.kind === "json-parse-failure");
  assert.ok(failure);
  assert.equal(failure.inputHead, "{");
  assert.ok(failure.stack.includes(fixture));
  assert.ok(records.some(r => r.kind === "uncaught-exception" && r.pid === failure.pid));
});
