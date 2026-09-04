# Hyphae 手动测试指南

> 场景：在两台/三台电脑上测试 hyphae 的核心功能。
> 预计用时：双人 10 分钟，三人追加 10 分钟，Profile/TUI 追加 5 分钟。
> 本文档原是一份未合并的草稿（找回于本地 stash），2026-07-24 校对更新：relay 默认改为本地自建（`wss://relay.aastar.io` 当前不可达，`curl` 返回 530），并补充了 Profile 和 TUI 部分。

---

## Part 0：跑一遍自动化自测（建议先做，5 分钟内出结果）

在拉着朋友一起测之前，先确认自动化测试是绿的——省得手动测试时把环境问题误判成功能 bug。

```bash
go build ./...      # 编译检查
go vet ./...         # 静态检查
go test ./... -cover # 单元测试 + 覆盖率
./test.sh            # 构建 + 单元测试 + CLI 冒烟检查
```

全部应该无报错退出。已知覆盖率薄弱区（不代表有 bug，只是没写测试，见 `docs/milestones/roadmap-v2.md` M1 测试状态一节）：`internal/nostr`、`internal/daemon`、`internal/common`。

---

## Part 1：双人测试（你 + 伙伴）

> 💡 如果以下测试已经做过，可以直接跳到 **Part 2：三人测试** 或 **Part 3：Profile / TUI**。

---

## 前置条件（两人都要做）

```bash
# 1. 克隆仓库
git clone https://github.com/iDoris-ai/hyphae.git
cd hyphae

# 2. 编译
./build.sh
# 或者：go build -o bin/hyphae ./cmd/hyphae

# 3. 确认二进制文件存在
./bin/hyphae --help
```

### Relay：优先用本地自建（推荐）

`wss://relay.aastar.io` 目前无法连接（`curl -I https://relay.aastar.io` 返回 530，relay 侧的问题，不是本项目的 bug）。在它恢复之前，用项目自带的 mini relay 测试完全等价：

```bash
# 任意一方（或找第三台机器）启动，另一方通过公网/内网地址连接
go run scripts/minirelay.go
# 输出 🚀 Mini Relay starting on ws://localhost:7777
```

下文命令里的 `--relay wss://relay.aastar.io` 全部替换成你实际用的 relay 地址（本地自测用 `--relay ws://localhost:7777`；两人分别在不同网络，就换成能互相访问的公网 relay 或做内网穿透）。`wss://relay.aastar.io` 恢复后可以直接换回去，用法不变。

---

## Step 1：各自创建身份

### 你做：
```bash
./bin/hyphae identity create --nickname jason --default
./bin/hyphae identity export --nickname jason
```
把输出里的 `Npub: npub1xxx...` 复制下来，发给对方。

### 伙伴做：
```bash
./bin/hyphae identity create --nickname annie --default
./bin/hyphae identity export --nickname annie
```
把输出里的 `Npub: npub1yyy...` 复制下来，发给你。

---

## Step 2：互相添加联系人

### 你做（用对方给你的 npub）：
```bash
./bin/hyphae contact add --nickname annie --npub "npub1yyy..."
```

### 伙伴做（用你给的 npub）：
```bash
./bin/hyphae contact add --nickname jason --npub "npub1xxx..."
```

---

## Step 3：互发消息（明文 + 加密）

### 你 → 伙伴（明文）：
```bash
./bin/hyphae agent msg \
  --from jason \
  --to annie \
  --content "Hi, this is a plain message!" \
  --relay ws://localhost:7777 \
  --encrypt=false
```

### 伙伴 → 你（加密）：
```bash
./bin/hyphae agent msg \
  --from annie \
  --to jason \
  --content "Hi, this is an encrypted reply!" \
  --relay ws://localhost:7777 \
  --encrypt=true
```

> 💡 提示：relay 传播可能需要 2-3 秒，如果下面查不到就稍等再试。

---

## Step 4：查看收件箱

```bash
# 你查收
./bin/hyphae agent inbox --as jason --decrypt=true

# 伙伴查收
./bin/hyphae agent inbox --as annie --decrypt=true
```

---

## Step 5：查看对话历史

```bash
# 你看和伙伴的对话
./bin/hyphae history conversation --with annie --limit 20

# 伙伴看和你的对话
./bin/hyphae history conversation --with jason --limit 20
```

> ⚠️ **已知限制**：`history conversation --with <name>` 要求 `<name>` 必须是你 **contact 列表里已存在的昵称**（跟 `agent msg --to` 的解析规则不完全一致，`agent msg --to` 对未知昵称/npub 更宽松）。如果提示 `contact 'xxx' not found`，先 `contact add` 一下。这是 M1.5 阶段值得统一的一个小的不一致，不影响核心功能。

---

## Step 6：查看统计

```bash
# 两人都可以运行
./bin/hyphae history stats
```
应该能看到 Total / Incoming / Outgoing / Encrypted 消息数。

---

## Step 7：搜索消息

```bash
./bin/hyphae history search --query "plain"
./bin/hyphae history search --query "encrypted"
```

---

## 双人测试通过标准

- [ ] 两人都能成功创建身份
- [ ] 两人都能成功添加对方为联系人
- [ ] 你能收到伙伴的加密消息
- [ ] 伙伴能收到你的明文消息
- [ ] 对话历史能正确显示双方消息
- [ ] 统计数据与消息数量一致
- [ ] 搜索能返回正确结果

---

