# CI 与本机联调交接（2026-10-07）

#132/#133 已合，Mac/Linux 联调四路已通过，正式版 v0.26.1 已发布。

## 当前 Release / CI 状态

[v0.26.1](https://github.com/iDoris-ai/Hyphae/releases/tag/v0.26.1) 已于 2026-10-07 发布为 latest；轻量 tag 指向 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22`。9 个 release asset 的远端 SHA-256 与两次本地可复现构建匹配；release lock SHA-256 为 `d5a3a76aa3aba34c24da6666bb089a93447b3cb5ea9c26e340f7540fc5615e48`。原生 macOS relay WebSocket smoke 也已通过。main CI [run 37622223697](https://github.com/iDoris-ai/Hyphae/actions/runs/37622223697) 全部 SUCCESS。

最新四路 workflow 证据来自 run [37627002418](https://github.com/iDoris-ai/Hyphae/actions/runs/37627002418)，PR head `8d619d7ea413bb3508f5e530c54f9c9c9d4935b3`，实际 merge source `6a41d989ecce0d7cc308592d8583773b3985a910`。candidate 与 pinned-production 在 native darwin-arm64、linux-x64 均各通过 10/10；macOS 50 轮 stress 通过。但该 run 的 macOS 常规 Go 测试在 `TestDaemonJSONLCLIOutputModesAndGenerationRestart` 因测试夹具的 stdout/stderr 观察时序竞态失败，`ci-ok` 因此失败，不能称为整体 CI 通过。最新夹具修复要求 JSONL 测试同时观察到目标 status 和 stderr 诊断后才向子进程发 TERM；只改变测试同步，不改生产 daemon、不降低断言、不加固定等待。此修复的新提交仍需 fresh CI 与 Daemon 评审。

本次四路证据中，candidate merge source 为 `6a41d989ecce0d7cc308592d8583773b3985a910`；Agent24 base/build source 为 `fc862cf3f765f3e59686e816aea6fa4792f10da2`。production lock SHA-256 为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`；candidate derived lock SHA-256 分别为 darwin-arm64 `cd749ee6eb7f6e19d9edaca4a7260cdbb89ab11ef1c481a9fac7b597db115955`、linux-x64 `656ec5075a92dcdf3ea5b4c9f2069dfbeaf30b5f4156925f130192850adff243`。pinned source 为 `671c584f9e9eb807a15968e2aa42fd7507e178b8`，原/derived lock 均为 production lock。candidate 派生锁仅为临时验证输入，不是生产 lock 更新或 Agent24 对生产 lock 的验收。

## CI 证据

| Run / PR head | 实际 Hyphae merge source | 结果与范围 | 来源与制品 |
|---|---|---|---|
| [run 37618734134](https://github.com/iDoris-ai/Hyphae/actions/runs/37618734134)，head `d4e6eb537418337b0a8bb47b6d6f4ad64433899b` | `8c40cb9ba1007ff458d15100264346f29483dadf` | 首次 candidate/pinned 在 runner 启动前因 ZIP 丢失可执行位而失败，`stages=0`；这不是十阶段验收失败。该 run 的 macOS CPython 3.14.7 stress 50/50 PASS。 | 历史 run 页面。 |
| [run 37620371296](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)，head `b8cac1d6b770a4fb8e5e147e14dfbdc718c26145` | `c1512f297fb095ab65619a4b81cd204c193c5e0f` | candidate 与 pinned-production 各 10/10 stages PASS；macOS CPython 3.14.7 stress 50/50（24.478s）PASS；`ci-ok` PASS。 | [run artifacts 页面](https://github.com/iDoris-ai/Hyphae/actions/runs/37620371296)：`joint-evidence-candidate-37620371296-1`、`joint-evidence-pinned-production-37620371296-1`。 |
| [run 37622440387](https://github.com/iDoris-ai/Hyphae/actions/runs/37622440387)，head `0e4d60d7535d55e1ffd826ef2e242cbd010c4856` | `efdb96660aefd5094741e01c77fcd321bfe1ae6b` | main `6c613b6` 基线上的 Linux candidate/pinned-production 各 10/10 PASS；当时尚无 macOS joint consumer。 | `joint-evidence-candidate-37622440387-1`、`joint-evidence-pinned-production-37622440387-1`。 |
| [run 37627002418](https://github.com/iDoris-ai/Hyphae/actions/runs/37627002418)，head `8d619d7ea413bb3508f5e530c54f9c9c9d4935b3` | `6a41d989ecce0d7cc308592d8583773b3985a910` | 四个 native mode/platform consumers 各 10/10 PASS，macOS stress PASS；macOS 常规 Go JSONL test fixture 时序失败，故 `ci-ok` FAIL。 | `joint-evidence-{candidate,pinned-production}-{darwin-arm64,linux-x64}-37627002418-1`。 |

Run 37622440387 两路 evidence 均为 `linux-x64`。candidate source 为 `efdb96660aefd5094741e01c77fcd321bfe1ae6b`，Agent24 base/built source 为 `fc862cf3f765f3e59686e816aea6fa4792f10da2`，production lock SHA-256 为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`，derived candidate lock SHA-256 为 `077245bb8fd3efb3caf4722d0d40d066223420bdf886717dafab3feee19ee76d`。pinned-production baseline source 为 `671c584f9e9eb807a15968e2aa42fd7507e178b8`，原/派生 lock SHA-256 都为 `a83b7a586b1e19693d4abbbe4d1c737cf9fb5d6e2f63b7d8ebc6a252e1032ffd`。

candidate lock 是临时 Agent24 checkout 中为候选源码派生的验证输入，不是生产 lock 更新或生产验收；原生产 lock bytes/hash 与严格 source/hash gate 保持独立。模式恢复只在 manifest/output/content hash 校验通过后对四个固定 regular binary 恢复执行权限，并在同一 FD 上复核 hash。

## 下一步（有序依赖）

1. **#134 owner / Daemon**：修复测试夹具同步后，对新 head 运行 CI 并请求 fresh review；四路 10/10 证据绑定旧 head `8d619d7`，不替代新 head 验收。
2. **Agent24 DEP-C8 owner**：基于已发布 v0.26.1 升级 Agent24 自身依赖，并单独验收/接受新的 production lock；candidate derived lock 不可代替此步骤。
3. **COMM6b / COMM7 owners**：Agent24 完成 DEP-C8 后，继续 profile 与 unlock；Hyphae 版本依赖解除不代替 Agent24 的桌面实测和业务验收。
4. **PR 132 文档兼容待办（非 blocker）**：v0.20 无 `c/v` 的历史行为不回查；三元素 `p` hint 不接受。

## PR/验收边界

#133 已按精确 head `42f5a8cc076036930a205699bcd65d9bc29e8321` 审批并合并为 main `6c613b64e8ce95fc89ac81a0d6d56a0e486e5d22`。#134 当前分支增加 native macOS ARM64 联调，尚待新 CI；run 37622440387 的 Linux-only legs 是其上一次 main 基线验收，不代表本轮 Mac PASS。

#135 本机已验证实时收件（接收方保持 TUI 打开），CI 全部 SUCCESS 且 Daemon 已批准，当前仅待合并。#136 收到 REQUEST_CHANGES；group UI 与 outbox 尚未完成，M1 整体仍未完成。CLI 十阶段不是桌面 M1 闭环。
