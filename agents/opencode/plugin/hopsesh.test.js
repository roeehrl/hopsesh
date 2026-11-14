// Tests of the hopsesh plugin's logic with a fake OpenCode context and a fake notice hook.
// Run with Bun (bun test, in this folder); `go test ./agents/opencode` runs them where Bun
// is installed and skips them otherwise.
import { describe, expect, test } from "bun:test"
import { EventEmitter } from "node:events"
import { create, ruleEffect, setup, EVENTS, NOTICE_KEY } from "./hopsesh.js"

// fakeChild is a notice-hook process that prints answer and exits with code (never, with
// answer null).
function fakeChild(answer, code = 0) {
  const child = new EventEmitter()
  child.stdout = new EventEmitter()
  child.stdin = { input: "", on() {}, end(s) { child.stdin.input = s } }
  child.killed = false
  child.kill = () => {
    child.killed = true
  }
  if (answer !== null) {
    setTimeout(() => {
      if (answer) child.stdout.emit("data", Buffer.from(answer))
      child.emit("close", code)
    }, 1)
  }
  return child
}

// world is a fake context: sessions (id → parent id), and what the plugin did.
function world({ parents = {}, answers = () => '{"block":false}', storage = {}, now } = {}) {
  const w = { spawned: [], put: [], removed: [], stored: storage, logs: [], hooks: {}, subscribed: null }
  w.ctx = {
    location: { directory: "/work/demo" },
    session: {
      get: async ({ sessionID }) => ({ data: { id: sessionID, ...(parents[sessionID] ? { parentID: parents[sessionID] } : {}) } }),
      hook: async (name, cb) => {
        w.hooks["session." + name] = cb
      },
      instructions: {
        entry: {
          put: async (e) => {
            w.put.push(e)
          },
          remove: async (e) => {
            w.removed.push(e)
          },
        },
      },
    },
    tool: {
      hook: async (name, cb) => {
        w.hooks["tool." + name] = cb
      },
    },
    permission: {
      hook: async (name, cb) => {
        w.hooks["permission." + name] = cb
      },
    },
    storage: {
      get: async (k) => w.stored[k],
      set: async (k, v) => {
        w.stored[k] = v
      },
    },
    event: {
      subscribe: (opts) => {
        w.subscribed = opts
        return (async function* () {})()
      },
    },
  }
  w.deps = {
    spawn: (cmd, args, opts) => {
      const child = fakeChild(answers(w.spawned.length))
      w.spawned.push({ cmd, args, opts, child })
      return child
    },
    log: (m) => w.logs.push(m),
    now,
  }
  return w
}

const config = { argv: ["/opt/hopsesh", "notice-hook", "--agent", "opencode", "--profile", ""], timeoutMs: 200 }
const blocked = '{"block":true,"reason":"Blocked by hopsesh. Moved to Claude Code on studio."}'
const tick = () => new Promise((r) => setTimeout(r, 5))

