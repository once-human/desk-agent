from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from .. import fmt  # noqa: E402
from ..widgets import (Segmented, avatar, badge, box, button, clear_list, icon_button,  # noqa: E402
                       label, set_button_content)
from . import Page  # noqa: E402
from .scan import ScanDialog  # noqa: E402

SOURCE = {"user": ("yours", "primary"), "seed": ("built-in", ""), "seen": ("seen, not named yet", "warn")}


def category_button(ctx, current: str, cats: list[str], on_pick) -> Gtk.MenuButton:
    """A small pill showing a category; opens a chip grid to change it.

    The popover is built on demand so a long list stays cheap.
    """
    mb = Gtk.MenuButton()
    mb.add_css_class("md-cat")
    mb.set_valign(Gtk.Align.CENTER)
    mb.set_label(fmt.category_label(current) or "Other")
    mb.set_always_show_arrow(True)

    def create(button_):
        pop = Gtk.Popover()
        pop.add_css_class("md-cats")
        flow = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, max_children_per_line=4, min_children_per_line=2,
                           column_spacing=6, row_spacing=6, homogeneous=False)
        for c in cats:
            b = Gtk.Button(label=fmt.category_label(c))
            b.add_css_class("md-chipbtn")
            if c == current:
                b.add_css_class("selected")

            def pick(_b, c=c):
                pop.popdown()
                if c != current:
                    mb.set_label(fmt.category_label(c))
                    on_pick(c)
            b.connect("clicked", pick)
            flow.append(b)
        flow.set_size_request(340, -1)
        pop.set_child(flow)
        button_.set_popover(pop)

    mb.set_create_popup_func(create)
    return mb


