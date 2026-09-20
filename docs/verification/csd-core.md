# CSD-01 verification record

Executed 2026-09-20 in the cloud execution container, using Go 1.23.2 on Linux/amd64. This is an implementation verification record, not independent Human approval, signed runner attestation or GitHub Actions evidence.

## Executed checks

| Check | Actual result |
| --- | --- |
| `go test ./...` | PASS: core and CLI packages |
| `go test -race ./...` | PASS: both packages |
| `go vet ./...` | PASS |
| `go test ./internal/csd -run Test -count=20` | PASS |
| `go test ./internal/csd -run '^$' -fuzz=FuzzLoadRoundTrip -fuzztime=5s -parallel=2` | PASS; 114,571 executions, 48 new interesting inputs in the final run (58 cached baseline inputs) |
| `go test ./... -cover` | core 87.7%; CLI 93.3% statement coverage |
| `gofmt -l internal/csd cmd/prd` | no files reported |
| `go build -o /mnt/data/prd ./cmd/prd` | PASS on Linux/amd64 |
| `GOOS=windows GOARCH=amd64 go build -o /mnt/data/prd-windows.exe ./cmd/prd` | cross-compilation PASS; Windows execution NOT performed |
| External CLI smoke flow in an empty directory | all nine capabilities executed successfully |
| Separate Python canonicalization + SHA-256 calculation | matched the Go digest for the Chinese sample |

The sample proceeds through create, two adds, link and update to revision 5. `validate` returned `VALID`; `serialize` and `load` outputs were byte-identical. The semantic diff between revisions 4 and 5 contained exactly one replacement at `/nodes/REQ-001/fields/behavior`.

Sample revision 5 digest:

`csd-semantic/v1:sha256:5aa98e39ad18731df8e722d91406c711f206b5d5a9332ef426da60a17079fedc`

Pinned ASCII golden digest:

`csd-semantic/v1:sha256:f5dc754a61889869033d1691a336deba139b9c217ed741cf161bd5a1b9484c18`

## Negative and invariant coverage

Duplicate IDs/keys/escaped keys, unknown and case-aliased fields, missing fields, unsupported schema, dangling/duplicate edges, invalid field/type/title, uninitialized typed values, invalid revisions, overflow, patch identity mutation, conflicting patch operations, failed-operation atomicity, defensive copies, null versus missing fields, UTF-8 and surrogate errors, out-of-range/fractional numbers, depth/size bounds, same-source presentation invariance, ordered-value changes, field/node/edge diff, output-file overwrite protection and broken writers are covered.

The core supports graph cycles; relation-specific cycle prohibition is deferred to domain schema. Statement coverage is not a completeness guarantee. Fuzzing covers only the declared bounded input protocol and this finite run.

## Pre-commit regression found and fixed

A targeted boundary test exposed that a standalone Value within the 64-level nesting limit could exceed that limit once wrapped by document/node/fields containers. The old AddNode accepted it, violating reloadability. The regression was observed failing before the fix. Validate now checks the entire serialized document against the same bounded decoder before accepting a snapshot. The regression covers accepted embedded depth 60, rejected depths 61/64 and failure atomicity. All checks above were rerun after the fix.

## Source binding and delivery

The delivery PR and issue #2 record the final Git commit and tree identity. The published tree must be compared to the exact locally tested file tree before reporting it as verified. No production CI or repository protection is installed by this task. GitHub is accessed through the connected API; the execution container cannot directly resolve github.com for git clone.

## Not verified or not implemented

No independent Human review, production authorization, concurrent file-store CAS, signed evidence, hidden acceptance corpus, PRD business completeness, LLM provider integration, Web UI, external E2E system or real Windows runtime test. The primitive `VALID` result must not be presented as a PRD Ready/Approved verdict.
