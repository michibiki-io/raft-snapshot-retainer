import contextlib
import copy
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import urllib.error

import image_manifest as manifest
import compute_release as compute

DIGEST = "sha256:" + "a" * 64


def pin(name):
    image = "ghcr.io/example/" + name
    return {"id": name, "image": image, "tag": "0.0.9", "digest": DIGEST,
            "digestRef": image + "@" + DIGEST, "platforms": ["linux/amd64"]}


def release(tag, **extra):
    return {"tag_name": tag, "draft": False, "prerelease": False,
            "assets": [{"name": "approved-runtime-images.json"}], **extra}


@contextlib.contextmanager
def directory():
    with tempfile.TemporaryDirectory() as temporary:
        before = Path.cwd()
        os.chdir(temporary)
        try:
            yield Path(temporary)
        finally:
            os.chdir(before)


class ImageManifestTests(unittest.TestCase):
    def test_reachable_tags_and_allocation_still_include_orphan_tag(self):
        with directory():
            def git(*args):
                return subprocess.check_output(["git", *args], text=True).strip()
            git("init", "-q", "-b", "main")
            git("config", "user.name", "Release fixture")
            git("config", "user.email", "fixture@example.invalid")
            git("commit", "--allow-empty", "-qm", "feat: first")
            git("tag", "v0.0.9")
            git("switch", "-q", "-c", "unrelated")
            git("commit", "--allow-empty", "-qm", "feat: unrelated")
            git("tag", "v9.0.0")
            git("switch", "-q", "main")
            git("commit", "--allow-empty", "-qm", "feat: orphan release")
            git("tag", "v0.1.0")
            git("commit", "--allow-empty", "-qm", "fix: recovery")
            self.assertEqual(manifest.reachable_tags("v0.1.0"), ["v0.1.0", "v0.0.9"])
            self.assertEqual(compute.previous_tag_for_merge("HEAD"), "v0.1.0")
            self.assertEqual(compute.compute_next_tag("v0.1.0", "patch"), "v0.1.1")

    def test_empty_history_needs_no_api(self):
        with patch.object(manifest, "published_release") as api:
            self.assertIsNone(manifest.previous_approved("owner/repo", ""))
            api.assert_not_called()

    def test_only_explicit_release_404_is_missing(self):
        with patch.dict(os.environ, GH_TOKEN="fixture-token"):
            with patch.object(manifest.urllib.request, "urlopen", side_effect=urllib.error.HTTPError("url", 404, "missing", {}, None)):
                self.assertIsNone(manifest.published_release("owner/repo", "v0.1.0"))
            for status in (401, 403, 429, 500):
                with self.subTest(status=status), patch.object(manifest.urllib.request, "urlopen", side_effect=urllib.error.HTTPError("url", status, "failure", {}, None)):
                    with self.assertRaises(RuntimeError):
                        manifest.published_release("owner/repo", "v0.1.0")
            for failure in (urllib.error.URLError("offline"), TimeoutError("timeout")):
                with patch.object(manifest.urllib.request, "urlopen", side_effect=failure):
                    with self.assertRaises(type(failure)):
                        manifest.published_release("owner/repo", "v0.1.0")
            for metadata, expected in [(release("v0.1.0"), True), (release("v0.1.0", draft=True), False), (release("v0.1.0", prerelease=True), False)]:
                with patch.object(manifest.urllib.request, "urlopen", return_value=io.StringIO(json.dumps(metadata))):
                    self.assertEqual(manifest.published_release("owner/repo", "v0.1.0") is not None, expected)
            with patch.object(manifest.urllib.request, "urlopen", return_value=io.StringIO('{}')):
                with self.assertRaises(ValueError):
                    manifest.published_release("owner/repo", "v0.1.0")

    def test_interrupted_first_release_can_start_empty(self):
        with patch.object(manifest, "reachable_tags", return_value=["v0.1.0"]), patch.object(manifest, "published_release", return_value=None) as api:
            self.assertIsNone(manifest.previous_approved("owner/repo", "v0.1.0"))
            api.assert_called_once_with("owner/repo", "v0.1.0")

    def test_orphan_tags_skip_to_published_predecessor(self):
        body = {"version": "v0.0.9", "images": [pin("other-service"), pin("raft-snapshot-retainer")]}
        def download(argv, **kwargs):
            self.assertEqual(argv[:4], ["gh", "release", "download", "v0.0.9"])
            self.assertTrue(kwargs["check"])
            (Path(argv[-1]) / "approved-runtime-images.json").write_text(json.dumps(body))
        with patch.object(manifest, "reachable_tags", return_value=["v0.1.0", "v0.0.9"]), patch.object(manifest, "published_release", side_effect=[None, release("v0.0.9")]) as api, patch.object(manifest.subprocess, "run", side_effect=download):
            self.assertEqual(manifest.previous_approved("owner/repo", "v0.1.0"), body)
            self.assertEqual(api.call_count, 2)

    def test_published_predecessor_missing_or_bad_asset_stops(self):
        cases = [None, "invalid-json", {"version": "v0.0.9", "images": "bad"},
                 {"version": "v0.0.9", "images": [{"id": "incomplete"}]},
                 {"version": "v0.0.9", "images": [pin("duplicate"), pin("duplicate")]},
                 {"version": "v0.0.8", "images": []}]
        for body in cases:
            with self.subTest(body=body):
                metadata = release("v0.0.9", assets=[] if body is None else [{"name": "approved-runtime-images.json"}])
                def download(argv, **kwargs):
                    (Path(argv[-1]) / "approved-runtime-images.json").write_text(body if isinstance(body, str) else json.dumps(body))
                with patch.object(manifest, "reachable_tags", return_value=["v0.0.9", "v0.0.8"]), patch.object(manifest, "published_release", return_value=metadata) as api, patch.object(manifest.subprocess, "run", side_effect=download):
                    with self.assertRaises(ValueError):
                        manifest.previous_approved("owner/repo", "v0.0.9")
                    self.assertEqual(api.call_count, 1)
        with patch.object(manifest, "reachable_tags", return_value=["v0.0.9"]), patch.object(manifest, "published_release", return_value=release("v0.0.9")), patch.object(manifest.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "gh")):
            with self.assertRaises(subprocess.CalledProcessError):
                manifest.previous_approved("owner/repo", "v0.0.9")

    def test_outputs_preserve_other_pins_and_replace_current(self):
        previous = {"version": "v0.0.9", "images": [pin("other-service"), pin("raft-snapshot-retainer")]}
        environment = {"IMAGE": "ghcr.io/example/retainer", "IMAGE_TAG": "0.1.1", "VERSION": "v0.1.1",
                       "PREVIOUS_TAG": "v0.1.0", "GITHUB_REPOSITORY": "owner/repo"}
        for predecessor in (None, previous):
            with self.subTest(predecessor=predecessor), directory(), patch.dict(os.environ, environment), patch.object(manifest, "previous_approved", return_value=copy.deepcopy(predecessor)), patch.object(manifest.subprocess, "check_output", return_value="Digest: " + DIGEST + "\n"):
                manifest.main()
                current = json.loads(Path("dist/release-images.json").read_text())
                approved = json.loads(Path("dist/approved-runtime-images.json").read_text())
                self.assertEqual(current["version"], "v0.1.1")
                self.assertEqual(approved["version"], "v0.1.1")
                self.assertEqual(approved["images"][-1], current["images"][0])
                if predecessor:
                    self.assertEqual(approved["images"][0], previous["images"][0])
                    self.assertEqual(len(approved["images"]), 2)
                else:
                    self.assertEqual(len(approved["images"]), 1)
