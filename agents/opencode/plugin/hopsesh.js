// The hopsesh plugin for OpenCode 2.x: it blocks the tool calls of a session whose copy
// hopsesh moved or continued elsewhere (the stale original), tells the model why, and lets
// hopsesh's read-only commands run without asking. It decides nothing itself: it asks
// hopsesh's notice hook (the command in config.json) about each session, as Claude Code's
// and Codex's hooks do. hopsesh writes this folder whole; edits are replaced on the next
// install.
//
// Plain JavaScript, as OpenCode loads a plugin folder (no build, no dependencies): index.js
// is the entry, this file the logic, which the tests run with a fake context.

import { spawn as spawnProcess } from "node:child_process"
import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"

// The instruction entry that carries hopsesh's notice to the model (keys are lowercase,
// with no slash).
export const NOTICE_KEY = "hopsesh-notice"

// The events after which a session's copy may have changed place.
export const EVENTS = ["session.created", "session.moved", "session.forked"]

const ANSWER_LIMIT = 64 << 10

// How many sessions the plugin remembers (verdicts, parents, notices); the oldest go first.
const REMEMBER = 2000

// setup registers the plugin's hooks on ctx, with the config file next to the entry.
// A missing or broken config registers nothing: the plugin fails open, as every hopsesh
// hook does.
export async function setup(ctx, configURL, deps = {}) {
  const log = deps.log ?? ((m) => console.error("hopsesh: " + m))
  let config
  try {
    const file = typeof configURL === "string" ? configURL : fileURLToPath(configURL)
    config = JSON.parse((deps.readFile ?? readText)(file))
  } catch (e) {
    log("no protection: cannot read the plugin's config.json (" + message(e) + ")")
    return undefined
  }
  if (!Array.isArray(config?.argv) || config.argv.length === 0 || config.argv.some((a) => typeof a !== "string")) {
    log("no protection: config.json names no notice hook")
    return undefined
  }
  const p = create(ctx, config, deps)
  // The tool hook is the protection: a failure to register it fails the plugin, which
  // OpenCode then lists as failed (and hopsesh reports). The others only inform.
  await ctx.tool.hook("execute.before", p.beforeTool)
  for (const [what, register] of [
    ["permission", () => ctx.permission.hook("evaluate", p.evaluate)],
    ["prompt", () => ctx.session.hook("prompt", p.onPrompt)],
  ]) {
    try {
      await register()
    } catch (e) {
      log("cannot register the " + what + " hook: " + message(e))
    }
  }
  const stop = new AbortController()
  p.listen(stop.signal)
  return () => stop.abort()
}

