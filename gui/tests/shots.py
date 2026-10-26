#!/usr/bin/env python3
"""Drive the GUI against a real agent in a fake desktop and save PNGs.

  GSK_RENDERER=cairo xvfb-run -a /usr/bin/python3.12 gui/tests/shots.py OUT \
      [--dark] [--seed #hex] [--size WxH] [--only a,b] [--check]

With --check it also asserts on widget state (exit status 1 on failure), so CI
can use it as a behaviour test, not just a picture generator.
"""
import argparse, json, os, sqlite3, sys, time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
sys.path.insert(0, os.path.join(ROOT, "gui"))
sys.path.insert(0, os.path.join(ROOT, "test", "e2e"))

ap = argparse.ArgumentParser()
ap.add_argument("out")
ap.add_argument("--dark", action="store_true")
ap.add_argument("--seed", default="#6750A4")
ap.add_argument("--size", default="1040x1100")
ap.add_argument("--only", default="")
ap.add_argument("--check", action="store_true")
args = ap.parse_args()
os.makedirs(args.out, exist_ok=True)

from fakeenv import FakeEnv  # noqa: E402

fe = FakeEnv(interval_s=5)
os.environ.update(HOME=fe.home, XDG_RUNTIME_DIR=fe.rt, DESK_AGENT_KEY="testkey", DESK_AGENT_BIN=fe.bin)
os.environ.pop("XDG_CONFIG_HOME", None)
fe.public.update({"online": True, "active": True, "doing": "Coding in Kitty", "playing": None})

# a Chromium history with a few hosts, one adult, so the scan dialog has content
hist = os.path.join(fe.home, ".config", "chromium", "Default")
os.makedirs(hist)
db = sqlite3.connect(os.path.join(hist, "History"))
db.execute("CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER)")
db.execute("CREATE TABLE visits (id INTEGER PRIMARY KEY, url INTEGER, visit_time INTEGER, visit_duration INTEGER)")
now_chrome = (int(time.time()) + 11644473600) * 1_000_000
hosts = [("github.com", 40), ("monkeytype.com", 25), ("docs.python.org", 18), ("news.ycombinator.com", 14),
         ("figma.com", 12), ("some-learning-academy.org", 9), ("pornhub.com", 30), ("random-tool-site.io", 4)]
for i, (h, n) in enumerate(hosts, 1):
    db.execute("INSERT INTO urls VALUES (?,?,?,?)", (i, f"https://{h}/SECRET/path?q=1", "SECRET TITLE", n))
    for _ in range(n):
        db.execute("INSERT INTO visits (url, visit_time, visit_duration) VALUES (?,?,?)", (i, now_chrome - 3600_000_000, 60_000_000))
db.commit(); db.close()

import gi  # noqa: E402
gi.require_version("Gtk", "4.0"); gi.require_version("Adw", "1")
from gi.repository import Adw, Gio, GLib, Gtk  # noqa: E402

from deskagent_gui import app as A  # noqa: E402
from deskagent_gui.backend import Backend  # noqa: E402

application = A.DeskAgentApp(backend=Backend(fe.bin), seed=args.seed, flags=Gio.ApplicationFlags.NON_UNIQUE)
W, H = (int(x) for x in args.size.split("x"))
suffix = "dark" if args.dark else "light"
fails = []


def check(name, cond, extra=""):
    print(("PASS " if cond else "FAIL ") + name + (f"  [{extra}]" if extra and not cond else ""))
    if not cond:
        fails.append(name)


def snap_to(widget, path):
    paintable = Gtk.WidgetPaintable.new(widget)
    snapshot = Gtk.Snapshot()
    paintable.snapshot(snapshot, widget.get_width(), widget.get_height())
    tex = widget.get_native().get_renderer().render_texture(snapshot.to_node(), None)
    tex.save_to_png(path)


def walk(w):
    yield w
    c = w.get_first_child()
    while c is not None:
        yield from walk(c)
        c = c.get_next_sibling()


def find_label(root, text):
    for w in walk(root):
        if isinstance(w, Gtk.Label) and text in (w.get_text() or ""):
            return w
    return None


def find_buttons(root, text):
    return [w for w in walk(root) if isinstance(w, Gtk.Button) and any(isinstance(l, Gtk.Label) and text in l.get_text() for l in walk(w))]


