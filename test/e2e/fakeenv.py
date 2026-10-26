"""A fake desktop for end-to-end tests: Hyprland sockets, an ingest server
and a public status endpoint, with the real desk-agent binary running."""
import http.server, json, os, socket, subprocess, tempfile, threading, time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SECRET_TITLE = "SECRET-WINDOW-TITLE-do-not-leak"


class FakeEnv:
    def __init__(self, interval_s=5, with_agent=True, extra_cfg=""):
        self.bin = os.environ.get("DESK_AGENT_BIN", os.path.join(ROOT, "bin", "desk-agent"))
        self.root = tempfile.mkdtemp(prefix="fe-")
        self.home = os.path.join(self.root, "home")
        self.rt = os.path.join(self.root, "rt")
        os.makedirs(self.home)
        os.makedirs(self.rt, mode=0o700)
        self.hdir = os.path.join(self.rt, "hypr", "sig")
        os.makedirs(self.hdir)
        self.received = []
        self.public = {"online": False, "lastSeen": None, "active": False, "lastActive": None,
                       "doing": None, "doingSince": None, "playing": None}
        self.current = {"class": "kitty", "pid": 4242}
        self.sub = []
        self._ingest()
        self._hypr()
        cfgdir = os.path.join(self.home, ".config", "desk-agent")
        os.makedirs(cfgdir)
        with open(os.path.join(cfgdir, "config.toml"), "w") as f:
            f.write(f'endpoint = "http://127.0.0.1:{self.port}/api/ingest/desk"\ninterval_s = {interval_s}\n'
                    f'idle_interval_s = 10\nbattery = false\n{extra_cfg}')
        self.env = dict(os.environ, HOME=self.home, XDG_RUNTIME_DIR=self.rt, DESK_AGENT_KEY="testkey")
        for k in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY"):
            self.env.pop(k, None)
        self.agent = None
        if with_agent:
            self.start_agent()

    def start_agent(self):
        self.log = open(os.path.join(self.root, "agent.log"), "a")
        self.agent = subprocess.Popen([self.bin, "run"], env=self.env, stderr=self.log)

    def stop_agent(self):
        if self.agent:
            self.agent.terminate()
            self.agent.wait(5)
            self.agent = None

    def _ingest(self):
        env = self

        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                out = json.dumps(env.public).encode()
                self.send_response(200); self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)

            def do_POST(self):
                body = self.rfile.read(int(self.headers.get("content-length", 0)))
                env.received.append(json.loads(body))
                out = b'{"ok":true}'
                self.send_response(200); self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)

        self.srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.port = self.srv.server_address[1]
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def _hypr(self):
        def serve1():
            s = socket.socket(socket.AF_UNIX); s.bind(os.path.join(self.hdir, ".socket.sock")); s.listen(5)
            while True:
                c, _ = s.accept(); c.recv(100)
                c.sendall(json.dumps({"address": "0x1", "class": self.current["class"], "title": SECRET_TITLE,
                                      "initialClass": self.current["class"], "pid": self.current["pid"]}).encode()); c.close()

        def serve2():
            s = socket.socket(socket.AF_UNIX); s.bind(os.path.join(self.hdir, ".socket2.sock")); s.listen(5)
            while True:
                c, _ = s.accept(); self.sub.append(c)

        threading.Thread(target=serve1, daemon=True).start()
        threading.Thread(target=serve2, daemon=True).start()

    def focus(self, cls, pid=1):
        self.current["class"], self.current["pid"] = cls, pid
        for c in list(self.sub):
            try:
                c.sendall(b"activewindowv2>>0x1\n")
            except OSError:
                pass

    def samples(self):
        return [s for d in self.received for s in d["samples"]]

    def cli(self, *a, stdin=None):
        return subprocess.run([self.bin, *a], env=self.env, capture_output=True, text=True, input=stdin)

    def close(self):
        self.stop_agent()
        self.srv.shutdown()
