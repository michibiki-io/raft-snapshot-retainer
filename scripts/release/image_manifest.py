#!/usr/bin/env python3
"""Record immutable image digests, carrying pins from a published predecessor."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request

SEMVER = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")


def reachable_tags(previous_tag):
    """Keep tag-based allocation separate from published-release evidence."""
    if not previous_tag:
        return []
    match = SEMVER.fullmatch(previous_tag)
    if not match:
        raise ValueError("previous tag must be stable semver")
    ceiling = tuple(map(int, match.groups()))
    tags = subprocess.check_output(
        ["git", "tag", "--merged", previous_tag, "--sort=-version:refname", "--list", "v[0-9]*"], text=True
    ).splitlines()
    return [tag for tag in tags if SEMVER.fullmatch(tag)
            and tuple(map(int, SEMVER.fullmatch(tag).groups())) <= ceiling]


def published_release(repo, tag):
    url = f"https://api.github.com/repos/{repo}/releases/tags/{urllib.parse.quote(tag, safe='')}"
    request = urllib.request.Request(url, headers={
        "Accept": "application/vnd.github+json", "Authorization": "Bearer " + os.environ["GH_TOKEN"],
        "User-Agent": "raft-snapshot-retainer-release",
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            release = json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None  # Only a missing Release may be skipped, never a missing asset.
        raise RuntimeError(f"release metadata request failed: HTTP {error.code}") from None
    if (not isinstance(release, dict) or release.get("tag_name") != tag
            or not isinstance(release.get("draft"), bool) or not isinstance(release.get("prerelease"), bool)):
        raise ValueError("invalid release metadata")
    if release["draft"] or release["prerelease"]:
        return None
    return release


def validate_manifest(manifest, tag):
    if not isinstance(manifest, dict) or manifest.get("version") != tag or not isinstance(manifest.get("images"), list):
        raise ValueError("invalid approved image manifest")
    ids = set()
    for item in manifest["images"]:
        if not isinstance(item, dict):
            raise ValueError("invalid approved image entry")
        for field in ("id", "image", "tag", "digest", "digestRef"):
            if not isinstance(item.get(field), str) or not item[field].strip():
                raise ValueError("incomplete approved image entry")
        if (item["id"] in ids or not DIGEST.fullmatch(item["digest"])
                or item["digestRef"] != item["image"] + "@" + item["digest"]
                or not isinstance(item.get("platforms"), list) or not item["platforms"]
                or any(not isinstance(p, str) or not p for p in item["platforms"])):
            raise ValueError("invalid approved image pin")
        ids.add(item["id"])
    return manifest


def previous_approved(repo, previous_tag):
    for tag in reachable_tags(previous_tag):
        release = published_release(repo, tag)
        if release is None:
            continue
        # Once a published stable predecessor is found its asset is mandatory.
        # Never skip broken evidence and silently discard its unrelated pins.
        assets = release.get("assets")
        if not isinstance(assets, list) or not any(
            isinstance(asset, dict) and asset.get("name") == "approved-runtime-images.json" for asset in assets
        ):
            raise ValueError("published predecessor has no approved image manifest")
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run(["gh", "release", "download", tag, "--repo", repo, "--pattern",
                            "approved-runtime-images.json", "--dir", directory], check=True)
            manifest = json.loads((Path(directory) / "approved-runtime-images.json").read_text())
        return validate_manifest(manifest, tag)
    return None


def main():
    image = os.environ["IMAGE"]
    tag = os.environ["IMAGE_TAG"]
    inspected = subprocess.check_output(["docker", "buildx", "imagetools", "inspect", f"{image}:{tag}"], text=True)
    match = re.search(r"^Digest:\s+(sha256:[0-9a-f]{64})$", inspected, re.MULTILINE)
    if not match:
        raise RuntimeError("missing published manifest digest")
    entry = {"id": "raft-snapshot-retainer", "image": image, "tag": tag, "digest": match[1],
             "digestRef": f"{image}@{match[1]}", "platforms": ["linux/amd64", "linux/arm64"]}
    current = {"version": os.environ["VERSION"], "images": [entry]}
    approved = previous_approved(os.environ["GITHUB_REPOSITORY"], os.environ.get("PREVIOUS_TAG", ""))
    if approved is None:
        approved = {"version": os.environ["VERSION"], "images": []}
    approved["version"] = os.environ["VERSION"]
    approved["images"] = [item for item in approved["images"] if item["id"] != entry["id"]] + [entry]
    Path("dist").mkdir(exist_ok=True)
    for name, body in [("release-images.json", current), ("approved-runtime-images.json", approved)]:
        (Path("dist") / name).write_text(json.dumps(body, indent=2) + "\n")


if __name__ == "__main__":
    main()
