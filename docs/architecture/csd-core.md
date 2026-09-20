# CSD core contract

Status: initial v1 protocol for CSD-01. Issue #2 defines this implementation scope; PRD rules arrive in #4.

## Authority and ownership

`Document` stores a private snapshot. NewDocument, FromSnapshot, AddNode, UpdateNode and Link validate and defensively copy all maps/slices. Successful operations return a new document and increment revision; failed operations return an error without changing the source. Even a no-op successful update increments revision. Snapshots returned to callers are detached.

`FromSnapshot` and `Load` import data, not approval. A supplied revision is metadata, not a CAS guarantee. Concurrent derivations are safe, but selecting a shared current head requires the store/command layer in #3. The CLI creates fresh output files exclusively; it is not a transaction journal or atomic multi-file store.

## Wire schema

Required root fields: `schema`, `id`, `type`, `title`, `revision`, `nodes`, `edges`. `presentation` is optional on import and explicit after serialization.

- `schema`: exactly `csd/v1`.
- IDs: `[A-Za-z][A-Za-z0-9._:-]{0,127}`; unique node IDs within one document.
- Types and top-level field keys: `[a-z][a-z0-9_]{0,63}`.
- Titles: nonempty, non-whitespace UTF-8 strings; stored verbatim.
- Revision: positive signed 64-bit integer; increment overflow rejected.
- Node: exactly `id`, `type`, `title`, `fields`; fields is an object of immutable Values.
- Edge: exactly `from`, `type`, `to`; endpoints must exist; duplicate tuples rejected.
- General graphs allow cycles and self-links. Domain-specific edge types, directions, multiplicities and DAG rules belong in the PRD schema.
- Presentation: only `node_order` and `collapsed`, arrays of unique existing node IDs. They are non-normative display state. Required workflow order MUST be an ordered semantic field, not presentation order.

Strict import rejects unknown/case-aliased keys, duplicate keys (including escaped aliases), invalid UTF-8, unpaired UTF-16 surrogate escapes, unsupported schema, trailing JSON values, missing fields, null object/array containers and invalid references. Unknown user-defined fields inside `fields` are allowed by the generic core, but their values still follow the Value protocol. PRD applicability and completeness are not checked here.

Maximum JSON input/canonical output: 4 MiB; JSON nesting: 64; nodes: 10,000; edges: 50,000. These are safety limits, not throughput/SLO claims.

## Value protocol

Values support null, booleans, UTF-8 strings, decimal int64 integers, ordered arrays and objects recursively. Values cannot be initialized by setting an exported raw field. An uninitialized Value is invalid. Numbers never pass through float64; fractions, exponents and out-of-range integers are rejected rather than rounded. `-0` normalizes to `0`.

Decimal prices should be domain-defined decimal strings or integer minor units; this core does not choose business currency/rounding rules. Money rules remain a product decision.

Object keys are sorted by Go `encoding/json` serialization; arrays preserve order. Strings retain their exact decoded content; no whitespace, Unicode normalization, paraphrase or synonym equivalence is inferred. Unicode escapes and literal characters for the same decoded string normalize together.

## Serialization and semantic hashing

`Serialize()` produces compact UTF-8 JSON without a trailing newline. Node arrays sort by ID. Edges sort lexicographically by `(from, type, to)` using a NUL-separated key; IDs/types cannot contain NUL. Empty maps/arrays normalize to `{}`/`[]`; collapsed IDs sort, while display order retains its order. CLI adds one newline for terminal use.

`SemanticBytes()` encodes the following exact object member order:

`schema, hash_version, id, type, title, nodes, edges`

`hash_version` is `csd-semantic/v1`. Node member order is `id,type,title,fields`; edge member order is `from,type,to`. Field values use the same canonical JSON described above. Default Go JSON string escaping, including HTML escaping and U+2028/U+2029 escaping, is part of v1.

`SemanticHash()` returns `csd-semantic/v1:sha256:<lowercase hex SHA-256 of SemanticBytes()>`.

Included: document identity/type/title/schema, hash protocol, node ID/type/title/all fields, all edges. Excluded: only revision and the two explicitly typed presentation lists. Unknown root metadata cannot be silently excluded because it is rejected.

This is a project-specific deterministic protocol, **not RFC 8785/JCS**, a natural-language equivalence detector, an integrity signature or proof that a PRD is complete. Rephrasing or whitespace inside business text conservatively changes the digest. Store formatting and array insertion order for nodes/edges do not.

The golden test pins exact bytes and an independently calculated Python hashlib digest. Protocol upgrades must preserve old fixtures and be explicitly versioned. The deployed production toolchain must be separately selected and verified; this implementation is tested with the toolchain available to the execution environment.

## Semantic diff

Diff uses the same semantic projection as hash. Comparison requires the same document ID/type/schema. It reports document title changes; node additions/removals; node type/title/field replacements, additions and removals; edge tuple additions/removals. Paths use node IDs and escaped JSON Pointer segments, not storage-array indices.

A missing field and an existing `null` value are different. A nested field value is reported as a whole field replacement, not a sub-value patch. Changes sort by path, independent of map iteration. Revision/presentation-only changes produce no semantic changes; full serialized snapshots still preserve them. Different node types across two imported snapshots are visible in diff, even though UpdateNode cannot mutate identity/type.

The core computes no downstream impact closure, does not determine text-equivalence and does not validate natural-language claims.

## References

Implementation choices are checked against the official Go documentation: [encoding/json](https://pkg.go.dev/encoding/json), including legacy decoder behavior, and [Organizing a Go module](https://go.dev/doc/modules/layout). This implementation uses the Go 1.23-compatible API with additional strict decoding rather than depending on a newer JSON implementation.