describe("verdicts", () => {
  test("a tool call is refused while the session is blocked, with hopsesh's reason", async () => {
    const w = world({ answers: () => blocked })
    const p = create(w.ctx, config, w.deps)
    await expect(p.beforeTool({ tool: "shell", sessionID: "ses_a" })).rejects.toThrow("Blocked by hopsesh. Moved to Claude Code on studio.")
    expect(w.spawned[0].cmd).toBe("/opt/hopsesh")
    expect(w.spawned[0].args).toEqual(["notice-hook", "--agent", "opencode", "--profile", ""])
    expect(JSON.parse(w.spawned[0].child.stdin.input)).toEqual({ session_id: "ses_a", directory: "/work/demo", event: "UserPromptSubmit" })
  })

  test("the verdict is kept: later calls run no hook while it is fresh", async () => {
    let t = 1000
    const w = world({ answers: () => blocked, now: () => t })
    const p = create(w.ctx, { ...config, cacheMs: 30000 }, w.deps)
    for (let i = 0; i < 3; i++) await expect(p.beforeTool({ sessionID: "ses_a" })).rejects.toThrow()
    expect(w.spawned.length).toBe(1)
    t += 31000
    await expect(p.beforeTool({ sessionID: "ses_a" })).rejects.toThrow()
    expect(w.spawned.length).toBe(2)
  })

  test("a tool call with no verdict yet (a restart, a resumed turn) asks first", async () => {
    const w = world()
    const p = create(w.ctx, config, w.deps)
    await p.beforeTool({ sessionID: "ses_b" })
    expect(w.spawned.length).toBe(1)
    expect(p.verdicts.get("ses_b").block).toBe(false)
  })

  test("a prompt asks again, so a block ends at once", async () => {
    const answers = [blocked, '{"block":false}']
    const w = world({ answers: (i) => answers[i] })
    const p = create(w.ctx, config, w.deps)
    await expect(p.beforeTool({ sessionID: "ses_a" })).rejects.toThrow()
    await p.onPrompt({ sessionID: "ses_a" })
    await p.beforeTool({ sessionID: "ses_a" })
    expect(w.spawned.length).toBe(2)
  })

  test("a subagent's session follows its root session's verdict", async () => {
    const w = world({ parents: { ses_child: "ses_root" }, answers: () => blocked })
    const p = create(w.ctx, config, w.deps)
    await expect(p.beforeTool({ sessionID: "ses_child" })).rejects.toThrow("Blocked by hopsesh")
    expect(JSON.parse(w.spawned[0].child.stdin.input).session_id).toBe("ses_root")
  })

  test("concurrent calls share one check", async () => {
    const w = world({ answers: () => blocked })
    const p = create(w.ctx, config, w.deps)
    const calls = [p.beforeTool({ sessionID: "ses_a" }), p.beforeTool({ sessionID: "ses_a" }), p.onPrompt({ sessionID: "ses_a" })]
    const results = await Promise.allSettled(calls)
    expect(results.map((r) => r.status)).toEqual(["rejected", "rejected", "fulfilled"])
    expect(w.spawned.length).toBe(1)
  })
})

describe("notices", () => {
  test("a block puts a loud instruction entry; its end takes it out", async () => {
    const answers = [blocked, '{"block":false}']
    const w = world({ answers: (i) => answers[i] })
    const p = create(w.ctx, config, w.deps)
    await p.onPrompt({ sessionID: "ses_a" })
    expect(w.put).toHaveLength(1)
    expect(w.put[0].key).toBe(NOTICE_KEY)
    expect(w.put[0].sessionID).toBe("ses_a")
    expect(w.put[0].value).toContain("Blocked by hopsesh")
    expect(w.put[0].value).toContain("Every tool call in this session fails")
    await p.onPrompt({ sessionID: "ses_a" })
    expect(w.removed).toEqual([{ sessionID: "ses_a", key: NOTICE_KEY }])
  })

  test("a notice without a block is the hook's context, put once", async () => {
    const w = world({ answers: () => '{"block":false,"context":"Hopsesh status: continued in Codex."}' })
    const p = create(w.ctx, config, w.deps)
    await p.onPrompt({ sessionID: "ses_a" })
    await p.onPrompt({ sessionID: "ses_a" })
    expect(w.put.map((e) => e.value)).toEqual(["Hopsesh status: continued in Codex."])
  })

  test("the key is one OpenCode accepts", () => {
    expect(/^[a-z0-9][a-z0-9._-]*$/.test(NOTICE_KEY)).toBe(true)
  })
})

describe("the prompt hook never throws", () => {
  for (const [name, broken] of [
    ["spawn", (w) => (w.deps.spawn = () => { throw new Error("no such program") })],
    ["session.get", (w) => (w.ctx.session.get = async () => { throw new Error("gone") })],
    ["instructions", (w) => (w.ctx.session.instructions.entry.put = async () => { throw new Error("too large") })],
    ["storage", (w) => (w.ctx.storage.set = async () => { throw new Error("locked") })],
  ]) {
    test("when " + name + " fails", async () => {
      const w = world({ answers: () => blocked })
      broken(w)
      const p = create(w.ctx, config, w.deps)
      await p.onPrompt({ sessionID: "ses_a" })
      await p.onPrompt(undefined)
      await tick()
    })
  }
})

