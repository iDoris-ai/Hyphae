# Nostr CLI 工具研究报告

> 原始研究文档汇总
> 时间: 2026-04-08

## 工具对比一览

| 工具名称 | 语言 | Stars | 最近提交 | 维护状态 | 推荐度 |
|---------|------|-------|----------|----------|--------|
| **nak** | Go | 356 | 2026-04-05 | ✅ 活跃 | ⭐⭐⭐⭐⭐ |
| **algia** | Go | 216 | 2026-03-18 | ✅ 活跃 | ⭐⭐⭐⭐⭐ |
| **noscl** | Go | 277 | 2024-01-27 | ⚠️ 基本废弃 | ⭐⭐⭐ |
| **nostril** | C | 113 | 2025-12-14 | ✅ 维护中 | ⭐⭐⭐⭐ |
| **nostr-commander-rs** | Rust | 79 | 2024-10-01 | ⚠️ 更新较少 | ⭐⭐⭐ |

## 推荐基座: nak (Go)

**选择理由:**
1. 本身就是 CLI 工具，可直接 fork 扩展
2. 作者 fiatjaf = Nostr 协议核心设计者
3. Go 开发效率: 添加新命令只需 3 步
4. 生态丰富: zstd, libp2p 等库完善

**常用命令示例:**
```bash
# 生成密钥
nak key-gen

# 发布事件
nak event --content "Hello Nostr!" --kind 1

# 查询事件
nak req -k 1 --limit 10 wss://relay.damus.io

# 实时监听
nak req -k 1 --stream wss://relay.damus.io
```

## 许可证信息

- nak: MIT/Unlicense ✅
- algia: MIT ✅
- nostril: 开源 ✅
- strfry: Apache 2.0 ✅
