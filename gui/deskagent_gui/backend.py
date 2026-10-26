"""Talks to the desk-agent binary and the running agent. No GTK in here.

* Live state comes straight from the agent's control socket (cheap to poll).
* Everything that changes something goes through the `desk-agent` CLI, so the
  GUI and the terminal can never disagree about how a file is edited.
"""
from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import threading
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable


class BackendError(Exception):
    """A CLI call failed; the message is safe to show the user."""


def runtime_dir() -> str:
    return os.path.join(os.environ.get("XDG_RUNTIME_DIR") or f"/run/user/{os.getuid()}", "desk-agent")


def find_binary() -> str | None:
    env = os.environ.get("DESK_AGENT_BIN")
    if env and os.access(env, os.X_OK):
        return env
    found = shutil.which("desk-agent")
    if found:
        return found
    for p in (os.path.expanduser("~/.local/bin/desk-agent"), "/usr/local/bin/desk-agent", "/usr/bin/desk-agent"):
        if os.access(p, os.X_OK):
            return p
    return None


def public_url(endpoint: str) -> str | None:
    """The site's public status URL, derived from the ingest endpoint.

    Only done for the conventional .../api/ingest/desk path, so the app never
    guesses at some other host's API.
    """
    u = urllib.parse.urlsplit(endpoint)
    if u.scheme not in ("http", "https") or not u.netloc or not u.path.endswith("/api/ingest/desk"):
        return None
    path = u.path[: -len("/ingest/desk")] + "/desk"
    return urllib.parse.urlunsplit((u.scheme, u.netloc, path, "", ""))


