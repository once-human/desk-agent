#!/usr/bin/env python3
"""Smoke test: the CLI the GUI drives (config/apps set, status --json) takes
effect in a RUNNING agent without a restart. Fake Hyprland + fake ingest."""
import json, os, socket, subprocess, sys, tempfile, threading, time, http.server

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
BIN = os.environ.get("DESK_AGENT_BIN", os.path.join(ROOT, "bin", "desk-agent"))
SECRET = "SECRET-WINDOW-TITLE-do-not-leak"

root = tempfile.mkdtemp(prefix="dl-")
home = os.path.join(root, "home"); rt = os.path.join(root, "rt")
os.makedirs(home); os.makedirs(rt, mode=0o700)
hdir = os.path.join(rt, "hypr", "sig"); os.makedirs(hdir)

received = []
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("content-length", 0)))
        d = json.loads(body); received.append(d)
        out = b'{"ok":true}'
        self.send_response(200); self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)
srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H); port = srv.server_address[1]
threading.Thread(target=srv.serve_forever, daemon=True).start()

current = {"class": "kitty", "pid": 4242}; sub = []
def serve1():
    s = socket.socket(socket.AF_UNIX); s.bind(os.path.join(hdir, ".socket.sock")); s.listen(5)
    while True:
        c, _ = s.accept(); c.recv(100)
        c.sendall(json.dumps({"address": "0x1", "class": current["class"], "title": SECRET,
                              "initialClass": current["class"], "pid": current["pid"]}).encode()); c.close()
def serve2():
    s = socket.socket(socket.AF_UNIX); s.bind(os.path.join(hdir, ".socket2.sock")); s.listen(5)
    while True:
        c, _ = s.accept(); sub.append(c)
threading.Thread(target=serve1, daemon=True).start(); threading.Thread(target=serve2, daemon=True).start()
def focus(cls, pid=1):
    current["class"], current["pid"] = cls, pid
    for line in (f"activewindowv2>>0x1",):
        for c in list(sub):
            try: c.sendall((line + "\n").encode())
            except OSError: pass

cfgdir = os.path.join(home, ".config", "desk-agent"); os.makedirs(cfgdir)
cfgfile = os.path.join(cfgdir, "config.toml")
open(cfgfile, "w").write(f'endpoint = "http://127.0.0.1:{port}/api/ingest/desk"\ninterval_s = 5\nidle_interval_s = 10\nbattery = false\n')
env = dict(os.environ, HOME=home, XDG_RUNTIME_DIR=rt, DESK_AGENT_KEY="testkey")
for k in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY"): env.pop(k, None)
log = open(os.path.join(root, "agent.log"), "w")
agent = subprocess.Popen([BIN, "run"], env=env, stderr=log)

fails = []
def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name + (f"  [{extra}]" if extra and not cond else ""))
    if not cond: fails.append(name)
def cli(*a, stdin=None):
    return subprocess.run([BIN, *a], env=env, capture_output=True, text=True, input=stdin)
def samples(): return [s for d in received for s in d["samples"]]
def status(): return json.loads(cli("status", "--json").stdout)
def wait(pred, secs=6):
    end = time.time() + secs
    while time.time() < end:
        if pred(): return True
        time.sleep(0.2)
    return False

time.sleep(3)
st = status()
check("status --json: running, version, pid", st.get("running") is True and st.get("pid") == agent.pid and st.get("version"))
check("status --json: current sample is kitty/terminal", (st.get("sample") or {}).get("app", {}).get("id") == "kitty")
check("status --json: endpoint host only, no path", st.get("endpointHost") == f"127.0.0.1:{port}", str(st.get("endpointHost")))
check("status --json: delivery ok", st["send"]["result"] == "ok" and st["send"]["queued"] == 0, str(st["send"]))
check("status --json: recent history present", len(st.get("recent") or []) >= 1)

# --- apps set applies live
focus("firefox", 99); wait(lambda: samples()[-1].get("app", {}).get("id") == "firefox")
r = cli("apps", "set", "firefox", "--name", "My Browser", "--category", "browser")
check("apps set reports it was applied to the running agent", "Applied to the running agent" in r.stdout, r.stdout + r.stderr)
check("renamed app is what is sent next, no restart",
      wait(lambda: samples()[-1].get("app", {}).get("name") == "My Browser"), str(samples()[-1]))

# --- private mode applies live and strips detail
r = cli("config", "set", "private", "true")
check("config set private applied live", "Applied to the running agent" in r.stdout, r.stdout)
check("private mode strips the app from the very next sample",
      wait(lambda: "app" not in samples()[-1] or samples()[-1].get("app") is None), str(samples()[-1]))
check("status says private", status()["private"] is True)
cli("config", "set", "private", "false")
check("leaving private mode brings detail back", wait(lambda: samples()[-1].get("app") is not None), str(samples()[-1]))

# --- a broken config is rejected by the CLI and, if hand-broken, by the agent
before = open(cfgfile).read()
r = cli("config", "set", "interval_s", "-3")
check("config set rejects an invalid value and leaves the file alone", r.returncode != 0 and open(cfgfile).read() == before, r.stderr)
open(cfgfile, "a").write("\nthis is [ not toml\n")
r = cli("apps", "set", "kitty", "--category", "editor")  # any change triggers reload
check("a hand-broken config.toml is refused by the CLI before anything is written", r.returncode != 0, r.stdout + r.stderr)
open(cfgfile, "w").write(before)
check("agent still alive and answering after all of that", status().get("running") is True and agent.poll() is None)

# --- pause via CLI is reflected in status
cli("pause"); time.sleep(1)
check("status shows paused", status()["paused"] is True and status()["sample"] is None)
cli("resume"); time.sleep(1)
check("status shows resumed", status()["paused"] is False)

# --- adult hosts can never be listed
r = cli("sites", "set", "pornhub.com", "--name", "x")
check("sites set refuses an adult host", r.returncode != 0 and "adult" in r.stderr, r.stderr)

wire = json.dumps(received)
check("window title never on the wire", SECRET not in wire)
check("window title never in status --json", SECRET not in json.dumps(status()))

agent.terminate(); agent.wait(5)
print("\nFAILS:", fails or "none")
sys.exit(1 if fails else 0)
