"""Guard the workflow event, native architecture and publication contracts."""
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
    def assert_native_matrix(self, job, amd64_runner, arm64_runner):
        self.assertEqual(job["runs-on"], "${{ matrix.runner }}")
        self.assertEqual(job["strategy"]["fail-fast"], "false")
        self.assertEqual(job["strategy"]["matrix"]["include"], [
            {"arch": "amd64", "machine": "x86_64", "runner": amd64_runner},
            {"arch": "arm64", "machine": "aarch64", "runner": arm64_runner},
        ])
        first_build = next(i for i, step in enumerate(job["steps"])
                           if step.get("uses", "").startswith("docker/build-push-action@"))
        guard = next(step for step in job["steps"][:first_build]
                     if step.get("name") == "Verify native Linux runner and Docker daemon")
        self.assertEqual(guard["env"]["MACHINE"], "${{ matrix.machine }}")
        self.assertIn('test "$(uname -sm)" = "Linux $MACHINE"', guard["run"])
        self.assertIn("{{.OSType}} {{.Architecture}}", guard["run"])

    def test_tests_run_only_for_active_pr_updates(self):
        test = workflow("test.yml")
        self.assertEqual(set(test["on"]), {"pull_request"})
        self.assertEqual(set(test["on"]["pull_request"]["types"]), {"opened", "synchronize", "reopened"})
        self.assertEqual(test["permissions"], {"contents": "read"})

    def test_pr_builds_and_checks_both_native_images_without_publishing(self):
        test = workflow("test.yml")
        job = test["jobs"]["test"]
        self.assert_native_matrix(job, "${{ vars.ACTIONS_TEST_RUNNER || 'ubuntu-latest' }}", "ubuntu-24.04-arm")
        steps = job["steps"]
        for command in ("make check test", "test/e2e/scripts/run.py"):
            step = next(step for step in steps if command in step.get("run", ""))
            self.assertEqual(step["if"], "matrix.arch == 'amd64'")
        build, = builds(test)
        options = build["with"]
        self.assertEqual(options["file"], "./Dockerfile")
        self.assertEqual(options["platforms"], "linux/${{ matrix.arch }}")
        self.assertEqual(options["load"], "true")
        self.assertEqual(options["push"], "false")
        cache = "type=gha,scope=raft-snapshot-retainer-${{ matrix.arch }}"
        self.assertEqual(options["cache-from"], cache)
        self.assertEqual(options["cache-to"], f"{cache},mode=max")
        verify = next(step for step in steps[steps.index(build) + 1:] if "verify_image.sh" in step.get("run", ""))
        self.assertNotIn("if", verify)
        self.assertEqual(verify["env"]["ARCH"], "${{ matrix.arch }}")

    def test_post_merge_release_never_exports_gha_cache(self):
        release = workflow("release.yml")
        self.assertEqual(release["on"], {"pull_request_target": {"types": ["closed"]}})
        prepare = release["jobs"]["prepare"]["if"]
        self.assertIn("github.event.pull_request.merged == true", prepare)
        self.assertIn("feature/", prepare)
        build, = builds(release)
        options = build["with"]
        self.assertEqual(options["cache-from"], "type=gha")
        self.assertNotIn("cache-to", options)
        self.assertEqual(options["push"], "true")
        self.assertEqual(release["permissions"]["packages"], "write")
        self.assertTrue(any(s.get("uses", "").startswith("docker/login-action@")
                            for s in release["jobs"]["publish"]["steps"]))

    def test_release_tags_require_both_verified_native_artifacts(self):
        release = workflow("release.yml")
        jobs = release["jobs"]
        job = jobs["build"]
        self.assert_native_matrix(job, "${{ vars.ACTIONS_RUNNER || 'ubuntu-latest' }}",
                                  "${{ vars.ACTIONS_RUNNER_ARM64 || 'ubuntu-24.04-arm' }}")
        self.assertEqual(job["if"], "needs.prepare.outputs.release_required == 'true'")
        for workflow_name in ("test.yml", "release.yml"):
            self.assertFalse(any("docker/setup-qemu-action" in step.get("uses", "")
                                 for job in workflow(workflow_name)["jobs"].values() for step in job["steps"]))
        build, = builds(release)
        options = build["with"]
        self.assertEqual(options["file"], "./Dockerfile")
        self.assertEqual(options["platforms"], "linux/${{ matrix.arch }}")
        self.assertIn("push-by-digest=true", options["outputs"])
        self.assertEqual(options["provenance"], "true")
        self.assertEqual(options["sbom"], "true")
        steps = jobs["build"]["steps"]
        verify = steps[steps.index(build) + 1]
        self.assertIn('docker buildx imagetools inspect --raw "$IMAGE@$INDEX_DIGEST"', verify["run"])
        self.assertIn('verify_image.sh "$ARCH" "$IMAGE@$child" "$VERSION"', verify["run"])
        self.assertEqual(steps[steps.index(verify) + 1]["with"]["name"], "image-digest-${{ matrix.arch }}")
        archive = next(step for step in steps if step.get("name") == "Build and verify native release archive")
        self.assertIn('tar -xzf "$archive"', archive["run"])
        self.assertIn('verify_elf.py "$ARCH" "$extracted/raft-snapshot-retainer"', archive["run"])
        self.assertIn("build", jobs["docker-manifest"]["needs"])
        self.assertIn("docker-manifest", jobs["publish"]["needs"])
