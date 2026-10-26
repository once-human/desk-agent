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


import shutil
repo = os.path.join(root, "work", "my-secret-project"); os.makedirs(os.path.join(repo, ".git")); os.makedirs(os.path.join(repo, "src"))
# a fake "terminal" (pid A) with a child shell (pid B) sitting inside the repo
shell_code = "import os,time; os.chdir(%r); time.sleep(120)" % os.path.join(repo, "src")
term = subprocess.Popen([sys.executable, "-c", "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c',%r]); time.sleep(120)" % shell_code], cwd=home)
time.sleep(1.0)

time.sleep(2)
focus("kitty", term.pid); time.sleep(3.5)
check("repo derived from the terminal's child shell cwd", any(x.get("repo") == "my-secret-project" for x in samples()[-3:]), str(samples()[-2:]))
check("only the repo NAME is sent (no path)", "/work/" not in json.dumps(samples()))

# Claude Code hook: payload carries a secret prompt that must never be stored or sent
payload = json.dumps({"session_id": "sess1", "cwd": repo, "hook_event_name": "UserPromptSubmit",
                      "prompt": "SECRET PROMPT TEXT about my startup", "transcript_path": "/x"})
focus("firefox", 1); time.sleep(2.5)       # a non-terminal is focused: repo must come from Claude
r = subprocess.run([BIN, "hook", "start"], input=payload, env=env, capture_output=True, text=True)
check("hook prints nothing and exits 0", r.returncode == 0 and r.stdout == "" and r.stderr == "", repr((r.returncode, r.stdout, r.stderr)))
time.sleep(3)
cs = [x for x in samples() if x.get("claude") is True]
check("claude:true appears after hook start", bool(cs), str(samples()[-2:]))
check("claude repo name used when no terminal is focused", bool(cs) and cs[-1].get("repo") == "my-secret-project", str(cs[-1:]))
state = open(os.path.join(rt, "desk-agent", "claude.json")).read()
check("state file never contains the prompt", "SECRET" not in state, state)

r = subprocess.run([BIN, "hook", "stop"], input=json.dumps({"session_id": "sess1", "cwd": repo, "hook_event_name": "Stop"}), env=env, capture_output=True, text=True)
time.sleep(3)
check("claude:false after hook stop", samples()[-1].get("claude") is False, str(samples()[-1]))

# garbage / empty stdin must still be harmless
r = subprocess.run([BIN, "hook", "start"], input="", env=env, capture_output=True, text=True, timeout=10)
check("hook survives empty stdin", r.returncode == 0 and r.stdout == "")
r = subprocess.run([BIN, "hook", "bogus"], input="{}", env=env, capture_output=True, text=True)
check("hook survives bad args", r.returncode == 0 and r.stdout == "")

# 90 s rule: a start with no stop expires (shorten by faking an old ts in the file)
sf = os.path.join(rt, "desk-agent", "claude.json"); d = json.load(open(sf))
for k in d["sessions"]: d["sessions"][k].update(working=True, ts=int(time.time()) - 85)
tmp = sf + ".x"; json.dump(d, open(tmp, "w")); os.replace(tmp, sf); time.sleep(1.5)
check("stale start is working until the 90s window ends", samples()[-1].get("claude") is True, str(samples()[-1]))
time.sleep(7.5)
check("working expires by itself ~90s after the last start", samples()[-1].get("claude") is False, str(samples()[-1]))

# settings merge via the CLI
cdir = os.path.join(home, ".claude"); os.makedirs(cdir)
json.dump({"model": "opus", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "echo mine"}]}]}}, open(os.path.join(cdir, "settings.json"), "w"))
r = subprocess.run([BIN, "install-hooks"], env=env, capture_output=True, text=True)
s = json.load(open(os.path.join(cdir, "settings.json")))
check("install-hooks keeps the user's own Stop hook and adds ours",
      s["model"] == "opus" and len(s["hooks"]["Stop"]) == 2 and "echo mine" in json.dumps(s["hooks"]["Stop"]) and set(s["hooks"]) == {"UserPromptSubmit","PreToolUse","PostToolUse","Stop","SessionEnd"}, r.stdout + r.stderr)
r = subprocess.run([BIN, "uninstall-hooks"], env=env, capture_output=True, text=True)
s = json.load(open(os.path.join(cdir, "settings.json")))
check("uninstall-hooks leaves only the user's hook", list(s["hooks"]) == ["Stop"] and len(s["hooks"]["Stop"]) == 1)

wire = json.dumps([d for _, _, d in received])
check("no title, prompt or path anywhere on the wire", SECRET not in wire and "SECRET PROMPT" not in wire and "my-secret-project/" not in wire)
term.kill(); agent.terminate(); agent.wait(5)
print("\nTOTAL samples:", len(samples()), " FAILS:", fails or "none")
sys.exit(1 if fails else 0)
