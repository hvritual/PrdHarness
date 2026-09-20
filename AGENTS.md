# Repository working rules

- Read issue #1 and the selected implementation issue before changing code.
- One implementation PR has one independently testable scope. No phase names in packages, types or functions.
- CSD is the semantic source; generated Markdown/context/indices are read-only projections.
- Keep `internal/csd` free of IO, LLM providers, product rules and approval authority.
- Persisted canonical documents may be changed only by `filestore.Apply(SemanticCommand)`, which runs the pure `internal/operations` engine inside the store lock; no arbitrary state-commit callback is exposed. CLI `apply` is the canonical file-store write path. Detached snapshot utilities never mutate a store.
- Every persisted mutation binds a command ID, expected revision and actor context. Idempotency is checked before CAS; a reused command ID with different content is a hard conflict.
- Current document and command receipt must commit in one atomic state envelope. Do not split them into independently committed files.
- File locks fail closed. Never auto-delete a residual lock unless a later recovery protocol can prove ownership is dead.
- Never change golden expectations merely to make tests pass. Hash protocol changes need an explicit version and review.
- All source and test data must be UTF-8. No dependencies without a concrete need.
- Never claim local actor labels are authenticated identities. CSD-02 receipts explicitly use `caller_asserted_local` assurance.
- Never claim local test evidence is independent review, signed provenance or GitHub CI.
- Do not modify `server/**`, other repositories or any production resource.
- Never store credentials, hidden acceptance corpora, user private data or generated binaries here.
- Run `go test ./...`, `go test -race ./...`, `go vet ./...`, formatting checks, CLI smoke tests and relevant negative tests.
- Preserve existing failing tests and document unresolved limitations. Do not auto-approve your own work.
