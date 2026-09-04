# Progress — 仓库实时状态

> `pilot run` 每推进一步就更新这里。宁可慢,不可让它和仓库真实状态脱节。
> 最后更新:2026-09-03

## 此刻在做什么

**当前里程碑**:M3 · L3 行为协议标准化(原 `roadmap-v2.md` 的 M2)—— 尚未开工,规划刚落地。

**进行中的 PR**:

| PR | 分支 | 状态 |
|---|---|---|
| [#33](https://github.com/iDoris-ai/Hyphae/pull/33) | `rename/agent-speaker-to-hyphae` | 🔄 已推修复(`d4767c3`+`c22240e`),等外部评审重新裁决 |

PR #33 内容:`agent-speaker` → `hyphae` 全仓改名 + 评审发现的 B1 修复(keystore 校验 token 跨持久化边界,旧的加密 keystore 会拒绝正确密码)+ `SaveKeyStore` 原子写与唯一临时文件名。

**下一个该做的 Task**:`T3.5.1`(修 `SaveOutbox` 并发写)。选它而不是选 `T3.1.1`(信封编解码器)的理由:F3.2 的新 behavior 会增加 outbox 并发写者,`roadmap-v2.md` 明确要求修复先于新 behavior 上线;而且 PR #33 刚在 `SaveKeyStore` 上做完同一个修法,照抄成本最低、上下文最热。

## 阻塞项

| 项 | 卡在什么 | 影响 |
|---|---|---|
| T2.10 · `relay-khatru` fork | D3 未拍板:放 `AuraAIHQ/` 还是 `iDoris-ai/`(仓库刚改名到 `iDoris-ai/Hyphae`,倾向后者) | **不阻塞 M3**;阻塞 M4 |
| T4.0 · D1/D2/D4/D5 | 邀请券上链 / 漂流瓶向量算法 / NIP-42 auth / 1对1 通道协议 | 阻塞 M4 开工 |
| `scripts/deploy-relay.sh` 的 `local`/`tunnel` | 硬编码 `examples/basic`,上游已改名 | 阻塞真实 relay 自部署演示 |

## 分支与 worktree

| worktree | 分支 | 用途 |
|---|---|---|
| `agent-speaker/` | `main` | 主 checkout —— 只读代码/盘点/合并,**不在这里开发** |
| `agent-speaker-hyphae-pr33/` | `rename/agent-speaker-to-hyphae` | PR #33 |
| `agent-speaker-plan/` | `docs/pilot-plan-m2` | 本规划文档 |

**本地待清理**:11 个已 squash-merge 的分支(`m1.5/*`、`docs/m1.5-test-guide`、`fix/m1.5-known-issues-cleanup`、`pr30-tmp`),清单和命令见 pilot status 输出。远程已干净(GitHub auto-delete-on-merge 已开启)。

## 跟进账本

见 [`followups.md`](followups.md)。当前 2 条 OPEN:FU-1(release 打包自动化缺失导致 `install.sh` 404)、FU-2(`outbox.go` 固定临时文件名 —— 已升格为 `T3.5.1` 的一部分)。

## 里程碑状态一览

| pilot | 原名 | 状态 |
|---|---|---|
| M1 | M1 | ✅ |
| M2 | M1.5 | 🔄 代码 10/10 done,F2.4(fork 仓库)BLOCKED |
| **M3** | **M2** | ⏳ **当前目标** |
| M4 | M2.5 | ⏳ 待 D1-D5 拍板 |
| M5 / M6 / M7 | M3 / M4 / M5 | ⏳ |