class LibraryPage(Page):
    title = "Library"

    def __init__(self, ctx):
        super().__init__(ctx)
        self.kind = "sites"
        self.data: dict[str, list[dict]] = {"sites": [], "apps": []}
        self.cats: dict[str, list[str]] = {"sites": [], "apps": []}
        self.loaded: dict[str, bool] = {"sites": False, "apps": False}
        self.query = ""

        self.seg = Segmented([("sites", "Sites"), ("apps", "Apps")])
        self.seg.connect("changed", self._kind_changed)
        self._scan_text = "Scan history"
        self.scan_btn = button("Scan history", "tonal", "deskagent-scan-symbolic")
        self.scan_btn.connect("clicked", lambda *_: self._scan())
        self.add_btn = button("Add", "filled", "deskagent-plus-symbolic")
        self.add_btn.connect("clicked", lambda *_: self._add_dialog())
        spacer = Gtk.Box(hexpand=True)
        self.col.append(box(self.seg, spacer, self.scan_btn, self.add_btn, vertical=False, spacing=10))

        self.intro = label("", "md-body", "md-dim")
        self.col.append(self.intro)
        self.search = Gtk.SearchEntry(placeholder_text="Search")
        self.search.add_css_class("md-search")
        self.search.connect("search-changed", self._search_changed)
        self.col.append(self.search)

        self.list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        self.list.add_css_class("md-list")
        self.list.set_filter_func(self._filter)
        self.list.connect("row-activated", lambda _l, row: self._edit_dialog(row._data) if getattr(row, "_data", None) else None)
        self.empty = label("", "md-body", "md-dim", xalign=0.5)
        self.builtin = button("Built-in entries", "tonal", toggle=True)
        self.builtin.set_active(True)
        self.builtin.connect("toggled", lambda *_: (self.list.invalidate_filter(), self._update_empty()))
        self.count = label("", "md-body", "md-dim", wrap=False)
        self.col.append(box(self.count, Gtk.Box(hexpand=True), self.builtin, vertical=False, spacing=10))
        self.col.append(self.list)
        self.col.append(self.empty)
        self._set_intro()

    def set_compact(self, on):
        super().set_compact(on)
        set_button_content(self.scan_btn, "" if on else self._scan_text, "deskagent-scan-symbolic")
        set_button_content(self.add_btn, "" if on else "Add", "deskagent-plus-symbolic")
        self.scan_btn.set_tooltip_text(self._scan_text)
        self.add_btn.set_tooltip_text("Add")
        self._compact = on

    # ---- loading ----
    def on_show(self):
        self.load(self.kind)

    def load(self, kind: str):
        b = self.ctx.backend

        def done(res, err):
            if err:
                self.ctx.toast(f"Couldn't read the {kind} list: {err}")
                return
            self.data[kind] = res["rows"]
            self.cats[kind] = res["categories"]
            self.loaded[kind] = True
            if kind == self.kind:
                self._rebuild()
        self.ctx.run(b.sites if kind == "sites" else b.apps, done)

    def _set_intro(self):
        if self.kind == "sites":
            self.intro.set_text("How websites are named and categorised. Only the host name is ever used, never a page address. “Hide” sends nothing for a site.")
            self._scan_text = "Scan history"
            set_button_content(self.scan_btn, "" if getattr(self, "_compact", False) else self._scan_text, "deskagent-scan-symbolic")
            self.search.set_placeholder_text("Search sites")
        else:
            self.intro.set_text("How desktop apps are named. Keyed by window class. Apps you've actually used show up here even before they have a name.")
            self._scan_text = "Name used apps"
            set_button_content(self.scan_btn, "" if getattr(self, "_compact", False) else self._scan_text, "deskagent-scan-symbolic")
            self.search.set_placeholder_text("Search apps")

    def _kind_changed(self, _seg, key):
        self.kind = key
        self._set_intro()
        self.builtin.set_active(True)
        if self.loaded[key]:
            self._rebuild()
        else:
            self.load(key)

    # ---- list ----
    def _key(self, r):
        return r["host"] if self.kind == "sites" else r["class"]

    def _rebuild(self):
        clear_list(self.list)
        rows = self.data[self.kind]
        for r in rows:
            self.list.append(self._row(r))
        n_builtin = sum(1 for r in rows if r["source"] == "seed")
        self.builtin.set_visible(n_builtin > 0)
        self.builtin.get_child().get_last_child().set_text(f"Built-in ({n_builtin})")
        self.list.invalidate_filter()
        self._update_empty()

    def _update_empty(self):
        n = self._visible_count()
        self.empty.set_visible(n == 0)
        if self.query:
            self.empty.set_text(f"Nothing matches “{self.query}”.")
        elif not self.builtin.get_active():
            self.empty.set_text("None of your own yet. Scan your history, add one, or turn on the built-in entries.")
        else:
            self.empty.set_text("Nothing here yet.")
        self.count.set_text(f"{n} shown")

    def _search_changed(self, e):
        self.query = e.get_text().strip().lower()
        self.list.invalidate_filter()
        self._update_empty()

    def _filter(self, row) -> bool:
        r = getattr(row, "_data", None)
        if r is None:
            return True
        q = self.query
        if r["source"] == "seed" and not self.builtin.get_active() and not q:
            return False
        if q and q not in (r.get("name", "") + " " + self._key(r) + " " + r.get("category", "")).lower():
            return False
        return True

    def _visible_count(self):
        n = 0
        i = 0
        while (row := self.list.get_row_at_index(i)) is not None:
            if row.get_child_visible() and self._filter(row):
                n += 1
            i += 1
        return n

    def _row(self, r: dict) -> Gtk.ListBoxRow:
        key = self._key(r)
        sites = self.kind == "sites"
        hidden = bool(r.get("hide"))
        av = avatar(r.get("name") or key, "tonal" if r["source"] == "seed" else "")
        title = label(r.get("name") or key, "md-title", wrap=False, ellipsize=True)
        src, skind = SOURCE.get(r["source"], (r["source"], ""))
        sub_bits = [key]
        if not sites and r.get("count"):
            sub_bits.append(f"focused {r['count']}×")
        sub = label(" · ".join(sub_bits), "md-body", "md-dim", wrap=False, ellipsize=True)
        text = box(title, sub, spacing=1, hexpand=True)
        text.set_valign(Gtk.Align.CENTER)
        right = box(vertical=False, spacing=6)
        right.set_valign(Gtk.Align.CENTER)
        if hidden:
            right.append(badge("hidden", "warn"))
        if r["source"] != "seed":
            right.append(badge(src, skind))
        cats = self.cats[self.kind]
        right.append(category_button(self.ctx, r["category"], cats, lambda c, k=key: self._set(k, category=c)))
        if sites:
            hb = icon_button("deskagent-hidden-symbolic", "Show nothing for this site" if not hidden else "Show this site again", toggle=True)
            hb.set_active(hidden)
            hb.connect("toggled", lambda b, k=key: self._set(k, hide=b.get_active()))
            right.append(hb)
        line = box(av, text, right, vertical=False, spacing=14)
        row = Gtk.ListBoxRow()
        row.set_child(line)
        row.set_activatable(True)
        row.set_tooltip_text("Click to rename")
        row._data = r  # type: ignore[attr-defined]
        return row

    # ---- mutations ----
    def _set(self, key, name=None, category=None, hide=None):
        b = self.ctx.backend
        if self.kind == "sites":
            fn = lambda: b.site_set(key, name=name, category=category, hide=hide)  # noqa: E731
        else:
            fn = lambda: b.app_set(key, name=name, category=category)  # noqa: E731
        self.ctx.apply(fn, ok="Saved, applied live", after=lambda: self.load(self.kind))

    def _edit_dialog(self, r):
        key = self._key(r)
        d = Adw.AlertDialog(heading="Rename", body=f"How {key} appears on your site.")
        entry = Adw.EntryRow(title="Display name")
        entry.set_text(r.get("name") or "")
        grp = Adw.PreferencesGroup()
        grp.add(entry)
        d.set_extra_child(grp)
        d.add_response("cancel", "Cancel")
        if r["source"] == "user":
            d.add_response("remove", "Remove my override")
            d.set_response_appearance("remove", Adw.ResponseAppearance.DESTRUCTIVE)
        d.add_response("save", "Save")
        d.set_response_appearance("save", Adw.ResponseAppearance.SUGGESTED)
        d.set_default_response("save")
        d.set_close_response("cancel")

        def resp(_d, response):
            if response == "save" and entry.get_text().strip() and entry.get_text().strip() != r.get("name"):
                self._set(key, name=entry.get_text().strip())
            elif response == "remove":
                b = self.ctx.backend
                fn = (lambda: b.site_remove(key)) if self.kind == "sites" else (lambda: b.app_remove(key))
                self.ctx.apply(fn, ok="Override removed", after=lambda: self.load(self.kind))
        d.connect("response", resp)
        d.present(self.ctx.window)

    def _add_dialog(self):
        sites = self.kind == "sites"
        d = Adw.AlertDialog(heading="Add a site" if sites else "Add an app",
                            body="Use just the host name, like github.com." if sites else "Use the window class: run `hyprctl activewindow` and read “class”.")
        key = Adw.EntryRow(title="Host" if sites else "Window class")
        name = Adw.EntryRow(title="Display name (optional)")
        cats = self.cats[self.kind]
        combo = Adw.ComboRow(title="Category", model=Gtk.StringList.new([fmt.category_label(c) for c in cats]))
        grp = Adw.PreferencesGroup()
        for w in (key, name, combo):
            grp.add(w)
        d.set_extra_child(grp)
        d.add_response("cancel", "Cancel")
        d.add_response("add", "Add")
        d.set_response_appearance("add", Adw.ResponseAppearance.SUGGESTED)
        d.set_default_response("add")
        d.set_close_response("cancel")

        def resp(_d, response):
            k = key.get_text().strip()
            if response != "add" or not k:
                return
            c = cats[combo.get_selected()]
            n = name.get_text().strip() or None
            self._set(k, name=n, category=c)
        d.connect("response", resp)
        d.present(self.ctx.window)

    # ---- scan ----
    def _scan(self):
        dlg = ScanDialog(self.ctx, self.kind, self.cats[self.kind], on_done=lambda: self.load(self.kind))
        dlg.start()
