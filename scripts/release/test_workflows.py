"""Guard the event/cache contracts that caused duplicate CI and failed releases."""
from pathlib import Path
import unittest
import yaml

ROOT = Path(__file__).resolve().parents[2]


def workflow(name):
    return yaml.load((ROOT / ".github/workflows" / name).read_text(), Loader=yaml.BaseLoader)


def builds(document):
    return [step for job in document["jobs"].values() for step in job["steps"]
            if step.get("uses", "").startswith("docker/build-push-action@")]


class WorkflowTests(unittest.TestCase):
    def test_tests_run_only_for_active_pr_updates(self):
        test = workflow("test.yml")
        self.assertEqual(set(test["on"]), {"pull_request"})
        self.assertEqual(set(test["on"]["pull_request"]["types"]), {"opened", "synchronize", "reopened"})
        self.assertIn("vars.ACTIONS_TEST_RUNNER", test["jobs"]["test"]["runs-on"])
        self.assertIn("ubuntu-latest", test["jobs"]["test"]["runs-on"])
        steps = test["jobs"]["test"]["steps"]
        self.assertTrue(any("make check test" in s.get("run", "") for s in steps))
        self.assertTrue(any("test/e2e/scripts/run.py" in s.get("run", "") for s in steps))
        for step in builds(test):
            self.assertEqual(step["with"]["cache-from"], "type=gha")
            self.assertEqual(step["with"]["cache-to"], "type=gha,mode=max")
            self.assertEqual(step["with"]["platforms"], "linux/amd64")

    def test_post_merge_release_never_exports_gha_cache(self):
        release = workflow("release.yml")
        self.assertEqual(release["on"], {"pull_request_target": {"types": ["closed"]}})
        prepare = release["jobs"]["prepare"]["if"]
        self.assertIn("github.event.pull_request.merged == true", prepare)
        self.assertIn("feature/", prepare)
        paths = builds(release)
        self.assertEqual(len(paths), 3)  # QEMU, native amd64, native arm64.
        self.assertEqual({s["with"]["platforms"] for s in paths}, {"linux/amd64,linux/arm64", "linux/amd64", "linux/arm64"})
        for step in paths:
            self.assertEqual(step["with"]["cache-from"], "type=gha")
            self.assertNotIn("cache-to", step["with"])
            self.assertEqual(step["with"]["push"], "true")
        self.assertEqual(release["permissions"]["packages"], "write")
        self.assertTrue(any(s.get("uses", "").startswith("docker/login-action@")
                            for s in release["jobs"]["publish"]["steps"]))
