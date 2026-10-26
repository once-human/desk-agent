from __future__ import annotations

import time

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import GLib, Gtk  # noqa: E402

from .. import fmt  # noqa: E402
from ..widgets import (ColorDot, Timeline, badge, box, button, card, clear, icon_avatar, label,  # noqa: E402
                       set_button_content, set_kind)
from . import Page  # noqa: E402

_FACT_ICON = {
    "active": "deskagent-live-symbolic", "idle": "deskagent-pause-symbolic", "app": "deskagent-app-symbolic",
    "site": "deskagent-search-symbolic", "repo": "deskagent-library-symbolic", "claude": "deskagent-scan-symbolic",
    "media": "deskagent-play-symbolic", "battery": "deskagent-health-symbolic",
}


class LivePage(Page):
    title = "Live"

    def __init__(self, ctx):
        super().__init__(ctx)
        self._last_facts = None
        self._public_at = 0.0
        self._public = None
        self._public_busy = False

        # ---- hero ----
        self.hero_icon = icon_avatar("deskagent-live-symbolic", "surface", big=True)
        self.hero_title = label("", "md-display", wrap=True)
        self.hero_sub = label("", "md-body", "md-dim", max_chars=64)
        text = box(self.hero_title, self.hero_sub, spacing=4, hexpand=True)
        text.set_valign(Gtk.Align.CENTER)
        self.pause_btn = button("Pause", "filled", "deskagent-pause-symbolic", big=True)
        self.pause_btn.connect("clicked", self._toggle_pause)
        self.private_btn = button("Private mode", "on-card", "deskagent-privacy-symbolic", big=True, toggle=True)
        self.private_btn.connect("toggled", self._toggle_private)
        self.start_btn = button("Start the agent", "filled", "deskagent-play-symbolic", big=True)
        self.start_btn.connect("clicked", self._start_service)
        actions = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, column_spacing=10, row_spacing=10,
                              min_children_per_line=1, max_children_per_line=3, homogeneous=False)
        for b in (self.pause_btn, self.private_btn, self.start_btn):
            actions.append(b)
        top = box(self.hero_icon, text, vertical=False, spacing=20)
        self.chips = box(vertical=False, spacing=8)
        self.chips.set_halign(Gtk.Align.START)
        self.chips.set_margin_top(2)
        self.hero = card(top, actions, self.chips, kind="primary", hero=True, spacing=18)
        self.col.append(self.hero)

        # ---- sample + delivery ----
        self.facts = box(spacing=10)
        self.json_label = label("", "md-code", selectable=True, wrap=False)
        self.json_label.set_xalign(0)
        json_scroll = Gtk.ScrolledWindow(vscrollbar_policy=Gtk.PolicyType.NEVER, hscrollbar_policy=Gtk.PolicyType.AUTOMATIC)
        json_scroll.set_child(self.json_label)
        self.json_reveal = Gtk.Revealer(child=json_scroll, transition_type=Gtk.RevealerTransitionType.SLIDE_DOWN)
        json_btn = button("Show the exact JSON", "text")
        json_btn.set_halign(Gtk.Align.START)
        json_btn.connect("clicked", lambda b: self._toggle_json(b))
        self._json_btn = json_btn
        self.sample_card = card(label("What the site gets right now", "md-title"), self.facts, json_btn, self.json_reveal, kind="")
        self.sample_card.set_hexpand(True)

        self.deliv_icon = icon_avatar("deskagent-check-symbolic", "ok")
        self.deliv_title = label("", "md-title")
        self.deliv_sub = label("", "md-body", "md-dim", max_chars=34)
        drow = box(self.deliv_icon, box(self.deliv_title, self.deliv_sub, spacing=2, hexpand=True), vertical=False, spacing=14)
        self.deliv_extra = label("", "md-body", "md-dim", max_chars=40)
        self.deliv_card = card(label("Delivery", "md-title"), drow, self.deliv_extra)
        self.deliv_card.set_hexpand(True)

        flow = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, homogeneous=True, column_spacing=16, row_spacing=16,
                           min_children_per_line=1, max_children_per_line=2)
        flow.append(self.sample_card)
        flow.append(self.deliv_card)
        for ch in (flow.get_child_at_index(0), flow.get_child_at_index(1)):
            ch.set_focusable(False)
        self.col.append(flow)

        # ---- timeline ----
        self.timeline = Timeline()
        self.legend = Gtk.FlowBox(selection_mode=Gtk.SelectionMode.NONE, column_spacing=14, row_spacing=6,
                                  min_children_per_line=2, max_children_per_line=12, homogeneous=False)
        self.tl_sub = label("", "md-body", "md-dim")
        self.col.append(card(label("Recently", "md-title"), self.tl_sub, self.timeline, self.legend))

        # ---- public view ----
        self.pub_title = label("", "md-headline")
        self.pub_sub = label("", "md-body", "md-dim")
        self.pub_icon = icon_avatar("deskagent-live-symbolic", "tonal")
        self.pub_card = card(label("What visitors see on your site", "md-title"),
                             box(self.pub_icon, box(self.pub_title, self.pub_sub, spacing=2, hexpand=True), vertical=False, spacing=14),
                             kind="low")
        self.col.append(self.pub_card)
        self.pub_card.set_visible(False)

    # ---- actions ----
    def _toggle_pause(self, _b):
        paused = bool(self.ctx.state and self.ctx.state.get("paused"))
        self.ctx.apply(lambda: self.ctx.backend.pause(not paused), after=self.ctx.poll_now)

    def _toggle_private(self, b):
        if getattr(self, "_syncing", False):
            return
        self.ctx.apply(lambda: self.ctx.backend.config_set("private", "true" if b.get_active() else "false"),
                       after=self.ctx.poll_now)

    def _start_service(self, _b):
        self.ctx.apply(lambda: self.ctx.backend.service("start"), ok="Starting the agent…", after=lambda: GLib.timeout_add(1200, self.ctx.poll_now))

    def _toggle_json(self, b):
        r = not self.json_reveal.get_reveal_child()
        self.json_reveal.set_reveal_child(r)
        set_button_content(b, "Hide the exact JSON" if r else "Show the exact JSON")

    # ---- state ----
    def on_state(self, st):
        self._syncing = True
        try:
            self._render(st)
        finally:
            self._syncing = False
        self._maybe_fetch_public()

    def _render(self, st):
        sample = (st or {}).get("sample")
        if st is None:
            kind, icon, title = "warn", "deskagent-warning-symbolic", "Agent is not running"
            sub = "Nothing is being sent, so your site shows the laptop as offline."
        elif st.get("paused"):
            kind, icon, title = "tonal", "deskagent-pause-symbolic", "Paused"
            sub = "Nothing is sent. Your site shows you as away."
        elif st.get("asleep"):
            kind, icon, title = "tonal", "deskagent-pause-symbolic", "Asleep"
            sub = "The laptop is suspended. The site will show you as away until it wakes."
        elif st.get("private"):
            kind, icon, title = "tertiary", "deskagent-privacy-symbolic", "Private mode"
            sub = "Only “at the computer” or “away” is shared. No app, repo, site or media."
        else:
            kind, icon, title = "primary", "deskagent-live-symbolic", "Sending"
            sub = f"Updating {st.get('endpointHost') or 'your site'} as things change."
        set_kind(self.hero, kind)
        self.hero_title.set_text(title)
        self.hero_sub.set_text(sub)
        self.hero_icon.get_first_child().set_from_icon_name(icon)
        running = st is not None
        paused = bool(st and st.get("paused"))
        self.pause_btn.set_visible(running)
        self.private_btn.set_visible(running)
        self.start_btn.set_visible(not running)
        set_button_content(self.pause_btn, "Resume" if paused else "Pause",
                           "deskagent-play-symbolic" if paused else "deskagent-pause-symbolic")
        self.private_btn.set_active(bool(st and st.get("private")))

        clear(self.chips)
        if st:
            for t in (f"v{st.get('version')}", f"pid {st.get('pid')}", "up " + fmt.duration(time.time() - (fmt.parse_iso(st.get("started")) or time.time()))):
                self.chips.append(badge(t))
            for b in st.get("browsers") or []:
                self.chips.append(badge(f"{b} extension connected", "ok"))
        # facts
        rows = fmt.sample_facts(sample) if sample else []
        sig = (rows, bool(st))
        if sig != self._last_facts:
            self._last_facts = sig
            clear(self.facts)
            if not rows:
                self.facts.append(label("Nothing is being sent right now." if st else "The agent is not running.", "md-body", "md-dim"))
            for ic, key, val in rows:
                r = box(spacing=12, vertical=False)
                im = Gtk.Image.new_from_icon_name(_FACT_ICON.get(ic, "deskagent-app-symbolic"))
                im.set_pixel_size(16)
                im.add_css_class("md-dim")
                r.append(im)
                k = label(key, "md-sample-key")
                k.set_valign(Gtk.Align.START)
                r.append(k)
                r.append(label(val, "md-body", max_chars=30))
                self.facts.append(r)
        self.json_label.set_text(fmt.pretty_json(sample))
        self._json_btn.set_visible(sample is not None)
        if sample is None and self.json_reveal.get_reveal_child():
            self._toggle_json(self._json_btn)
        self._render_delivery(st)
        self._render_timeline(st)

    def _render_delivery(self, st):
        if not st:
            set_kind(self.deliv_card, "")
            self.deliv_title.set_text("No agent")
            self.deliv_sub.set_text("Start it to begin sending.")
            self.deliv_extra.set_text("")
            for k in ("ok", "warn", "error"):
                self.deliv_icon.remove_css_class(k)
            self.deliv_icon.add_css_class("tonal")
            self.deliv_icon.get_first_child().set_from_icon_name("deskagent-skip-symbolic")
            return
        send = st.get("send") or {}
        res = send.get("result") or ""
        last_ok = fmt.parse_iso(send.get("lastOK"))
        age = (time.time() - last_ok) if last_ok else None
        host = st.get("endpointHost") or "the site"
        kind, icon = "", "deskagent-check-symbolic"
        av = "ok"
        if res in ("ok", ""):
            title = "Delivered" if res == "ok" else "Waiting for the first send"
            sub = f"Last delivered {fmt.ago(age)} to {host}" if res == "ok" else f"Will send to {host} on the next change"
            if st.get("paused") or st.get("asleep"):
                title, av = "Idle", "tonal"
        elif res == "unauthorized":
            kind, icon, av = "error", "deskagent-error-symbolic", "error"
            title, sub = "The site rejected the key", "Fix it on the Health page (Set site key), then it retries by itself."
        elif res == "nokey":
            kind, icon, av = "error", "deskagent-key-symbolic", "error"
            title, sub = "No site key yet", "Add it on the Health page. Samples queue safely until then."
        elif res == "network":
            kind, icon, av = "warn", "deskagent-warning-symbolic", "warn"
            title, sub = "Can't reach the site", f"Retrying with back-off. Last delivered {fmt.ago(age)}."
        else:
            kind, icon, av = "warn", "deskagent-warning-symbolic", "warn"
            title, sub = f"The site answered {send.get('status')}", f"Result: {res}. Last delivered {fmt.ago(age)}."
        set_kind(self.deliv_card, kind)
        self.deliv_title.set_text(title)
        self.deliv_sub.set_text(sub)
        icon_box = self.deliv_icon
        for k in ("ok", "warn", "error", "tonal"):
            icon_box.remove_css_class(k)
        icon_box.add_css_class(av)
        icon_box.get_first_child().set_from_icon_name(icon)
        q = send.get("queued") or 0
        self.deliv_extra.set_text((f"{q} sample{'s' if q != 1 else ''} waiting for the next upload" if res in ("ok", "") else f"{q} sample{'s' if q != 1 else ''} waiting offline") if q else "Nothing waiting in the offline queue.")

    def _render_timeline(self, st):
        recent = (st or {}).get("recent") or []
        now = time.time()
        pts = [(fmt.parse_t(s.get("t")), s) for s in recent]
        pts = [(t, s) for t, s in pts if t]
        s_scheme = self.ctx.scheme
        if not pts:
            self.timeline.set_data([], now - 3600, now, s_scheme["surface_container_highest"])
            self.tl_sub.set_text("Nothing yet. Samples show up here as they are sent.")
            clear(self.legend)
            return
        cap = 150.0
        segs, cats = [], {}
        for i, (t, s) in enumerate(pts):
            nxt = pts[i + 1][0] if i + 1 < len(pts) else now
            end = min(nxt, t + cap)
            cat = fmt.primary_category(s)
            lbl = fmt.primary_label(s)
            colour = self.ctx.cat_colour(cat) if cat else s_scheme["outline_variant"]
            segs.append((t, end, colour, lbl))
            key = fmt.category_label(cat) if cat else "Away"
            cats[key] = colour
        t0 = max(pts[0][0], now - 3 * 3600)
        self.timeline.set_data(segs, t0, now, s_scheme["surface_container_highest"])
        self.tl_sub.set_text(f"The last {fmt.duration(now - t0)}, one block per update. Hover for details.")
        clear(self.legend)
        for name, colour in cats.items():
            sw = ColorDot(colour)
            self.legend.append(box(sw, label(name, "md-body", "md-dim"), vertical=False, spacing=6))

    def on_theme(self):
        self._last_facts = None
        self._render_timeline(self.ctx.state)

    # ---- public view ----
    def _maybe_fetch_public(self):
        cfg = self.ctx.config
        if not cfg or self._public_busy or time.time() - self._public_at < 15:
            return
        endpoint = (cfg.get("values") or {}).get("endpoint")
        if not endpoint:
            return
        self._public_busy = True

        def done(res, err):
            self._public_busy = False
            self._public_at = time.time()
            self._public = res
            self._render_public(res)
        self.ctx.run(lambda: self.ctx.backend.fetch_public(endpoint), done)

    def _render_public(self, pub):
        from ..backend import public_url
        endpoint = ((self.ctx.config or {}).get("values") or {}).get("endpoint", "")
        if not public_url(endpoint):
            self.pub_card.set_visible(False)
            return
        self.pub_card.set_visible(True)
        if pub is None:
            self.pub_title.set_text("Couldn't load your public page status")
            self.pub_sub.set_text("The site's public endpoint didn't answer. That is separate from whether the agent is sending.")
            return
        p = fmt.public_summary(pub)
        if not p["online"]:
            self.pub_title.set_text("Offline")
            self.pub_sub.set_text("The site currently shows your laptop as offline.")
        else:
            self.pub_title.set_text(p["doing"] or ("Active" if p["active"] else "Away"))
            bits = []
            if p["playing"]:
                bits.append(f"Playing: {p['playing']}")
            bits.append("Active" if p["active"] else "Away")
            self.pub_sub.set_text(" · ".join(bits))

    def on_show(self):
        self._public_at = 0
