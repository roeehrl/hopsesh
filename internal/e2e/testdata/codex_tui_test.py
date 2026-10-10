"""Verify fixture cleanup without vendor binaries, accounts, or network access."""
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest


class OwnedFamilyTest(unittest.TestCase):
    def test_normal_exit_and_interrupt_reap_detached_children(self):
        for normal_exit in (False, True):
            with self.subTest(normal_exit=normal_exit), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                vendor = root / "vendor"
                vendor.write_text(f"#!{sys.executable}\n" + '''
import os, pathlib, time
root = pathlib.Path(os.environ["CODEX_HOME"])
child = os.fork()
if child == 0:
    os.setsid()
    while True:
        time.sleep(.1)
(root / "detached.pid").write_text(str(child))
(root / "thread-writer-locks").mkdir()
(root / "thread-writer-locks" / "fixture.lock").touch()
while not (root / "exit").exists():
    time.sleep(.02)
''')
                vendor.chmod(0o700)
                unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
                driver = subprocess.Popen([sys.executable, str(Path(__file__).with_name("codex_tui.py")),
                                           str(vendor), tmp, "fixture", tmp], stdout=subprocess.PIPE, text=True)
                try:
                    self.assertTrue(driver.stdout.readline().startswith("open "))
                    detached = int((root / "detached.pid").read_text())
                    os.kill(detached, 0)
                    if normal_exit:
                        (root / "exit").touch()
                    else:
                        driver.send_signal(signal.SIGINT)
                    self.assertEqual(driver.wait(timeout=10), 0)
                    with self.assertRaises(ProcessLookupError):
                        os.kill(detached, 0)
                    self.assertIsNone(unrelated.poll())
                finally:
                    if driver.poll() is None:
                        driver.send_signal(signal.SIGINT)
                        driver.wait(timeout=10)
                    driver.stdout.close()
                    unrelated.terminate()
                    unrelated.wait(timeout=5)


if __name__ == "__main__":
    unittest.main()
