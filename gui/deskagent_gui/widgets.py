"""Small GTK building blocks in the app's Material style."""
from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
gi.require_version("Gdk", "4.0")
gi.require_version("Graphene", "1.0")
gi.require_version("Gsk", "4.0")
from gi.repository import Adw, Gdk, GObject, Graphene, Gsk, Gtk  # noqa: E402

from . import fmt  # noqa: E402


def label(text: str = "", *classes: str, xalign: float = 0.0, wrap: bool = True, selectable: bool = False,
          ellipsize: bool = False, max_chars: int = 0) -> Gtk.Label:
    lb = Gtk.Label(label=text, xalign=xalign, wrap=wrap, selectable=selectable)
    lb.set_wrap_mode(2)  # word-char
    if max_chars:
        lb.set_max_width_chars(max_chars)  # bounds the *natural* width so cards can sit side by side
    if ellipsize:
        lb.set_ellipsize(3)  # end
        lb.set_wrap(False)
    for c in classes:
        lb.add_css_class(c)
    return lb


def box(*children: Gtk.Widget, vertical: bool = True, spacing: int = 8, classes: tuple[str, ...] = (),
        hexpand: bool = False) -> Gtk.Box:
    b = Gtk.Box(orientation=Gtk.Orientation.VERTICAL if vertical else Gtk.Orientation.HORIZONTAL, spacing=spacing)
    for c in children:
        b.append(c)
    for c in classes:
        b.add_css_class(c)
    b.set_hexpand(hexpand)
    return b


def card(*children: Gtk.Widget, kind: str = "", hero: bool = False, spacing: int = 12) -> Gtk.Box:
    b = box(*children, spacing=spacing, classes=("md-card",) + ((kind,) if kind else ()) + (("md-hero",) if hero else ()))
    return b


def image(icon: str, size: int = 16) -> Gtk.Image:
    im = Gtk.Image.new_from_icon_name(icon)
    im.set_pixel_size(size)
    return im


def button(text: str = "", kind: str = "filled", icon: str | None = None, big: bool = False,
           toggle: bool = False) -> Gtk.Button:
    b = Gtk.ToggleButton() if toggle else Gtk.Button()
    b.add_css_class("md-btn")
    b.add_css_class(kind)
    if big:
        b.add_css_class("big")
    inner = Gtk.Box(spacing=8, halign=Gtk.Align.CENTER)
    if icon:
        inner.append(image(icon, 18 if big else 16))
    if text:
        inner.append(Gtk.Label(label=text))
    b.set_child(inner)
    return b


def set_button_content(b: Gtk.Button, text: str, icon: str | None = None) -> None:
    inner = Gtk.Box(spacing=8, halign=Gtk.Align.CENTER)
    if icon:
        inner.append(image(icon, 18 if b.has_css_class("big") else 16))
    inner.append(Gtk.Label(label=text))
    b.set_child(inner)


def icon_button(icon: str, tooltip: str, toggle: bool = False, danger: bool = False) -> Gtk.Button:
    b = Gtk.ToggleButton() if toggle else Gtk.Button()
    b.add_css_class("md-icon")
    if danger:
        b.add_css_class("danger")
    b.set_child(image(icon, 16))
    b.set_tooltip_text(tooltip)
    b.set_valign(Gtk.Align.CENTER)
    b.update_property([Gtk.AccessibleProperty.LABEL], [tooltip])
    return b


def avatar(text: str, kind: str = "", big: bool = False) -> Gtk.Label:
    lb = Gtk.Label(label=fmt.initial(text))
    lb.add_css_class("md-avatar")
    if kind:
        lb.add_css_class(kind)
    if big:
        lb.add_css_class("big")
    lb.set_valign(Gtk.Align.CENTER)
    lb.set_halign(Gtk.Align.CENTER)
    return lb


