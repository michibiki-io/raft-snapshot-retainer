#!/usr/bin/env bash
set -euo pipefail

arch=$1
image=$2
version=$3
tmp_dir=$(mktemp -d)
container=
trap 'if [[ -n "$container" ]]; then docker rm "$container" >/dev/null; fi; rm -rf "$tmp_dir"' EXIT

container=$(docker create --pull=never --platform "linux/$arch" "$image")
docker cp "$container:/usr/local/bin/raft-snapshot-retainer" "$tmp_dir/raft-snapshot-retainer"
python3 "$(dirname "$0")/verify_elf.py" "$arch" "$tmp_dir/raft-snapshot-retainer"

actual=$(docker run --rm --pull=never --network=none --platform "linux/$arch" "$image" --version)
if [[ "$actual" != "$version" ]]; then
    printf 'Expected image version %s, got %s\n' "$version" "$actual" >&2
    exit 1
fi
printf '%s: linux/%s entrypoint returned version %s\n' "$image" "$arch" "$actual"
