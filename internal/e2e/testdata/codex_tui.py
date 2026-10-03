"""Opens a Codex thread in the real Codex TUI, in a pseudo-terminal, and keeps it open.

Used by the opt-in tests (HOPSESH_REAL_AGENTS=1): it answers the terminal's queries and
Codex's trust prompt, prints "open <pid>" once Codex holds the thread's writer lock, and
exits when Codex does (or after a minute).

    python3 codex_tui.py <codex> <CODEX_HOME> <thread-id> <cwd>
"""
import fcntl, os, pty, select, struct, sys, termios, time

codex, home, tid, cwd = sys.argv[1:5]
pid, fd = pty.fork()
if pid == 0:
    os.environ["CODEX_HOME"] = home
    os.environ["TERM"] = "xterm-256color"
    os.chdir(cwd)
    os.execv(codex, [codex, "resume", tid])
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
lock = os.path.join(home, "thread-writer-locks", tid + ".lock")
replies = {b"\x1b[6n": b"\x1b[24;1R", b"\x1b]10;?": b"\x1b]10;rgb:ffff/ffff/ffff\x1b\\",
           b"\x1b]11;?": b"\x1b]11;rgb:0000/0000/0000\x1b\\", b"\x1b[c": b"\x1b[?62;22c", b"\x1b[?u": b"\x1b[?0u"}
enter_at, announced, end = 0, False, time.time() + 60
while time.time() < end:
    done, _ = os.waitpid(pid, os.WNOHANG)
    if done:
        break
    r, _, _ = select.select([fd], [], [], 0.2)
    if r:
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        for q, a in replies.items():
            if q in chunk:
                os.write(fd, a)
        if b"Press enter to continue" in chunk:
            enter_at = time.time() + 1
    if enter_at and time.time() > enter_at:
        os.write(fd, b"\r")
        enter_at = 0
    if not announced and os.path.exists(lock):
        print("open", pid, flush=True)
        announced = True
else:
    os.kill(pid, 9)
