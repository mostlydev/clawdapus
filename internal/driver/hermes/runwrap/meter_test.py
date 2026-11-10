import importlib.util
import os
from pathlib import Path
import tempfile
import threading
import subprocess
import sys
import unittest

spec = importlib.util.spec_from_file_location("meter", Path(__file__).with_name("meter.py"))
meter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(meter)


class MeterTests(unittest.TestCase):
    def test_worker_python_startup_code_cannot_disable_meter(self):
        with tempfile.TemporaryDirectory() as home:
            root = Path(home)
            (root/'large').write_bytes(b'x'*100)
            (root/'sitecustomize.py').write_text(
                "import os\nreal=os.stat\n"
                "def fake(*a,**kw):\n r=list(real(*a,**kw));r[6]=0;return os.stat_result(r)\n"
                "os.stat=fake\nprint('SUBVERTED',flush=True)\n")
            source = Path(__file__).with_name('meter.py').read_text()
            env = dict(os.environ, PYTHONPATH=home)
            result = subprocess.run([sys.executable, '-I', '-S', '-c', source, home, '1'],
                                    env=env, cwd=home, capture_output=True, timeout=5)
            self.assertEqual(result.returncode, 75, result.stderr)
            self.assertNotIn(b'SUBVERTED', result.stdout)
            # Deliberate regression control: without isolation, user startup
            # code falsifies sizes and the over-budget meter keeps running.
            child = subprocess.Popen([sys.executable, '-c', source, home, '1'],
                                     env=env, cwd=home, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                with self.assertRaises(subprocess.TimeoutExpired):
                    child.communicate(timeout=0.5)
            finally:
                child.kill()
                out, _ = child.communicate()
            self.assertIn(b'SUBVERTED', out)

    def test_counts_owned_files_and_ignores_outside_symlinks(self):
        with tempfile.TemporaryDirectory() as home, tempfile.TemporaryDirectory() as outside:
            Path(home, "private").mkdir(mode=0o700)
            Path(home, "private", "file").write_bytes(b"a" * 12)
            Path(outside, "secret").write_bytes(b"b" * 10000)
            Path(home, "escape").symlink_to(outside, target_is_directory=True)
            self.assertEqual(meter.home_size(home, 100), 12)
            with self.assertRaises(meter.BudgetExceeded):
                meter.home_size(home, 11)

    def test_entry_budget_is_enforced(self):
        with tempfile.TemporaryDirectory() as home:
            for n in range(4):
                Path(home, str(n)).touch()
            with self.assertRaises(meter.Unmeasured):
                meter.home_size(home, 100, max_entries=3)
            # Many small files under the byte threshold are not "too big".
            self.assertEqual(meter.home_size(home, 100), 0)

    def test_unmeasurable_home_is_not_reported_as_full(self):
        with tempfile.TemporaryDirectory() as home:
            Path(home, "locked").mkdir()
            Path(home, "locked", "f").write_bytes(b"x")
            os.chmod(Path(home, "locked"), 0)
            source = Path(__file__).with_name('meter.py').read_text()
            try:
                result = subprocess.run([sys.executable, '-I', '-S', '-c', source, home, '100'],
                                        capture_output=True, timeout=10)
            finally:
                os.chmod(Path(home, "locked"), 0o700)
            self.assertEqual(result.returncode, meter.EXIT_UNMEASURED, result.stderr)
            self.assertIn(b'home_unmeasured', result.stdout)

    def test_root_symlink_is_refused(self):
        with tempfile.TemporaryDirectory() as parent:
            Path(parent, "real").mkdir()
            Path(parent, "link").symlink_to(Path(parent, "real"))
            with self.assertRaises(OSError):
                meter.home_size(str(Path(parent, "link")), 100)

    def test_concurrent_directory_symlink_swap_stays_confined(self):
        with tempfile.TemporaryDirectory() as home, tempfile.TemporaryDirectory() as outside:
            root = Path(home)
            (root/'slot').mkdir()
            (root/'slot'/'small').write_bytes(b'x'*12)
            Path(outside, 'large').write_bytes(b'z'*1000000)
            stop = threading.Event()

            def swap():
                while not stop.is_set():
                    (root/'slot').rename(root/'parked')
                    (root/'slot').symlink_to(outside, target_is_directory=True)
                    (root/'slot').unlink()
                    (root/'parked').rename(root/'slot')

            thread = threading.Thread(target=swap)
            thread.start()
            try:
                for _ in range(100):
                    self.assertLess(meter.home_size(home, 10000000), 1000000)
            finally:
                stop.set()
                thread.join()


if __name__ == "__main__":
    unittest.main()
