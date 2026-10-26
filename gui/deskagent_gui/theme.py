"""Material You style colour: a tonal palette from one seed colour.

Pure Python (no GTK) so it can be tested anywhere.

This is *inspired by* Material 3, not a port of Google's HCT. Tone is true
CIE L* (the same "tone" axis Material uses), but hue and chroma come from
OKLCH, which is close to HCT without needing CAM16. Colours therefore land
near, not exactly on, what material-color-utilities or matugen would give for
the same seed.
"""
from __future__ import annotations

import math
import re
import zlib

HEX_RE = re.compile(r"^#?([0-9a-fA-F]{6})$")


# ---- colour maths -----------------------------------------------------------

def _lin(c: float) -> float:
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def _gam(c: float) -> float:
    c = max(0.0, min(1.0, c))
    return 12.92 * c if c <= 0.0031308 else 1.055 * c ** (1 / 2.4) - 0.055


def hex_to_rgb(h: str) -> tuple[float, float, float]:
    m = HEX_RE.match(h.strip())
    if not m:
        raise ValueError(f"not a #rrggbb colour: {h!r}")
    v = m.group(1)
    return tuple(int(v[i:i + 2], 16) / 255 for i in (0, 2, 4))  # type: ignore[return-value]


def rgb_to_hex(r: float, g: float, b: float) -> str:
    return "#{:02x}{:02x}{:02x}".format(*(round(max(0.0, min(1.0, x)) * 255) for x in (r, g, b)))


def oklab_to_linear(L: float, a: float, b: float) -> tuple[float, float, float]:
    l_ = L + 0.3963377774 * a + 0.2158037573 * b
    m_ = L - 0.1055613458 * a - 0.0638541728 * b
    s_ = L - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_ ** 3, m_ ** 3, s_ ** 3
    return (
        4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
        -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
        -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s,
    )


def linear_to_oklab(r: float, g: float, b: float) -> tuple[float, float, float]:
    l = 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b
    m = 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b
    s = 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b
    l_, m_, s_ = l ** (1 / 3), m ** (1 / 3), s ** (1 / 3)
    return (
        0.2104542553 * l_ + 0.7936177850 * m_ - 0.0040720468 * s_,
        1.9779984951 * l_ - 2.4285922050 * m_ + 0.4505937099 * s_,
        0.0259040371 * l_ + 0.7827717662 * m_ - 0.8086757660 * s_,
    )


def hex_to_oklch(h: str) -> tuple[float, float, float]:
    r, g, b = (_lin(c) for c in hex_to_rgb(h))
    L, a, bb = linear_to_oklab(r, g, b)
    return L, math.hypot(a, bb), math.degrees(math.atan2(bb, a)) % 360


def _in_gamut(rgb: tuple[float, float, float]) -> bool:
    return all(-1e-4 <= c <= 1 + 1e-4 for c in rgb)


def _from_oklch(L: float, C: float, h: float) -> tuple[float, float, float]:
    a, b = C * math.cos(math.radians(h)), C * math.sin(math.radians(h))
    return oklab_to_linear(L, a, b)


def _max_chroma(L: float, h: float) -> float:
    lo, hi = 0.0, 0.4
    for _ in range(18):
        mid = (lo + hi) / 2
        if _in_gamut(_from_oklch(L, mid, h)):
            lo = mid
        else:
            hi = mid
    return lo


def _luminance(rgb_lin: tuple[float, float, float]) -> float:
    r, g, b = (max(0.0, min(1.0, c)) for c in rgb_lin)
    return 0.2126729 * r + 0.7151522 * g + 0.0721750 * b


def lstar(hex_colour: str) -> float:
    """CIE L* of a colour: Material's "tone"."""
    y = _luminance(tuple(_lin(c) for c in hex_to_rgb(hex_colour)))
    f = y ** (1 / 3) if y > 216 / 24389 else (24389 / 27 * y + 16) / 116
    return 116 * f - 16


def tone_colour(hue: float, chroma: float, tone: float) -> str:
    """The colour at this hue with at most this chroma whose L* is `tone`."""
    if tone <= 0:
        return "#000000"
    if tone >= 100:
        return "#ffffff"
    t = (tone + 16) / 116
    target_y = t ** 3 if tone > 8 else tone / (24389 / 27)
    lo, hi = 0.0, 1.0
    rgb = (0.0, 0.0, 0.0)
    for _ in range(40):
        L = (lo + hi) / 2
        C = min(chroma, _max_chroma(L, hue))
        rgb = _from_oklch(L, C, hue)
        if _luminance(rgb) < target_y:
            lo = L
        else:
            hi = L
    return rgb_to_hex(*(_gam(c) for c in rgb))


# ---- palette and scheme -----------------------------------------------------

# Chroma per palette, in OKLCH units (about HCT chroma / 250).
_CHROMA = {"secondary": 0.045, "tertiary": 0.075, "neutral": 0.012, "neutral_variant": 0.022, "error": 0.20}


