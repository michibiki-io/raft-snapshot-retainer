import unittest
from unittest.mock import patch
import compute_release as compute
import generate_release_notes as notes


class ReleaseTests(unittest.TestCase):
    def test_semver_uses_commits(self):
        for subject, message, expected in [("feat: retention #1", "", "minor"), ("fix: error #1", "", "patch"), ("feat!: changed #1", "", "major"), ("docs: text", "BREAKING CHANGE: contract", "major"), ("docs: text", "", "none")]:
            with self.subTest(subject=subject):
                self.assertEqual(compute.classify_release([{"subject": subject, "message": message}]), expected)
        self.assertEqual(compute.compute_next_tag("", "minor"), "v0.1.0")
        self.assertEqual(compute.compute_next_tag("v1.2.3", "patch"), "v1.2.4")
        with patch.dict("os.environ", {"PR_TITLE": "feat!: never classify this"}):
            self.assertEqual(compute.classify_release([{"subject": "docs: text", "message": ""}]), "none")

    def test_notes_commit_subject_and_issue(self):
        result = notes.build_entry("owner/repo", {"sha": "abc", "subject": "feat: retention #1", "body": "Details"}, "", {})
        self.assertEqual(result, ("Features", "- retention #1"))
        self.assertIsNone(notes.build_entry("owner/repo", {"sha": "abc", "subject": "docs: text", "body": ""}, "", {}))


if __name__ == "__main__":
    unittest.main()
