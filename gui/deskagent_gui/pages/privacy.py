from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from ..widgets import (box, button, card, clear, icon_avatar, icon_button, image, label,  # noqa: E402
                       set_kind)
from . import Page  # noqa: E402

NEVER = [
    ("Window titles", "Not read, not stored, not sent. Only the app's class name is used."),
    ("Page addresses and paths", "A website is reduced to its host name (github.com), never a URL."),
    ("Keystrokes, screenshots, clipboard", "No code in the agent can see them."),
    ("Browser media titles", "Only “watching something on YouTube” and a video count. Native music players may show a track."),
    ("Adult sites", "They collapse to nothing at all: no name, no category."),
    ("Private and incognito windows", "Nothing about the site is sent (needs the browser extension to know)."),
]


class PrivacyPage(Page):
    title = "Privacy"

    def __init__(self, ctx):
        super().__init__(ctx)
        self._syncing = False
        self._debounce: dict[str, int] = {}

        # ---- private mode ----
        self.pm_switch = Gtk.Switch(valign=Gtk.Align.CENTER)
        self.pm_switch.connect("state-set", self._pm_set)
        self.pm_title = label("Private mode", "md-headline")
        self.pm_sub = label("Share only “at the computer” or “away”. Applies instantly, no restart.", "md-body", "md-dim")
        row = box(icon_avatar("deskagent-privacy-symbolic", "surface", big=True),
                  box(self.pm_title, self.pm_sub, spacing=4, hexpand=True), self.pm_switch, vertical=False, spacing=18)
        row.get_first_child().set_valign(Gtk.Align.CENTER)
        self.pm_card = card(row, kind="tonal", hero=True)
        self.col.append(self.pm_card)

        # ---- never sent ----
        never_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        never_list.add_css_class("md-list")
        never_list.add_css_class("md-never")
        for title, sub in NEVER:
            r = box(spacing=14, vertical=False)
            r.append(image("deskagent-hidden-symbolic", 18))
            r.get_first_child().add_css_class("md-dim")
            r.append(box(label(title, "md-title"), label(sub, "md-body", "md-dim"), spacing=2, hexpand=True))
            never_list.append(r)
        self.col.append(card(label("What is never sent", "md-title"),
                             label("These are built into the program, not settings you could turn on by accident.", "md-body", "md-dim"),
                             never_list, kind="low"))

        # ---- hide repos ----
        self.chips = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, column_spacing=8, row_spacing=8,
                                 min_children_per_line=1, max_children_per_line=8, homogeneous=False)
        self.repo_entry = Gtk.Entry(placeholder_text="repo name, e.g. client-project", hexpand=True)
        self.repo_entry.add_css_class("md-search")
        self.repo_entry.connect("activate", lambda *_: self._add_repo())
        add = button("Hide", "tonal", "deskagent-plus-symbolic")
        add.connect("clicked", lambda *_: self._add_repo())
        self.col.append(card(label("Hidden repos", "md-title"),
                             label("The agent shows the repo you're working in. Names listed here are never reported, not even while Claude Code works in them.", "md-body", "md-dim"),
                             self.chips, box(self.repo_entry, add, vertical=False, spacing=10)))

        # ---- timing + battery ----
        self.grp = Adw.PreferencesGroup(title="Sending")
        self.spin: dict[str, Adw.SpinRow] = {}
        for key, title, sub, lo, hi, step in (
            ("interval_s", "Update interval", "Seconds between heartbeats while you're active", 5, 600, 5),
            ("idle_interval_s", "Idle interval", "Seconds between heartbeats while you're idle", 10, 1800, 10),
            ("upload_every_s", "Upload every", "Seconds between uploads (samples are batched). 0 sends each one at once", 0, 3600, 60),
            ("active_window_s", "Counts as active for", "Seconds after your last input that still count as at the computer", 30, 3600, 30),
        ):
            sr = Adw.SpinRow.new_with_range(lo, hi, step)
            sr.set_title(title)
            sr.set_subtitle(sub)
            sr.connect("notify::value", lambda r, _p, k=key: self._spin_changed(k, r))
            self.spin[key] = sr
            self.grp.add(sr)
        self.battery = Adw.SwitchRow(title="Share battery level", subtitle="Percentage and whether it's charging")
        self.battery.connect("notify::active", self._battery_changed)
        self.grp.add(self.battery)
        self.col.append(self.grp)
        self.on_show()

    def on_show(self):
        self.ctx.refresh_config(self._apply_config)

    # ---- config → widgets ----
    def _apply_config(self, cfg):
        if not cfg:
            return
        v = cfg["values"]
        self._syncing = True
        try:
            self.pm_switch.set_active(bool(v.get("private")))
            self._pm_style(bool(v.get("private")))
            for k, sr in self.spin.items():
                sr.set_value(v.get(k, sr.get_value()))
            self.battery.set_active(bool(v.get("battery")))
            clear(self.chips)
            repos = v.get("hide_repos") or []
            if not repos:
                self.chips.append(label("No repos hidden.", "md-body", "md-dim"))
            for r in repos:
                chip = box(label(r, "md-body"), vertical=False, spacing=4, classes=("md-chip", "selected"))
                x = icon_button("deskagent-error-symbolic", f"Stop hiding {r}")
                x.set_margin_start(2)
                x.connect("clicked", lambda _b, name=r: self._remove_repo(name))
                chip.append(x)
                chip.set_margin_bottom(0)
                self.chips.append(chip)
        finally:
            self._syncing = False

    def _pm_style(self, on: bool):
        set_kind(self.pm_card, "tertiary" if on else "tonal")
        self.pm_title.set_text("Private mode is on" if on else "Private mode")

    def on_state(self, st):
        if st is not None and not self._syncing:
            on = bool(st.get("private"))
            if on != self.pm_switch.get_active():
                self._syncing = True
                self.pm_switch.set_active(on)
                self._pm_style(on)
                self._syncing = False

    # ---- widgets → config ----
    def _pm_set(self, sw, state):
        if self._syncing:
            return False
        self._pm_style(state)
        self.ctx.apply(lambda: self.ctx.backend.config_set("private", "true" if state else "false"), after=self.ctx.poll_now)
        return False

    def _battery_changed(self, row, _p):
        if self._syncing:
            return
        self.ctx.apply(lambda: self.ctx.backend.config_set("battery", "true" if row.get_active() else "false"))

    def _spin_changed(self, key, row):
        if self._syncing:
            return
        if key in self._debounce:
            GLib.source_remove(self._debounce[key])
        val = str(int(row.get_value()))

        def fire():
            self._debounce.pop(key, None)
            self.ctx.apply(lambda: self.ctx.backend.config_set(key, val), after=self.on_show)
            return False
        self._debounce[key] = GLib.timeout_add(700, fire)

    def _repos(self):
        return list(((self.ctx.config or {}).get("values") or {}).get("hide_repos") or [])

    def _add_repo(self):
        name = self.repo_entry.get_text().strip()
        if not name:
            return
        repos = self._repos()
        if name.lower() in (r.lower() for r in repos):
            self.repo_entry.set_text("")
            return
        self.repo_entry.set_text("")
        self.ctx.apply(lambda: self.ctx.backend.config_set("hide_repos", *repos, name), after=self.on_show)

    def _remove_repo(self, name):
        repos = [r for r in self._repos() if r != name]
        self.ctx.apply(lambda: self.ctx.backend.config_set("hide_repos", *repos), after=self.on_show)
