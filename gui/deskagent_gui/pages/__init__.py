from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402


class Page(Gtk.ScrolledWindow):
    """A scrolling, centred column. Subclasses fill self.col."""

    title = ""

    def __init__(self, ctx):
        super().__init__()
        self.ctx = ctx
        self.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        self.set_vexpand(True)
        clamp = Adw.Clamp(maximum_size=980, tightening_threshold=760)
        self.col = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=16)
        self.col.set_margin_top(20)
        self.col.set_margin_bottom(28)
        self.col.set_margin_start(24)
        self.col.set_margin_end(24)
        clamp.set_child(self.col)
        self.set_child(clamp)

    # called every ~1.5 s with the agent's state (None when it is not running)
    def on_state(self, state: dict | None) -> None:
        pass

    # called when the page becomes visible
    def on_show(self) -> None:
        pass

    # called when the window crosses the phone-width breakpoint
    def set_compact(self, on: bool) -> None:
        m = 12 if on else 24
        self.col.set_margin_start(m)
        self.col.set_margin_end(m)
        self.col.set_margin_top(12 if on else 20)

    # called after the theme changed (colours used in custom drawing)
    def on_theme(self) -> None:
        pass
