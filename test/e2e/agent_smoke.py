#!/usr/bin/env python3
"""Smoke test: fake Hyprland + fake ingest server against the real binary."""
import json, os, socket, subprocess, sys, tempfile, threading, time, http.server

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
BIN = os.environ.get("DESK_AGENT_BIN", os.path.join(ROOT, "bin", "desk-agent"))
SECRET = "SECRET-WINDOW-TITLE-do-not-leak"

root = tempfile.mkdtemp(prefix="da-")
home = os.path.join(root, "home"); rt = os.path.join(root, "rt")
os.makedirs(home); os.makedirs(rt, mode=0o700)
sig = "testsig"
hdir = os.path.join(rt, "hypr", sig); os.makedirs(hdir)

# ---------- fake ingest server ----------
received, up = [], {"ok": True}
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("content-length", 0)); body = self.rfile.read(n)
        if not up["ok"]:
            self.send_response(503); self.end_headers(); return
        if self.headers.get("authorization") != "Bearer testkey":
            self.send_response(401); self.end_headers(); return
        d = json.loads(body); received.append((time.time(), self.headers.get("user-agent"), d))
        out = json.dumps({"ok": True, "kept": len(d["samples"])}).encode()
        self.send_response(200); self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(out))); self.end_headers(); self.wfile.write(out)
srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
port = srv.server_address[1]
threading.Thread(target=srv.serve_forever, daemon=True).start()

# ---------- fake Hyprland ----------
current = {"class": "kitty", "pid": 4242}
sub = []  # socket2 client connections
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
threading.Thread(target=serve1, daemon=True).start()
threading.Thread(target=serve2, daemon=True).start()
def hypr_event(line):
    for c in list(sub):
        try: c.sendall((line + "\n").encode())
        except OSError: pass
def focus(cls, pid=1):
    current["class"], current["pid"] = cls, pid
    hypr_event(f"windowtitlev2>>0x1,{SECRET}")      # title-bearing noise
    hypr_event(f"activewindow>>{cls},{SECRET}")      # legacy event with title
    hypr_event("activewindowv2>>0x1")

# ---------- config + run ----------
cfgdir = os.path.join(home, ".config", "desk-agent"); os.makedirs(cfgdir)
open(os.path.join(cfgdir, "config.toml"), "w").write(
    f'endpoint = "http://127.0.0.1:{port}/api/ingest/desk"\ninterval_s = 5\nidle_interval_s = 10\nbattery = false\n')
env = dict(os.environ, HOME=home, XDG_RUNTIME_DIR=rt, DESK_AGENT_KEY="testkey")
for k in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY"): env.pop(k, None)
log = open(os.path.join(root, "agent.log"), "w")
agent = subprocess.Popen([BIN, "run"], env=env, stderr=log)

fails = []
def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name + (f"  [{extra}]" if extra and not cond else ""))
    if not cond: fails.append(name)
def samples(): return [s for _, _, d in received for s in d["samples"]]
def last(): return samples()[-1] if samples() else None

time.sleep(3)
s = samples()
check("first sample arrives within 3s", len(s) >= 1)
check("focus mapped from class (kitty -> Terminal/terminal)",
      any(x.get("app", {}).get("id") == "kitty" and x["app"]["category"] == "terminal" for x in s), str(s[-1:]))
check("User-Agent is desk-agent/<ver>", all((ua or "").startswith("desk-agent/") for _, ua, _ in received))

n0 = len(samples()); t0 = time.time(); focus("firefox", 99); time.sleep(3.5)
fx = [x for x in samples()[n0:] if x.get("app", {}).get("id") == "firefox"]
check("focus change sends a new sample promptly (<=3.5s incl. 2s debounce)", len(fx) >= 1)
check("firefox categorised as browser", bool(fx) and fx[0]["app"]["category"] == "browser")

n1 = len(samples()); time.sleep(6.5)
check("heartbeat while idle-state steady (interval 5s)", len(samples()) > n1)

# privacy: nothing with the title anywhere on the wire
wire = json.dumps([d for _, _, d in received])
check("window title never appears in any payload", SECRET not in wire and "title" not in wire.replace('"title":null', ''))

# t is floored to 5s grid
ok_grid = all(int(__import__("calendar").timegm(time.strptime(x["t"], "%Y-%m-%dT%H:%M:%SZ"))) % 5 == 0 for x in samples())
check("t rounded to interval grid", ok_grid)

# offline queue
up["ok"] = False; n2 = len(samples()); focus("discord", 7); time.sleep(8)
qf = os.path.join(home, ".local", "state", "desk-agent", "queue.jsonl")
qlines = open(qf).read().strip().splitlines() if os.path.exists(qf) else []
check("samples queue while the site is down", len(qlines) >= 1, f"{len(qlines)} lines")
check("queue file mode 600", os.path.exists(qf) and (os.stat(qf).st_mode & 0o777) == 0o600)
up["ok"] = True; time.sleep(9)   # backoff 2s -> 4s -> ... then flush
qlines2 = open(qf).read().strip().splitlines() if os.path.exists(qf) else []
check("queue flushed after site returns", len(qlines2) == 0, f"{len(qlines2)} left")
check("queued discord sample delivered", any(x.get("app", {}).get("id") == "discord" for x in samples()[n2:]))

# pause / resume
r = subprocess.run([BIN, "pause"], env=env, capture_output=True, text=True); time.sleep(1.5)
fin = last()
check("pause sends final active:false", fin is not None and fin["active"] is False and "app" not in fin, str(fin))
npause = len(samples()); focus("kitty", 5); time.sleep(8)
check("nothing sent while paused", len(samples()) == npause)
subprocess.run([BIN, "resume"], env=env, capture_output=True, text=True); time.sleep(2.5)
check("resume sends a sample right away", len(samples()) > npause and last()["active"] is True)

agent.terminate(); agent.wait(5)
print("\n--- agent log (tail) ---"); print(open(os.path.join(root, "agent.log")).read()[-900:])
print("\nTOTAL samples:", len(samples()), " FAILS:", fails or "none")
sys.exit(1 if fails else 0)