class Backend:
    def __init__(self, binary: str | None = None):
        self.binary = binary or find_binary()

    # ---- live state --------------------------------------------------------

    def state(self, timeout: float = 1.5) -> dict | None:
        """The running agent's state, or None if it is not running."""
        path = os.path.join(runtime_dir(), "ctl.sock")
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
                s.settimeout(timeout)
                s.connect(path)
                s.sendall(b"state\n")
                buf = b""
                while not buf.endswith(b"\n"):
                    chunk = s.recv(65536)
                    if not chunk:
                        break
                    buf += chunk
            line = buf.decode("utf-8", "replace").strip()
            if not line.startswith("{"):
                return None
            return json.loads(line)
        except (OSError, ValueError):
            return None

    def reload(self, timeout: float = 4.0) -> str | None:
        """Ask the running agent to re-read everything. None if it isn't running."""
        path = os.path.join(runtime_dir(), "ctl.sock")
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
                s.settimeout(timeout)
                s.connect(path)
                s.sendall(b"reload\n")
                return s.recv(4096).decode("utf-8", "replace").strip()
        except OSError:
            return None

    # ---- CLI ---------------------------------------------------------------

    def run(self, *args: str, stdin: str | None = None, timeout: float = 60) -> str:
        if not self.binary:
            raise BackendError("The desk-agent program was not found. Install it first (see the README), or set DESK_AGENT_BIN.")
        try:
            p = subprocess.run([self.binary, *args], input=stdin, capture_output=True, text=True, timeout=timeout)
        except subprocess.TimeoutExpired:
            raise BackendError(f"`desk-agent {args[0]}` took too long and was stopped.")
        except OSError as e:
            raise BackendError(f"Could not run desk-agent: {e}")
        if p.returncode != 0:
            msg = (p.stderr.strip() or p.stdout.strip() or f"exit status {p.returncode}")
            raise BackendError(msg.removeprefix("desk-agent: "))
        return p.stdout

    def run_json(self, *args: str, stdin: str | None = None, timeout: float = 60) -> Any:
        out = self.run(*args, stdin=stdin, timeout=timeout)
        try:
            return json.loads(out)
        except ValueError:
            raise BackendError("desk-agent printed something unexpected.")

    def doctor(self) -> dict:
        # `doctor --json` exits 1 when a check fails but still prints the report.
        if not self.binary:
            raise BackendError("The desk-agent program was not found.")
        p = subprocess.run([self.binary, "doctor", "--json"], capture_output=True, text=True, timeout=60)
        try:
            return json.loads(p.stdout)
        except ValueError:
            raise BackendError(p.stderr.strip() or "doctor printed something unexpected.")

    def config(self) -> dict:
        return self.run_json("config", "show", "--json")

    def config_set(self, key: str, *values: str) -> str:
        return self.run("config", "set", key, *values)

    def sites(self) -> dict:
        return self.run_json("sites", "list", "--json")

    def site_set(self, host: str, name: str | None = None, category: str | None = None, hide: bool | None = None) -> str:
        a = ["sites", "set", host]
        if name is not None:
            a += ["--name", name]
        if category is not None:
            a += ["--category", category]
        if hide is True:
            a += ["--hide"]
        elif hide is False:
            a += ["--show"]
        return self.run(*a)

    def site_remove(self, host: str) -> str:
        return self.run("sites", "remove", host)

    def sites_import(self, rows: list[dict]) -> str:
        return self.run("sites", "import", stdin=json.dumps(rows))

    def flush(self, timeout: float = 3.0) -> None:
        """Ask the agent to save the focused-apps record now (best effort)."""
        path = os.path.join(runtime_dir(), "ctl.sock")
        try:
            with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
                s.settimeout(timeout)
                s.connect(path)
                s.sendall(b"flush\n")
                s.recv(64)
        except OSError:
            pass

    def apps(self) -> dict:
        self.flush()
        return self.run_json("apps", "list", "--json")

    def scan_apps_fresh(self) -> dict:
        self.flush()
        return self.scan_apps()

    def app_set(self, cls: str, name: str | None = None, category: str | None = None) -> str:
        a = ["apps", "set", cls]
        if name is not None:
            a += ["--name", name]
        if category is not None:
            a += ["--category", category]
        return self.run(*a)

    def app_remove(self, cls: str) -> str:
        return self.run("apps", "remove", cls)

    def apps_import(self, rows: list[dict]) -> str:
        return self.run("apps", "import", stdin=json.dumps(rows))

    def scan_history(self, days: int = 180, top: int = 200) -> dict:
        return self.run_json("scan-history", "--json", "--days", str(days), "--top", str(top), timeout=180)

    def scan_apps(self) -> dict:
        self.flush()
        return self.run_json("scan-apps", "--json")

    def pause(self, paused: bool) -> str:
        return self.run("pause" if paused else "resume")

    def set_key(self, key: str) -> str:
        """Stores the site key via `desk-agent set-key` (stdin, mode 600)."""
        key = key.strip()
        if not key:
            raise BackendError("The key is empty.")
        return self.run("set-key", stdin=key + "\n")

    def fetch_blocklist(self) -> str:
        out = self.run("fetch-blocklist", timeout=180)
        self.reload()
        return out

    def install_native_host(self) -> str:
        return self.run("install-native-host")

    # ---- systemd user service ---------------------------------------------

    def service_active(self) -> str:
        try:
            p = subprocess.run(["systemctl", "--user", "is-active", "desk-agent"], capture_output=True, text=True, timeout=5)
            return p.stdout.strip() or "unknown"
        except (OSError, subprocess.TimeoutExpired):
            return "unavailable"

    def service(self, action: str) -> str:
        if action not in ("start", "stop", "restart"):
            raise BackendError("unknown service action")
        try:
            p = subprocess.run(["systemctl", "--user", action, "desk-agent"], capture_output=True, text=True, timeout=20)
        except (OSError, subprocess.TimeoutExpired) as e:
            raise BackendError(f"systemctl failed: {e}")
        if p.returncode != 0:
            raise BackendError(p.stderr.strip() or f"systemctl {action} failed")
        return p.stdout

    # ---- the public page ---------------------------------------------------

    def fetch_public(self, endpoint: str, timeout: float = 6) -> dict | None:
        url = public_url(endpoint)
        if not url:
            return None
        req = urllib.request.Request(url, headers={"User-Agent": "desk-agent-gui", "Accept": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return json.loads(r.read(1 << 20).decode("utf-8", "replace"))
        except (urllib.error.URLError, OSError, ValueError):
            return None


def in_thread(fn: Callable[[], Any], done: Callable[[Any, Exception | None], None], schedule: Callable[[Callable], Any]) -> None:
    """Run fn off the UI thread; call done(result, error) via `schedule`.

    `schedule` is GLib.idle_add in the app and a direct call in tests.
    """
    def work():
        try:
            res, err = fn(), None
        except Exception as e:  # noqa: BLE001: surfaced to the caller
            res, err = None, e
        schedule(lambda: done(res, err))

    threading.Thread(target=work, daemon=True).start()
