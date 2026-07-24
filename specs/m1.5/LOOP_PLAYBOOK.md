# M1.5 Loop Playbook

> 这是喂给 `/loop` 的操作手册。每一轮 loop 唤醒后，从头执行这个流程一次（通常对应完成"一个任务的一步"，不是每次唤醒都能走完整个任务——任务大的话会跨好几轮唤醒）。

## 前提

- 本地仓库在 `main` 分支，`git status` 干净，`git pull` 到最新
- `gh auth status` 正常，有权限对 `iDoris-ai/agent-speaker` 开 PR
- 已知：这个仓库配了一个后台 bot，大约每 5 分钟轮询一次待 review 的 PR，PR 开出后**通常 8-10 分钟内**会收到 review 结果（有时 3 分钟）。不要比这个更频繁地去戳它，也不要假设它会秒回。

## 主循环（每次唤醒执行一遍）

### Step 0：状态判断——我现在在流程的哪一步？

先检查有没有一个**尚未合并、还在等结果或等修复**的 PR（`gh pr list --repo iDoris-ai/agent-speaker --author @me --state open`）：

- **有开着的 PR** → 跳到 Step 5（去看 review 结果），不要开新任务
- **没有开着的 PR** → 这是一个新任务的开始，跳到 Step 1

### Step 1：选任务

打开 `specs/m1.5/README.md` 的任务表，选**第一个** `status = ready` 且 `depends_on` 全部是 `done` 的任务。读对应的 `tasks/NN-xxx.md` 文件——那就是这一轮的完整需求（接口/设计/数据/流程/验收标准都在里面）。

如果任务表里已经没有 `ready` 或 `blocked`-但-依赖已满足 的任务了：**M1.5 全部完成**，停止循环，给人类发个通知总结一下（不要继续瞎找事做）。

### Step 2：实现

1. 新建分支：`git checkout -b m1.5/NN-short-name`（NN 用任务编号，短名字取任务标题）
2. 严格按任务 spec 的「接口」「设计」「数据」「流程」实现，不要顺手做 spec 之外的事（哪怕看到别的能改进的地方，记一笔到 `docs/TODO.md` 或对应 spec 文件的「实现笔记」里，不要在这个 PR 里顺带改）
3. 按任务 spec 的「验收标准」写/改测试

### Step 3：本地自测（硬门槛，不过不能进下一步）

```bash
go build ./...
go vet ./...
go test ./...
```

三个必须全绿。如果任务涉及 CLI 行为改动，额外手动跑一下受影响的命令确认行为符合预期（不需要跑全套 `QUICK_MANUAL_TEST.md`，除非任务本身就是消息/群聊相关的核心路径）。

### Step 4：两轮 review，再开 PR

**第一轮：自我挑战 review。** 把这一步当成一个跟实现者不同立场的人在读代码：重新读一遍任务 spec 的验收标准，逐条对照 diff 检查有没有漏掉；专门找一遍边界情况、错误处理、并发/竞态、安全问题（尤其涉及 keystore/加密/审计链的任务）。发现问题就地修，改完重新跑 Step 3。

**第二轮：Codex review（Tier 1），配额用尽时自动降级 Tier 2/3。** 遵循全局 CLAUDE.md 里定义的 Code Review Workflow：

- **Tier 1（优先）**：用 `/codex:rescue` 把这次的 diff（`git diff main`）交给 Codex，要求严格审查（正确性、竞态、安全、错误处理、性能）。
- **Tier 2（Codex 配额/额度耗尽或不可用时降级）**：Codex 是**服务端**跑在这个 repo 之外的账号额度上的，跟 loop 的执行节奏无关——一旦报 quota/rate-limit/usage-limit 错误，不是"再等一会就好"，按 CLAUDE.md 的定义直接切 gh Copilot：先正常走 Step 4.5 把这一轮的改动提交、push、开 PR（Copilot 只能 review 服务端上的 PR，不能 review 本地 diff），然后 `gh pr edit <num> --add-reviewer copilot`，用跟 Step 5 一样的等待节奏（`ScheduleWakeup`，不要紧密轮询）检查 `gh pr view <num> --json reviews,comments`；Copilot 提的问题按 Step 4 自我 review 同样的严格程度处理，修完 push 后 `gh pr comment <num> --body "@copilot please review again"` 重新申请。跟本仓库自己的后台 review bot（Step 5）不冲突——两者可以对同一个 PR 各自留 review，真正决定能不能 merge 的还是本仓库 bot 的 `reviewDecision`。
- **Tier 3（Copilot 也不可用）**：自己以本地模型身份严格 review，态度跟对 Codex/Copilot 一样挑剔（竞态、安全、错误处理、边界情况），在 PR 描述里明确标注"local-model review（Tier 3）"，方便以后 Codex/Copilot 恢复了重新走一遍。

