# CI 与本机联调交接（2026-10-07）

132 已合并到 main，当前 main 为 `d333c25`；#133/#134 已提交，仍待 Daemon 精确 head review。
#129/#130 的 Actions 验证已完成；M1 尚未完成：新 TUI 本机六方向 PASS，待 PR，group 与 TX offline retry 仍未闭环。
最新已验证 CI head 是 `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145`；此结果属于 #134 仍以 #133 分支为 base 的旧栈，不是当前 main `d333c25` 的验证。

## CI 证据

| Run / PR head | 实际 Hyphae merge source | 结果与范围 | 来源与制品 |
|---|---|---|---|
| [run 37618734134](https://github.com/iDoris-ai/Hyphae/actions/runs/37618734134)，head `d4e6eb537418337b0a8bb47b6d6f4ad64433899b` | `8c40cb9ba1007ff458d15100264346f29483dadf` | 首次 candidate/pinned 在 runner 启动前因 ZIP 丢失可执行位而失败，`stages=0`；这不是十阶段验收失败。该 run 的 macOS CPython 3.14.7 stress 50/50 PASS。 | 历史失败证据见 run 页面；无须假定 artifact ID。 |
| [run 37620371296](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)，head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145` | `c1512f297fb095ab65619a4b81cd204c193c5e0f` | candidate 与 pinned-production 各 10/10 stages PASS；macOS CPython 3.14.7 stress 50/50（24.478s）PASS；`ci-ok` PASS。 | [run artifacts 页面](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)：`joint-evidence-candidate-37620371296-1`、`joint-evidence-pinned-production-37620371296-1`。只记录 artifact 名，不猜 artifact ID。 |

两路 evidence 都是 `linux-x64`。candidate 使用 Hyphae source `c1512f297fb095ab65619a4b81cd204c193c5e0f`、Agent24 base/built source `fc862cf3f765f3e59686e816aea6fa4792f10da2`、production lock SHA-256 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，派生 candidate lock SHA-256 `ceb381fe28f13626d87c895662f36e15a2dcb7e466d816d57820ab6ad0b097a5`。pinned-production 使用 baseline Hyphae source `671c584f9e9eb807a15968e2aa42fd7507e178b8`，原/派生 lock SHA-256 都是 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，Agent24 source 同为 `fc862cf3f765f3e59686e816aea6fa4792f10da2`。

candidate lock 是临时 Agent24 checkout 中为当前候选源码派生的验证输入，绝非生产 lock 更新或生产验收；原生产 lock bytes/hash 与严格 source/hash gate 保持独立。run 37620371296 验证的 merge source 来自 #134 的旧 #133 base；不得将其表述为对 main `d333c25` 的验证。模式恢复修复只在 manifest/output/content hash 校验通过后对四个固定 regular binary 恢复执行权限，并在同一 FD 上复核 hash。

## 下一步（有序依赖）

1. **#133 owner / Daemon reviewer**：在精确 head `42f5a8cc076036930a205699bcd65d9bc29e8321` 上完成审批；当前 CI 全绿但 review 尚缺、状态 `REVIEW_REQUIRED` / `BLOCKED`。随后由授权 merge owner 正常合并 #133 到 main。
2. **#134 owner**：待 #133 合并后，只将 #134 自身提交 rebase 到实际 main merge SHA，retarget `main`，重新跑 CI，并请求 Daemon 对新精确 head review。不要把旧 #133 栈代码重复带入 #134，也不要把此次旧栈 run 当作新 main 的验收。
3. **Agent24 owner / COMM7 owner**：生产 lock 升级必须单独审阅并改动真实生产输入；candidate 派生 lock 不得混作 production lock。COMM7 仅在 M1 完成后进入。
4. **PR 132 文档兼容待办（非 blocker）**：记录并后续澄清 v0.20 无 `c/v` 的历史行为不回查，三元素 `p` hint 不接受。不要为此触碰缺失的 kind contract 文件；该兼容边界不是当前 CI/M1 gate。

## PR/验收边界

#133 当前精确 head `42f5a8cc076036930a205699bcd65d9bc29e8321` 的 Ubuntu、macOS、CLA 与 `ci-ok` 检查均 SUCCESS；尚无 clestons review，故不可称 CLEAN/已批准。#134 最新 head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145` 的检查也均 SUCCESS，review 仍待 Daemon。本文档是后续新提交，尚未被 Actions 验收；最后实际测试 head 仍为 `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145`。

本轮本机 TUI 结果仅代表六方向本机验证；接收方实时刷新、group broadcast 与 TX offline retry 仍不闭环。它不替代 PR/CI 或真实远端用户验收。
