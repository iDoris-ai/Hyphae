# Task 03：统一 `history conversation` 与 `agent msg` 的联系人解析规则

> 规模：S · depends_on：无 · 发现来源：2026-07-24 真实自测（`QUICK_MANUAL_TEST.md` Step 5 已知限制说明）

## 问题

实测发现：`agent msg --to bob` 能正常发送（对 "bob" 的解析比较宽松），但 `history conversation --with bob` 会报 `Error: contact 'bob' not found`——即使 "bob" 是一个真实存在的 identity（只是没有出现在当前身份的 contact 列表里）。两个命令对"一个字符串到底是谁"的解析规则不一致，用户会把这当成 bug。

## 接口

`history conversation --with <target>` 的行为变得和 `agent msg --to <target>` 一致，命令本身签名不变。

## 设计

先读代码确认两边各自现在怎么解析（大概率在 `internal/messaging/` 和 `internal/identity/` 之间有重复或不一致的查找逻辑），抽出一个共享的解析函数，两个命令都调用它：

```go
// internal/identity 或 internal/common，具体放哪个包看现有代码组织，避免循环 import
func ResolveRecipient(mgr *identity.Manager, target string) (npub string, err error)
```

解析优先级（建议，实现时如果发现 `agent msg` 现在的真实优先级跟这个不一样，**以 `agent msg` 现有行为为准**，因为那是当前"更宽松、已经在用"的行为，不要反过来改窄它）：

1. 当前 identity 的 contact 列表里精确匹配 nickname
2. 直接是合法的 npub 字符串
3. identity 列表里的 nickname（比如问自己发的场景，或者对方其实是本机另一个身份）
4. 都不匹配 → 返回明确的错误："recipient 'X' not found in contacts or identities"

## 数据

无新增数据结构。

## 流程

`history conversation` 命令的 Action 从直接查 contact 表，改成先调用 `ResolveRecipient` 拿到 npub，再用 npub 去查历史（而不是用 nickname 字符串去查）。

## 验收标准

1. 复现 `QUICK_MANUAL_TEST.md` 里的场景：`agent msg --to bob` 发送成功后，`history conversation --with bob` 能查到，不再报错
2. 加一个回归测试：mock 一个不在 contact 列表但是合法 npub 的场景，两个命令的解析结果应该一致
3. 原有的"真的找不到"场景（乱打一个不存在的名字）依然应该报清楚的错误，不能因为放宽了规则就把错误也吞掉
4. `go test ./...` 全绿

## 实现笔记

（留空）