class Palette:
    """Tonal palettes derived from a seed colour."""

    def __init__(self, seed: str):
        _, c, h = hex_to_oklch(seed)
        self.seed = rgb_to_hex(*hex_to_rgb(seed))
        self.hue = h
        self._spec = {
            "primary": (h, max(0.10, min(c, 0.17))),
            "secondary": (h, _CHROMA["secondary"]),
            "tertiary": ((h + 60) % 360, _CHROMA["tertiary"]),
            "neutral": (h, _CHROMA["neutral"]),
            "neutral_variant": (h, _CHROMA["neutral_variant"]),
            "error": (27.0, _CHROMA["error"]),
        }

    def tone(self, name: str, tone: float) -> str:
        hue, chroma = self._spec[name]
        return tone_colour(hue, chroma, tone)


def scheme(seed: str, dark: bool) -> dict[str, str]:
    """Material 3 colour roles (the ones this app uses) for light or dark."""
    p = Palette(seed)
    t = p.tone
    if not dark:
        return {
            "primary": t("primary", 40), "on_primary": t("primary", 100),
            "primary_container": t("primary", 90), "on_primary_container": t("primary", 10),
            "secondary": t("secondary", 40), "on_secondary": t("secondary", 100),
            "secondary_container": t("secondary", 90), "on_secondary_container": t("secondary", 10),
            "tertiary": t("tertiary", 40), "on_tertiary": t("tertiary", 100),
            "tertiary_container": t("tertiary", 90), "on_tertiary_container": t("tertiary", 10),
            "error": t("error", 40), "on_error": t("error", 100),
            "error_container": t("error", 90), "on_error_container": t("error", 10),
            "background": t("neutral", 98), "on_background": t("neutral", 10),
            "surface": t("neutral", 98), "on_surface": t("neutral", 10),
            "surface_dim": t("neutral", 87), "surface_bright": t("neutral", 98),
            "surface_container_lowest": t("neutral", 100), "surface_container_low": t("neutral", 96),
            "surface_container": t("neutral", 94), "surface_container_high": t("neutral", 92),
            "surface_container_highest": t("neutral", 90),
            "on_surface_variant": t("neutral_variant", 30), "surface_variant": t("neutral_variant", 90),
            "outline": t("neutral_variant", 50), "outline_variant": t("neutral_variant", 80),
            "inverse_surface": t("neutral", 20), "inverse_on_surface": t("neutral", 95),
            "inverse_primary": t("primary", 80), "scrim": "#000000",
        }
    return {
        "primary": t("primary", 80), "on_primary": t("primary", 20),
        "primary_container": t("primary", 30), "on_primary_container": t("primary", 90),
        "secondary": t("secondary", 80), "on_secondary": t("secondary", 20),
        "secondary_container": t("secondary", 30), "on_secondary_container": t("secondary", 90),
        "tertiary": t("tertiary", 80), "on_tertiary": t("tertiary", 20),
        "tertiary_container": t("tertiary", 30), "on_tertiary_container": t("tertiary", 90),
        "error": t("error", 80), "on_error": t("error", 20),
        "error_container": t("error", 30), "on_error_container": t("error", 90),
        "background": t("neutral", 6), "on_background": t("neutral", 90),
        "surface": t("neutral", 6), "on_surface": t("neutral", 90),
        "surface_dim": t("neutral", 6), "surface_bright": t("neutral", 24),
        "surface_container_lowest": t("neutral", 4), "surface_container_low": t("neutral", 10),
        "surface_container": t("neutral", 12), "surface_container_high": t("neutral", 17),
        "surface_container_highest": t("neutral", 22),
        "on_surface_variant": t("neutral_variant", 80), "surface_variant": t("neutral_variant", 30),
        "outline": t("neutral_variant", 60), "outline_variant": t("neutral_variant", 30),
        "inverse_surface": t("neutral", 90), "inverse_on_surface": t("neutral", 20),
        "inverse_primary": t("primary", 40), "scrim": "#000000",
    }


def contrast(a: str, b: str) -> float:
    """WCAG contrast ratio of two colours."""
    def y(h):
        return _luminance(tuple(_lin(c) for c in hex_to_rgb(h)))
    hi, lo = max(y(a), y(b)), min(y(a), y(b))
    return (hi + 0.05) / (lo + 0.05)


def mix(a: str, b: str, amount: float) -> str:
    """`a` blended toward `b` by amount (0..1), in linear light."""
    ra, rb = hex_to_rgb(a), hex_to_rgb(b)
    return rgb_to_hex(*(_gam(_lin(x) * (1 - amount) + _lin(y) * amount) for x, y in zip(ra, rb)))


def with_alpha(hex_colour: str, alpha: float) -> str:
    r, g, b = (round(c * 255) for c in hex_to_rgb(hex_colour))
    return f"rgba({r},{g},{b},{alpha:.3f})"


