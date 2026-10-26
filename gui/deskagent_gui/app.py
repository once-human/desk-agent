from __future__ import annotations

import json
import os
import sys

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
gi.require_version("Gdk", "4.0")
from gi.repository import Adw, Gdk, Gio, GLib, Gtk  # noqa: E402

from . import APP_ID, __version__, theme  # noqa: E402
from .backend import Backend, in_thread  # noqa: E402
from .pages.health import HealthPage  # noqa: E402
from .pages.library import LibraryPage  # noqa: E402
from .pages.live import LivePage  # noqa: E402
from .pages.privacy import PrivacyPage  # noqa: E402
from .widgets import ColorDot, NavRail, box, label  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_SEED = "#6750A4"
PRESETS = ["#6750A4", "#0b6bcb", "#00897b", "#2e7d32", "#c77700", "#d6336c", "#8e24aa", "#546e7a"]

NAV = [
    ("live", "deskagent-live-symbolic", "Live"),
    ("privacy", "deskagent-privacy-symbolic", "Privacy"),
    ("library", "deskagent-library-symbolic", "Library"),
    ("health", "deskagent-health-symbolic", "Health"),
]


def settings_path() -> str:
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "desk-agent", "gui.json")


def load_settings() -> dict:
    try:
        with open(settings_path()) as f:
            d = json.load(f)
            return d if isinstance(d, dict) else {}
    except (OSError, ValueError):
        return {}


def save_settings(d: dict) -> None:
    try:
        os.makedirs(os.path.dirname(settings_path()), mode=0o700, exist_ok=True)
        with open(settings_path(), "w") as f:
            json.dump(d, f)
    except OSError:
        pass


class Ctx:
    """What pages need from the app: backend, theme, async helpers, toasts."""

    def __init__(self, window: "MainWindow", backend: Backend, seed: str):
        self.window = window
        self.backend = backend
        self.seed = seed
        self.dark = False
        self.scheme: dict[str, str] = theme.scheme(seed, False)
        self.state: dict | None = None
        self.config: dict | None = None
        self._cat_cache: dict[tuple, str] = {}
        self._polling = False

    # ---- async ----
    def run(self, fn, done):
        in_thread(fn, done, GLib.idle_add)

    def apply(self, fn, ok: str | None = None, after=None):
        """Run a change off the UI thread, then toast the outcome."""
        def done(res, err):
            if err:
                self.toast(str(err), long=True)
                return
            msg = ok
            if isinstance(res, str) and "rejected it" in res:
                msg = res.strip().splitlines()[-1]
            if msg:
                self.toast(msg)
            if after:
                after()
            self.refresh_config()
        self.run(fn, done)

    def toast(self, text: str, long: bool = False):
        self.window.toasts.add_toast(Adw.Toast(title=text, timeout=7 if long else 3))

    # ---- state / config ----
    def poll_now(self):
        if self._polling:
            return True
        self._polling = True

        def done(res, _err):
            self._polling = False
            self.state = res
            self.window.on_state(res)
        self.run(self.backend.state, done)
        return True

    def poll_later(self, ms: int):
        GLib.timeout_add(ms, lambda: (self.poll_now(), False)[1])

    def refresh_config(self, cb=None):
        def done(res, err):
            if not err:
                self.config = res
            if cb:
                cb(self.config)
        self.run(self.backend.config, done)

    # ---- colours ----
    def cat_colour(self, cat: str) -> str:
        k = (cat, self.seed, self.dark)
        if k not in self._cat_cache:
            self._cat_cache[k] = theme.category_colour(cat, self.seed, self.dark)
        return self._cat_cache[k]


