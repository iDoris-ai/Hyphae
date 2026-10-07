# CI 与本机联调交接（2026-10-07）

#132/#133 已合；新 main 基线上的 Linux 联调已通过，本轮扩展 macOS ARM64 双模式验收并准备正式版本供 Agent24 升锁。
正式 release 待发布；其后由 Agent24 完成 DEP-C8 升级，再推进 COMM6b profile 与 COMM7 unlock。M1 桌面/业务验收仍需继续：[PR #135](https://github.com/iDoris-ai/Hyphae/pull/135) 六方向 paired PTY 与 Go/race 本机 PASS、Daemon 已批准但 CI/merge 待完成；[PR #136](https://github.com/iDoris-ai/Hyphae/pull/136) protocol 收到 REQUEST_CHANGES，group UI/outbox 未完成。
最新已通过 Actions 的是 run 37622440387（Linux x64）；本轮新增 macOS ARM64 consumer legs 尚未运行通过。CLI 十阶段不是桌面 M1 闭环。

## CI 证据

| Run / PR head | 实际 Hyphae merge source | 结果与范围 | 来源与制品 |
|---|---|---|---|
| [run 37618734134](https://github.com/iDoris-ai/Hyphae/actions/runs/37618734134)，head `d4e6eb537418337b0a8bb47b6d6f4ad64433899b` | `8c40cb9ba1007ff458d15100264346f29483dadf` | 首次 candidate/pinned 在 runner 启动前因 ZIP 丢失可执行位而失败，`stages=0`；这不是十阶段验收失败。该 run 的 macOS CPython 3.14.7 stress 50/50 PASS。 | 历史 run 页面。 |
| [run 37620371296](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)，head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145` | `c1512f297fb095ab65619a4b81cd204c193c5e0f` | candidate 与 pinned-production 各 10/10 stages PASS；macOS CPython 3.14.7 stress 50/50（24.478s）PASS；`ci-ok` PASS。 | [run artifacts 页面](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)：`joint-evidence-candidate-37620371296-1`、`joint-evidence-pinned-production-37620371296-1`。 |
| [run 37622440387](https://github.com/iDoris-ai/Hyphae/actions/runs/37622440387)，head `0e4d60d7535d55e1ffd826ef2e242cbd010c4856` | `efdb96660aefd5094741e01c77fcd321bfe1ae6b` | main `6c613b6` 基线上的 Linux candidate/pinned-production 各 10/10 PASS，标准 CI、macOS stress 与 `ci-ok` 全绿；当时尚无 macOS joint consumer。 | `joint-evidence-candidate-37622440387-1`、`joint-evidence-pinned-production-37622440387-1`。 |

Run 37622440387 两路 evidence 均为 `linux-x64`。candidate source 为 `efdb96660aefd5094741e01c77fcd321bfe1ae6b`，Agent24 base/built source 为 `fc862cf3f765f3e59686e816aea6fa4792f10da2`，production lock SHA-256 为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，derived candidate lock SHA-256 为 `077245bb8fd3efb3caf4722d0d40d066223420bdf886717dafab3feee19ee76d`。pinned-production baseline source 为 `671c584f9e9eb807a15968e2aa42fd7507e178b8`，原/派生 lock SHA-256 都为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`。

candidate lock 是临时 Agent24 checkout 中为候选源码派生的验证输入，不是生产 lock 更新或生产验收；原生产 lock bytes/hash 与严格 source/hash gate 保持独立。模式恢复只在 manifest/output/content hash 校验通过后对四个固定 regular binary 恢复执行权限，并在同一 FD 上复核 hash。

## 下一步（有序依赖）

1. **#134 owner / Daemon**：本次四个 native platform/mode legs 通过后，按 exact new head 评审；candidate lock 仍不是 production lock。
2. **Release / Agent24 DEP-C8 owners**：发布正式版本后，Agent24 升级其 DEP-C8 自身依赖；Hyphae 侧版本依赖解除不代替桌面联调与业务验收。
3. **COMM6b / COMM7 owners**：依 Agent24 DEP-C8 完成后继续 profile 与 unlock；group UI/outbox 仍是 M1 缺口，不把 CLI 十阶段结果外推为桌面闭环。
4. **PR 132 文档兼容待办（非 blocker）**：v0.20 无 `c/v` 的历史行为不回查；三元素 `p` hint 不接受。

## PR/验收边界

#133 已按精确 head `42f5a8cc076036930a205699bcd65d9bc29e8321` 审批并合并为 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22`。#134 当前分支增加 native macOS ARM64 联调，尚待新 CI；run 37622440387 的 Linux-only legs 是其上一次 main 基线验收，不代表本轮 Mac PASS。

#135 本机已验证实时收件（接收方保持 TUI 打开）；Daemon 已批准，CI/merge 待完成。group UI 与 outbox 尚未完成。