describe("failing open", () => {
  test("a hook that does not answer in time lets the call through, and is counted", async () => {
    const w = world({ answers: () => null })
    const p = create(w.ctx, { ...config, timeoutMs: 30 }, w.deps)
    const start = Date.now()
    await p.beforeTool({ sessionID: "ses_a" })
    expect(Date.now() - start).toBeLessThan(1000)
    expect(w.spawned[0].child.killed).toBe(true)
    await tick()
    expect(w.stored.failures.count).toBe(1)
    expect(w.stored.failures.reason).toContain("did not answer")
  })

  test("a hook that fails, exits non-zero or says nothing lets calls through and counts each", async () => {
    const answers = [["", 0], ["not json", 0], ['{"context":"x"}', 0], [blocked, 2]]
    const w = world({ storage: { failures: { count: 4 } } })
    w.deps.spawn = () => {
      const [answer, code] = answers[w.spawned.length]
      const child = fakeChild(answer, code)
      w.spawned.push({ child })
      return child
    }
    const p = create(w.ctx, config, w.deps)
    for (let i = 0; i < answers.length; i++) await p.onPrompt({ sessionID: "ses_a" })
    await p.beforeTool({ sessionID: "ses_a" })
    await tick()
    expect(w.spawned.length).toBe(4)
    expect(w.stored.failures.count).toBe(8)
    expect(w.put).toHaveLength(0)
  })

  test("after a failure the plugin does not ask again at once", async () => {
    let t = 0
    const w = world({ answers: () => "", now: () => t })
    const p = create(w.ctx, { ...config, failMs: 10000 }, w.deps)
    await p.beforeTool({ sessionID: "ses_a" })
    await p.beforeTool({ sessionID: "ses_a" })
    expect(w.spawned.length).toBe(1)
    t = 10001
    await p.beforeTool({ sessionID: "ses_a" })
    expect(w.spawned.length).toBe(2)
  })
})

describe("permissions", () => {
  const rules = { bin: "hopsesh", allow: [["ls"], ["show"], ["hosts", "--json"]], ask: [["pull"], ["undo"]] }

  test("hopsesh's read-only commands are allowed, its moves ask", () => {
    expect(ruleEffect(rules, "shell", ["hopsesh ls --json"])).toBe("allow")
    expect(ruleEffect(rules, "shell", ["hopsesh hosts --json"])).toBe("allow")
    expect(ruleEffect(rules, "shell", ["hopsesh ls --json", "hopsesh show x --json"])).toBe("allow")
    expect(ruleEffect(rules, "shell", ["hopsesh pull x --json --yes"])).toBe("ask")
    expect(ruleEffect(rules, "shell", ["hopsesh ls", "hopsesh undo --yes"])).toBe("ask")
  })

  test("anything else is OpenCode's to decide", () => {
    expect(ruleEffect(rules, "shell", ["hopsesh ls", "rm -rf /"])).toBeUndefined()
    expect(ruleEffect(rules, "shell", ["hopsesh"])).toBeUndefined()
    expect(ruleEffect(rules, "shell", ["hopsesh hosts add x"])).toBeUndefined()
    expect(ruleEffect(rules, "shell", ["/tmp/evil/hopsesh ls"])).toBeUndefined()
    expect(ruleEffect(rules, "shell", ["hopseshls"])).toBeUndefined()
    expect(ruleEffect(rules, "read", ["hopsesh ls"])).toBeUndefined()
    expect(ruleEffect(rules, "shell", [])).toBeUndefined()
    expect(ruleEffect(null, "shell", ["hopsesh ls"])).toBeUndefined()
  })

  test("a blocked session's permissions are denied with the reason", async () => {
    const w = world({ answers: () => blocked })
    const p = create(w.ctx, config, w.deps)
    const e = { sessionID: "ses_a", action: "shell", resources: ["hopsesh ls"], effect: "ask" }
    await p.evaluate(e)
    expect(e.effect).toBe("deny")
    expect(e.message).toContain("Blocked by hopsesh")
  })

  test("rules come from the file config.json names; a user's deny stays", async () => {
    const files = { "/cfg/.hopsesh/rules.json": JSON.stringify(rules) }
    const w = world()
    w.deps.readFile = (f) => {
      if (!(f in files)) throw new Error("ENOENT")
      return files[f]
    }
    const p = create(w.ctx, { ...config, rules: "/cfg/.hopsesh/rules.json" }, w.deps)
    const allow = { sessionID: "ses_a", action: "shell", resources: ["hopsesh ls --json"], effect: "ask" }
    await p.evaluate(allow)
    expect(allow.effect).toBe("allow")
    const ask = { sessionID: "ses_a", action: "shell", resources: ["hopsesh pull x"], effect: "allow" }
    await p.evaluate(ask)
    expect(ask.effect).toBe("ask")
    const deny = { sessionID: "ses_a", action: "shell", resources: ["hopsesh ls"], effect: "deny" }
    await p.evaluate(deny)
    expect(deny.effect).toBe("deny")
    delete files["/cfg/.hopsesh/rules.json"]
    const none = { sessionID: "ses_a", action: "shell", resources: ["hopsesh ls"], effect: "ask" }
    await p.evaluate(none)
    expect(none.effect).toBe("ask")
  })
})

