import json
import os
import subprocess
import uuid

import boto3
from botocore.config import Config
import pytest


@pytest.fixture
def s3():
    return boto3.client("s3", endpoint_url=os.environ["E2E_ENDPOINT"],
                        region_name="us-east-1", aws_access_key_id=os.environ["E2E_ACCESS_KEY"],
                        aws_secret_access_key=os.environ["E2E_SECRET_KEY"],
                        config=Config(s3={"addressing_style": "path"}))


@pytest.fixture
def bucket(s3):
    name = "retainer-e2e-" + uuid.uuid4().hex
    s3.create_bucket(Bucket=name)
    yield name
    # Cleanup is restricted to this test's unique disposable bucket.
    for page in s3.get_paginator("list_objects_v2").paginate(Bucket=name):
        for obj in page.get("Contents", []):
            s3.delete_object(Bucket=name, Key=obj["Key"])
    s3.delete_bucket(Bucket=name)


@pytest.fixture
def cli(bucket):
    def invoke(*args, **overrides):
        env = {k: v for k, v in os.environ.items() if not k.startswith(("RSR_", "AWS_"))}
        env.update(AWS_ACCESS_KEY_ID=env["E2E_ACCESS_KEY"], AWS_SECRET_ACCESS_KEY=env["E2E_SECRET_KEY"],
                   RSR_ENDPOINT=env["E2E_ENDPOINT"], RSR_BUCKET=bucket, RSR_REGION="us-east-1",
                   RSR_PREFIX="openbao", RSR_DAILY="2", RSR_MINIMUM="1", RSR_PATH_STYLE="true",
                   RSR_ALLOW_LOCAL_HTTP="true", RSR_TIMEOUT="30s", AWS_EC2_METADATA_DISABLED="true")
        env.update(overrides)
        names = [k for k in env if k.startswith("RSR_") or k in ("AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_EC2_METADATA_DISABLED")]
        command = ["docker", "run", "--rm", "--network", "host", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"]
        for name in names:
            command += ["-e", name]
        result = subprocess.run(command + [env["E2E_IMAGE"], *args], env=env, capture_output=True, text=True, timeout=90)
        return result.returncode, json.loads(result.stdout) if result.stdout else {}, result.stderr
    return invoke
