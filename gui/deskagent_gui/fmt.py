"""Small, pure formatting helpers (no GTK)."""
from __future__ import annotations

import json
import time
from datetime import datetime, timezone


def parse_t(t: str | None) -> float | None:
    """Epoch seconds from a sample's `t` ("2026-10-26T10:00:00Z")."""
    if not t:
        return None
    try:
        return datetime.strptime(t, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc).timestamp()
    except ValueError:
        return None


def parse_iso(s: str | None) -> float | None:
    """Epoch seconds from the Go agent's RFC 3339 times (may have fractions/offsets)."""
    if not s or s.startswith("0001-"):
        return None
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except ValueError:
        return None


def ago(seconds: float | None) -> str:
    if seconds is None:
        return "never"
    s = max(0, int(seconds))
    if s < 5:
        return "just now"
    if s < 60:
        return f"{s}s ago"
    if s < 3600:
        return f"{s // 60} min ago"
    if s < 86400:
        h, m = divmod(s // 60, 60)
        return f"{h} h {m} min ago" if m else f"{h} h ago"
    return f"{s // 86400} d ago"


def duration(seconds: float) -> str:
    s = max(0, int(seconds))
    if s < 60:
        return f"{s}s"
    if s < 3600:
        return f"{s // 60}m"
    h, m = divmod(s // 60, 60)
    if h < 24:
        return f"{h}h {m:02d}m"
    return f"{h // 24}d {h % 24}h"


def clock(epoch: float) -> str:
    return datetime.fromtimestamp(epoch).strftime("%H:%M")


CATEGORY_LABEL = {
    "ide": "IDE", "ai": "AI", "tool": "Tool", "other": "Other", "private": "Private",
}


def category_label(cat: str | None) -> str:
    if not cat:
        return ""
    return CATEGORY_LABEL.get(cat, cat.capitalize())


def initial(name: str | None) -> str:
    for ch in (name or "?").strip():
        if ch.isalnum():
            return ch.upper()
    return "?"


def sample_facts(s: dict | None) -> list[tuple[str, str, str]]:
    """What a sample tells the site, as (icon, label, value) rows.

    These are the exact fields on the wire, nothing inferred. How the site
    words them is up to the site.
    """
    if not s:
        return []
    rows: list[tuple[str, str, str]] = []
    if not s.get("active"):
        idle = s.get("idleS")
        rows.append(("idle", "Presence", f"Away, idle {duration(idle)}" if idle else "Away"))
    else:
        rows.append(("active", "Presence", "At the computer"))
    app = s.get("app")
    if app:
        rows.append(("app", "App", f"{app.get('name')} · {category_label(app.get('category'))}"))
    site = s.get("site")
    if site:
        rows.append(("site", "Site", f"{site.get('name')} · {category_label(site.get('category'))}"))
    if s.get("repo"):
        rows.append(("repo", "Repo", s["repo"]))
    if s.get("claude") is not None:
        rows.append(("claude", "Claude Code", "working" if s["claude"] else "idle"))
    m = s.get("media")
    if m:
        what = m.get("player") or "player"
        if m.get("source") == "youtube":
            n = m.get("videos")
            what = "watching something on YouTube" + (f" ({n} video{'s' if n != 1 else ''})" if n else "")
        elif m.get("title"):
            what = f"{m.get('artist') + ' — ' if m.get('artist') else ''}{m['title']}"
        else:
            what = f"{m.get('source') or what} (no title shared)"
        rows.append(("media", "Media", f"{m.get('state', '')}: {what}".strip(": ")))
    b = s.get("battery")
    if b:
        rows.append(("battery", "Battery", f"{b.get('pct')}%" + (", charging" if b.get("charging") else "")))
    return rows


def pretty_json(s: dict | None) -> str:
    return json.dumps(s, indent=2, ensure_ascii=False) if s else "(nothing is being sent)"


def primary_category(s: dict | None) -> str:
    """Category used to colour a sample on the timeline."""
    if not s or not s.get("active"):
        return ""
    if s.get("site"):
        return s["site"].get("category") or "other"
    if s.get("app"):
        return s["app"].get("category") or "other"
    return "other"


def primary_label(s: dict | None) -> str:
    if not s:
        return "no data"
    if not s.get("active"):
        return "away"
    if s.get("site"):
        return s["site"].get("name") or "a site"
    if s.get("app"):
        return s["app"].get("name") or "an app"
    return "active"


def public_summary(pub: dict | None) -> dict:
    """Normalise whatever /api/desk returns into a few displayable fields.

    The shape beyond {online, lastSeen, active, lastActive, doing, doingSince,
    playing} is not known to the app, so `doing` and `playing` are shown as
    text whether they are strings or objects.
    """
    pub = pub or {}

    def text(v):
        if v is None:
            return None
        if isinstance(v, str):
            return v
        if isinstance(v, dict):
            for k in ("label", "text", "name", "title", "summary"):
                if isinstance(v.get(k), str) and v[k]:
                    return v[k]
            parts = [str(x) for x in v.values() if isinstance(x, (str, int)) and not isinstance(x, bool)]
            return " · ".join(parts) or None
        return str(v)

    return {
        "online": bool(pub.get("online")),
        "active": bool(pub.get("active")),
        "doing": text(pub.get("doing")),
        "playing": text(pub.get("playing")),
        "lastSeen": pub.get("lastSeen"),
        "lastActive": pub.get("lastActive"),
    }


def now() -> float:
    return time.time()