describe("setup", () => {
  test("registers the hooks and listens for moves", async () => {
    const w = world({ answers: () => blocked })
    w.deps.readFile = () => JSON.stringify(config)
    const cleanup = await setup(w.ctx, "/cfg/plugins/hopsesh/config.json", w.deps)
    expect(Object.keys(w.hooks).sort()).toEqual(["permission.evaluate", "session.prompt", "tool.execute.before"])
    expect(w.subscribed.types).toEqual(EVENTS)
    expect(typeof cleanup).toBe("function")
    cleanup()
    expect(w.subscribed.signal.aborted).toBe(true)
    await expect(w.hooks["tool.execute.before"]({ sessionID: "ses_a" })).rejects.toThrow("Blocked by hopsesh")
  })

  test("a move event checks the session again", async () => {
    const w = world({ answers: () => blocked })
    w.ctx.event.subscribe = () =>
      (async function* () {
        yield { type: "session.moved", data: { sessionID: "ses_m" } }
      })()
    const p = create(w.ctx, config, w.deps)
    p.listen(new AbortController().signal)
    await tick()
    await tick()
    expect(JSON.parse(w.spawned[0].child.stdin.input)).toEqual({ session_id: "ses_m", directory: "/work/demo", event: "SessionStart" })
    expect(p.verdicts.get("ses_m").block).toBe(true)
  })

  test("a missing or broken config registers nothing (fail open)", async () => {
    for (const read of [() => { throw new Error("ENOENT") }, () => "{", () => '{"argv":[]}', () => '{"argv":[1]}']) {
      const w = world()
      w.deps.readFile = read
      expect(await setup(w.ctx, "/cfg/config.json", w.deps)).toBeUndefined()
      expect(Object.keys(w.hooks)).toEqual([])
      expect(w.logs.length).toBe(1)
    }
  })

  test("a context without a permission or prompt hook still protects", async () => {
    const w = world({ answers: () => blocked })
    w.deps.readFile = () => JSON.stringify(config)
    w.ctx.permission.hook = async () => {
      throw new Error("not in this OpenCode")
    }
    await setup(w.ctx, "/cfg/config.json", w.deps)
    expect(w.hooks["tool.execute.before"]).toBeDefined()
    expect(w.logs.some((l) => l.includes("permission"))).toBe(true)
  })
})
