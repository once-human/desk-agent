"""The review dialog for scan-history / scan-apps proposals."""
from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from ..widgets import avatar, badge, box, button, label  # noqa: E402


def _category_button(current, cats, on_pick):
    from .library import category_button
    return category_button(None, current, cats, on_pick)


class ScanDialog:
    def __init__(self, ctx, kind: str, cats: list[str], on_done):
        self.ctx, self.kind, self.cats, self.on_done = ctx, kind, cats, on_done
        self.dialog = Adw.Dialog()
        self.dialog.set_title("Scan browser history" if kind == "sites" else "Name the apps you've used")
        self.dialog.set_content_width(620)
        self.dialog.set_content_height(640)
        self.stack = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE)
        # loading page
        spin = Adw.Spinner() if hasattr(Adw, "Spinner") else Gtk.Spinner(spinning=True)
        if isinstance(spin, Gtk.Spinner):
            spin.set_size_request(48, 48)
        loading = box(spin, label("Reading locally…" if kind == "sites" else "Looking at your installed apps…", "md-title", xalign=0.5),
                      label("Nothing leaves this computer. History is read from a copy and only host names are kept.", "md-body", "md-dim", xalign=0.5),
                      spacing=14)
        loading.set_valign(Gtk.Align.CENTER)
        loading.set_margin_start(32)
        loading.set_margin_end(32)
        self.stack.add_named(loading, "loading")
        self.result = box(spacing=12)
        self.stack.add_named(self.result, "result")
        header = Adw.HeaderBar()
        tv = Adw.ToolbarView()
        tv.add_top_bar(header)
        tv.set_content(self.stack)
        self.bottom = box(vertical=False, spacing=10)
        self.bottom.set_margin_top(10)
        self.bottom.set_margin_bottom(14)
        self.bottom.set_margin_start(18)
        self.bottom.set_margin_end(18)
        tv.add_bottom_bar(self.bottom)
        self.dialog.set_child(tv)
        self.checks: list[tuple[Gtk.CheckButton, dict]] = []
        self.add_btn: Gtk.Button | None = None

    def start(self):
        self.stack.set_visible_child_name("loading")
        self.dialog.present(self.ctx.window)
        b = self.ctx.backend
        self.ctx.run(lambda: b.scan_history() if self.kind == "sites" else b.scan_apps(), self._loaded)

    def _loaded(self, res, err):
        if err:
            self._message("Couldn't scan", str(err))
            return
        props = res.get("proposals") or []
        if not props:
            self._message("Nothing new to add", "Everything you've used is already in your list." if self.kind == "apps"
                          else "No new sites found in the last 180 days that aren't already in your list.")
            return
        self.props = props
        self._fill(res)

    def _message(self, title, text):
        from ..widgets import clear
        clear(self.result)
        self.result.append(label(title, "md-headline", xalign=0.5))
        self.result.append(label(text, "md-body", "md-dim", xalign=0.5))
        self.result.set_valign(Gtk.Align.CENTER)
        self.result.set_margin_start(32)
        self.result.set_margin_end(32)
        close = button("Close", "tonal")
        close.connect("clicked", lambda *_: self.dialog.close())
        close.set_halign(Gtk.Align.CENTER)
        self.result.append(close)
        self.stack.set_visible_child_name("result")

    def _fill(self, res):
        from ..widgets import clear
        clear(self.result)
        self.result.set_valign(Gtk.Align.FILL)
        sites = self.kind == "sites"
        n = len(self.props)
        if sites:
            guess = sum(1 for p in self.props if p["source"] != "seed")
            head = f"{n} sites found"
            sub = (f"From {res.get('read', 0)} browser profile(s), last {res.get('days', 180)} days. "
                   f"{n - guess} matched the built-in list; {guess} are guesses, so check the category. "
                   f"{res.get('adultDropped', 0)} adult host(s) were left out and are never listed.")
        else:
            head = f"{n} apps to name"
            sub = f"Out of {res.get('recorded', 0)} you've focused. Names and categories come from each app's .desktop file."
        head_box = box(label(head, "md-headline"), label(sub, "md-body", "md-dim"), spacing=4)
        head_box.set_margin_start(20)
        head_box.set_margin_end(20)
        head_box.set_margin_top(4)
        self.result.append(head_box)

        lb = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        lb.add_css_class("md-list")
        lb.add_css_class("flat-rows")
        self.checks = []
        for p in self.props:
            key = p["host"] if sites else p["class"]
            cb = Gtk.CheckButton()
            cb.set_active(p["source"] in ("seed", "guess", "desktop"))
            cb.set_valign(Gtk.Align.CENTER)
            text = box(label(p["name"], "md-title", wrap=False, ellipsize=True),
                       label(key + (f" · {p['visits']} visits" if sites and p.get("visits") else "") +
                             (f" · focused {p['count']}×" if not sites and p.get("count") else ""),
                             "md-body", "md-dim", wrap=False, ellipsize=True), spacing=1, hexpand=True)
            src = p["source"]
            kind = {"seed": "ok", "desktop": "ok", "guess": "warn", "unknown": "warn"}.get(src, "")
            txt = {"seed": "known", "desktop": "from .desktop", "guess": "guess", "unknown": "unknown"}.get(src, src)
            cat = _category_button(p["category"], self.cats, lambda c, pp=p: pp.__setitem__("category", c))
            row = box(cb, avatar(p["name"], "tonal"), text, badge(txt, kind), cat, vertical=False, spacing=12)
            r = Gtk.ListBoxRow()
            r.set_child(row)
            r.set_activatable(False)
            lb.append(r)
            self.checks.append((cb, p))
        sc = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        sc.set_child(lb)
        sc.set_margin_start(18)
        sc.set_margin_end(18)
        self.result.append(sc)

        from ..widgets import clear as clear_box
        clear_box(self.bottom)
        all_btn = button("All", "text")
        none_btn = button("None", "text")
        all_btn.connect("clicked", lambda *_: self._set_all(True))
        none_btn.connect("clicked", lambda *_: self._set_all(False))
        self.add_btn = button("", "filled")
        self.add_btn.connect("clicked", lambda *_: self._import())
        self.bottom.append(all_btn)
        self.bottom.append(none_btn)
        self.bottom.append(Gtk.Box(hexpand=True))
        self.bottom.append(self.add_btn)
        for cb, _p in self.checks:
            cb.connect("toggled", lambda *_: self._update_count())
        self._update_count()
        self.stack.set_visible_child_name("result")

    def _selected(self):
        return [p for cb, p in self.checks if cb.get_active()]

    def _set_all(self, v):
        for cb, _p in self.checks:
            cb.set_active(v)

    def _update_count(self):
        from ..widgets import set_button_content
        n = len(self._selected())
        set_button_content(self.add_btn, f"Add {n} {'site' if self.kind == 'sites' else 'app'}{'s' if n != 1 else ''}")
        self.add_btn.set_sensitive(n > 0)

    def _import(self):
        sel = self._selected()
        b = self.ctx.backend
        if self.kind == "sites":
            rows = [{"host": p["host"], "name": p["name"], "category": p["category"], "hide": False} for p in sel]
            fn = lambda: b.sites_import(rows)  # noqa: E731
        else:
            rows = [{"class": p["class"], "name": p["name"], "category": p["category"]} for p in sel]
            fn = lambda: b.apps_import(rows)  # noqa: E731
        self.dialog.close()
        self.ctx.apply(fn, ok=f"Added {len(sel)}, applied live", after=self.on_done)