def wait_for(pred, secs=6):
    """generator helper: yields until pred() or timeout"""
    end = time.time() + secs
    while time.time() < end and not pred():
        yield 200


# ---------------------------------------------------------------- scenarios
def sc_live(win, snap):
    win.show_page("live")
    yield 2500
    snap("live")
    if args.check:
        p = win.pages["live"]
        check("live: hero says Sending", find_label(p, "Sending") is not None)
        check("live: shows the app fact", find_label(p, "Kitty · Terminal") is not None)
        check("live: delivery says Delivered", find_label(p, "Delivered") is not None)
        check("live: public card shows the site's text", any("Coding in Kitty" in (w.get_text() or "") for w in walk(p) if isinstance(w, Gtk.Label)))


def sc_live_json(win, snap):
    win.show_page("live")
    yield 1500
    for b in find_buttons(win.pages["live"], "exact JSON"):
        b.emit("clicked")
    yield 800
    snap("live-json")
    if args.check:
        txt = [w.get_text() for w in walk(win.pages["live"]) if isinstance(w, Gtk.Label) and '"active"' in (w.get_text() or "")]
        check("live: JSON panel shows the exact sample", bool(txt))
        check("live: window title never in the JSON panel", not any("SECRET-WINDOW-TITLE" in t for t in txt))
    for b in find_buttons(win.pages["live"], "exact JSON"):
        b.emit("clicked")


def sc_live_paused(win, snap):
    win.show_page("live")
    btn = find_buttons(win.pages["live"], "Pause")[0]
    btn.emit("clicked")
    yield from wait_for(lambda: win.ctx.state and win.ctx.state.get("paused"))
    yield 1200
    snap("live-paused")
    if args.check:
        check("live: paused is reflected from the agent", bool(win.ctx.state and win.ctx.state["paused"]))
        check("live: hero says Paused", find_label(win.pages["live"], "Paused") is not None)
    find_buttons(win.pages["live"], "Resume")[0].emit("clicked")
    yield from wait_for(lambda: win.ctx.state and not win.ctx.state.get("paused"))
    yield 800


def sc_live_private(win, snap):
    win.show_page("live")
    yield 500
    pb = win.pages["live"].private_btn
    pb.set_active(True)
    yield from wait_for(lambda: win.ctx.state and win.ctx.state.get("private"))
    yield 1500
    snap("live-private")
    if args.check:
        check("live: private mode reaches the agent", bool(win.ctx.state and win.ctx.state["private"]))
        sample = (win.ctx.state or {}).get("sample") or {}
        check("live: private sample has no app", "app" not in sample, str(sample))
    pb.set_active(False)
    yield from wait_for(lambda: win.ctx.state and not win.ctx.state.get("private"))
    yield 800


def sc_privacy(win, snap):
    win.show_page("privacy")
    yield 1500
    snap("privacy")


def sc_privacy_edit(win, snap):
    win.show_page("privacy")
    yield 1200
    page = win.pages["privacy"]
    page.repo_entry.set_text("client-secret-repo")
    page._add_repo()
    yield from wait_for(lambda: (win.ctx.config or {}).get("values", {}).get("hide_repos"), 5)
    yield 1500
    snap("privacy-hidden-repo")
    if args.check:
        check("privacy: hidden repo saved via the CLI", "client-secret-repo" in (win.ctx.config["values"]["hide_repos"]))
        check("privacy: chip is shown", find_label(page, "client-secret-repo") is not None)
    page._remove_repo("client-secret-repo")
    yield 1500


def sc_library_sites(win, snap):
    win.show_page("library")
    yield 2200
    snap("library-sites")
    if args.check:
        p = win.pages["library"]
        check("library: built-in sites listed", p._visible_count() > 50, str(p._visible_count()))


def sc_library_search(win, snap):
    p = win.pages["library"]
    win.show_page("library")
    yield 1000
    p.search.set_text("git")
    yield 800
    snap("library-search")
    if args.check:
        check("library: search filters the list", 0 < p._visible_count() < 20, str(p._visible_count()))
    p.search.set_text("")
    yield 300