// create is the plugin's state and hooks over ctx, with config's notice hook.
export function create(ctx, config, deps = {}) {
  const spawn = deps.spawn ?? spawnProcess
  const read = deps.readFile ?? readText
  const now = deps.now ?? Date.now
  const log = deps.log ?? ((m) => console.error("hopsesh: " + m))
  const timeoutMs = positive(config.timeoutMs, 5000)
  const cacheMs = positive(config.cacheMs, 30000)
  const failMs = positive(config.failMs, 10000)
  const retryMs = positive(config.retryMs, 1000)
  const verdicts = new Map() // root session id → {block, reason, text, at, failed}
  const pending = new Map() // root session id and event → the check running for them
  const parents = new Map() // session id → its parent's id ("" for a root)
  const noted = new Map() // session id → the notice put on it ("" once cleared)
  let failures = Promise.resolve()

  // root is the session a subagent's session belongs to: hopsesh keeps lineage there.
  // session.get returns the session itself (the Promise API unwraps {data}). A lookup that
  // fails throws and is not kept, so the next call asks again.
  async function root(id) {
    let at = id
    for (let i = 0; i < 8; i++) {
      let parent = parents.get(at)
      if (parent === undefined) {
        const got = await ctx.session.get({ sessionID: at })
        parent = typeof got?.parentID === "string" ? got.parentID : ""
        remember(parents, at, parent)
      }
      if (!parent) return at
      at = parent
    }
    return at
  }

  // ask runs the notice hook once: a verdict, or why it failed.
  function ask(id, event) {
    const input = JSON.stringify({ session_id: id, directory: ctx.location?.directory ?? "", event })
    return new Promise((resolve) => {
      let done = false
      let out = ""
      let child
      let timer
      const finish = (r) => {
        if (done) return
        done = true
        clearTimeout(timer)
        resolve(r)
      }
      try {
        child = spawn(config.argv[0], config.argv.slice(1), { stdio: ["pipe", "pipe", "ignore"], windowsHide: true })
      } catch (e) {
        finish({ error: "cannot run hopsesh: " + message(e) })
        return
      }
      timer = setTimeout(() => {
        kill(child)
        finish({ error: "hopsesh did not answer within " + timeoutMs + " ms" })
      }, timeoutMs)
      child.on("error", (e) => finish({ error: "cannot run hopsesh: " + message(e) }))
      child.stdout?.setEncoding?.("utf8") // a character split across chunks stays whole
      child.stdout?.on("data", (b) => {
        out += String(b)
        if (out.length > ANSWER_LIMIT) {
          kill(child)
          finish({ error: "hopsesh's answer is too long" })
        }
      })
      child.on("close", (code) => {
        if (code !== 0) return finish({ error: "hopsesh exited with status " + code })
        let answer
        try {
          answer = JSON.parse(out.trim())
        } catch {
          return finish({ error: out.trim() ? "hopsesh's answer is not JSON" : "hopsesh gave no answer" })
        }
        if (typeof answer?.block !== "boolean") return finish({ error: "hopsesh's answer has no verdict" })
        finish({
          block: answer.block,
          reason: answer.block ? text(answer.reason) : "",
          text: answer.block ? text(answer.reason) : text(answer.context),
        })
      })
      child.stdin?.on?.("error", () => {})
      child.stdin?.end?.(input)
    })
  }

  // check asks hopsesh about the session (its root's lineage), keeps the verdict, and puts
  // the notice on the root session (and on a subagent's session while blocked). One check
  // runs per root and event at a time. A failure keeps a fresh block (one failed check
  // does not lift it); otherwise it lets calls through for a while (failMs). Each failure
  // is counted.
  async function check(id, event) {
    let top
    try {
      top = await root(id)
    } catch (e) {
      count("cannot find the session's root session: " + message(e))
      return { block: false, reason: "", text: "", at: now(), failed: true }
    }
    const key = top + "\n" + event
    let running = pending.get(key)
    if (!running) {
      running = ask(top, event).then((r) => {
        pending.delete(key)
        const at = now()
        if (r.error) {
          count(r.error)
          const known = verdicts.get(top)
          if (known?.block && !known.failed && at - known.at < cacheMs) return known
          return remember(verdicts, top, { block: false, reason: "", text: "", at, failed: true })
        }
        return remember(verdicts, top, { block: r.block, reason: r.reason, text: r.text, at, failed: false })
      })
      pending.set(key, running)
    }
    const v = await running
    if (!v.failed) {
      await note(top, v)
      // A subagent's session shows the block too, and loses it when the block ends; a
      // notice that is not a block is the root's (it is shown once).
      if (id !== top && (v.block || noted.get(id))) await note(id, v.block ? v : { block: false, text: "" })
    }
    return v
  }

  // verdict is the session's kept verdict while fresh, else a new check.
  async function verdict(id, event) {
    const top = await root(id).catch(() => undefined)
    const v = top === undefined ? undefined : verdicts.get(top)
    if (v && now() - v.at < (v.failed ? failMs : cacheMs)) return v
    return check(id, event)
  }

  // note puts hopsesh's notice on the session as an instruction entry the model reads
  // (OpenCode has no way to refuse a prompt), or takes an earlier one out.
  async function note(id, v) {
    const want = v.block
      ? "hopsesh: " + v.reason + " Every tool call in this session fails until then. Do not try to work around it: tell the user."
      : v.text
    if (noted.get(id) === want) return
    try {
      if (want) {
        await ctx.session.instructions.entry.put({ sessionID: id, key: NOTICE_KEY, value: want })
      } else {
        await ctx.session.instructions.entry.remove({ sessionID: id, key: NOTICE_KEY })
      }
      remember(noted, id, want)
    } catch (e) {
      log("cannot " + (want ? "add" : "clear") + " the notice on " + id + ": " + message(e))
    }
  }

  // count records a failed check in the plugin's storage, where hopsesh reads it.
  function count(reason) {
    log("let a call through: " + reason)
    failures = failures.then(async () => {
      try {
        const old = await ctx.storage.get("failures")
        const n = Number.isInteger(old?.count) ? old.count : 0
        await ctx.storage.set("failures", { count: n + 1, at: new Date(now()).toISOString(), reason })
      } catch (e) {
        log("cannot count the failure: " + message(e))
      }
    })
  }

  // rules are the approval rules hopsesh wrote (skill install --add-rules), read each time.
  function rules() {
    if (typeof config.rules !== "string" || !config.rules) return null
    try {
      const r = JSON.parse(read(config.rules))
      return typeof r?.bin === "string" && r.bin ? r : null
    } catch {
      return null
    }
  }

  return {
    verdicts,
    check,
    // A prompt: ask again, so a block (or its end) shows at once. Never throws: a throw
    // here is a defect in OpenCode's service.
    async onPrompt(e) {
      try {
        if (e?.sessionID) await check(e.sessionID, "UserPromptSubmit")
      } catch (err) {
        log("prompt check failed: " + message(err))
      }
    },
    // A tool call: refused while the session is blocked. A session with no fresh verdict
    // (after a restart, or a turn resumed without a prompt) is checked first.
    async beforeTool(e) {
      let v
      try {
        v = e?.sessionID ? await verdict(e.sessionID, "UserPromptSubmit") : undefined
      } catch (err) {
        log("tool check failed: " + message(err))
      }
      if (v?.block) throw new Error(v.reason)
    },
    // A permission: denied while blocked; else hopsesh's rules for its own commands. A
    // deny the user configured never reaches here (OpenCode decides it first).
    async evaluate(e) {
      try {
        const v = e?.sessionID ? await verdict(e.sessionID, "UserPromptSubmit") : undefined
        if (v?.block) {
          e.effect = "deny"
          e.message = v.reason
          return
        }
        if (e.effect === "deny") return
        const effect = ruleEffect(rules(), e.action, e.resources)
        if (effect) e.effect = effect
      } catch (err) {
        log("permission check failed: " + message(err))
      }
    },
    // Events after which a copy may have moved: check the session again. A subagent's new
    // session only records its parent (its root's verdict covers it, and its start is not
    // the root's). A subscription that ends is made again, waiting longer each time.
    listen(signal) {
      const onEvent = (ev) => {
        const id = ev?.data?.sessionID
        if (typeof id !== "string" || !id) return
        if (ev.type === "session.created") {
          const parent = typeof ev.data.parentID === "string" ? ev.data.parentID : ""
          remember(parents, id, parent)
          if (parent) return
        }
        check(id, "SessionStart").catch((err) => log("event check failed: " + message(err)))
      }
      ;(async () => {
        let wait = retryMs
        while (!signal.aborted) {
          const since = now()
          try {
            for await (const ev of ctx.event.subscribe({ types: EVENTS, signal })) onEvent(ev)
            if (!signal.aborted) log("event subscription ended; subscribing again")
          } catch (err) {
            if (!signal.aborted) log("event subscription ended: " + message(err))
          }
          if (now() - since > 60000) wait = retryMs
          await sleep(wait, signal)
          wait = Math.min(wait * 2, 60000)
        }
      })()
    },
  }
}

