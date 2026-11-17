# raft-snapshot-retainer

One-shot GFS retention for raw OpenBao Raft snapshots in S3-compatible storage.
Default execution is read-only. This program neither creates nor decrypts snapshots,
and needs no OpenBao or Kubernetes access.

## Contents

- [English](#english): [Quick start](#quick-start), [Configuration](#configuration), [Selection and safety](#selection-and-safety), [Storage and credentials](#storage-and-credentials), [Operations and integration](#operations-and-integration), [Build, test and release](#build-test-and-release)
- [日本語](#日本語): [実行例](#実行例), [設定](#設定), [保持と削除の安全条件](#保持と削除の安全条件), [接続と認証](#接続と認証), [運用と連携](#運用と連携), [ビルド・テスト・リリース](#ビルドテストリリース)
- [Acceptance evidence](docs/acceptance.md), [illustrative CronJob](examples/cronjob.yaml)

## English

### Quick start

Use Go 1.27.4, or the container. Supply credentials through SDK environment,
a workload identity or mounted files; never put credential values in arguments.
The example bucket, prefix and endpoint below are placeholders.

```sh
make build
export RSR_BUCKET=example-backups RSR_PREFIX=openbao RSR_REGION=us-east-1
export RSR_HOURLY=0 RSR_DAILY=7 RSR_WEEKLY=0 RSR_MONTHLY=0
# Credential environment is injected by the scheduler/Secret, not printed here.
dist/raft-snapshot-retainer                          # dry-run
# Run only after a successful upload; use its EXACT completed key.
dist/raft-snapshot-retainer --apply \
  --successful-upload-key openbao/bao_2026-11-18-0000.snapshot
```

`--help` and `--version` require no configuration or storage access. Configuration
is strict JSON in `--config FILE`, overridden by `RSR_*` environment, then the
`--successful-upload-key` flag. `--apply` is a command-line-only switch: neither a
configuration file nor an environment variable can enable deletion. No credentials
are accepted as CLI arguments or JSON values. Unknown JSON fields (including
nested policy fields), unknown `RSR_*` variables and positional arguments fail.

### Configuration

| JSON field | Environment | Meaning / default |
|---|---|---|
| `bucket` | `RSR_BUCKET` | Required bucket name |
| `prefix` | `RSR_PREFIX` | Required non-root directory; one trailing slash normalized |
| `region` | `RSR_REGION` | Required signing region, already resolved by consumer |
| `endpoint` | `RSR_ENDPOINT` | Optional HTTPS origin; empty uses SDK AWS endpoint |
| `path_style` | `RSR_PATH_STYLE` | Boolean, default false (virtual hosted) |
| `allow_local_http` | `RSR_ALLOW_LOCAL_HTTP` | Default false; HTTP only for literal loopback IP or `localhost` |
| `policy.hourly` | `RSR_HOURLY` | Occupied hourly periods, default 0 |
| `policy.daily` | `RSR_DAILY` | Occupied UTC days, default 0 |
| `policy.weekly` | `RSR_WEEKLY` | Occupied ISO weeks, default 0 |
| `policy.monthly` | `RSR_MONTHLY` | Occupied calendar months, default 0 |
| `policy.minimum` | `RSR_MINIMUM` | Positive minimum newest-object floor, default 1 |
| `successful_upload_key` | `RSR_SUCCESSFUL_UPLOAD_KEY` | Exact completed key, required for apply |
| `ca_file` | `RSR_CA_FILE` | PEM custom CA bundle path; TLS verification remains enabled |
| `access_key_file` | `RSR_ACCESS_KEY_FILE` | Mounted access key path, paired with secret key file |
| `secret_key_file` | `RSR_SECRET_KEY_FILE` | Mounted secret key path |
| `session_token_file` | `RSR_SESSION_TOKEN_FILE` | Optional mounted token, requires both key files |
| `timeout` | `RSR_TIMEOUT` | Whole-run Go duration, default `5m`, positive and at most `24h` |
| `format` | `RSR_FORMAT` | `json` (default) or `text` |

All GFS counts must be nonnegative and at least one must be positive; there is no
implicit production retention schedule. The defaults alone are intentionally invalid.
A daily-only 7-generation example is [`examples/daily.json`](examples/daily.json).
The alternative hourly=24, daily=7, weekly=4, monthly=12 is illustrative and is
**not** an approved production default. Acquisition frequency belongs to the producer.
Endpoints must have no userinfo, path (except `/`), query or fragment. Prefixes
cannot start with `/`, be empty/root, contain empty/dot path segments or surrounding
whitespace. The consumer resolves provider/inheritance/Secret configuration.

### Selection and safety

The [upstream agent at the pinned reference](https://github.com/openbao/openbao-snapshot-agent/blob/a137a863f3642999f4bb7fe629afed56bb4be3d2/kubernetes/bao-snapshot.sh)
produces `bao_YYYY-MM-DD-HHMM.snapshot`. Run the producer with `TZ=UTC`:
these names have no offset and are interpreted as UTC, never by S3 modification time.
No alternate filename parser or clock-skew grace is implemented: a recognized
snapshot later than the injected/current UTC clock is inconsistent.

Only direct children of the normalized prefix are eligible. A key containing a
further `/` is ignored, even if its basename looks like a snapshot. Directory
markers, unrelated objects and unparseable/invalid calendar names are ignored
with reasons; prefix `openbao/` never includes `openbao-other/`. Recognized
nonpositive-size or future objects and duplicate listing keys invalidate the
whole plan and prevent deletion. Snapshot contents are never read.

For each configured class, keep the newest object from each of its newest N
**distinct occupied periods**. Empty periods consume no count. UTC calendar hours,
days, Monday-start ISO week/year and calendar months determine the buckets.
Keep the union of these representatives, the minimum newest eligible objects and
the supplied successful-upload key. This is an occupied-period count, not an age
TTL or a guarantee of a fixed calendar span. A sparse or empty store never
fabricates backups or broadens cleanup. Sorting is parsed timestamp then object
key; lexicographically greatest key wins ties, independent of input order.

The full paginated list and validations finish before any DELETE. Apply requires
the exact completed key in the eligible listing and a successful HEAD with the
same positive size; that key is always kept. Missing, empty, mismatched, nested,
future or unreadable confirmation means zero deletes. This confirms integration
sequencing, **not snapshot integrity or restorability**. Use the same confirmation
key in dry-run to inspect the exact apply selection.

Apply supports only unversioned, unlocked buckets. `GetBucketVersioning` must
successfully report no versioning/MFA-delete state (Enabled and Suspended both
stop). `GetObjectLockConfiguration` must return the explicit S3 API error code
`ObjectLockConfigurationNotFoundError`, meaning no configuration. Any configured
lock, empty/ambiguous success response, AccessDenied, NotImplemented, generic 404,
or unreadable/unknown state stops apply. Dry-run can still plan while reporting
unsupported/unknown protection; this is not apply approval. Version-history
cleanup, delete-marker cleanup and WORM bypass are unsupported.

Deletes run individually, oldest first. Per-object API failure stops immediately,
reports the failed key and confirmed earlier deletions, and exits nonzero. There
is no rollback. Timeouts/SIGINT/SIGTERM propagate through SDK calls; at most three
SDK attempts per operation use standard bounded retries under the whole-run
timeout. If a response is lost after a server-side deletion, the failed object's
outcome may be uncertain; `deleted` counts only acknowledged successes. Rerun
re-lists and recalculates from current storage without local persistent state.

### Storage and credentials

Connection support includes custom HTTPS endpoint, signing region, bucket/prefix,
path-style or virtual-hosted addressing, optional session token and custom CA.
No TLS-insecure mode exists. Explicit HTTP is restricted to local test endpoints
on loopback. AWS S3, OCI Object Storage, Wasabi, Synology C2, R2, B2, DigitalOcean
Spaces and generic S3 can supply this interface; this does not establish provider
acceptance. **Only disposable MinIO is integration-tested.** A provider that cannot
prove the required protection state is dry-run-only with this initial implementation.

Credential precedence:

1. Explicit mounted file pair, with optional token file; trimmed nonempty contents
   are loaded once. Missing/empty files fail with no environment/SDK fallback.
2. Selected environment credentials: presence of any nonempty `AWS_ACCESS_KEY_ID`,
   `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` requires both key and secret. A token
   alone or a partial pair fails without probing alternative sources.
3. Otherwise the maintained AWS SDK v2 default chain (shared profiles, web identity,
   container/instance roles etc.), including its supported file locations and role
   refresh behavior. A selected provider failure is surfaced, not replaced with a
   second provider by this application. Disable metadata probing with
   `AWS_EC2_METADATA_DISABLED=true` where instance roles are unwanted.

`AWS_PROFILE`, `AWS_SHARED_CREDENTIALS_FILE`, `AWS_CONFIG_FILE` and workload identity
environment follow SDK conventions. Do not enable external SDK/HTTP debug tracing
around secrets. Application output intentionally uses stable error codes instead
of raw SDK errors and excludes credentials, headers and payloads. Ensure mounted
Secret/CA files are readable by UID 65532; do not bake them into images.

Required IAM actions, narrowed to the target bucket and prefix where supported:

| Resource | Permission | Purpose |
|---|---|---|
| Bucket | `s3:ListBucket` with `s3:prefix` condition | Full object listing |
| Successful-upload object | `s3:GetObject` | HEAD metadata (no payload download) |
| Bucket | `s3:GetBucketVersioning` | Fail-closed capability check |
| Bucket | `s3:GetBucketObjectLockConfiguration` | Fail-closed lock check |
| Eligible prefix objects | `s3:DeleteObject` | Apply only; omit for dry-run role |

Provider permission names may differ. No `ListObjectVersions`, version deletion,
PutObject, KMS, OpenBao or Kubernetes permissions are needed by this CLI.

### Operations and integration

JSON `schema_version: 1` reports mode, status, bucket/prefix, effective non-secret
policy, protection, counts, each decision and reasons, acknowledged deleted keys
and errors. Decisions are `keep`, `delete`, `ignore`, `invalid`; `delete` remains the
planned action even after successful deletion, so compare `deleted` for progress.
Counts describe the plan. `text` produces a summary plus every decision and error.

| Exit | Meaning |
|---|---|
| 0 | Read-only plan (`status=planned`) or successful apply (`completed`), including no-op |
| 1 | Storage initialization, preflight/inconsistency, unsupported apply, cancellation, output or partial deletion failure |
| 2 | CLI/configuration validation failure |

A dry-run protection notice alone can return 0 with `protection=unknown`; recognized
inconsistent snapshots still return 1. Configuration/init failures are fixed readable
stderr messages; JSON is emitted once a storage run has begun. Plans include public
object identifiers. Protect any identifiers your organization considers sensitive.

Require immutable unique object keys, one producer/retainer sequence per cluster/prefix
and one retention runner at a time. Concurrent arbitrary writers/retainers and hostile
object replacement are outside the contract. The CLI has no remote lock. A later
catalog supplies `concurrencyPolicy: Forbid`, disables the upstream `S3_EXPIRE_DAYS`
cleanup, runs only after successful snapshot upload, and hands over that exact key.
Do not rely on independently scheduled backup and retention CronJobs to prove success.

[`examples/cronjob.yaml`](examples/cronjob.yaml) is illustrative **and suspended**;
its literal key must be replaced by an on-success orchestration step, with resolved
S3 configuration and Secret injection. It is not a production deployment or catalog.
The same image can be a final step in the producer Job. The CLI exits once and has
no HTTP listener, daemon, CRD or controller.

Download a retained `.snapshot` using your storage tooling and pass the raw file to
`bao operator raft snapshot restore` in the appropriate recovery procedure; there
is no proprietary repository or extra recovery password introduced here. Restore
authorization, validation and KMS keys belong to that procedure. GFS cannot preserve
restorability after a separate KMS workflow deliberately retires old key generations.
This program does not infer KMS versions, mutate keys or provide a destructive purge.
OpenBao initialization, ESO, SOPS/age originals, provider inheritance/resolvers and
changes to `oci-talos-foundation`/`kubara-catalogs` are outside this repository.

### Build, test and release

```sh
make check test                 # format check, vet, unit, race
make build VERSION=v0.1.0       # Linux host binary in dist/
make image VERSION=v0.1.0       # non-root trixie container
python3 -m venv .tmp/e2e-venv
.tmp/e2e-venv/bin/pip install -r test/e2e/requirements.txt
.tmp/e2e-venv/bin/python test/e2e/scripts/run.py
make e2e-compose-down           # cleanup a retained environment
```

The container entrypoint is `raft-snapshot-retainer`; arguments/env are identical
to the binary. It runs as UID/GID 65532, supports read-only filesystem and dropped
capabilities, and includes CA certificates. Both build and runtime bases are trixie,
pinned by digest. BuildKit caches Go modules and build artifacts. Cross-build with:

```sh
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -o dist/raft-snapshot-retainer-$arch ./cmd/raft-snapshot-retainer
done
docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=v0.1.0 .
```

The integration runner requires Linux Docker/Compose, Python 3.11+ and internet
for pinned dependencies/assets. It builds a test-only MinIO image from upstream's
checksum-pinned release binary (amd64), exposes a random loopback port, generates
ephemeral credentials, and invokes the shipped non-root CLI container with host
networking. Tests use unique disposable buckets and synthetic objects; no production
endpoint/credential is accepted by the runner. State is mode 0600 and ignored.
Command environment overrides `E2E_ENV_FILE` (default `test/e2e/.env`), then defaults.
`E2E_PORT`, `E2E_IMAGE`, `E2E_KEEP=true` or `E2E_KEEP_ON_FAILURE=true` aid debugging;
results go to `test/e2e/results/`; cleanup runs by default. `make e2e-compose` uses
your Python interpreter, so activate the venv first or use its Python explicitly.

The `Test` workflow verifies formatting/vet/unit/race, amd64/arm64 cross-builds,
release helpers and amd64 MinIO/container integration. `ACTIONS_TEST_RUNNER` selects
an **amd64 Linux** runner with Docker/Compose; unset falls back to `ubuntu-latest`.
All JavaScript Actions use Node 24 runtimes. No Node application/plugin is added.

The `Release` workflow runs after merged `feature/*` PRs targeting `main`, computes
semver from individual Conventional Commit messages (never PR titles), initially
`v0.1.0`, and publishes GitHub Release tarballs/checksums plus
`ghcr.io/michibiki-io/raft-snapshot-retainer:0.1.0` (image tags omit `v`). It uses
`GITHUB_TOKEN` for GitHub/GHCR, with repository Actions package-write permission.
`ACTIONS_RUNNER` defaults to `ubuntu-latest`; absent `ACTIONS_RUNNER_ARM64` uses QEMU
for both platforms, otherwise native amd64/arm64 jobs create a shared manifest.
Release assets `release-images.json` and `approved-runtime-images.json` record the
published digest and carry forward prior approved pins without repository commits.
Binary/image `--version` is the release tag. Publication is not exercised by a PR;
local image verification does not claim a registry release. Pin consumers to the
published digest after their acceptance tests.

## 日本語

### 実行例

S3 互換ストレージにある raw snapshot に GFS 保持を適用する一回実行の CLI です。
上記の Quick start と `examples/daily.json` が日次 7 世代の実行例です。
通常は dry-run となり、削除には `--apply` と成功アップロードの正確なキーが必要です。
`--help` と `--version` は S3 に接続しません。

### 設定

設定は厳密な JSON、`RSR_*` 環境変数、成功キーの CLI オプションの順で上書きします。
全項目は英語の Configuration 表を参照してください。未知の項目、空・root prefix、
負の保持数、全 GFS クラスが 0、非正の最低保持数、不正な endpoint・timeout は拒否します。
毎時 24・日次 7・週次 4・月次 12 は設定例であり、本番の既定値ではありません。

### 保持と削除の安全条件

producer を `TZ=UTC` にし、`bao_YYYY-MM-DD-HHMM.snapshot` を UTC として解釈します。
prefix 直下だけを扱い、ネストしたキー、無関係な名前、不正な日付、ディレクトリは理由付きで無視します。
認識した snapshot が 0 バイト・負のサイズ・未来日時、または一覧に重複キーがある場合は削除を停止します。
時刻の猶予はありません。S3 の更新日時や snapshot の内容は判定に使いません。

各クラスの新しい占有期間から代表を選び、最低保持数と成功キーを加えた和集合を保持します。
週は ISO 週、月は暦月です。欠落期間は数えず、固定日数の保持を保証する TTL ではありません。
全ページ取得と検証を終え、成功キーの存在・対象範囲・正のサイズを HEAD で確認してから、古い順に削除します。
この確認はアップロード連携の確認であり、snapshot の完全性を証明しません。

apply は versioning と Object Lock がないバケットだけを対象とします。
保護状態が不明、Enabled/Suspended、Object Lock 設定ありの場合は停止します。
Object Lock 未設定として受理する応答は `ObjectLockConfigurationNotFoundError` だけです。
dry-run は保護状態の注意を表示して計画を出せますが、apply 可否の承認を意味しません。
削除失敗時は停止し、成功済みの件数と失敗キーを返します。rollback はなく、再実行時は再取得・再計算します。

### 接続と認証

endpoint・region・bucket・prefix は consumer が解決した値を渡します。provider resolver は実装しません。
HTTPS、path-style/virtual-hosted、SDK 認証、マウントした認証ファイル、session token、独自 CA に対応します。
TLS 検証は無効化できず、HTTP は明示指定した loopback のローカルテストだけに限ります。
認証の優先順はマウントファイル、完全な環境変数の組、SDK 既定 chain です。
選択した認証が不完全・失敗なら別の認証へ切り替えません。値を引数・ログ・image に含めないでください。
必要な IAM 権限は英語の表を参照してください。実測済み provider は使い捨て MinIO のみです。
Wasabi などの実サービス、version 履歴削除、WORM 回避は検証・対応していません。

### 運用と連携

JSON は policy、対象、各判断と理由、保護状態、削除済みキー、失敗を返します。
終了コードは成功・no-op が 0、I/O・preflight・非対応 apply・部分失敗が 1、設定不正が 2 です。
不整合 snapshot は dry-run でも 1 です。初期化・設定失敗は stderr に固定メッセージを出します。

immutable な一意のキーと prefix ごとに一つの実行者を前提とします。
後続 catalog が公式 agent の `S3_EXPIRE_DAYS` を無効化し、アップロード成功後だけ実行して正確なキーを渡します。
CronJob の重複防止と成功時の順序制御は consumer の責務です。サンプルは suspended の説明用です。
本 repository から cluster に deploy したり、隣接 consumer の正本を変更したりしません。
復元は raw `.snapshot` を OpenBao の標準手順へ渡します。KMS の旧鍵廃止後の復元可能性は保証せず、
KMS 操作、snapshot 作成・復元、OpenBao/ESO 初期化、SOPS/age 管理は実装範囲外です。

### ビルド・テスト・リリース

上記の再現コマンドで format/vet/unit/race、コンテナ、MinIO 結合テストを検証できます。
Go 1.27.4 と依存を固定し、build/runtime とも trixie、UID/GID 65532 で実行します。
Linux amd64/arm64 のバイナリ・image をリリース対象とし、PR の実行検証は amd64 です。
テスト runner は `ACTIONS_TEST_RUNNER`、未設定なら `ubuntu-latest` です。Actions は Node 24 を使います。
feature PR のマージ後に、個別 commit メッセージから semver と release notes を作成します。
今回の PR で merge・公開・deploy は実施しません。受け入れ根拠と未検証範囲は `docs/acceptance.md` を参照してください。
