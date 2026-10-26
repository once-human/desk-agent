"""Pure-Python tests for the formatting helpers, including the privacy wording."""
import os, sys, unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from deskagent_gui import fmt  # noqa: E402


class Times(unittest.TestCase):
    def test_ago(self):
        self.assertEqual(fmt.ago(None), "never")
        self.assertEqual(fmt.ago(2), "just now")
        self.assertEqual(fmt.ago(42), "42s ago")
        self.assertEqual(fmt.ago(125), "2 min ago")
        self.assertEqual(fmt.ago(3600), "1 h ago")
        self.assertEqual(fmt.ago(3900), "1 h 5 min ago")
        self.assertEqual(fmt.ago(3 * 86400), "3 d ago")
        self.assertEqual(fmt.ago(-5), "just now")

    def test_duration(self):
        self.assertEqual(fmt.duration(7), "7s")
        self.assertEqual(fmt.duration(600), "10m")
        self.assertEqual(fmt.duration(3720), "1h 02m")
        self.assertEqual(fmt.duration(90000), "1d 1h")

    def test_parse(self):
        self.assertEqual(fmt.parse_t("1970-01-01T00:01:00Z"), 60)
        self.assertIsNone(fmt.parse_t("garbage"))
        self.assertIsNone(fmt.parse_t(None))
        self.assertEqual(fmt.parse_iso("1970-01-01T00:00:30Z"), 30)
        self.assertEqual(fmt.parse_iso("1970-01-01T01:00:00+01:00"), 0)
        self.assertIsNone(fmt.parse_iso("0001-01-01T00:00:00Z"))
        self.assertIsNone(fmt.parse_iso(""))


class Facts(unittest.TestCase):
    def rows(self, s):
        return {label: value for _, label, value in fmt.sample_facts(s)}

    def test_active_app_site_repo(self):
        r = self.rows({"active": True, "app": {"name": "Kitty", "category": "terminal"},
                       "site": {"name": "GitHub", "category": "dev"}, "repo": "desk-agent", "claude": True})
        self.assertEqual(r["Presence"], "At the computer")
        self.assertIn("Kitty", r["App"])
        self.assertIn("GitHub", r["Site"])
        self.assertEqual(r["Repo"], "desk-agent")
        self.assertEqual(r["Claude Code"], "working")

    def test_away(self):
        self.assertEqual(self.rows({"active": False, "idleS": 600})["Presence"], "Away, idle 10m")
        self.assertEqual(self.rows({"active": False})["Presence"], "Away")

    def test_youtube_never_has_a_title(self):
        r = self.rows({"active": True, "media": {"source": "youtube", "state": "playing", "videos": 2,
                                                 "title": "SHOULD NOT APPEAR"}})
        self.assertIn("watching something on YouTube (2 videos)", r["Media"])
        self.assertNotIn("SHOULD NOT APPEAR", r["Media"])

    def test_native_player_track(self):
        r = self.rows({"active": True, "media": {"source": "spotify", "state": "playing", "artist": "A", "title": "T"}})
        self.assertIn("A — T", r["Media"])

    def test_nothing_sent(self):
        self.assertEqual(fmt.sample_facts(None), [])
        self.assertIn("nothing", fmt.pretty_json(None))

    def test_primary_label_and_category(self):
        s = {"active": True, "app": {"name": "Firefox", "category": "browser"}, "site": {"name": "GitHub", "category": "dev"}}
        self.assertEqual(fmt.primary_label(s), "GitHub")
        self.assertEqual(fmt.primary_category(s), "dev")
        self.assertEqual(fmt.primary_category({"active": False}), "")
        self.assertEqual(fmt.primary_label({"active": False}), "away")


class Public(unittest.TestCase):
    def test_defensive_about_unknown_shapes(self):
        p = fmt.public_summary({"online": True, "active": True, "doing": {"label": "Coding"}, "playing": ["x"]})
        self.assertTrue(p["online"])
        self.assertEqual(p["doing"], "Coding")
        self.assertIsInstance(p["playing"], str)
        self.assertEqual(fmt.public_summary(None)["doing"], None)
        self.assertFalse(fmt.public_summary({})["online"])


if __name__ == "__main__":
    unittest.main()