def sc_library_category(win, snap):
    win.show_page("library")
    p = win.pages["library"]
    yield 1000
    p.search.set_text("github")
    yield 600
    row = p.list.get_row_at_index(0)
    while row is not None and not row.get_child_visible() or (row is not None and not p._filter(row)):
        row = row.get_next_sibling()
    mb = [w for w in walk(row) if isinstance(w, Gtk.MenuButton)][0]
    mb.popup()
    yield 700
    snap("library-category-popover")
    if args.check:
        pop = mb.get_popover()
        chips = [w for w in walk(pop) if isinstance(w, Gtk.Button)] if pop else []
        check("library: category popover offers every category", len(chips) == len(p.cats["sites"]), f"{len(chips)} vs {len(p.cats['sites'])}")
        check("library: current category is marked", any(c.has_css_class("selected") for c in chips))
    mb.popdown()
    yield 300
    # pick "tool" for github.com through the same code path the chips use
    p._set("github.com", category="tool")
    yield from wait_for(lambda: any(r["host"] == "github.com" and r["category"] == "tool" for r in p.data["sites"]), 6)
    yield 700
    snap("library-after-change")
    if args.check:
        check("library: category change is written by the CLI and re-read", any(r["host"] == "github.com" and r["category"] == "tool" and r["source"] == "user" for r in p.data["sites"]))
        txt = open(os.path.join(fe.home, ".config", "desk-agent", "sites.toml")).read()
        check("library: sites.toml has the override", '["github.com"]' in txt and 'category = "tool"' in txt, txt)
    p._set("github.com", category="code")
    yield 1500
    p.search.set_text("")
    yield 300


def sc_library_edit(win, snap):
    win.show_page("library")
    p = win.pages["library"]
    yield 800
    r = next(x for x in p.data["sites"] if x["host"] == "monkeytype.com") if any(x["host"] == "monkeytype.com" for x in p.data["sites"]) else p.data["sites"][0]
    p._edit_dialog(r)
    yield 900
    snap("library-rename-dialog")


def sc_library_add(win, snap):
    win.show_page("library")
    p = win.pages["library"]
    yield 600
    p._add_dialog()
    yield 900
    snap("library-add-dialog")


def sc_close_dialogs(win, snap):
    for w in list(walk(win)):
        if isinstance(w, Adw.AlertDialog) or isinstance(w, Adw.Dialog):
            w.force_close()
    yield 400


def sc_library_scan(win, snap):
    win.show_page("library")
    p = win.pages["library"]
    yield 600
    p._scan()
    yield 300
    snap("scan-loading")
    yield 5000
    snap("scan-sites")
    if args.check:
        d = [w for w in walk(win) if isinstance(w, Gtk.CheckButton)]
        names = " ".join(w.get_text() for w in walk(win) if isinstance(w, Gtk.Label))
        check("scan: proposals are listed", len(d) >= 5, str(len(d)))
        check("scan: adult host never shown", "pornhub" not in names.lower())
        check("scan: no URL path or title shown", "SECRET" not in names)
    # import everything checked
    for b in find_buttons(win, "Add "):
        if "site" in [l.get_text() for l in walk(b) if isinstance(l, Gtk.Label)][0]:
            b.emit("clicked")
            break
    yield 3000
    if args.check:
        txt = open(os.path.join(fe.home, ".config", "desk-agent", "sites.toml")).read()
        check("scan: import wrote the checked sites", "github.com" in txt and "news.ycombinator.com" in txt, txt[:200])
        check("scan: unchecked (unknown) site was not imported", "monkeytype.com" not in txt)
        check("scan: import never wrote the adult host", "pornhub" not in txt)
        check("scan: import wrote no URL or title", "SECRET" not in txt and "https://" not in txt)
    snap("library-after-scan")


def sc_library_apps(win, snap):
    for c in ("firefox", "discord", "obsidian", "mystery-app"):
        fe.focus(c)
        time.sleep(0.3)
    fe.focus("kitty")
    win.show_page("library")
    p = win.pages["library"]
    yield 500
    p.seg.set_value("apps")
    yield 2000
    snap("library-apps")
    if args.check:
        classes = [r["class"] for r in p.data["apps"]]
        check("apps: focused app that has no name yet is listed", "mystery-app" in classes, str(classes[:10]))
    p.seg.set_value("sites")
    yield 300


def sc_health(win, snap):
    win.show_page("health")
    yield 4000
    snap("health")
    if args.check:
        h = win.pages["health"]
        check("health: checks are listed", sum(1 for _ in walk(h.checks) if isinstance(_, Gtk.ListBoxRow)) >= 8)
        check("health: summary is not an error", find_label(h, "to fix") is None)


