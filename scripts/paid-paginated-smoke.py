#!/usr/bin/env python3
"""Opt-in two-call installed-agent smoke for a controlled paginated Codex fixture.

HOPSESH_PAID_SMOKE=1 BIN=bin/hopsesh scripts/paid-paginated-smoke.py
Only exact test sessions/journals are removed; scratch logs remain for review.
This tests standalone paginated append/resume, not vendor fork materialization.
"""
import datetime
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import uuid

if os.environ.get("HOPSESH_PAID_SMOKE") != "1":
    sys.exit("This calls models on your logins; set HOPSESH_PAID_SMOKE=1 to run it.")

binary = str(pathlib.Path(os.environ.get("BIN", "bin/hopsesh")).resolve())
root = pathlib.Path(tempfile.mkdtemp(prefix="hopsesh-paged-smoke-")).resolve()
project = root / "repo"
project.mkdir()
env = dict(os.environ, HOPSESH_CONFIG_DIR=str(root / "config"),
           HOPSESH_STATE_DIR=str(root / "state"))
session_id = str(uuid.uuid4())
now = datetime.datetime.now(datetime.timezone.utc)
agent_home = pathlib.Path(os.environ.get("CODEX_HOME", str(pathlib.Path.home() / ".codex")))
transcript = (agent_home / "sessions" / now.strftime("%Y/%m/%d") /
              ("rollout-" + now.strftime("%Y-%m-%dT%H-%M-%S") + "-" + session_id + ".jsonl"))
journals = []
owned_transcript = False


def run(args, log):
    result = subprocess.run(args, cwd=project, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=180)
    (root / log).write_text(result.stdout)
    if result.returncode:
        raise RuntimeError(f"{args[0]} failed; {root / log}")
    return result.stdout


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def pull(key, agent, log):
    result = json.loads(run([binary, "pull", key, "--in", agent, "--to", str(project),
                             "--yes", "--json"], log))
    journals.insert(0, result["result"]["journal"])
    return result["plan"]


try:
    run(["git", "init", "-q"], "init.log")
    run(["git", "-c", "user.name=smoke", "-c", "user.email=smoke@example.com",
         "commit", "--allow-empty", "-qm", "seed"], "git.log")
    rows = [
        {"type": "session_meta", "payload": {
            "id": session_id, "timestamp": now.isoformat(), "cwd": str(project),
            "originator": "hopsesh-test", "cli_version": "0.160.1", "source": "cli",
            "model_provider": "openai", "history_mode": "paginated"}},
        {"type": "event_msg", "payload": {"type": "user_message", "images": [],
                                           "message": "Remember the codeword POMELO-83."}},
        {"type": "response_item", "payload": {
            "type": "message", "role": "user", "content": [
                {"type": "input_text", "text": "Remember the codeword POMELO-83."}]}},
    ]
    for ordinal, row in enumerate(rows):
        row.update(timestamp=now.isoformat(), ordinal=ordinal)
    transcript.parent.mkdir(parents=True, exist_ok=True)
    with transcript.open("x") as stream:
        owned_transcript = True
        os.chmod(transcript, 0o600)
        stream.write("".join(json.dumps(row, separators=(",", ":")) + "\n" for row in rows))
    original = transcript.read_bytes()
    plan = pull("codex/" + session_id, "claude", "to-claude.json")
    claude_id = plan["placement"]["key"]["session"]
    answer = run(["claude", "-p", "--resume", claude_id,
                  "Give the remembered codeword in lowercase, then remember that the next "
                  "codeword is LYCEE-64. Reply briefly."], "claude.log")
    check("pomelo-83" in answer.lower(), "Claude did not recall initial history")
    plan = pull("claude/" + claude_id, "codex", "to-codex.json")
    check(plan["continue"]["relation"] == "append" and
          plan["placement"]["key"]["session"] == session_id, "Did not append into original")
    check(transcript.read_bytes().startswith(original), "Original native bytes rewritten")
    records = [json.loads(line) for line in transcript.read_text().splitlines()]
    check(all(row["ordinal"] == i for i, row in enumerate(records)), "Invalid append ordinals")
    answer = run(["codex", "exec", "resume", session_id,
                  "What is the next codeword that Claude was asked to remember? "
                  "Reply only with that codeword."], "codex.log")
    check("LYCEE-64" in answer.upper(), "Codex did not recall returned delta")
    print("PASS: installed agents resumed the original paginated Codex fixture; "
          "prefix and ordinals preserved, returned work recalled.")
finally:
    for journal in journals:
        result = subprocess.run([binary, "undo", journal, "--yes", "--force"], cwd=project,
                                env=env, stdout=subprocess.DEVNULL,
                                stderr=subprocess.DEVNULL, timeout=60)
        if result.returncode:
            print("Could not undo test journal:", journal, file=sys.stderr)
    if owned_transcript:
        transcript.unlink(missing_ok=True)
        pathlib.Path(str(transcript) + ".hopsesh.json").unlink(missing_ok=True)
    print("Evidence:", root)
