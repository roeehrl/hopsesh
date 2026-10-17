#!/usr/bin/env python3
"""Compare disposable macOS e2e apps on matching prepared local-session homes.

The native counters cover the app process, excluding separate WebKit services.
This finite idle sample is not a whole-machine energy or three-peer load claim.
"""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import signal
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("baseline", "current", "baseline-env", "current-env", "output"):
        parser.add_argument("--" + name, required=True, type=pathlib.Path)
    parser.add_argument("--seconds", type=int, default=30)
    parser.add_argument("--baseline-ref", default="provided-binary")
    parser.add_argument("--current-ref", default="provided-binary")
    args = parser.parse_args()
    if platform.system() != "Darwin" or not 10 <= args.seconds <= 300:
        parser.error("requires macOS and a 10-300 second finite sample")
    if not args.output.is_absolute() or args.output.exists():
        parser.error("output must be a new absolute directory")
    for binary in (args.baseline, args.current):
        if not binary.is_absolute() or not binary.is_file() or binary.is_symlink() or not os.access(binary, os.X_OK):
            parser.error("pass built disposable e2e app executables")
    os.umask(0o077)
    args.output.mkdir(mode=0o700)
    counter = args.output / "native-counter"
    source = pathlib.Path(__file__).with_name("resource-counter-darwin.c")
    subprocess.run(["xcrun", "clang", "-Wall", "-Wextra", "-Werror", str(source), "-o", str(counter)], check=True)
    ready = args.output / "ready.js"
    ready.write_text('''(async()=>{
      const end=Date.now()+90000;
      while(Date.now()<end){
        if(document.querySelectorAll(".row").length>=2){
          const {Call}=await import("/wails/runtime.js");
          await Call.ByName("github.com/roeehrl/hopsesh/internal/ui/gui.App.SetReceive",true);
          return;
        }
        await new Promise(r=>setTimeout(r,300));
      }
    })();\n''')

    def sample(process):
        if process.poll() is not None:
            raise RuntimeError("disposable app exited before the idle sample completed")
        return json.loads(subprocess.check_output([str(counter), str(process.pid)], text=True))

    results = []
    for label, binary, env_path, schema in (("0.4", args.baseline, args.baseline_env, 4), ("0.5", args.current, args.current_env, 5)):
        values = json.loads(env_path.read_text())
        home = pathlib.Path(values["HOME"])
        if not home.is_absolute() or home.resolve() == pathlib.Path.home().resolve() or not home.is_dir() or home.is_symlink():
            raise RuntimeError("qualification requires a separate prepared disposable home")
        for key, suffix in (("HOPSESH_CONFIG_DIR", "config"), ("HOPSESH_STATE_DIR", "state")):
            if pathlib.Path(values[key]) != home / suffix:
                raise RuntimeError("namespace must stay inside the disposable home")
        env = {key: values[key] for key in ("HOME", "USERPROFILE", "HOPSESH_CONFIG_DIR", "HOPSESH_STATE_DIR", "HOPSESH_MACHINE")}
        env.update(PATH="/usr/bin:/bin:/usr/sbin:/sbin", SHELL="/bin/sh", HOPSESH_TAILSCALE="off",
                   CLAUDE_CONFIG_DIR=str(home / ".claude"), CODEX_HOME=str(home / ".codex"), HOPSESH_E2E_SCRIPT=str(ready))
        config = home / "config" / "config.toml"
        config.parent.mkdir(mode=0o700, exist_ok=True)
        if config.exists():
            raise RuntimeError("use a fresh fixture; qualification does not overwrite settings")
        config.write_text(f'schema = {schema}\nlayout = "flat"\nupdate_check = "off"\n[desktop]\nmode = "both"\nclose = "keep"\n')
        with (args.output / (label + "-app.log")).open("wb") as log:
            process = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 100
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError("disposable app exited before rendering the fixture")
                    if any(line.strip() in ("receive = true", "receive=true") for line in config.read_text().splitlines()):
                        break
                    time.sleep(0.25)
                else:
                    raise RuntimeError("real window did not render both fixture agents in time")
                time.sleep(10)  # exclude startup, account registration and WebView loading
                before = sample(process)
                start = time.monotonic()
                time.sleep(args.seconds)
                after = sample(process)
                elapsed = time.monotonic() - start
                if before["start"] != after["start"]:
                    raise RuntimeError("app identity changed during qualification")
                cpu_mach = after["userMach"] + after["systemMach"] - before["userMach"] - before["systemMach"]
                cpu_seconds = cpu_mach * after["timebaseNumer"] / after["timebaseDenom"] / 1e9
                with binary.open("rb") as source_binary:
                    digest = hashlib.sha256()
                    for chunk in iter(lambda: source_binary.read(1 << 20), b""):
                        digest.update(chunk)
                results.append({"version": label, "sourceRef": args.baseline_ref if label == "0.4" else args.current_ref,
                                "binarySHA256": digest.hexdigest(), "seconds": elapsed, "before": before, "after": after,
                                "cpuPercentOneCore": 100 * cpu_seconds / elapsed,
                                "interruptWakeups": after["interruptWakeups"] - before["interruptWakeups"],
                                "packageIdleWakeups": after["packageIdleWakeups"] - before["packageIdleWakeups"]})
            finally:
                if process.poll() is None:
                    process.send_signal(signal.SIGTERM)
                    try:
                        process.wait(timeout=15)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait()
    report = {"schema": 1, "workload": "native-gui-two-agent-local-fixture", "measurement": "app-process-rusage-info-v4",
              "webKitServicesIncluded": False, "wholeMachineEnergyQualified": False, "threePeerBudgetQualified": False, "samples": results}
    (args.output / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
    print("Completed matching native GUI idle samples; separate WebKit and three-peer budgets remain to qualify.")


if __name__ == "__main__":
    main()