def icon_avatar(icon: str, kind: str = "", big: bool = False) -> Gtk.Box:
    b = Gtk.Box(halign=Gtk.Align.CENTER, valign=Gtk.Align.CENTER)
    b.add_css_class("md-avatar")
    if kind:
        b.add_css_class(kind)
    if big:
        b.add_css_class("big")
    im = image(icon, 28 if big else 18)
    im.set_hexpand(True)
    im.set_vexpand(True)
    b.append(im)
    b.set_hexpand(False)
    b.set_vexpand(False)
    return b


def badge(text: str, kind: str = "") -> Gtk.Label:
    lb = Gtk.Label(label=text)
    lb.add_css_class("md-badge")
    if kind:
        lb.add_css_class(kind)
    lb.set_valign(Gtk.Align.CENTER)
    return lb


def dot(kind: str = "") -> Gtk.Box:
    d = Gtk.Box(valign=Gtk.Align.CENTER)
    d.add_css_class("md-dot")
    if kind:
        d.add_css_class(kind)
    return d


def set_dot(d: Gtk.Box, kind: str) -> None:
    for k in ("ok", "warn", "bad"):
        d.remove_css_class(k)
    if kind:
        d.add_css_class(kind)


def set_kind(w: Gtk.Widget, kind: str, kinds: tuple[str, ...] = ("primary", "tonal", "tertiary", "error", "ok", "warn", "outlined", "low", "highest")) -> None:
    for k in kinds:
        w.remove_css_class(k)
    if kind:
        w.add_css_class(kind)


def clear(container: Gtk.Widget) -> None:
    child = container.get_first_child()
    while child is not None:
        nxt = child.get_next_sibling()
        container.remove(child)
        child = nxt


def clear_list(lb: Gtk.ListBox) -> None:
    while (row := lb.get_row_at_index(0)) is not None:
        lb.remove(row)


class NavRail(Gtk.Box):
    """Material navigation rail; becomes a bottom bar when `compact`."""

    __gsignals__ = {"selected": (GObject.SignalFlags.RUN_FIRST, None, (str,))}

    def __init__(self, items: list[tuple[str, str, str]]):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=6)
        self.add_css_class("md-rail")
        self.set_hexpand(False)
        self._buttons: dict[str, Gtk.Button] = {}
        self._compact = False
        self._current = items[0][0]
        for key, icon, text in items:
            b = Gtk.Button()
            b.add_css_class("md-nav")
            pill = Gtk.Box(halign=Gtk.Align.CENTER)
            pill.add_css_class("md-nav-pill")
            im = image(icon, 18)
            im.set_hexpand(True)
            im.set_margin_start(0)
            pill.append(im)
            col = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=4)
            col.append(pill)
            lb = Gtk.Label(label=text)
            col.append(lb)
            b.set_child(col)
            b.update_property([Gtk.AccessibleProperty.LABEL], [text])
            b.connect("clicked", lambda _b, k=key: self.select(k, emit=True))
            self._buttons[key] = b
            self.append(b)
        self.select(self._current)

    def select(self, key: str, emit: bool = False) -> None:
        self._current = key
        for k, b in self._buttons.items():
            (b.add_css_class if k == key else b.remove_css_class)("current")
        if emit:
            self.emit("selected", key)

    @GObject.Property(type=bool, default=False)
    def compact(self) -> bool:
        return self._compact

    @compact.setter
    def compact(self, value: bool) -> None:
        self._compact = value
        self.set_orientation(Gtk.Orientation.HORIZONTAL if value else Gtk.Orientation.VERTICAL)
        (self.add_css_class if value else self.remove_css_class)("compact")
        for b in self._buttons.values():
            b.set_hexpand(value)


class Segmented(Gtk.Box):
    """Two or more mutually exclusive pill segments."""

    __gsignals__ = {"changed": (GObject.SignalFlags.RUN_FIRST, None, (str,))}

    def __init__(self, items: list[tuple[str, str]]):
        super().__init__()
        self.add_css_class("linked")
        self.add_css_class("md-segmented")
        self._btns: dict[str, Gtk.ToggleButton] = {}
        first: Gtk.ToggleButton | None = None
        for key, text in items:
            b = Gtk.ToggleButton(label=text)
            if first is None:
                first = b
            else:
                b.set_group(first)
            b.connect("toggled", self._toggled, key)
            self._btns[key] = b
            self.append(b)
        self.value = items[0][0]
        self._btns[self.value].set_active(True)

    def _toggled(self, b: Gtk.ToggleButton, key: str) -> None:
        if b.get_active() and key != self.value:
            self.value = key
            self.emit("changed", key)

    def set_value(self, key: str) -> None:
        self._btns[key].set_active(True)


