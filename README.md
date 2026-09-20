# PrdHarness

基于 Canonical Semantic Document（CSD）的 PRD Harness，使用 Go 构建。

## 当前交付

本轮仅实现 [CSD-01 / #2](https://github.com/hvritual/PrdHarness/issues/2) 的语义文档内核与 CLI。完整路线见 [总 Issue #1](https://github.com/hvritual/PrdHarness/issues/1)：19 个实施任务，分 MVP、Full Runtime、Production。Issues 是路线状态入口，本文件不是第二份路线。

已实现：create document、add node、update node、link node、validate、serialize、load、semantic hash、semantic diff。

**尚未实现**：PRD 领域完备性 Gate、持久化 CAS、用户权限、Human 审批、签名 Evidence、LLM、Web UI、业务 E2E。`validate` 只表示 CSD 结构有效，不表示 PRD 已准备好开发或已通过审核。

## 运行

模块最低语言版本 Go 1.23，无第三方依赖。本轮实际测试环境和限制见验证报告；最低编译版本不是当前生产工具链推荐。

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o prd ./cmd/prd
```

Linux/macOS 使用 `./prd`，Windows 编译为 `prd.exe` 后使用 `./prd.exe`。下列命令每次创建新快照，`--out` 永不覆盖现有文件；重复演示请使用新目录/文件名。

```sh
./prd create --id PRD-001 --type prd --title "套餐升级" --out v1.json
./prd add-node --file v1.json --node examples/requirement.json --out v2.json
./prd add-node --file v2.json --node examples/goal.json --out v3.json
./prd link-node --file v3.json --from REQ-001 --type serves --to GOAL-001 --out v4.json
./prd update-node --file v4.json --id REQ-001 --patch examples/update.json --out v5.json
./prd validate --file v5.json
./prd serialize --file v5.json
./prd load --file v5.json
./prd semantic-hash --file v5.json
./prd semantic-diff --before v4.json --after v5.json
```

无 `--out` 时输出 JSON 到 stdout。**不要使用 `> input.json` 覆盖输入文件**，shell 会在进程读取前截断文件。

退出码：`0` 操作成功（非空 diff 也为成功），`1` 输入/操作失败，`2` 命令用法错误。错误以带 `code/path/message` 的 JSON 输出 stderr。

## 边界

- `internal/csd`：不可变快照、类型与引用校验、严格 JSON、确定性序列化与摘要、字段级 diff。
- `cmd/prd`：文件读取和命令适配。它不是共享文档存储，也不负责审批或身份认证。
- `examples`：虚构演示输入，不是已批准业务规则。
- [CSD 协议](docs/architecture/csd-core.md)：hash 字段、数字类型、规范化和安全边界。
- [验证记录](docs/verification/csd-core.md)：实际执行的测试与证据限制。

一个 CSD 是一份文档快照。`v1.json`、`v2.json` 等是历史/候选版本，不是可独立维护的 Human PRD / AI PRD；持久化 current-head 与 CAS 在 #3 实现。

Semantic hash 只比较协议明确规定的内容，不证明自然语言含义等价，不证明来源可信，更不是签名。当前 JSON 文件仍可以被操作者修改后加载；生产单一写入权威与隔离属于后续任务。