# ---- categorical colours for the timeline -----------------------------------

_CATEGORY_HUE = {
    "terminal": 150, "ide": 250, "editor": 285, "code": 250, "browser": 60, "chat": 340,
    "design": 20, "media": 320, "music": 320, "video": 25, "stream": 15, "docs": 200,
    "writing": 95, "mail": 215, "social": 355, "shopping": 45, "news": 80, "learning": 175,
    "ai": 300, "tool": 120, "files": 110, "notes": 190, "other": 0, "private": 0,
}


def category_colour(category: str, seed: str, dark: bool) -> str:
    """A stable colour per category, nudged toward the seed so it feels themed."""
    if category in ("other", "private", "", None):
        return Palette(seed).tone("neutral_variant", 60 if dark else 62)
    h = _CATEGORY_HUE.get(category, (zlib.crc32(category.encode()) % 360))
    sh = hex_to_oklch(seed)[2]
    d = ((sh - h + 540) % 360) - 180
    hue = (h + 0.18 * d) % 360
    return tone_colour(hue, 0.13, 72 if dark else 52)


# ---- CSS --------------------------------------------------------------------

def libadwaita_overrides(s: dict[str, str], css_variables: bool) -> str:
    """Re-colour libadwaita's own widgets from the scheme.

    `@define-color` works on every libadwaita. libadwaita 1.6 reads CSS
    variables for its own widgets, so on a new enough GTK the same values are
    also written as `:root` variables (older GTK would warn about them).
    """
    named = {
        "accent_bg_color": s["primary"], "accent_fg_color": s["on_primary"], "accent_color": s["primary"],
        "destructive_bg_color": s["error"], "destructive_fg_color": s["on_error"], "destructive_color": s["error"],
        "window_bg_color": s["surface"], "window_fg_color": s["on_surface"],
        "view_bg_color": s["surface_container_lowest"], "view_fg_color": s["on_surface"],
        "headerbar_bg_color": s["surface"], "headerbar_fg_color": s["on_surface"],
        "headerbar_border_color": s["outline_variant"], "headerbar_backdrop_color": s["surface"],
        "card_bg_color": s["surface_container"], "card_fg_color": s["on_surface"],
        "dialog_bg_color": s["surface_container_high"], "dialog_fg_color": s["on_surface"],
        "popover_bg_color": s["surface_container_high"], "popover_fg_color": s["on_surface"],
        "sidebar_bg_color": s["surface_container_low"], "sidebar_fg_color": s["on_surface"],
        "sidebar_backdrop_color": s["surface_container_low"],
        "secondary_sidebar_bg_color": s["surface_container_low"], "secondary_sidebar_fg_color": s["on_surface"],
    }
    out = [f"@define-color {k} {v};" for k, v in named.items()]
    if css_variables:
        out.append(":root {")
        out += [f"  --{k.replace('_', '-')}: {v};" for k, v in named.items()]
        out.append("}")
    return "\n".join(out) + "\n"


def render_css(template: str, s: dict[str, str], seed: str, dark: bool, css_variables: bool) -> str:
    """Fill style.css's @@role@@ placeholders, then prepend libadwaita overrides.

    Our own CSS uses literal colours rather than @names, so it does not depend
    on named-colour support surviving in future GTK releases.
    """
    values = dict(s)
    values["shadow"] = "rgba(0,0,0,0.18)" if not dark else "rgba(0,0,0,0.45)"
    values["state_hover"] = with_alpha(s["on_surface"], 0.08)
    values["state_press"] = with_alpha(s["on_surface"], 0.12)
    values["primary_hover"] = mix(s["primary"], s["on_primary"], 0.08)
    values["primary_press"] = mix(s["primary"], s["on_primary"], 0.14)
    values["secondary_container_hover"] = mix(s["secondary_container"], s["on_secondary_container"], 0.08)
    values["error_container_hover"] = mix(s["error_container"], s["on_error_container"], 0.08)
    values["ok"] = category_colour("terminal", seed, dark)
    values["ok_container"] = mix(s["surface_container_high"], values["ok"], 0.30)
    values["on_ok_container"] = tone_colour(150, 0.08, 85 if dark else 22)
    values["warn_container"] = mix(s["surface_container_high"], tone_colour(75, 0.14, 70), 0.26)
    values["on_warn_container"] = tone_colour(75, 0.08, 88 if dark else 22)
    def fill(m: "re.Match[str]") -> str:
        key = m.group(1)
        if key not in values:  # a typo in style.css must be loud, not silent
            raise KeyError(f"style.css uses unknown colour role @@{key}@@")
        return values[key]

    return libadwaita_overrides(s, css_variables) + _PLACEHOLDER.sub(fill, template)


_PLACEHOLDER = re.compile(r"@@(\w+)@@")