# Part 2：三人测试（你 + 伙伴A + 伙伴B）

> 💡 需要第三个人加入。如果双人测试已经通过，三人部分只需要新伙伴创建身份，然后三人互相加联系人即可。

---

## Step 1：第三人创建身份并加入

```bash
./bin/hyphae identity create --nickname brother --default
./bin/hyphae identity export --nickname brother
```
把 `Npub: npub1zzz...` 分别发给另外两人，另外两人各自 `contact add`，第三人也把另外两人的 npub `contact add` 回来（三方两两互加）。

---

## Step 2：创建三人聊天群组

> ⚠️ 群组**管理**（创建、加人、列群、离开/重新加入）已可用，但**群聊消息目前没有专门的广播命令/TUI 视图**——群消息需要给每个成员分别 `agent msg`，relay 会把它们当独立的 1 对 1 加密/明文消息处理。下面的测试就是覆盖这两块：已实现的群组管理 + 通过私聊模拟群广播。

```bash
./bin/hyphae group create \
  --name "ThreeAmigos" \
  --description "three-person group chat" \
  --members annie,brother
```

> 说明：`--members` 后面跟的是已添加的 contact nickname，逗号分隔。

---

## Step 3：验证群组信息

```bash
# 三人都可以运行
./bin/hyphae group list
```

---

## Step 4：模拟群聊（当前通过私聊广播）

```bash
# 你给另外两人各发一条
MSG="Hey team, welcome to our group chat!"
./bin/hyphae agent msg --from jason --to annie --content "$MSG" --relay ws://localhost:7777 --encrypt=false
./bin/hyphae agent msg --from jason --to brother --content "$MSG" --relay ws://localhost:7777 --encrypt=false
```
其余两人依样回复给另外两人。

> 💡 等 3-5 秒让 relay 传播。

---

## Step 5：验证三人都能收到消息

```bash
./bin/hyphae agent inbox --as jason --decrypt=true
./bin/hyphae agent inbox --as annie --decrypt=true
./bin/hyphae agent inbox --as brother --decrypt=true
```

---

## Step 6：查看各对话历史

```bash
./bin/hyphae history conversation --with annie --limit 10
./bin/hyphae history conversation --with brother --limit 10
```

---

## Step 7：群组管理测试

```bash
# 想离开群组
./bin/hyphae group leave --name "ThreeAmigos"

# 群主重新添加
./bin/hyphae group add-member --name "ThreeAmigos" --user brother
```

---

## Step 8：三人统计与搜索

```bash
./bin/hyphae history stats
./bin/hyphae history search --query "group"
```

---

## 三人测试通过标准

- [ ] 第三人成功创建身份并被另外两人添加为联系人
- [ ] 三人互相都能成功添加联系人
- [ ] 成功创建群组
- [ ] `group list` 能正确显示群组成员
- [ ] 三人都能收到另外两人的消息
- [ ] 每个人的对话历史都包含与另外两人的记录
- [ ] 统计数据与消息数量一致
- [ ] 搜索能返回群聊相关的消息
- [ ] 成员能正常离开/重新加入群组

---

# Part 3：Agent Profile + TUI（可选，5 分钟）

## Profile 发布与发现

```bash
./bin/hyphae profile publish --as jason \
  --name "Jason" --description "manual test run" \
  --relay ws://localhost:7777

./bin/hyphae profile search --query "Jason"
```

## TUI 聊天界面

```bash
./bin/hyphae tui --relay ws://localhost:7777
# 或直接 1 对 1：./bin/hyphae chat --with annie --relay ws://localhost:7777
```
在 TUI 里应该能看到和 Part 1 里发的消息同一份历史，收发新消息也应实时刷新。这部分是交互式界面，无法脚本化验证，需要人工盯着看。

## Profile / TUI 通过标准

- [ ] `profile publish` 成功，`profile search` 能搜到刚发布的 profile
- [ ] `tui`/`chat` 能正常打开，不崩溃
- [ ] TUI 里能看到 Part 1 的历史消息
- [ ] 在 TUI 里发消息，对方（无论是 TUI 还是 CLI）能收到

---

## 常见问题

**Q: inbox 看不到消息？**
A: 等 3-5 秒让 relay 传播，再试一次。如果还是收不到，检查：
- relay 是否连通：`./bin/hyphae relay info ws://localhost:7777`（或你实际用的 relay 地址）
- 双方的 identity 和 contact 是否配置正确

**Q: 加密消息解密失败？**
A: 确认发送方和接收方都有对方的正确 npub，且使用的是同一 relay。

**Q: `history conversation --with X` 提示 contact 找不到？**
A: 见 Step 5 的已知限制说明，先 `contact add` 那个昵称。

**Q: 命令输出里出现一堆 `[32m`/`[0m` 这种乱码？**
A: 已知问题——`cmd/hyphae/main.go` 里强制打开了终端颜色（不管输出是不是被重定向/管道），把命令输出存文件或喂给脚本时会看到这些转义符。已记录在 `docs/TODO.md`，等 CLI 支持 `--json`/纯文本输出模式时一并修。不影响功能，只是重定向输出会不好看。

**Q: 想重新测试？**
A: 删除本地数据即可重新开始：
```bash
rm -rf ~/.hyphae
```

---

## 完整测试通过标准

如果 **Part 1**、**Part 2**、**Part 3** 的全部勾选都完成，hyphae 的核心通信 + 群组管理 + Profile + TUI 就验证完毕了。
