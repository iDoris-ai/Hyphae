# CI 与本机联调交接（2026-10-07）

132、133 已合并到 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22`；#134 已只重放自身提交并 retarget main，新 CI 与精确 head review 待完成。
#129/#130 Actions 验证已完成；M1 尚未完成：[PR #135](https://github.com/iDoris-ai/Hyphae/pull/135) 本机 paired PTY 六方向（含接收方保持打开、无 daemon 与 restart）及 Go/race 均 PASS，等待 CI/review；[PR #136](https://github.com/iDoris-ai/Hyphae/pull/136) protocol 已发布，但 group UI 与 outbox 尚未完成。
最近通过 CI 的代码 head 仍是 `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145`，验证属于 #134 旧栈；不是新 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22` 或 rebased #134 的验收。

## CI 证据

| Run / PR head | 实际 Hyphae merge source | 结果与范围 | 来源与制品 |
|---|---|---|---|
| [run 37618734134](https://github.com/iDoris-ai/Hyphae/actions/runs/37618734134)，head `d4e6eb537418337b0a8bb47b6d6f4ad64433899b` | `8c40cb9ba1007ff458d15100264346f29483dadf` | 首次 candidate/pinned 在 runner 启动前因 ZIP 丢失可执行位而失败，`stages=0`；这不是十阶段验收失败。该 run 的 macOS CPython 3.14.7 stress 50/50 PASS。 | 历史 run 页面。 |
| [run 37620371296](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)，head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145` | `c1512f297fb095ab65619a4b81cd204c193c5e0f` | candidate 与 pinned-production 各 10/10 stages PASS；macOS CPython 3.14.7 stress 50/50（24.478s）PASS；`ci-ok` PASS。 | [run artifacts 页面](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)：`joint-evidence-candidate-37620371296-1`、`joint-evidence-pinned-production-37620371296-1`。 |

两路 evidence 都是 `linux-x64`。candidate 使用 Hyphae source `c1512f297fb095ab65619a4b81cd204c193c5e0f`、Agent24 base/built source `fc862cf3f765f3e59686e816aea6fa4792f10da2`、production lock SHA-256 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，派生 candidate lock SHA-256 `ceb381fe28f13626d87c895662f36e15a2dcb7e466d816d57820ab6ad0b097a5`。pinned-production 使用 baseline Hyphae source `671c584f9e9eb807a15968e2aa42fd7507e178b8`，原/派生 lock SHA-256 都是 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，Agent24 source 同为 `fc862cf3f765f3e59686e816aea6fa4792f10da2`。

candidate lock 是临时 Agent24 checkout 中为候选源码派生的验证输入，不是生产 lock 更新或生产验收；原生产 lock bytes/hash 与严格 source/hash gate 保持独立。模式恢复只在 manifest/output/content hash 校验通过后对四个固定 regular binary 恢复执行权限，并在同一 FD 上复核 hash。

## 下一步（有序依赖）

1. **#134 owner / Daemon**：对 rebase 到 main 后的新精确 head 等 CI 完成并请求 review；run 37620371296 仍只是旧 #133 栈结果。
2. **#135/#136 owner**：跟进本机六方向已过的 #135 新 CI/review；推进已发布的 #136 protocol，但 group UI/outbox 仍未交付。
3. **Agent24 owner / COMM7 owner**：生产 lock 升级需独立修改真实生产输入；candidate 派生 lock 不是 production lock。COMM7 在 M1 完成后推进。
4. **PR 132 文档兼容待办（非 blocker）**：v0.20 无 `c/v` 的历史行为不回查；三元素 `p` hint 不接受。

## PR/验收边界

#133 已按精确 head `42f5a8cc076036930a205699bcd65d9bc29e8321` 审批并合并为 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22`。#134 最近通过 CI 的仍是旧栈代码 head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145`；rebase 后新 head 与本文档提交均尚待 CI。

#135 本机已验证实时收件（接收方保持 TUI 打开）；该 PR 尚待 CI/review/merge。group UI 与 outbox 尚未完成。
