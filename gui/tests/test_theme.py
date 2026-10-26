"""Pure-Python tests for the colour maths and CSS rendering (no GTK needed)."""
import os, re, sys, unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from deskagent_gui import theme  # noqa: E402

SEEDS = ["#6750a4", "#0b6bcb", "#2e7d32", "#c2185b", "#ff9800", "#00897b", "#795548"]
HEX = re.compile(r"^#[0-9a-f]{6}$")
CSS = os.path.join(os.path.dirname(__file__), "..", "deskagent_gui", "style.css")


class Tones(unittest.TestCase):
    def test_tone_is_the_requested_lstar(self):
        for hue in (0, 60, 120, 200, 280, 330):
            for tone in (10, 30, 50, 80, 90):
                got = theme.lstar(theme.tone_colour(hue, 0.10, tone))
                self.assertAlmostEqual(got, tone, delta=2.5, msg=f"hue {hue} tone {tone}")

    def test_extremes(self):
        self.assertEqual(theme.tone_colour(120, 0.1, 100), "#ffffff")
        self.assertEqual(theme.tone_colour(120, 0.1, 0), "#000000")

    def test_always_in_gamut_hex(self):
        for hue in range(0, 360, 20):
            for tone in range(0, 101, 10):
                self.assertRegex(theme.tone_colour(hue, 0.30, tone), HEX)


class Scheme(unittest.TestCase):
    def test_all_roles_are_hex_in_both_modes(self):
        for seed in SEEDS:
            for dark in (False, True):
                for role, value in theme.scheme(seed, dark).items():
                    self.assertRegex(value, HEX, f"{seed} dark={dark} {role}")

    def test_light_and_dark_have_the_same_roles(self):
        self.assertEqual(theme.scheme(SEEDS[0], False).keys(), theme.scheme(SEEDS[0], True).keys())

    def test_text_pairs_are_readable(self):
        pairs = [("on_primary", "primary"), ("on_primary_container", "primary_container"),
                 ("on_secondary_container", "secondary_container"), ("on_tertiary_container", "tertiary_container"),
                 ("on_error", "error"), ("on_error_container", "error_container"),
                 ("on_surface", "surface"), ("on_surface", "surface_container_high"),
                 ("on_surface_variant", "surface_container")]
        for seed in SEEDS:
            for dark in (False, True):
                s = theme.scheme(seed, dark)
                for fg, bg in pairs:
                    self.assertGreaterEqual(theme.contrast(s[fg], s[bg]), 4.5, f"{seed} dark={dark} {fg} on {bg}")

    def test_dark_is_darker_than_light(self):
        self.assertLess(theme.lstar(theme.scheme(SEEDS[0], True)["surface"]),
                        theme.lstar(theme.scheme(SEEDS[0], False)["surface"]))

    def test_seed_hue_is_kept(self):
        for seed in SEEDS[:4]:
            want = theme.hex_to_oklch(seed)[2]
            got = theme.hex_to_oklch(theme.scheme(seed, False)["primary"])[2]
            d = abs((want - got + 180) % 360 - 180)
            self.assertLess(d, 12, seed)


class Categories(unittest.TestCase):
    def test_stable_and_distinct(self):
        cats = ["terminal", "editor", "browser", "chat", "music", "video", "docs", "shopping"]
        for dark in (False, True):
            cols = [theme.category_colour(c, SEEDS[0], dark) for c in cats]
            self.assertEqual(cols, [theme.category_colour(c, SEEDS[0], dark) for c in cats])
            self.assertEqual(len(set(cols)), len(cols))

    def test_unknown_category_colour_is_stable_across_launches(self):
        # str hash() is randomised per process; the colour must not be.
        import subprocess
        code = ("import sys; sys.path.insert(0, %r); from deskagent_gui import theme; "
                "print(theme.category_colour('game', '#6750a4', False))") % os.path.join(os.path.dirname(__file__), "..")
        runs = {subprocess.run([sys.executable, "-c", code], capture_output=True, text=True,
                               env=dict(os.environ, PYTHONHASHSEED=str(i))).stdout for i in (1, 2, 3)}
        self.assertEqual(len(runs), 1, runs)

    def test_neutral_for_other(self):
        self.assertEqual(theme.category_colour("other", SEEDS[0], False), theme.category_colour("private", SEEDS[0], False))


class Css(unittest.TestCase):
    def setUp(self):
        self.tpl = open(CSS).read()

    def test_every_placeholder_resolves(self):
        for dark in (False, True):
            out = theme.render_css(self.tpl, theme.scheme(SEEDS[0], dark), SEEDS[0], dark, False)
            self.assertNotIn("@@", out)

    def test_unknown_role_is_loud(self):
        with self.assertRaises(KeyError):
            theme.render_css("a { color: @@nope@@; }", theme.scheme(SEEDS[0], False), SEEDS[0], False, False)

    def test_css_variables_only_when_asked(self):
        s = theme.scheme(SEEDS[0], False)
        self.assertNotIn(":root", theme.render_css(self.tpl, s, SEEDS[0], False, False))
        self.assertIn("--accent-bg-color", theme.render_css(self.tpl, s, SEEDS[0], False, True))
        self.assertIn("@define-color accent_bg_color", theme.render_css(self.tpl, s, SEEDS[0], False, False))


if __name__ == "__main__":
    unittest.main()
