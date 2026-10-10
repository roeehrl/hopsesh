import { execFileSync, spawn } from "node:child_process";
import { copyFileSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const port = process.env.CDP_PORT || "9333";
const root = "../../..";

// Make a demo home, start the real app on it with the debugging port open, and wait for
// the port to answer. The returned function quits the app.
//
// HOPSESH_DEMO_WORLD picks the home: "test" (default; the fixtures' sessions, for the
// tests) or "laptop" (demoseed's made-up laptop with Claude Code and Codex sessions, a
// studio machine and an update on offer, for the screenshots). HOPSESH_DEMO_HOME puts it
// somewhere with tidy paths.
export default async function setup() {
  const exe = process.env.HOPSESH_APP_EXE;
  if (!exe) throw new Error("set HOPSESH_APP_EXE to a test build of hopsesh-app (-tags e2e)");
  const laptop = process.env.HOPSESH_DEMO_WORLD === "laptop";
  const home = process.env.HOPSESH_DEMO_HOME || join(tmpdir(), `hopsesh-real-${process.pid}`);
  const env = JSON.parse(execFileSync("go", ["run", "./internal/devtools/webtest", "-home", home, "-prepare", "-world", laptop ? "empty" : "test"],
    { cwd: root, encoding: "utf8" }));
  if (laptop) Object.assign(env, laptopWorld(home, env, dirname(exe)));
  const app = spawn(exe, [], { env: { ...process.env, ...env, HOPSESH_E2E_CDP_PORT: port }, stdio: "inherit" });
  app.on("exit", (code) => { if (code) console.error(`hopsesh-app exited with ${code}`); });
  for (let i = 0; i < 120; i++) {
    try {
      if ((await fetch(`http://127.0.0.1:${port}/json/version`)).ok) return () => { app.kill(); };
    } catch { /* not yet */ }
    await new Promise((r) => setTimeout(r, 500));
  }
  app.kill();
  throw new Error(`the app's debugging port ${port} never answered`);
}

// laptopWorld seeds demoseed's laptop (with Codex threads) into home and returns the
// environment changes: the machine is "laptop", and a stand-in codex is on PATH.
function laptopWorld(home: string, env: Record<string, string>, appDir: string): Record<string, string> {
  const exe = process.platform === "win32" ? ".exe" : "";
  const tools = join(tmpdir(), `hopsesh-tools-${process.pid}`);
  const bin = join(home, "bin");
  mkdirSync(bin, { recursive: true });
  execFileSync("go", ["build", "-o", join(tools, "demoseed" + exe), "./internal/devtools/demoseed"], { cwd: root });
  execFileSync("go", ["build", "-o", join(tools, "fakeagent" + exe), "./internal/devtools/fakeagent"], { cwd: root });
  copyFileSync(join(tools, "fakeagent" + exe), join(bin, "codex" + exe));
  const sep = process.platform === "win32" ? ";" : ":";
  const out = { HOPSESH_MACHINE: "laptop", PATH: bin + sep + (env.PATH || process.env.PATH || "") };
  execFileSync(join(tools, "demoseed" + exe), ["-role", "laptop", "-codex"], { env: { ...process.env, ...env, ...out } });
  // The settings demo/laptop-setup.sh leaves: offers answered, the agents' own marks, a
  // studio machine, and update checks on.
  mkdirSync(env.HOPSESH_CONFIG_DIR, { recursive: true });
  writeFileSync(join(env.HOPSESH_CONFIG_DIR, "config.toml"), [
    "schema = 5",
    `repos_dir = '${join(home, "src")}'`,
    'layout = "flat"',
    'update_check = "on"',
    'skill_prompt = "declined"',
    'cli_prompt = "declined"',
    "app_icons = false",
    "",
    "[[hosts]]",
    'name = "studio"',
    'destination = "studio"',
    'via = "ssh-config"',
    "allowed = true",
    'os = "linux"',
    "",
  ].join("\n"));
  // studio: scripts/windows-demo-studio.ps1 made it (WSL2 Ubuntu over SSH) and wrote its
  // ssh config entry; the demo home gets the entry too, and hopsesh trusts its host key.
  if (process.env.DEMO_SSH_CONFIG) {
    mkdirSync(join(home, ".ssh"), { recursive: true });
    copyFileSync(process.env.DEMO_SSH_CONFIG, join(home, ".ssh", "config"));
    execFileSync(join(appDir, "hopsesh" + exe), ["trust", "studio", "--yes"], { env: { ...process.env, ...env, ...out }, stdio: "inherit" });
  }
  // An update on offer (the app reads the last check for a day): Settings shows Install
  // and restart. Nothing is installed; the screenshots never click it.
  mkdirSync(env.HOPSESH_STATE_DIR, { recursive: true });
  writeFileSync(join(env.HOPSESH_STATE_DIR, "update-check.json"), JSON.stringify({
    at: new Date().toISOString(),
    result: { latest: "0.3.1", newer: true, url: "https://github.com/roeehrl/hopsesh/releases/tag/v0.3.1" },
  }));
  return out;
}