class MainWindow(Adw.ApplicationWindow):
    def __init__(self, app: Adw.Application, backend: Backend, seed: str):
        super().__init__(application=app, title="Desk Agent", default_width=1040, default_height=760)
        self.add_css_class("md-window")
        self.set_size_request(340, 480)
        self.ctx = Ctx(self, backend, seed)
        self._provider: Gtk.CssProvider | None = None
        self.toasts = Adw.ToastOverlay()

        self.pages = {}
        self.stack = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE, transition_duration=140)
        self.stack.set_hexpand(True)
        self.stack.set_vexpand(True)
        for key, _icon, _t in NAV:
            cls = {"live": LivePage, "privacy": PrivacyPage, "library": LibraryPage, "health": HealthPage}[key]
            page = cls(self.ctx)
            self.pages[key] = page
            self.stack.add_named(page, key)

        self.rail = NavRail(NAV)
        self.rail.connect("selected", lambda _r, k: self.show_page(k))
        self.pane = Gtk.Box()
        self.pane.add_css_class("md-pane")
        self.pane.set_overflow(Gtk.Overflow.HIDDEN)
        self.pane.set_hexpand(True)
        self.pane.append(self.stack)
        self.root_box = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL)
        self.root_box.append(self.rail)
        self.root_box.append(self.pane)

        header = Adw.HeaderBar()
        header.add_css_class("flat")
        header.set_title_widget(Adw.WindowTitle(title="Desk Agent", subtitle=""))
        header.pack_end(self._menu_button())
        tv = Adw.ToolbarView()
        tv.add_top_bar(header)
        tv.set_content(self.root_box)
        self.toasts.set_child(tv)
        self.set_content(self.toasts)

        bp = Adw.Breakpoint.new(Adw.BreakpointCondition.parse("max-width: 640sp"))
        bp.connect("apply", lambda *_: self._compact(True))
        bp.connect("unapply", lambda *_: self._compact(False))
        self.add_breakpoint(bp)

        sm = Adw.StyleManager.get_default()
        sm.connect("notify::dark", lambda *_: self.apply_theme())
        self.apply_theme()
        self.current = "live"
        self.ctx.refresh_config()
        self.ctx.poll_now()
        self._ticks = 0
        GLib.timeout_add(3000, self._tick)

    def _tick(self):
        # Every 3 seconds while the window is in front; every 30 when it's behind
        # other windows or minimised, so an open settings app costs next to nothing.
        self._ticks += 1
        if self.is_active() or self._ticks % 10 == 0:
            self.ctx.poll_now()
        return True

    # ---- layout ----
    def _compact(self, on: bool):
        (self.add_css_class if on else self.remove_css_class)("compact")
        for p in self.pages.values():
            p.set_compact(on)
        self.rail.set_property("compact", on)
        self.root_box.set_orientation(Gtk.Orientation.VERTICAL if on else Gtk.Orientation.HORIZONTAL)
        if on:
            self.root_box.reorder_child_after(self.rail, self.pane)
            self.pane.add_css_class("compact")
        else:
            self.root_box.reorder_child_after(self.pane, self.rail)
            self.pane.remove_css_class("compact")

    def show_page(self, key: str):
        self.current = key
        self.rail.select(key)
        self.stack.set_visible_child_name(key)
        self.pages[key].on_show()
        if self.ctx.state is not None:
            self.pages[key].on_state(self.ctx.state)

    def on_state(self, st):
        # Only the page on screen is redrawn; the others catch up when shown.
        page = self.pages.get(self.current)
        if page is not None:
            page.on_state(st)

    # ---- theme ----
    def apply_theme(self, seed: str | None = None):
        if seed:
            self.ctx.seed = seed
        dark = Adw.StyleManager.get_default().get_dark()
        self.ctx.dark = dark
        self.ctx.scheme = theme.scheme(self.ctx.seed, dark)
        # libadwaita >= 1.6 (GTK >= 4.16) reads CSS variables for its own widgets.
        css_vars = Gtk.get_minor_version() >= 16 or Adw.get_minor_version() >= 6
        with open(os.path.join(HERE, "style.css")) as f:
            css = theme.render_css(f.read(), self.ctx.scheme, self.ctx.seed, dark, css_vars)
        display = Gdk.Display.get_default()
        if self._provider is not None:
            Gtk.StyleContext.remove_provider_for_display(display, self._provider)
        self._provider = Gtk.CssProvider()
        self._provider.load_from_string(css) if hasattr(self._provider, "load_from_string") else self._provider.load_from_data(css.encode())
        Gtk.StyleContext.add_provider_for_display(display, self._provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
        for p in getattr(self, "pages", {}).values():
            p.on_theme()

    def set_seed(self, seed: str):
        self.apply_theme(seed)
        s = load_settings()
        s["seed"] = seed
        save_settings(s)

    # ---- header menu ----
    def _menu_button(self) -> Gtk.Box:
        pop = Gtk.Popover()
        col = box(spacing=10)
        col.set_margin_top(12)
        col.set_margin_bottom(12)
        col.set_margin_start(12)
        col.set_margin_end(12)
        col.append(label("Theme colour", "md-title"))
        col.append(label("Light or dark follows your system.", "md-body", "md-dim"))
        flow = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, max_children_per_line=4, min_children_per_line=4,
                           column_spacing=8, row_spacing=8)
        for hexc in PRESETS:
            sw = Gtk.Button()
            sw.add_css_class("circular")
            sw.set_size_request(40, 40)
            area = ColorDot(hexc, 26)
            sw.set_child(area)
            sw.update_property([Gtk.AccessibleProperty.LABEL], [f"Use {hexc}"])
            sw.connect("clicked", lambda _b, c=hexc: (self.set_seed(c), pop.popdown()))
            flow.append(sw)
        col.append(flow)
        if hasattr(Gtk, "ColorDialogButton"):
            cdb = Gtk.ColorDialogButton(dialog=Gtk.ColorDialog())
            rgba = Gdk.RGBA()
            rgba.parse(self.ctx.seed)
            cdb.set_rgba(rgba)

            def picked(btn, _p):
                c = btn.get_rgba()
                self.set_seed("#{:02x}{:02x}{:02x}".format(round(c.red * 255), round(c.green * 255), round(c.blue * 255)))
            cdb.connect("notify::rgba", picked)
            row = box(label("Custom", "md-body"), cdb, vertical=False, spacing=10)
            col.append(row)
        pop.set_child(col)
        menu = Gio.Menu()
        menu.append("About Desk Agent", "app.about")
        menu.append("Quit", "app.quit")
        mb = Gtk.MenuButton(icon_name="open-menu-symbolic", tooltip_text="Menu")
        mb.update_property([Gtk.AccessibleProperty.LABEL], ["Menu"])
        mb.set_menu_model(menu)
        theme_btn = Gtk.MenuButton(icon_name="deskagent-palette-symbolic", tooltip_text="Theme colour", popover=pop)
        theme_btn.update_property([Gtk.AccessibleProperty.LABEL], ["Theme colour"])
        box_ = Gtk.Box(spacing=2)
        box_.append(theme_btn)
        box_.append(mb)
        return box_


