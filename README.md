# PrdHarness

基于 Canonical Semantic Document（CSD）的 PRD Harness，使用 Go 构建。

## 当前交付

完整路线见 [总 Issue #1](https://github.com/hvritual/PrdHarness/issues/1)。CSD-01 提供不可变语义文档内核；本分支实现 [CSD-02 / #3](https://github.com/hvritual/PrdHarness/issues/3) 的受控语义命令、文件存储与 CAS。CSD-02 依赖 CSD-01，因此在 #2 未合并期间使用 stacked branch/PR，不直接把依赖绕进 `main`。

CSD-02 增加：

- `Semantic Command -> Validate -> CAS -> Atomic Commit` 唯一 canonical file-store 写链。
- command ID 幂等、防止同 ID 不同载荷重放。
- expected revision 乐观并发控制；跨进程目录锁串行化每个 Document ID 的提交窗口。
- current CSD + operation receipt 单 envelope 原子提交，避免两份状态半提交。
- create document / add node / update node / link / unlink / deprecate node 批次操作；批次失败零持久化。
- 同目录临时文件、flush、原子 replace；Unix 目录 fsync，Windows 使用 `MoveFileExW(REPLACE_EXISTING|WRITE_THROUGH)`。
- Document ID 只用于摘要寻址，不拼接文件路径；store/root/state symlink 明确拒绝。
- 本地 provenance 记录 actor class/subject，但 assurance 固定为 `caller_asserted_local`，不是认证或审批。

**仍未实现**：PRD 领域 Schema/Ready Gate、Human 审批、签名 evidence、OAuth/RBAC、多租户、数据库、LLM、Web UI、业务 E2E。结构有效、command 成功或 receipt 存在均不代表产品需求已批准。

## Canonical store

构建：

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o prd ./cmd/prd
```

创建命令并提交：

```sh
./prd apply \
  --store ./var/prd \
  --command examples/commands/create.json
```

读取当前 canonical CSD：

```sh
./prd get --store ./var/prd --id PRD-001
```

更新使用新的 command ID，并绑定当前 revision：

```sh
./prd apply \
  --store ./var/prd \
  --command examples/commands/update.json
```

`apply` 默认最多等待文档锁 5 秒，可用 `--lock-timeout` 调整。残留锁不会被自动抢占；需要先确认没有活跃 writer，再按运行手册处理。失败发生在原子 rename 之后、目录 durability sync 之前时，调用方不能自行判断“未提交”；应使用**相同 command ID 和相同载荷重试**，系统会根据已提交 receipt 返回 replay。

Store layout 使用 Document ID 的 SHA-256 文件名：

```text
<store>/
├── documents/<sha256(document-id)>.json
└── locks/<sha256(document-id)>.lock/
```

`documents/*.json` 是内部原子 state envelope，包含 current CSD 和最小 command receipts。不要直接编辑；当前版本没有签名/可信执行环境，拥有同一文件系统写权限的主体仍可篡改本地文件。

## Detached CSD utilities

CSD-01 的命令仍可用于创建和比较**脱离 canonical store 的快照**：

```sh
./prd create --id PRD-001 --type prd --title "套餐升级" --out v1.json
./prd add-node --file v1.json --node examples/requirement.json --out v2.json
./prd semantic-hash --file v2.json
```

这些命令不能修改 canonical store，也不能绕过 CAS。`--out` 永不覆盖现有文件。

## 关键边界

- `internal/csd`：纯不可变语义内核；本轮仅补充 exact edge unlink 和稳定 ID 校验接口。
- `internal/operations`：命令协议、批次语义、幂等、CAS 判断、receipt。
- `internal/filestore`：跨进程锁、严格 store 读取、原子 envelope 替换；不决定产品业务语义。
- `cmd/prd apply`：canonical file-store 写入口；`get` 只读。
- [CSD-02 设计](docs/architecture/csd-controlled-store.md)：事务、锁、幂等与失败语义。
- [CSD-02 验证记录](docs/verification/csd-controlled-store.md)：实际执行结果和未验证范围。

Semantic hash 仍只代表版本化规范字段摘要，不证明自然语言同义、来源可信或审批成立。Receipt 也不是签名 attestation。