// remember keeps key's value in map, forgetting the oldest entries past REMEMBER.
function remember(map, key, value) {
  map.delete(key)
  map.set(key, value)
  while (map.size > REMEMBER) map.delete(map.keys().next().value)
  return value
}

// sleep waits ms, or until signal aborts, without keeping the process alive.
function sleep(ms, signal) {
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer)
      signal?.removeEventListener?.("abort", done)
      resolve()
    }
    const timer = setTimeout(done, ms)
    timer.unref?.()
    signal?.addEventListener?.("abort", done, { once: true })
  })
}

// ruleEffect is what hopsesh's rules say about a permission: "ask" when any command is one
// that always asks, "allow" when every command is one that runs without asking, else
// nothing (OpenCode's own rules stand). Only shell commands are hopsesh's. OpenCode splits
// a shell call into commands at &&, ;, | and $(…), but a redirection stays in its command:
// a command with a redirection, a substitution or a second line is never allowed.
export function ruleEffect(rules, action, resources) {
  if (!rules || action !== "shell" || !Array.isArray(resources) || resources.length === 0) return undefined
  let allowed = true
  for (const resource of resources) {
    const command = String(resource)
    const words = command.trim().split(/\s+/)
    if (prefixed(rules.ask, rules.bin, words)) return "ask"
    if (SHELL_SYNTAX.test(command) || !prefixed(rules.allow, rules.bin, words)) allowed = false
  }
  return allowed ? "allow" : undefined
}

// SHELL_SYNTAX is what makes a command more than hopsesh with its arguments.
const SHELL_SYNTAX = /[<>|&;`\n\r]|\$\(/

function prefixed(prefixes, bin, words) {
  if (!Array.isArray(prefixes) || words[0] !== bin) return false
  return prefixes.some(
    (p) => Array.isArray(p) && p.length > 0 && p.length < words.length && p.every((w, i) => words[i + 1] === w),
  )
}

function readText(file) {
  return readFileSync(file, "utf8")
}

function positive(v, def) {
  return Number.isFinite(v) && v > 0 ? v : def
}

function text(v) {
  return typeof v === "string" ? v : ""
}

function message(e) {
  return e instanceof Error ? e.message : String(e)
}

function kill(child) {
  try {
    child.kill()
  } catch {}
}