class DeskAgentApp(Adw.Application):
    def __init__(self, backend: Backend | None = None, seed: str | None = None, start_page: str = "live",
                 flags=Gio.ApplicationFlags.DEFAULT_FLAGS):
        super().__init__(application_id=APP_ID, flags=flags)
        self.backend = backend or Backend()
        self.seed = seed or load_settings().get("seed") or DEFAULT_SEED
        try:
            theme.hex_to_rgb(self.seed)
        except ValueError:
            self.seed = DEFAULT_SEED
        self.start_page = start_page
        self.win: MainWindow | None = None
        GLib.set_application_name("Desk Agent")

    def do_startup(self):
        Adw.Application.do_startup(self)
        for name, cb in (("about", self._about), ("quit", lambda *_: self.quit())):
            a = Gio.SimpleAction.new(name, None)
            a.connect("activate", cb)
            self.add_action(a)
        self.set_accels_for_action("app.quit", ["<Primary>q"])
        display = Gdk.Display.get_default()
        icons = Gtk.IconTheme.get_for_display(display)
        icons.add_search_path(os.path.join(HERE, "icons"))

    def do_activate(self):
        if self.win is None:
            self.win = MainWindow(self, self.backend, self.seed)
            if self.start_page != "live":
                self.win.show_page(self.start_page)
        self.win.present()

    def _about(self, *_):
        d = Adw.AboutDialog(application_name="Desk Agent", application_icon=APP_ID, version=__version__,
                            developer_name="Onkar Yaglewad", website="https://github.com/once-human/desk-agent",
                            license_type=Gtk.License.MIT_X11,
                            comments="Shows what this laptop is doing, on your terms. Nothing is sent that you can't see here first.")
        d.present(self.win)


def main(argv: list[str] | None = None) -> int:
    import argparse
    ap = argparse.ArgumentParser(prog="desk-agent-gui", description="GTK front end for desk-agent")
    ap.add_argument("--seed", help="theme seed colour, e.g. #6750A4")
    ap.add_argument("--page", choices=[k for k, *_ in NAV], default="live")
    args = ap.parse_args(argv)
    app = DeskAgentApp(seed=args.seed, start_page=args.page)
    return app.run([sys.argv[0]])
