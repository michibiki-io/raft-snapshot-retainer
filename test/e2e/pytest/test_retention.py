import pytest

KEYS = [f"openbao/bao_2025-01-{day:02d}-0000.snapshot" for day in range(1, 5)]
IGNORED = ["openbao-other/bao_2025-01-01-0000.snapshot", "openbao/cluster-b/bao_2025-01-01-0000.snapshot", "openbao/notes.txt"]


def inventory(s3, bucket):
    return {o["Key"] for page in s3.get_paginator("list_objects_v2").paginate(Bucket=bucket) for o in page.get("Contents", [])}


def seed(s3, bucket):
    for key in KEYS + IGNORED:
        s3.put_object(Bucket=bucket, Key=key, Body=b"disposable synthetic fixture")


def test_plan_apply_rerun(s3, bucket, cli):
    seed(s3, bucket)
    before = inventory(s3, bucket)
    code, plan, stderr = cli("--successful-upload-key", KEYS[-1])
    assert code == 0, stderr
    assert plan["status"] == "planned"
    assert plan["counts"]["delete"] == 2
    assert plan["deleted"] == []
    assert inventory(s3, bucket) == before
    code, result, stderr = cli("--apply", "--successful-upload-key", KEYS[-1])
    assert code == 0, (result, stderr)
    assert result["deleted"] == KEYS[:2]
    assert inventory(s3, bucket) == set(KEYS[2:] + IGNORED)
    code, result, stderr = cli("--apply", "--successful-upload-key", KEYS[-1])
    assert code == 0, stderr
    assert result["deleted"] == []


@pytest.mark.parametrize("failure", ["missing", "empty", "nested", "inconsistent", "future", "invalid_config"])
def test_failed_preflight_never_deletes(s3, bucket, cli, failure):
    seed(s3, bucket)
    pin = KEYS[-1]
    overrides = {}
    if failure == "missing":
        pin = "openbao/bao_2025-02-01-0000.snapshot"
    elif failure == "empty":
        s3.put_object(Bucket=bucket, Key=pin, Body=b"")
    elif failure == "nested":
        pin = IGNORED[1]
    elif failure == "inconsistent":
        s3.put_object(Bucket=bucket, Key=KEYS[0], Body=b"")
    elif failure == "future":
        s3.put_object(Bucket=bucket, Key="openbao/bao_2999-01-01-0000.snapshot", Body=b"future")
    else:
        overrides["RSR_PREFIX"] = "/"
    before = inventory(s3, bucket)
    code, result, stderr = cli("--apply", "--successful-upload-key", pin, **overrides)
    assert code != 0
    assert not result.get("deleted")
    assert inventory(s3, bucket) == before


def test_empty_store(s3, bucket, cli):
    code, result, stderr = cli()
    assert code == 0, stderr
    assert result["plan"]["decisions"] == []
    assert result["deleted"] == []