def sc_health_keydialog(win, snap):
    win.show_page("health")
    yield 500
    win.pages["health"]._key_dialog()
    yield 800
    snap("health-key-dialog")


def sc_theme_popover(win, snap):
    win.show_page("live")
    yield 300
    mbs = [w for w in walk(win) if isinstance(w, Gtk.MenuButton) and w.get_tooltip_text() == "Theme colour"]
    mbs[0].popup()
    yield 800
    snap("theme-popover")
    mbs[0].popdown()
    yield 300


def sc_theme_switch(win, snap):
    win.show_page("live")
    win.set_seed("#00897b")
    yield 1200
    snap("live-teal")
    win.set_seed(args.seed)
    yield 500


def sc_live_offline(win, snap):
    win.show_page("live")
    fe.stop_agent()
    yield from wait_for(lambda: win.ctx.state is None, 8)
    yield 1500
    snap("live-offline")
    if args.check:
        check("live: offline state when the agent stops", win.ctx.state is None)
        check("live: offers to start the agent", find_label(win.pages["live"], "Agent is not running") is not None)
    fe.start_agent()
    yield from wait_for(lambda: win.ctx.state is not None, 8)
    yield 500


def sc_min_widths(win, snap):
    """No page may need more than a phone's width, or the bottom bar gets cut off."""
    win._compact(True)
    for key in ("live", "privacy", "library", "health"):
        win.show_page(key)
        yield 1200
        page = win.pages[key]
        minimum = page.measure(Gtk.Orientation.HORIZONTAL, -1)[0]
        print(f"   min width of {key}: {minimum}px")
        if minimum > 330:
            wide = sorted(((w.measure(Gtk.Orientation.HORIZONTAL, -1)[0], type(w).__name__, (w.get_css_name(), tuple(w.get_css_classes()))) for w in walk(page) if w.get_visible()), reverse=True)[:6]
            for item in wide:
                print("      ", item)
        if args.check:
            check(f"layout: {key} fits a 360px phone (needs {minimum}px)", minimum <= 330)
    win._compact(False)


ORDER = [sc_live, sc_live_json, sc_live_paused, sc_live_private, sc_privacy, sc_privacy_edit, sc_library_sites,
         sc_library_search, sc_library_category, sc_library_edit, sc_close_dialogs, sc_library_add, sc_close_dialogs,
         sc_library_scan, sc_library_apps, sc_health, sc_health_keydialog, sc_close_dialogs, sc_theme_popover,
         sc_theme_switch, sc_live_offline, sc_min_widths]
only = [x for x in args.only.split(",") if x]
scenarios = [s for s in ORDER if not only or s.__name__[3:] in only or s is sc_close_dialogs]


def drive():
    win = application.win
    if win is None:
        fe.close(); application.quit(); return False
    win.set_default_size(W, H)
    Adw.StyleManager.get_default().set_color_scheme(Adw.ColorScheme.FORCE_DARK if args.dark else Adw.ColorScheme.FORCE_LIGHT)
    it = iter(scenarios)
    cur = {"gen": None, "name": ""}

    def snap(name):
        snap_to(win, os.path.join(args.out, f"{name}-{suffix}.png"))

    def advance():
        while True:
            if cur["gen"] is None:
                try:
                    fn = next(it)
                except StopIteration:
                    fe.close()
                    application.quit()
                    return False
                cur["name"] = fn.__name__
                cur["gen"] = fn(win, snap)
            try:
                ms = next(cur["gen"])
            except StopIteration:
                cur["gen"] = None
                continue
            except Exception as e:  # a crashing scenario is a failed check, not a hang
                import traceback; traceback.print_exc()
                fails.append(f"{cur['name']} crashed: {e}")
                cur["gen"] = None
                continue
            GLib.timeout_add(ms, advance)
            return False

    GLib.timeout_add(6500, advance)  # let the agent produce samples first
    return False


application.connect("activate", lambda *_: GLib.idle_add(drive))
application.run([sys.argv[0]])
wire = json.dumps(fe.received)
if args.check:
    check("window title never on the wire", "SECRET-WINDOW-TITLE" not in wire)
print("FAILS:", fails or "none")
sys.exit(1 if fails else 0)
