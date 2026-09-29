# Contributing Guide

> Part of [Mycelium Protocol](https://github.com/AAStarCommunity/Brood) ecosystem.  
> Full contribution guide & CLA text: [protocol/CONTRIBUTING.md](https://github.com/AAStarCommunity/Brood/blob/main/protocol/CONTRIBUTING.md)

---

## Apache 2.0 — 2 分钟白话版

本项目使用 Apache License 2.0，对所有人开放：

**可以做**：免费用、商业用、修改、集成进闭源产品、分发  
**必须做**：保留版权行 · 保留 NOTICE 文件 · 修改文件须注明 · 不能蹭品牌（见 TRADEMARK.md）  
**不要求**：改了代码不用开源（这是和 GPL 最大的区别）

---

## 签署 CLA

本项目要求所有贡献者签署 **CLA（贡献者许可协议）**，以确保项目对所有贡献代码有清晰的法律授权。

> **CLA 是什么**：你签一次的声明："我提交的代码是我的原创（或我有权提交），授权本项目在 Apache 2.0 下使用。"  
> **为什么需要**：没有明确授权，项目分发你的代码存在法律漏洞。  
> **怎么签**：提交 PR 后，`@cla-assistant` 机器人自动评论并引导你签名，只需 1 分钟，永久有效，只签一次。

完整 CLA 协议文本：[CLA.md](https://github.com/AAStarCommunity/Brood/blob/main/protocol/CLA.md) | [中文参考译本](https://github.com/AAStarCommunity/Brood/blob/main/protocol/CLA-zh.md)

---

## 贡献流程

```
Fork → 新建分支 → 写代码 → 提交 PR → 签 CLA → Review → Merge
```

- 分支命名：`feat/xxx` · `fix/xxx` · `docs/xxx`
- Commit 规范：[Conventional Commits](https://www.conventionalcommits.org/)
- 问题反馈：在本仓库提 Issue
- 功能和修复在独立 worktree、工作分支开发，及时 commit、push 和提 PR，保留主工作树的未提交修改。
- PR 的实现、脚本和配置改动原则上控制在 300～500 行以内；测试和设计文档单独计算。特殊情况可酌情处理，例如不可分割的接口迁移或生成的依赖锁文件，并在 PR 中说明实际规模与原因。
- PR 说明应包含行为变化、兼容影响、实际验证命令和结果；依赖其他 PR 时写明 base 与合并顺序。
- 上游依赖维护见 [上游跟踪规则](docs/upstream-maintenance.md)，跨仓接线见 [协作清单](docs/cooperation/README.md)。

## License

Contributions are licensed under [Apache License 2.0](LICENSE).  
See [NOTICE](./NOTICE) · [TRADEMARK.md](./TRADEMARK.md) · [LICENSE-zh.md](./LICENSE-zh.md) · [TRADEMARK-zh.md](./TRADEMARK-zh.md) for details.
