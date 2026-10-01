# GitHub 接手入口 — 2026-10-01

代码、用户文件副本、候选历史及验收证据现均有 GitHub 副本。main `671c584f9e9eb807a15968e2aa42fd7507e178b8` 已包含 #118、#119、#120，真实 CI 通过；Goal 暂停，定时扫描关闭，E-M1 未完成。完整里程碑、依赖和磁盘补救步骤见 [交接文档](../handoff-20261001.md)。

## 获取代码及证据

[交接存档](https://github.com/iDoris-ai/Hyphae/releases/tag/handoff-em1-20261001) 提供 514 个现存文件及逐文件摘要，资产包含固定 a4 二进制、原始新验收日志、旧失败记录、补丁、Git bundle 和删除清单。归档 SHA256 为 `5a4ae77b45e7b5fa190714c2be07e6c1defe2c9a4fc49fe701eea873121dc468`。

```bash
git clone https://github.com/iDoris-ai/Hyphae.git
cd Hyphae
git fetch origin '+refs/heads/archive/em1-handoff-20261001/*:refs/remotes/origin/archive/em1-handoff-20261001/*'
gh release download handoff-em1-20261001 --repo iDoris-ai/Hyphae --dir handoff-download
cd handoff-download
shasum -a 256 -c SHA256SUMS
tar -xzf handoff-em1-20261001.tar.gz
```

解包后的 `evidence/` 对应原主仓 `build/agent-handoff/20261001/`，`MANIFEST.json` 给出每个文件的 SHA256。Linux 可用 `sha256sum -c SHA256SUMS`。不要执行归档中的诊断脚本或直接合并候选栈来恢复生产行为。

## 本地文件和分支

- [branch-map.json](branch-map.json)：19 个原本地分支 tip 与远端归档分支逐项对应，原提交 SHA 未改变。源码的正式接手入口是 main；归档分支保存历史候选与诊断内容。
- [AGENTS.md 副本](local-files/AGENTS.md.txt)、[tmux.sh 副本](local-files/tmux.sh.txt)：原主仓未跟踪用户文件的逐字节快照；原文件在原机器保留，副本未改变 main 的工具配置。
- [manifest.json](manifest.json)：固定 main、CI、存档和用户文件摘要。

开发分支均干净，stash 为空。主仓保留的两份未跟踪用户文件已逐字节保存到上面的 GitHub 副本。当前存在的提交、两份用户文件和被归档的证据可仅通过 GitHub 恢复；过去已被误删、从未提交且没有备份的内容仍不能保证恢复。缓存及可重建输出不属于交接源文件；私钥、身份库和凭据不入存档。后续正式 R3 与四仓验收按原交接门槛推进。

远端复核已完成：从 GitHub 重新下载归档后，514 个文件逐项通过大小和 SHA256 校验；19 个原分支 tip 与远端逐项相符；两份用户文件通过 GitHub Contents API 读取后与本地字节完全一致。结果见 [remote-verification.json](remote-verification.json)。