无论走哪一层，Codex/Copilot/本地 review 提出的问题，能修的当场修；有分歧或明确决定不修的，在 PR 描述里写清楚原因（不要静默忽略）。改完重新跑 Step 3。**在 PR 描述和对应 spec 文件的实现笔记里写清楚这一轮实际用的是哪一层**，别让人以为一直是 Tier 1。

两轮都过了，才进入 Step 4.5。

### Step 4.5：提交 + 开 PR

```bash
git add -A
git commit -m "<conventional commit message，引用 specs/m1.5/tasks/NN-xxx.md>"
git push -u origin m1.5/NN-short-name
gh pr create --repo iDoris-ai/agent-speaker --base main --head m1.5/NN-short-name \
  --title "<清晰标题>" \
  --body "对照 specs/m1.5/tasks/NN-xxx.md 的验收标准写 Summary + Test plan，注明走过自我 review 和 Codex review 两轮，Codex 提的问题分别是什么、怎么处理的"
```

### Step 5：等后台 review bot

后台 bot 大约每 5 分钟轮询一次，PR 开出后通常 8-10 分钟内出结果（有时更快，3 分钟）。**不要用比这更短的间隔去戳它**——用 `ScheduleWakeup` 排一个几分钟后的下次唤醒，而不是在这一轮里死等或紧密轮询。

下次唤醒时，检查 PR 状态：`gh pr view <N> --repo iDoris-ai/agent-speaker --json state,reviews,reviewDecision,comments`

- **`reviewDecision` 还是空/PENDING** → 还没审完，继续 ScheduleWakeup 等下一轮（同样给几分钟间隔，别加速轮询）
- **`APPROVED`** → 跳到 Step 6a
- **`CHANGES_REQUESTED`（或 review comments 里有实质性问题）** → 跳到 Step 6b

### Step 6a：Approved → merge，继续下一个任务

```bash
gh pr merge <N> --repo iDoris-ai/agent-speaker --squash --delete-branch
git checkout main && git pull
```

把 `specs/m1.5/README.md` 任务表里这一条的 status 改成 `done`，提交（可以是一个单独的小 commit，也可以合并进下一个任务的第一个 commit）。回到 Step 0（此时应该走到"没有开着的 PR"分支，开始下一个任务）。

> **注意**：`main` 是受保护分支，不能直接 `git push origin main`——哪怕只改一行文档也会被拒（`GH006: Protected branch update failed`）。默认走"合并进下一个任务分支的第一个 commit"这条路（省一次单独开 PR 的开销）；只有当下一个任务还没确定、或者需要立刻让 main 反映最新状态时，才单独开一个纯文档的小分支/PR（参考任务 4→5 之间因为这个原因产生的 PR #17）。

### Step 6b：Request changes → 修复，重新申请 review

1. 读 review 意见，逐条对照修
2. 改完重新跑 Step 3（本地自测）
3. `git push`（同一个分支追加 commit，不要开新 PR）
4. `gh pr comment <N> --repo iDoris-ai/agent-speaker --body "已按意见修复：<简述改了什么>，重新申请 review"`
5. 回到 Step 5（继续等下一轮 bot review）

## 什么情况下要暂停、通知人类，而不是自己硬闯

- 同一个 PR 被 `CHANGES_REQUESTED` 超过 **3 轮**还没过——大概率是任务 spec 本身有问题或者跟实现方式有根本分歧，不要无限重试，暂停并总结分歧点给人类看
- Codex review 和自我 review 出现互相矛盾的意见，拿不准该听谁的
- 任务实现过程中发现任务 spec 描述的接口/设计和代码里的实际现状对不上（比如假设的某个函数根本不存在）——先别猜着改，暂停确认
- 任务涉及删除/修改看起来是别人在用的数据、密钥、已发布的公开接口——这类不可逆或影响面大的操作，即使 spec 里写了，也先确认一下

## 每轮结束前，检查一下

- 有没有把不属于当前任务范围的改动也提交进去了（`git diff main --stat` 扫一眼文件列表）
- commit message 和 PR 描述有没有讲清楚"为什么"，而不只是"改了什么"
- 是不是又不小心跑了危险的 git 操作（`rebase --abort`/`reset --hard`/`push --force`）而没有先 `git status` 检查——2026-07-24 这天就因为没检查直接吃过一次亏，教训写在这里防止重犯
