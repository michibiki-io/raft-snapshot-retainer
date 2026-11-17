#!/usr/bin/env python3
"""Linux Docker Compose lifecycle. Only disposable local MinIO is targeted."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
STATE = ROOT / "test/e2e/.state"
RESULTS = ROOT / "test/e2e/results"
env = dict(os.environ)
config = Path(env.get("E2E_ENV_FILE", ROOT / "test/e2e/.env"))
if config.exists():
    for line in config.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            k, v = line.split("=", 1)
            env.setdefault(k.strip(), v.strip())
env.setdefault("LOCAL_UID", str(os.getuid()))
env.setdefault("LOCAL_GID", str(os.getgid()))
STATE.mkdir(parents=True, exist_ok=True)
state_file = STATE / "env.json"
compose = ["docker", "compose", "-p", "raft-retainer-e2e", "-f", str(ROOT / "test/e2e/compose.yaml")]


def run(args, **kwargs):
    return subprocess.run(args, cwd=ROOT, env=env, check=True, **kwargs)


def down():
    if state_file.exists():
        saved = json.loads(state_file.read_text())
        env.update(saved)
        run(compose + ["down", "--volumes", "--remove-orphans"])
        state_file.unlink()


if "--down" in sys.argv:
    down()
    sys.exit(0)
if state_file.exists():
    raise SystemExit("Existing e2e state; run make e2e-compose-down first")
env["E2E_ACCESS_KEY"] = secrets.token_hex(12)
env["E2E_SECRET_KEY"] = secrets.token_hex(24)
env.setdefault("E2E_IMAGE", "raft-snapshot-retainer:e2e")
state_file.touch(mode=0o600)
state_file.write_text(json.dumps({k: v for k, v in env.items() if k.startswith("E2E_")}))
RESULTS.mkdir(parents=True, exist_ok=True)
failed = True
try:
    run(["docker", "build", "-t", env["E2E_IMAGE"], "."])
    run(compose + ["up", "-d", "--build", "--wait", "--wait-timeout", "150"])
    port = run(compose + ["port", "s3", "9000"], capture_output=True, text=True).stdout.strip().rsplit(":", 1)[1]
    env["E2E_ENDPOINT"] = f"http://127.0.0.1:{port}"
    print(f"Compose S3 endpoint: {env['E2E_ENDPOINT']} (isolated fixtures only)", flush=True)
    run([sys.executable, "-m", "pytest", "test/e2e/pytest", "-q", "--junitxml", str(RESULTS / "junit/e2e.xml")])
    failed = False
finally:
    with (RESULTS / "compose.log").open("w") as logs:
        subprocess.run(compose + ["logs", "--no-color"], cwd=ROOT, env=env, stdout=logs, stderr=subprocess.STDOUT)
    keep = env.get("E2E_KEEP", "false").lower() == "true" or (failed and env.get("E2E_KEEP_ON_FAILURE", "false").lower() == "true")
    if keep:
        print("Environment retained. Logs: docker compose -p raft-retainer-e2e -f test/e2e/compose.yaml logs")
        print("Cleanup: make e2e-compose-down")
    else:
        down()
