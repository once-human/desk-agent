from __future__ import annotations

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from ..widgets import (badge, box, button, card, icon_avatar, label, set_button_content,  # noqa: E402
                       set_kind)
from . import Page  # noqa: E402

LEVEL = {
    "ok": ("deskagent-check-symbolic", "ok", "Working"),
    "warn": ("deskagent-warning-symbolic", "warn", "Optional / unavailable"),
    "fail": ("deskagent-error-symbolic", "error", "Needs attention"),
    "skip": ("deskagent-skip-symbolic", "tonal", "Skipped"),
}


class HealthPage(Page):
    title = "Health"

    def __init__(self, ctx):
        super().__init__(ctx)
        self.report = None
        self.running = False

        self.sum_icon = icon_avatar("deskagent-health-symbolic", "surface", big=True)
        self.sum_title = label("Checking…", "md-display")
        self.sum_sub = label("", "md-body", "md-dim")
        text = box(self.sum_title, self.sum_sub, spacing=4, hexpand=True)
        text.set_valign(Gtk.Align.CENTER)
        self.run_btn = button("Run checks", "filled", "deskagent-refresh-symbolic", big=True)
        self.run_btn.connect("clicked", lambda *_: self.run_checks())
        self.run_btn.set_valign(Gtk.Align.CENTER)
        self.summary = card(box(self.sum_icon, text, self.run_btn, vertical=False, spacing=20), kind="tonal", hero=True)
        self.col.append(self.summary)

        self.checks = Gtk.ListBox(selection_mode=Gtk.SelectionMode.NONE)
        self.checks.add_css_class("md-list")
        self.col.append(self.checks)

        # ---- actions ----
        self.svc_state = badge("…")
        self.svc_start = button("Start", "tonal")
        self.svc_restart = button("Restart", "tonal")
        self.svc_stop = button("Stop", "danger")
        for b, a in ((self.svc_start, "start"), (self.svc_restart, "restart"), (self.svc_stop, "stop")):
            b.connect("clicked", lambda _b, act=a: self._service(act))
        self.col.append(card(
            box(label("Background service", "md-title"), self.svc_state, vertical=False, spacing=10),
            label("desk-agent runs as a systemd user service so it keeps going when this window is closed.", "md-body", "md-dim"),
            box(self.svc_start, self.svc_restart, self.svc_stop, vertical=False, spacing=8), kind="low"))

        key_btn = button("Set site key…", "filled", "deskagent-key-symbolic")
        key_btn.connect("clicked", lambda *_: self._key_dialog())
        key_btn.set_halign(Gtk.Align.START)
        self.col.append(card(
            label("Site key", "md-title"),
            label("The secret your site checks before accepting updates. It is saved to ~/.config/desk-agent/key (owner-only) and is only ever sent to your own site. It is never shown here.", "md-body", "md-dim"),
            key_btn, kind="low"))

        ext_btn = button("Set up browser extension link", "tonal", "deskagent-app-symbolic")
        ext_btn.connect("clicked", lambda *_: self._install_host())
        ext_btn.set_halign(Gtk.Align.START)
        self.ext_state = label("", "md-body", "md-dim")
        self.col.append(card(
            label("Browser extension", "md-title"),
            label("Lets the agent see which site is open (host name only) and whether a window is private. Without it, a browser shows up as just “Browser”.", "md-body", "md-dim"),
            self.ext_state, ext_btn, kind="low"))

        bl_btn = button("Download community blocklist", "tonal", "deskagent-download-symbolic")
        bl_btn.connect("clicked", lambda *_: self._blocklist(bl_btn))
        bl_btn.set_halign(Gtk.Align.START)
        self.col.append(card(
            label("Adult-site filter", "md-title"),
            label("A small built-in filter always runs. This optionally adds a large community list (about 64,000 hosts) so more sites collapse to nothing. It downloads once, over HTTPS, and nothing is uploaded.", "md-body", "md-dim"),
            bl_btn, kind="low"))

    def set_compact(self, on):
        super().set_compact(on)
        set_button_content(self.run_btn, "" if on else "Run checks", "deskagent-refresh-symbolic")
        self.run_btn.set_tooltip_text("Run checks")

    def on_show(self):
        self.run_checks()
        self._svc()

    def on_state(self, st):
        b = (st or {}).get("browsers") or []
        self.ext_state.set_text("Connected: " + ", ".join(b) if b else "Not connected right now.")

    # ---- doctor ----
    def run_checks(self):
        if self.running:
            return
        self.running = True
        self.run_btn.set_sensitive(False)
        self.ctx.run(self.ctx.backend.doctor, self._report)

    def _report(self, res, err):
        self.running = False
        self.run_btn.set_sensitive(True)
        if err:
            self.sum_title.set_text("Couldn't run the checks")
            self.sum_sub.set_text(str(err))
            set_kind(self.summary, "error")
            return
        self.report = res
        items = res.get("checks") or []
        fails = [i for i in items if i["level"] == "fail"]
        warns = [i for i in items if i["level"] == "warn"]
        if fails:
            set_kind(self.summary, "error")
            self.sum_title.set_text(f"{len(fails)} thing{'s' if len(fails) != 1 else ''} to fix")
            self.sum_sub.set_text(f"Start with: {fails[0]['name']}")
            self._sum_icon("deskagent-error-symbolic", "error")
        elif warns:
            set_kind(self.summary, "ok")
            self.sum_title.set_text("All good")
            self.sum_sub.set_text(f"{len(warns)} optional signal{'s' if len(warns) != 1 else ''} not available on this machine. That's normal.")
            self._sum_icon("deskagent-check-symbolic", "ok")
        else:
            set_kind(self.summary, "ok")
            self.sum_title.set_text("Everything works")
            self.sum_sub.set_text("Every signal source answered.")
            self._sum_icon("deskagent-check-symbolic", "ok")
        from ..widgets import clear_list
        clear_list(self.checks)
        for it in items:
            icon, kind, _ = LEVEL.get(it["level"], LEVEL["skip"])
            av = icon_avatar(icon, kind)
            detail = label(it["detail"], "md-body", "md-dim")
            detail.set_selectable(True)
            row = box(av, box(label(it["name"].capitalize(), "md-title"), detail, spacing=2, hexpand=True), vertical=False, spacing=14)
            r = Gtk.ListBoxRow()
            r.set_child(row)
            r.set_activatable(False)
            self.checks.append(r)

    def _sum_icon(self, icon, kind):
        self.sum_icon.get_first_child().set_from_icon_name(icon)  # the tile stays "surface"; the glyph carries the state

    # ---- service ----
    def _svc(self):
        def done(res, err):
            res = res or "unavailable"
            self.svc_state.set_text({"active": "running", "inactive": "stopped", "failed": "failed"}.get(res, res))
            for k in ("ok", "warn"):
                self.svc_state.remove_css_class(k)
            self.svc_state.add_css_class("ok" if res == "active" else "warn")
            self.svc_start.set_sensitive(res != "active" and res != "unavailable")
            self.svc_restart.set_sensitive(res != "unavailable")
            self.svc_stop.set_sensitive(res == "active")
        self.ctx.run(self.ctx.backend.service_active, done)

    def _service(self, action):
        self.ctx.apply(lambda: self.ctx.backend.service(action), ok=f"Service: {action} done",
                       after=lambda: (self._svc(), self.ctx.poll_later(1200)))

    # ---- key ----
    def _key_dialog(self):
        d = Adw.AlertDialog(heading="Set site key",
                            body="Paste the key your site expects. It is stored owner-only and not displayed again.")
        entry = Adw.PasswordEntryRow(title="Site key")
        grp = Adw.PreferencesGroup()
        grp.add(entry)
        d.set_extra_child(grp)
        d.add_response("cancel", "Cancel")
        d.add_response("save", "Save")
        d.set_response_appearance("save", Adw.ResponseAppearance.SUGGESTED)
        d.set_default_response("save")
        d.set_close_response("cancel")

        def resp(_d, r):
            if r != "save":
                return
            key = entry.get_text()
            entry.set_text("")
            if not key.strip():
                return
            self.ctx.apply(lambda: self.ctx.backend.set_key(key), ok="Key saved",
                           after=lambda: (self.run_checks(), self.ctx.backend.reload()))
        d.connect("response", resp)
        d.present(self.ctx.window)

    def _install_host(self):
        def done(res, err):
            body = str(err) if err else (res or "Done.")
            d = Adw.AlertDialog(heading="Browser link" if not err else "Couldn't set it up", body=body.strip())
            d.add_response("ok", "OK")
            d.present(self.ctx.window)
        self.ctx.run(self.ctx.backend.install_native_host, done)

    def _blocklist(self, btn):
        btn.set_sensitive(False)

        def done(res, err):
            btn.set_sensitive(True)
            self.ctx.toast(str(err) if err else "Blocklist downloaded and applied")
        self.ctx.run(self.ctx.backend.fetch_blocklist, done)
