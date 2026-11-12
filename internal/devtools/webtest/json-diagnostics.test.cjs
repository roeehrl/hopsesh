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

test("the native IPC decoder retains a failed read that bypasses JSON.parse", t => {
  const { result, records } = run(t, `
    require('node:child_process').fork('-e', ["require('node:fs').writeSync(3,Buffer.from([10]))"], {silent:true});
  `);
  assert.equal(result.status, 1, result.stderr);
  assert.match(result.stderr, /SyntaxError/);
  const ipc = records.find(r => r.kind === "ipc-read-failure");
  assert.ok(ipc);
  assert.match(ipc.stack, /parseChannelMessages/);
  assert.equal(ipc.rawRead.bytes, 1);
  assert.equal(ipc.rawRead.headHex, "0a");
  assert.equal(records.at(-1).ipcFailure.rawRead.headHex, "0a");
  assert.equal(records.at(-1).parseFailure, undefined);
});

test("IPC diagnostics retain the buffered prefix of a fragmented invalid frame", t => {
  const input = '{"text":"' + "x".repeat(65536);
  const script = `
    const fs=require('node:fs');
    const bytes=Buffer.from(${JSON.stringify(input)});let offset=0;
    function send(){
      try {offset+=fs.writeSync(3,bytes,offset,bytes.length-offset);}
      catch(error){if(error.code!=='EAGAIN')throw error;}
      if(offset<bytes.length)setImmediate(send);
      else setTimeout(()=>fs.writeSync(3,Buffer.from([10])),30);
    }send();
  `;
  const { result, records } = run(t, `require('node:child_process').fork('-e',[${JSON.stringify(script)}],{silent:true});`);
  assert.equal(result.status, 1, result.stderr);
  const ipc = records.find(r => r.kind === "ipc-read-failure");
  assert.ok(ipc);
  const prefixBytes = ipc.bufferedJsonPrefix.bytes;
  assert.ok(prefixBytes > 0 && prefixBytes <= Buffer.byteLength(input));
  const prefix = Buffer.from(input).subarray(0,prefixBytes);
  const read = Buffer.from(input.slice(prefixBytes) + "\n");
  assert.equal(ipc.bufferedJsonPrefix.sha256, crypto.createHash("sha256").update(prefix).digest("hex"));
  assert.equal(ipc.bufferedJsonPrefix.headHex, prefix.subarray(0,1024).toString("hex"));
  assert.equal(ipc.rawRead.bytes,read.length);
  assert.equal(ipc.rawRead.sha256,crypto.createHash("sha256").update(read).digest("hex"));
});

test("large valid Unicode IPC messages retain their contents and order without diagnostics", t => {
  const { result, records } = run(t, `
    const assert=require('node:assert/strict');
    const c=require('node:child_process').fork('-e',['process.on("message",m=>process.send(m))'],{silent:true});
    const inputs=[{i:0,text:'😀'.repeat(100000)},{i:1,text:'x'.repeat(1048577)},{i:2,text:'last'}];
    let i=0;c.on('message',m=>{assert.deepEqual(m,inputs[i++]);if(i===inputs.length)c.disconnect();});
    for(const input of inputs)c.send(input);
  `);
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(records, []);
});

test("failed diagnostic I/O cannot mask a native IPC parser exception", t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hopsesh-json-ipc-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir,"file");fs.writeFileSync(file,"occupied");
  const { result } = run(t, `require('node:child_process').fork('-e',["require('node:fs').writeSync(3,Buffer.from([10]))"],{silent:true});`,file);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /SyntaxError/);
  assert.doesNotMatch(result.stderr, /EEXIST|ENOTDIR/);
});

function workerControl(t, body) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "hopsesh-json-worker-"));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const diagnostics = path.join(dir, "diagnostics");
  const fixture = path.join(dir, "worker.spec.js");
  fs.writeFileSync(fixture, `
    const {test} = require(${JSON.stringify(require.resolve("@playwright/test"))});
    test('uncaught parser control', () => {
      ${body}
      return new Promise(() => {});
    });
  `);
  const config = path.join(dir, "playwright.config.cjs");
  fs.writeFileSync(config, `module.exports = {testDir:${JSON.stringify(dir)},outputDir:${JSON.stringify(path.join(dir,"results"))},workers:1,retries:0,timeout:3000,reporter:'line'};`);
  const result = spawnSync(process.execPath, ["--require", preload, require.resolve("@playwright/test/cli"), "test", "--config", config], {
    encoding: "utf8", timeout: 15000, env: { ...process.env, WEBTEST_JSON_DIAGNOSTICS: diagnostics },
  });
  const records = fs.readdirSync(diagnostics).flatMap(name => fs.readFileSync(path.join(diagnostics,name),"utf8").trim().split("\n").map(line => JSON.parse(line)));
  return {result,records,fixture};
}

test("the actual Playwright worker inherits the recorder and still fails on an uncaught parser error", t => {
  const {result,records,fixture} = workerControl(t,"process.nextTick(() => JSON.parse('{'));");
  assert.equal(result.status, 1, result.stdout + result.stderr);
  assert.match(result.stdout + result.stderr, /SyntaxError/);
  const failure = records.find(r => r.kind === "json-parse-failure");
  assert.ok(failure);
  assert.equal(failure.inputHead, "{");
  assert.ok(failure.stack.includes(fixture));
  assert.ok(records.some(r => r.kind === "uncaught-exception" && r.pid === failure.pid));
});

test("the actual Playwright worker retains native IPC bytes and fails without masking the exception", t => {
  const {result,records} = workerControl(t,`require('node:child_process').fork('-e',["require('node:fs').writeSync(3,Buffer.from([10]))"],{silent:true});`);
  assert.equal(result.status,1,result.stdout+result.stderr);
  assert.match(result.stdout+result.stderr,/SyntaxError/);
  const ipc=records.find(r=>r.kind==="ipc-read-failure");
  assert.ok(ipc);
  assert.match(ipc.stack,/parseChannelMessages/);
  assert.equal(ipc.rawRead.headHex,"0a");
  assert.ok(records.some(r=>r.kind==="uncaught-exception" && r.pid===ipc.pid && r.ipcFailure?.rawRead.headHex==="0a"));
});
