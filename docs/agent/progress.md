# Progress — 仓库实时状态

> `pilot run` 每推进一步就更新这里。宁可慢,不可让它和仓库真实状态脱节。
> 最后更新:2026-09-10

## 此刻在做什么

**当前里程碑**:M2 · L3 行为协议标准化。尚未开工写 behavior 代码——按 `roadmap.md`,**M2-F5(存储层加固)先于其余 Feature**,而那一层的缺陷比原先估计的多。

**进行中的 PR**:

| PR | 分支 | 状态 |
|---|---|---|
| [#35](https://github.com/iDoris-ai/Hyphae/pull/35) | `fix/storage-upsert-monotonic` | 🔄 OPEN,等外部评审裁决 |

#35 内容:`StoreMessage` 从 `INSERT OR REPLACE` 改为 UPSERT(`is_incoming` 单调、空值不再清空已有列),外加根治 legacy 迁移的 id 碰撞。**缺陷最初由 Agent24 会话在排查自己的入站活性探针时发现并写出补丁,随后主动交还**;本仓库独立复现后接手、扩展并收口。

**下一个该做的 Task**:`M2-F5-T1`(SQLite PRAGMA 没有真正生效)。它排在 `M2-F5-T2`(SaveOutbox 并发)之前,因为**底下的连接仍然 `busy_timeout=0` 会让任何并发修复看起来无效**——继续以 `database is locked` 收场,让人误判修错了地方。

## 阻塞项

| 项 | 卡在什么 | 影响 |
|---|---|---|
| `relay-khatru` fork(M1.5-F4-T1) | D3 未拍板:放 `AuraAIHQ/` 还是 `iDoris-ai/` | **不阻塞 M2**;阻塞 M2.5 |
| D1/D2/D4/D5(M2.5-F0-T1) | 邀请券上链 / 漂流瓶向量算法 / NIP-42 auth / 1对1 通道协议 | 阻塞 M2.5 开工 |
| `scripts/deploy-relay.sh` 的 `local`/`tunnel` | 硬编码 `examples/basic`,上游已改名 | 阻塞真实 relay 自部署演示 |

## 分支与 worktree

| worktree | 分支 | 用途 |
|---|---|---|
| `agent-speaker/` | `main` | 主 checkout —— 只读代码/盘点/合并,**不在这里开发** |
| `agent-speaker-storage/` | `fix/storage-upsert-monotonic` | PR #35 |

2026-09-10 清理:12 个已 squash-merge 的本地分支 + 2 个已合并 worktree(PR #33/#34)已删除。远程一直是干净的(GitHub auto-delete-on-merge 已开启)。

## 跟进账本

见 [`followups.md`](followups.md)。FU-2~FU-7 已全部升格为 `tasks.md` 里的 `M2-F5-*` 正式 Task(它们不再是「零散小事」,而是当前 Feature 的主体)。FU-1(release 打包自动化缺失导致 `install.sh` 404)仍是纯跟进项。

## 里程碑状态

| 里程碑 | 状态 |
|---|---|
| M1 | ✅ |
| M1.5 | 🔄 代码 10/10 done,M1.5-F4(fork 仓库)BLOCKED |
| **M2** | ⏳ **当前目标**,从 M2-F5 存储层加固开始 |
| M2.5 | ⏳ 待 D1-D5 拍板 |
| M3 / M4 / M5 | ⏳ |

## 近期决策记录

- **2026-09-10 · 里程碑编号撤回重映射。** 早先把 `M1.5→M2`、`M2→M3` 映射成整数以适配点号式 Task ID,现已撤销,改用 `M2-F5-T1` 这种 `-` 分隔。原因:原编号在 `protocol-v2.md`、`roadmap-v2.md`、`testing-integration-plan.md`、`specs/m1.5/`、GitHub issue 里到处都是且全都还活着,让「M2」在两套活文档里指不同的东西是永久陷阱,而换来的只是点号。
- **2026-09-10 · 第一个 READY 任务从「SaveOutbox 并发」改成「PRAGMA 只在一条连接上生效」。** PR #35 的评审证明后者会掩盖前者的修复效果(`busy_timeout=0` 会让并发修复继续以 `database is locked` 收场)。
- **2026-09-10 · T1 的描述经实测修正。** 初版只点名 `busy_timeout`。外部评审用 `mattn/go-sqlite3` 复核后认为该条不成立(那个驱动默认就是 5000),但本仓库用的是 `modernc.org/sqlite`,默认是 **0**——原判断对本仓库成立,却漏了一半:`foreign_keys` 和 `synchronous` 同样只在一条连接上生效,只有 `journal_mode` 幸免(WAL 是数据库文件属性,不是每连接状态)。带对照的实测数据已写进 `tasks.md` 的 T1。**方法上的教训:验证「没有 X 就退回默认值」必须去量那个默认值,而且要用本仓库真正在用的驱动量。**
