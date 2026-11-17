# Acceptance evidence / 受け入れ根拠

## Contents / 目次

- [English](#english)
- [日本語](#日本語)

## English

Issue [#1](https://github.com/michibiki-io/raft-snapshot-retainer/issues/1) defines R1–R13.
This map identifies reproducible checks; CI and review links for the final head are
recorded on the PR, rather than asserting that an earlier source revision passed.

| Criterion | Implementation and evidence |
|---|---|
| R1 | `internal/config`, `internal/cli`; strict JSON/env validation, help/version/exit tests |
| R2 | `internal/retainer/plan.go`; pure injected-clock tests for UTC hours/days, ISO week/year, months/year, gaps, union, floor, pin and order independence; key tie-break is explicit (the fixed upstream layout has only one distinct key per minute) |
| R3 | Anchored parser and prefix boundary; malformed calendar/name, nested, collision, directory, zero/future and duplicate tests |
| R4 | AWS SDK v2 S3 adapter; paginated HTTP tests, local custom endpoint/path-style MinIO, mounted/environment credentials, untrusted TLS rejection and trusted custom CA test |
| R5 | `Run` read-only preflight and HEAD/pin verification; fake/HTTP/MinIO missing, nested, empty, future, mismatched and unreadable confirmation cases with zero deletes |
| R6 | Dry-run read-only request test; oldest-first delete, per-object failure, partial progress and cancellation tests; MinIO rerun/no-op |
| R7 | SDK versioning/Object Lock checks; Enabled, Suspended, unknown enum, configured/empty/denied/unsupported lock response tests; only explicit not-configured code accepted; DELETE header tests distinguish empty/null IDs from real IDs/delete markers |
| R8 | Versioned JSON and text output, plan counts/reasons and fixed non-secret failure codes; command exit/secret suppression, file/env/flag precedence and JSON/text success/error tests; documented statuses |
| R9 | `go test -race ./...`, fake clock/storage and hermetic HTTP/TLS endpoints; no production secrets |
| R10 | `test/e2e`: checksum-pinned MinIO container, actual shipped CLI image, 8 pytest cases, isolated disposable buckets and generated credentials |
| R11 | Pinned Go 1.27.4/dependencies, trixie build/runtime, UID 65532, Test workflow, amd64/arm64 cross-build, multi-arch release workflow and binary assets |
| R12 | README configuration/env/JSON, GFS examples, raw restore, permissions, credential chain, sequencing, naming, limits and suspended CronJob example |
| R13 | README reproduction commands and CLI/image/release interface; this evidence map; no consumer resolver, KMS mutation/purge, OpenBao client or consumer changes |

Local verification during initial implementation:

- Passed: format, `go vet ./...`, unit/race tests, Linux amd64 and arm64 builds,
  release helper unit tests and Python syntax checks, workflow actionlint.
- Passed: MinIO container integration, 8 tests. Actual non-root trixie application
  ran with a read-only root filesystem, dropped capabilities and no-new-privileges.
  The first attempt could not pull upstream registry images; the final harness
  builds a test-only image from the upstream checksum-pinned release binary.
- Unrun: AWS/Wasabi/OCI/R2/B2/C2/Spaces or other live-provider acceptance, arm64
  native runtime, release/GHCR publication and a real OpenBao snapshot restore.
  No production bucket, Kubernetes cluster or neighboring consumer was modified.

Claude round-1 follow-up used focused tests for trailing empty-prefix rejection,
file/env/flag precedence, CLI JSON/text outcomes and DELETE version headers, plus
non-mutating format/vet and workflow actionlint. No full local e2e rerun was needed
for this fix round; exact-head CI owns the complete suite.

The final PR head must pass the `Test` workflow and the independent review loop
before completion. A PR does not execute merged-PR release publication. Operators
must verify their provider's protection-state responses and scoped IAM policy;
unknown capability is unsupported for apply, not an implied successful integration.

## 日本語

R1〜R13 と実装、検証の対応は上表のとおりです。
最終 head の CI とレビューの根拠は PR に記録します。

ローカルでは format、vet、unit/race、amd64/arm64 ビルド、Python 構文、release helper、
workflow の actionlint、MinIO 結合テスト 8 件が通過しました。
結合テストでは配布する CLI コンテナを非 root、read-only filesystem で実行しています。
MinIO のレジストリ取得失敗を受け、上流 Release の checksum 固定バイナリを使うテスト用 image に変更しました。

実 provider、arm64 の native 実行、release/GHCR 公開、実 snapshot の OpenBao 復元は未実施です。
本番 snapshot の削除、cluster への deploy、隣接 consumer の変更は行っていません。
マージ後の公開 workflow は PR 検証とは別であり、公開済みとは報告しません。
