# CSD controlled store protocol

Status: CSD-02 local file-store protocol. This document does not define PRD domain completeness or approval.

## 1. Authority boundary

`internal/csd` remains an immutable semantic value model. It does not know files, actors, commands or approvals. A persisted canonical document can change only through:

```text
Semantic Command
      ↓
command/schema validation
      ↓
per-document transaction lock
      ↓
idempotency check
      ↓
expected-revision CAS
      ↓
apply operations in memory
      ↓
CSD Validate + semantic hash
      ↓
current document + receipt envelope
      ↓
same-directory atomic replacement
```

Detached CSD CLI commands create files outside this store and are not authoritative writes.

## 2. Command protocol

Protocol: `semantic-command/v1`.

Required command fields:

- stable `id` used as the idempotency key;
- `document_id`;
- `expected_revision` (`0` only when no current document exists);
- actor `class` and `subject`;
- ordered operations, maximum 100.

Supported operations:

- `create_document`;
- `add_node`;
- `update_node`;
- `link` exact edge;
- `unlink` exact edge;
- `deprecate_node`.

There is no persisted physical-delete operation in CSD-02. `deprecate_node` sets the framework-reserved normative field `csd_deprecated=true`; therefore deprecation changes semantic hash and remains visible to later domain policies.

Commands that finish with the same semantic hash as the current document are rejected as `NO_SEMANTIC_CHANGE`. Intermediate mutations never escape if a later operation fails.

## 3. CAS and idempotency order

Inside the document lock:

1. Load and validate the current state envelope.
2. Search receipts by command ID.
3. If the ID already exists and the command hash is equal, return a replay without a write, even if later commands advanced the current revision.
4. If the ID exists with a different command hash, reject `IDEMPOTENCY_CONFLICT`.
5. For a new ID, compare `expected_revision` with current revision and reject `REVISION_CONFLICT` on mismatch.
6. Apply all operations to immutable CSD values.
7. Validate final CSD and reject a semantic no-op.
8. Append one receipt and atomically replace one envelope.

This order makes retries safe after an ambiguous client/server failure while preventing a stale new command from overwriting a newer document.

## 4. Receipt and provenance

Protocol: `semantic-receipt/v1`.

A receipt binds:

- command ID and deterministic command hash;
- document ID;
- base/result revisions;
- before/after semantic hashes;
- operation count;
- actor class/subject;
- recorded timestamp.

CSD-02 has no authentication provider. `identity_assurance` is therefore always `caller_asserted_local`. A caller writing `class=human` does **not** prove a Human reviewed or approved anything. Signed identity and trusted evidence are later production concerns.

Receipts are minimal transaction provenance, not full event sourcing. Exact operation payload is represented by its command hash, not retained as a second current truth.

## 5. Atomic persistence

Current CSD and receipts are serialized into **one** `prd-filestore/v1` envelope. This avoids a crash window where the CSD advances but its idempotency receipt does not, or vice versa.

State file addressing is:

```text
documents/sha256(document-id).json
```

Raw document IDs never become path components.

Commit sequence:

1. Create a unique temporary file inside `documents/`.
2. Set owner-only permissions where supported.
3. Write all bytes and `Sync()` the temporary file.
4. Reject a target that is a symbolic link or non-regular file.
5. Atomically replace the target in the same directory.
6. Sync the directory on Unix. Windows uses `MoveFileExW` with `REPLACE_EXISTING|WRITE_THROUGH`.

If replacement fails, the old envelope remains current. If replacement succeeds but the following durability sync reports failure, the caller receives an error with an **ambiguous commit**; retrying the exact same command ID/contents reconciles safely via the stored receipt.

## 6. Cross-process lock

Per-document locks use atomic directory creation:

```text
locks/sha256(document-id).lock/
  owner.json
```

`mkdir` is the mutual-exclusion primitive and therefore works across different processes sharing the file system. `owner.json` contains a random ownership token, PID and timestamp so a writer does not remove a lock whose ownership changed.

Residual locks are fail-closed. CSD-02 deliberately does **not** infer that a PID/timestamp proves a lock is dead, because PID reuse, namespaces and remote/shared file systems make automatic reclamation unsafe. CLI `apply` has a bounded lock wait; operational recovery must first establish that no writer is active.

## 7. Store validation

Every read validates:

- envelope protocol and JSON shape;
- duplicate/unknown JSON fields;
- CSD strict loader and document identity;
- receipt uniqueness;
- monotonic revision chain;
- operation count equals revision delta;
- before/after semantic-hash chain;
- receipt head exactly binds the current CSD revision/hash.

This detects many accidental/casual corruptions. It is **not** tamper-proof: an attacker with write permission to the same files can rewrite both document and receipts. Cryptographic signing and independent evidence are not part of CSD-02.

## 8. File-system threat boundary

Implemented defenses include hashed file names, rejection of store/state symlinks, regular-file checks, same-directory temporary files and lock ownership tokens. They reduce path traversal and common symlink mistakes.

They do not create a hostile multi-user sandbox. A malicious principal with equivalent OS permissions can race directory replacements or alter local state. Production isolation belongs to the later trust-boundary tasks.

## 9. Limits

- semantic command: at most 4 MiB input;
- operations per command: 1..100;
- state envelope: at most 16 MiB;
- receipts per document: at most 100,000;
- CSD limits continue to apply unchanged.

Receipt retention/compaction is intentionally deferred until an audited migration protocol exists; silent receipt deletion would weaken idempotency history.