def _rgba(h: str) -> Gdk.RGBA:
    c = Gdk.RGBA()
    c.red, c.green, c.blue = (int(h[i:i + 2], 16) / 255 for i in (1, 3, 5))
    c.alpha = 1.0
    return c


class ColorDot(Gtk.Widget):
    """A filled circle. Drawn with GtkSnapshot, so no pycairo is needed."""

    __gtype_name__ = "DeskAgentColorDot"

    def __init__(self, colour: str, size: int = 12):
        super().__init__()
        self.colour, self.size = colour, size
        self.set_valign(Gtk.Align.CENTER)
        self.set_halign(Gtk.Align.CENTER)

    def do_measure(self, orientation, for_size):
        return (self.size, self.size, -1, -1)

    def do_snapshot(self, snapshot):
        w, h = self.get_width(), self.get_height()
        rect = Graphene.Rect().init(0, 0, w, h)
        rr = Gsk.RoundedRect()
        rr.init_from_rect(rect, min(w, h) / 2)
        snapshot.push_rounded_clip(rr)
        snapshot.append_color(_rgba(self.colour), rect)
        snapshot.pop()


class Timeline(Gtk.Widget):
    """A strip of coloured segments, one per recent sample.

    segments: (start, end, colour_hex, label) in epoch seconds.
    """

    __gtype_name__ = "DeskAgentTimeline"

    def __init__(self):
        super().__init__()
        self.set_hexpand(True)
        self.add_css_class("md-timeline")
        self.segments: list[tuple[float, float, str, str]] = []
        self.t0 = 0.0
        self.t1 = 1.0
        self.bg = "#dddddd"
        mc = Gtk.EventControllerMotion()
        mc.connect("motion", self._motion)
        mc.connect("leave", lambda *_: self.set_tooltip_text(None))
        self.add_controller(mc)

    def do_measure(self, orientation, for_size):
        return (120, 120, -1, -1) if orientation == Gtk.Orientation.HORIZONTAL else (56, 56, -1, -1)

    def set_data(self, segments, t0: float, t1: float, bg: str) -> None:
        self.segments, self.t0, self.t1, self.bg = segments, t0, max(t1, t0 + 1), bg
        self.queue_draw()

    def _x(self, t: float, w: float) -> float:
        return (t - self.t0) / (self.t1 - self.t0) * w

    def do_snapshot(self, snapshot):
        w, h = self.get_width(), self.get_height()
        full = Graphene.Rect().init(0, 0, w, h)
        rr = Gsk.RoundedRect()
        rr.init_from_rect(full, 14)
        snapshot.push_rounded_clip(rr)
        snapshot.append_color(_rgba(self.bg), full)
        for start, end, colour, _lbl in self.segments:
            x0, x1 = max(0.0, self._x(start, w)), min(float(w), self._x(end, w))
            if x1 - x0 < 1:
                x1 = x0 + 1
            snapshot.append_color(_rgba(colour), Graphene.Rect().init(x0, 0, x1 - x0, h))
        snapshot.pop()

    def _motion(self, _ctl, x, _y):
        w = self.get_width()
        if w <= 0:
            return
        t = self.t0 + x / w * (self.t1 - self.t0)
        for start, end, _c, lbl in self.segments:
            if start <= t <= end:
                self.set_tooltip_text(f"{lbl}\n{fmt.clock(start)} – {fmt.clock(end)}  ({fmt.duration(end - start)})")
                return
        self.set_tooltip_text("no data (agent off, asleep or paused)")


def toast_overlay_child(overlay: Adw.ToastOverlay, text: str) -> None:
    overlay.add_toast(Adw.Toast(title=text, timeout=3))
