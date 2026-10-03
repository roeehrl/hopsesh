import { execFileSync, spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const port = process.env.CDP_PORT || "9333";

// Make a demo home, start the real app on it with the debugging port open, and wait for
// the port to answer. The returned function quits the app.
export default async function setup() {
  const exe = process.env.HOPSESH_APP_EXE;
  if (!exe) throw new Error("set HOPSESH_APP_EXE to a test build of hopsesh-app (-tags e2e)");
  // HOPSESH_DEMO_HOME gives the screenshots tidy paths (C:\Users\demo, not a temp folder).
  const home = process.env.HOPSESH_DEMO_HOME || join(tmpdir(), `hopsesh-real-${process.pid}`);
  const env = JSON.parse(execFileSync("go", ["run", "./internal/devtools/webtest", "-home", home, "-prepare"], { cwd: "../../..", encoding: "utf8" }));
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
