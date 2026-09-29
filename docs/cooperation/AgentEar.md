# AgentEar × Hyphae

状态：Hyphae 侧协作提案，待 AgentEar 确认。基线：AgentEar `a26c914`。

## 边界

AgentEar 管麦克风、ASR、TTS 和设备权限；语音识别后的消息经 Agent24 已有通信入口进入 Hyphae。AgentEar 无需直接导入 Go Nostr 库、管理 relay 或复制 Nostr 私钥。

已有 A3 附着入口在 `src/a3.rs`、`src/a3_pair.rs`，manifest 为 `assets/agent24/domain-os.yml`；现有主机命令 `speak/stop_playback` 保持能力范围。

## 协作事项

- Agent24 CLI/基础 UI 通信稳定后，将语音输入和消息/回执关联，避免识别重试造成重复发送。
- 只播报本地策略允许的消息；收到普通消息不等同于获准执行。设备权限拒绝、附着断开、重复回执均有明确结果。
- 若 Agent24 提供新的通信状态事件，由 Agent24 和 Hyphae 先冻结格式，再由 AgentEar 消费；不要求此次上游迁移修改 AgentEar。

## 验收与待确认

复用 `cargo test --test contracts` 和 `scripts/e2e-agent24.sh`。现有脚本依赖 release 二进制、ASR、本地模型与 macOS 音频工具；需追加真实 relay 上的请求/回执关联与重复播报测试。缺设备或权限只能报告未验收。

待确认：播报策略、请求/消息 ID 透传字段、UI 是否需要确认发送。文本 CLI 通信不依赖这些事项完成。
