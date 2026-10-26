import sys
#!/usr/bin/env python3
"""Browser e2e: a real Chromium with the real extension -> native messaging ->
agent -> fake ingest server. Needs: node + playwright, xvfb-run, a built
extension (extension/build.sh). Not part of CI."""
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

import http.server, threading, subprocess, json, os, time

# local page server: every hostname maps to it via --host-resolver-rules
class P(http.server.BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_GET(self):
        body=b"<html><head><title>SECRET PAGE TITLE</title></head><body>SECRET BODY</body></html>"
        self.send_response(200); self.send_header("content-type","text/html"); self.send_header("content-length",str(len(body))); self.end_headers(); self.wfile.write(body)
psrv = http.server.ThreadingHTTPServer(("127.0.0.1",0),P); pport = psrv.server_address[1]
threading.Thread(target=psrv.serve_forever,daemon=True).start()

# native messaging manifests into the profile Chromium will use
r = subprocess.run([BIN,"install-native-host","--all","--binary",BIN],env=env,capture_output=True,text=True)
print(r.stdout.strip().splitlines()[-1] if r.stdout else r.stderr)
profile = os.path.join(home,".config","chromium")
check("chromium native host manifest installed", os.path.exists(os.path.join(profile,"NativeMessagingHosts","desk_agent.json")))

focus("chromium", 4242)
time.sleep(1.5)
urls = [
  "http://monkeytype.com:PORT/secret/path?token=SECRETQUERY#SECRETFRAG",
  "http://github.com:PORT/once-human/private-repo",
  "http://docs.google.com:PORT/document/d/SECRETDOCID/edit",
  "http://free-nudes.example.net:PORT/x",
  "http://tenant-alice-secret.some-saas.io:PORT/inbox",
]
t0=time.time()
d = subprocess.run(["xvfb-run","-a","node",os.path.join(os.path.dirname(__file__),"ext_e2e.js"),profile,str(pport),json.dumps(urls)],env=dict(env,NODE_PATH=os.environ.get("NODE_PATH",""),EXT_DIR=os.path.join(ROOT,"extension","dist","chromium")),capture_output=True,text=True,timeout=120)
print(d.stdout.strip()); 
if d.returncode: print("NODE ERR", d.stderr[-1500:])
time.sleep(2)

ss = samples()
sites = [x.get("site") for x in ss if "site" in x]
seen_hosts = {s["host"] for s in sites}
print("sites seen:", sorted(seen_hosts))
check("node driver ran", d.returncode==0, d.stderr[-300:])
check("unknown host shown by friendly name (monkeytype.com -> Monkeytype/other)", any(s["host"]=="monkeytype.com" and s["name"]=="Monkeytype" and s["category"]=="other" for s in sites))
check("seed host mapped (github.com -> GitHub/code)", any(s["host"]=="github.com" and s["name"]=="GitHub" and s["category"]=="code" for s in sites))
check("subdomain seed (docs.google.com -> Google Docs/docs)", any(s["host"]=="docs.google.com" and s["name"]=="Google Docs" for s in sites))
check("adult host never named", not any("nudes" in json.dumps(x) for x in ss))
check("tenant subdomain collapsed to registrable domain", any(s["host"]=="some-saas.io" for s in sites) and not any("alice" in json.dumps(x) for x in ss))
wire = json.dumps([d for _,_,d in received])
for leak in ("SECRET","secret/path","private-repo","token=","SECRETDOCID","PAGE TITLE"):
    check("no %r on the wire"%leak, leak not in wire)
check("every site object has only host/name/category", all(set(s)=={"host","name","category"} for s in sites))
agent.terminate(); agent.wait(5)
print("\nTOTAL samples:",len(samples())," FAILS:",fails or "none")
print(open(os.path.join(root,"agent.log")).read()[-600:])
sys.exit(1 if fails else 0)
