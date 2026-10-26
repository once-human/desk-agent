"""Backend tests: the real desk-agent binary against a throwaway HOME (no GTK needed).

Needs bin/desk-agent (go build -o bin/desk-agent .) or DESK_AGENT_BIN.
"""
import json, os, shutil, socket, sys, tempfile, threading, unittest

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
sys.path.insert(0, os.path.join(ROOT, "gui"))
BIN = os.environ.get("DESK_AGENT_BIN") or os.path.join(ROOT, "bin", "desk-agent")

from deskagent_gui import backend  # noqa: E402


class PublicUrl(unittest.TestCase):
    def test_conventional_endpoint(self):
        self.assertEqual(backend.public_url("https://www.example.com/api/ingest/desk"), "https://www.example.com/api/desk")
        self.assertEqual(backend.public_url("http://127.0.0.1:9/api/ingest/desk"), "http://127.0.0.1:9/api/desk")

    def test_never_guesses(self):
        for e in ("", "ftp://x/api/ingest/desk", "https://x/other", "https://x/api/ingest/desk/extra", "not a url"):
            self.assertIsNone(backend.public_url(e), e)


@unittest.skipUnless(os.access(BIN, os.X_OK), "build bin/desk-agent first")
class WithBinary(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="dagui-")
        self.saved = dict(os.environ)
        home = os.path.join(self.tmp, "home")
        rt = os.path.join(self.tmp, "rt")
        os.makedirs(home)
        os.makedirs(rt, mode=0o700)
        for k in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "DESK_AGENT_KEY"):
            os.environ.pop(k, None)
        os.environ.update(HOME=home, XDG_RUNTIME_DIR=rt)
        self.be = backend.Backend(BIN)

    def tearDown(self):
        os.environ.clear()
        os.environ.update(self.saved)
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_state_and_reload_without_an_agent(self):
        self.assertIsNone(self.be.state())
        self.assertIsNone(self.be.reload())
        self.be.flush()  # must not raise

    def test_errors_are_readable(self):
        with self.assertRaises(backend.BackendError) as cm:
            self.be.site_set("example.com", category="not-a-category")
        self.assertNotIn("Traceback", str(cm.exception))
        self.assertTrue(str(cm.exception))

    def test_missing_binary_is_explained(self):
        with self.assertRaises(backend.BackendError) as cm:
            backend.Backend("/nonexistent/desk-agent").run("status")
        self.assertTrue(str(cm.exception))

    def test_site_round_trip(self):
        before = self.be.sites()
        self.assertTrue(before["rows"])
        self.be.site_set("example.org", name="Example", category="news")
        row = next(r for r in self.be.sites()["rows"] if r["host"] == "example.org")
        self.assertEqual((row["name"], row["category"]), ("Example", "news"))
        self.be.site_remove("example.org")
        self.assertFalse(any(r["host"] == "example.org" for r in self.be.sites()["rows"]))

    def test_adult_hosts_are_refused(self):
        with self.assertRaises(backend.BackendError):
            self.be.site_set("pornhub.com", name="x", category="video")
        self.assertFalse(any("pornhub" in r["host"] for r in self.be.sites()["rows"]))

    def test_config_round_trip_and_file_mode(self):
        cfg = self.be.config()
        self.assertIn("endpoint", json.dumps(cfg))
        self.be.config_set("private", "true")
        path = os.path.join(os.environ["HOME"], ".config", "desk-agent", "config.toml")
        self.assertIn("private = true", open(path).read())

    def test_site_key_is_stored_private_and_never_echoed(self):
        # A throwaway value in a throwaway HOME: nothing real is involved.
        out = self.be.set_key("throwaway-test-key-123")
        self.assertNotIn("throwaway-test-key-123", out)
        path = os.path.join(os.environ["HOME"], ".config", "desk-agent", "key")
        self.assertEqual(os.stat(path).st_mode & 0o777, 0o600)
        self.assertNotIn("throwaway-test-key-123", json.dumps(self.be.config()))

    def test_endpoint_is_not_settable(self):
        with self.assertRaises(backend.BackendError):
            self.be.config_set("endpoint", "https://evil.example/api/ingest/desk")

    def test_doctor_reports_even_when_checks_fail(self):
        d = self.be.doctor()
        self.assertTrue(d.get("checks"))
        self.assertTrue(all("level" in c and "name" in c for c in d["checks"]))

    def test_state_over_a_fake_control_socket(self):
        path = os.path.join(backend.runtime_dir(), "ctl.sock")
        os.makedirs(os.path.dirname(path), mode=0o700)
        srv = socket.socket(socket.AF_UNIX)
        srv.bind(path)
        srv.listen(2)
        seen = []

        def serve():
            for _ in range(3):
                c, _ = srv.accept()
                cmd = c.recv(64).decode().strip()
                seen.append(cmd)
                c.sendall({"state": b'{"version":"t","paused":false}\n', "reload": b"ok paused=false\n"}.get(cmd, b"ok\n"))
                c.close()

        t = threading.Thread(target=serve, daemon=True)
        t.start()
        self.assertEqual(self.be.state()["version"], "t")
        self.assertEqual(self.be.reload(), "ok paused=false")
        self.be.flush()
        t.join(3)
        srv.close()
        self.assertEqual(seen, ["state", "reload", "flush"])


class InThread(unittest.TestCase):
    def test_delivers_result_and_error(self):
        out = []
        ev = threading.Event()

        def done(res, err):
            out.append((res, err))
            if len(out) == 2:
                ev.set()

        backend.in_thread(lambda: 7, done, lambda f: f())
        backend.in_thread(lambda: 1 / 0, done, lambda f: f())
        self.assertTrue(ev.wait(3))
        self.assertIn((7, None), out)
        self.assertTrue(any(isinstance(e, ZeroDivisionError) for _, e in out))


if __name__ == "__main__":
    unittest.main()
