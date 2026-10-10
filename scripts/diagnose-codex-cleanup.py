"""Diagnostic branch only. Observe and clean only this disposable test's children."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def snapshot():
    result = {}
    for p in Path('/proc').glob('[0-9]*/stat'):
        try:
            text = p.read_text()
            left, tail = text.rsplit(')', 1)
            fields = tail.split()
            pid = int(p.parent.name)
            result[pid] = {'pid': pid, 'ppid': int(fields[1]), 'state': fields[0],
                           'start': fields[19], 'name': left.split('(', 1)[1]}
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            pass
    return result


seen = {}
log = Path('codex-cleanup.log').resolve()
with log.open('w') as output:
    child = subprocess.Popen(['/tmp/hopsesh-agent-diagnostic', '-test.run=Codex',
                              '-test.v', '-test.timeout=3m'], cwd='internal/e2e',
                             env=dict(os.environ, HOPSESH_REAL_AGENTS='1'),
                             stdout=output, stderr=subprocess.STDOUT)
    while child.poll() is None:
        processes = snapshot()
        owned = {child.pid}
        while True:
            found = {pid for pid, v in processes.items() if v['ppid'] in owned}
            if found <= owned:
                break
            owned |= found
        for pid in owned:
            if pid in processes:
                item = dict(processes[pid])
                if pid in seen and seen[pid]['start'] == item['start']:
                    item['firstParent'] = seen[pid]['firstParent']
                else:
                    item['firstParent'] = item['ppid']
                seen[pid] = item
        time.sleep(.02)
    time.sleep(2)
    processes = snapshot()
    survivors = [v for pid, v in processes.items()
                 if pid in seen and v['start'] == seen[pid]['start']]
    for item in survivors:
        try:
            args = (Path('/proc') / str(item['pid']) / 'cmdline').read_bytes().split(b'\0')
            item['ownedCommand'] = [s.decode(errors='replace') for s in args[:5] if s]
        except FileNotFoundError:
            item['exitedDuringReadback'] = True
    Path('codex-cleanup.json').write_text(json.dumps({
        'testExitCode': child.returncode, 'seen': list(seen.values()),
        'survivorsAfterTwoSeconds': survivors,
    }, indent=2) + '\n')
    print(log.read_text())
    print(json.dumps({'testExitCode': child.returncode, 'survivors': survivors}))
    # Never touch processes that were not observed as descendants of our test.
    for sig in (signal.SIGTERM, signal.SIGKILL):
        now = snapshot()
        for item in survivors:
            live = now.get(item['pid'])
            if live and live['start'] == item['start'] and live['state'] != 'Z':
                try:
                    os.kill(item['pid'], sig)
                except ProcessLookupError:
                    pass
        time.sleep(1)
    raise SystemExit(child.returncode or int(bool(survivors)))
