# CSD-02 verification record

Date: 2026-09-20  
Environment: Go 1.23.2, Linux 6.18.44 x86_64  
Scope: issue #3 only. Local/cloud-container execution is not independent Human review, signed provenance or GitHub Actions evidence.

## Verified behavior

CSD-02 implements and directly exercises:

- command validation and deterministic command hash;
- atomic multi-operation semantic transition;
- current-revision CAS;
- command-ID idempotent replay;
- same command ID + different content rejection;
- create/add/update/link/unlink/deprecate operations;
- no persisted physical delete path;
- current CSD + receipt single-envelope commit;
- atomic replace failure preserving the previous envelope;
- post-replace durability error reconciled by same-command replay;
- two store instances racing on one expected revision: one commit, one conflict;
- separate OS processes racing on one expected revision: one commit, one conflict;
- residual lock timeout without automatic lock theft;
- path hashing and symlink rejection;
- strict/corrupt store rejection;
- existing CSD-01 hash, diff, strict-load and immutability regression tests.

## Commands executed

```text
go test ./...
PASS


go test -race ./...
PASS


go test -race ./internal/operations/... ./internal/filestore/...
PASS


go vet ./...
PASS


go test ./internal/filestore -run '^TestCrossProcessCAS$' -count=20
PASS


go test ./internal/operations ./internal/filestore -count=10
PASS
```

Coverage run (coverage is diagnostic, not an acceptance proof):

```text
go test -cover ./internal/csd ./internal/operations ./internal/filestore ./cmd/prd

internal/csd        87.3%
internal/operations 72.1%
internal/filestore  72.1%
cmd/prd             88.6%
```

Fuzz runs:

```text
go test ./internal/operations -run '^$' -fuzz=FuzzDecodeCommand -fuzztime=5s -parallel=2
PASS; 120,812 executions; 65 new interesting inputs (67 total at completion)


go test ./internal/csd -run '^$' -fuzz=FuzzLoadRoundTrip -fuzztime=5s -parallel=2
PASS; 123,486 executions; 31 new interesting inputs (137 total at completion)
```

Build checks:

```text
go build -o /tmp/prd ./cmd/prd
PASS

GOOS=windows GOARCH=amd64 go build -o /tmp/prd.exe ./cmd/prd
PASS

GOOS=windows GOARCH=amd64 go test -c ./internal/filestore -o /tmp/filestore.test.exe
PASS
```

The Windows checks are cross-compilation only. CSD-02 was not executed on a real Windows file system in this verification run.

## CLI smoke run

A clean store was created from `examples/commands/create.json`, the exact same command was replayed, then `examples/commands/update.json` was applied and current state read back.

Observed transaction chain:

```text
CMD-001: base revision 0 -> result revision 2
CMD-001 replay: replayed=true, no extra receipt
CMD-002: base revision 2 -> result revision 3
current revision: 3
receipt count: 2
identity assurance: caller_asserted_local for both receipts
```

The state file was addressed by:

```text
64de3e858953035c3552ef9a57d367282e14dfbf3280623901881d18566330a8.json
```

which is derived from the document ID rather than using `PRD-001` as a path component.

The demo state SHA-256 after the two commits was:

```text
26a1357b0bcb94c358e95e8d11d1b7dcda94c446108eda9bcf23287ded1488ae
```

This demo digest is environment/run evidence only; receipt timestamps mean the full store-envelope digest is not a protocol golden value.

## Failure semantics explicitly tested

### Batch failure

A two-operation command where operation 1 was valid and operation 2 attempted to unlink a missing edge returned an error. Current revision, semantic hash and receipt count remained unchanged.

### Replace failure

The atomic-replace primitive was injected to fail before rename. The previous state bytes remained unchanged and the temporary file was cleaned up.

### Ambiguous durability failure

Directory sync was injected to fail **after** atomic rename. The caller received `DIRECTORY_SYNC_FAILED`; retrying the same command ID/content returned an idempotent replay with one receipt. This documents that callers must retry an ambiguous commit rather than assume an error means “not committed”.

### CAS contention

Two commands with different IDs and the same expected revision were submitted concurrently. Exactly one committed; the other returned `REVISION_CONFLICT`. This was tested both with distinct Store instances and with separate OS processes.

### Residual lock

A pre-existing lock directory caused bounded wait and `LOCK_TIMEOUT`. The lock was not automatically removed and no document was written.

### Path/symlink

Document IDs are SHA-256 addressed before entering paths. A symlink store root and a symlink state file were rejected; the symlink target was not modified. These checks reduce common path attacks but do not constitute hostile same-user filesystem isolation.

## Trust and unverified areas

- `actor.class` / `actor.subject` are caller assertions. There is no OAuth/OIDC/RBAC in CSD-02; receipts label this `caller_asserted_local`.
- Store envelopes are not cryptographically signed. A principal with equivalent OS write permission can rewrite local files and receipts.
- The lock protocol deliberately does not auto-reclaim stale locks; recovery automation remains future work because PID/time alone cannot safely establish liveness across namespaces/shared filesystems.
- No database, distributed lock, network filesystem guarantee, CRDT or multi-host consistency is claimed.
- Windows atomic replacement code cross-compiles but has not been exercised on a real Windows volume in this run.
- No PRD domain Ready/approval semantics are implemented. A successful CSD-02 command is not product approval.
- This verification is performed by the implementation agent. It must not be presented as independent review.
