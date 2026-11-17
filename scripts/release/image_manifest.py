#!/usr/bin/env python3
"""Record immutable image digests and carry forward previous approved pins."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def main():
    image = os.environ["IMAGE"]
    tag = os.environ["IMAGE_TAG"]
    inspected = subprocess.check_output(["docker", "buildx", "imagetools", "inspect", f"{image}:{tag}"], text=True)
    match = re.search(r"^Digest:\s+(sha256:[0-9a-f]{64})$", inspected, re.MULTILINE)
    if not match:
        raise RuntimeError("missing published manifest digest")
    entry = {"id": "raft-snapshot-retainer", "image": image, "tag": tag, "digest": match[1], "digestRef": f"{image}@{match[1]}", "platforms": ["linux/amd64", "linux/arm64"]}
    current = {"version": os.environ["VERSION"], "images": [entry]}
    approved = {"version": os.environ["VERSION"], "images": []}
    previous = os.environ.get("PREVIOUS_TAG", "")
    if previous:
        with tempfile.TemporaryDirectory() as directory:
            # Missing/unreadable previous evidence is a failure, never silently
            # discard another approved image from the carried-forward set.
            subprocess.run(["gh", "release", "download", previous, "--repo", os.environ["GITHUB_REPOSITORY"], "--pattern", "approved-runtime-images.json", "--dir", directory], check=True)
            approved = json.loads((Path(directory) / "approved-runtime-images.json").read_text())
    approved["version"] = os.environ["VERSION"]
    approved["images"] = [item for item in approved["images"] if item["id"] != entry["id"]] + [entry]
    Path("dist").mkdir(exist_ok=True)
    for name, body in [("release-images.json", current), ("approved-runtime-images.json", approved)]:
        (Path("dist") / name).write_text(json.dumps(body, indent=2) + "\n")


if __name__ == "__main__":
    main()
