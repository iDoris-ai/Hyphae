# nostrlib / fiatjaf.com/nostr × Hyphae

状态：Hyphae 侧已发现并准备两项上游修正约定，上游尚未修复。跨仓实现由用户推动；本轮未联系上游或更改依赖。Hyphae 的空 payload 入口保护已提出独立 [PR #93](https://github.com/iDoris-ai/Hyphae/pull/93)，仍待审阅合并。

2026-09-30 使用 Go module 元数据核对：固定版本与当时 `@latest` 均为 `v0.0.0-20260928115942-58e4c715304e`，源码 commit `58e4c715304e3e443cc05cdb6894c90d91729554`。module 来源为 [nostrlib.git](https://basspistol.org/npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6/nostrlib.git)，此地址来自固定版本 `.info` 的 Origin，不将旧 GitHub 仓库当成当前代码来源。

## MAC 比较差异与最小修正

该版本 `nip44/nip44.go` 的 `Decrypt` 在算出 `expectedMac` 后使用 `bytes.Equal(givenMac, expectedMac)`。官方 [NIP-44](https://github.com/nostr-protocol/nips/blob/master/44.md) 要求常数时间比较；Go 的 [hmac.Equal](https://pkg.go.dev/crypto/hmac#Equal) 为 MAC 提供该比较语义。

上游最小生产改动是一行，现文件已经导入 `crypto/hmac`：

```diff
- if !bytes.Equal(givenMac, expectedMac) {
+ if !hmac.Equal(givenMac, expectedMac) {
```

保留 `bytes` 导入，文件其他私钥检查仍使用它。不改变 payload 版本、padding、conversation key、HMAC 输入或错误返回；旧密文应仍可解密。

正常升级到当前 `@latest` 不会修复，因为版本相同。优先等待上游带修正的明确 commit，然后固定版本升级并跑兼容验收。若必须先维护受控 fork/patch，需另行明确来源、许可、校验、撤销替换条件与测试，不能直接改本机 module cache 充当发布修复，也不在本仓复制一套 NIP-44 实现。

## 空解码结果导致 panic

同一固定版本的 `Decrypt` 在 Base64 解码成功后先访问 `decoded[0]`，随后才检查解码长度。Go 的标准 Base64 解码忽略 CR/LF，所以 `strings.Repeat("\r\n", 66)` 虽然有 132 字符，解码结果仍为空。在 PR #93 保护加入前，隔离探针已实际复现 SDK 和 Hyphae `DecryptMessage` 的 `index out of range [0] with length 0` panic；未改动 module cache。

上游最小修正：将现有解码长度检查移到 `decoded[0]` 访问之前，保留已支持的 payload 和 CR/LF 包裹行为。为仅含 CR、LF、CRLF 的最小长度及更长输入补回归测试，要求返回错误且不 panic；有效密文及错误 MAC/版本案例继续通过。需要注明无效输入的报错优先级变化。

Hyphae [PR #93](https://github.com/iDoris-ai/Hyphae/pull/93) 在生成 conversation key 后、调用 SDK 前拒绝 CR/LF 去除后为空的输入，并测试有效包裹密文仍可解密。这仅保护 Hyphae 入口；直接调用 SDK 的消费者仍需上游修正。此项与 MAC 常数时间比较、最大输入限制分别验收，不以其中一项通过替代其他项。

## 双方验收

上游按官方 NIP-44 vectors 验证正常、错误密钥、错误 MAC、截断、padding 和扩展长度前缀，确认 MAC 比较与空解码结果保护的各自范围。使用经过审阅的 `hmac.Equal` 是实现依据；重复计时实验不能单独证明或否定常数时间性质。

Hyphae 升级后运行 crypto 单元、现有消息回归、真实 CLI/relay integration 和相关 race 检查，固定 SDK 与双方二进制版本。已有 16/32/64 KiB 同库加解密仅证明该 Go wrapper 的边界兼容；跨语言、旧密文 fixture 和此项标准符合性各自记录。

此外，当前 SDK 解密前没有最大 Base64 输入限制。Hyphae 新接收入口需要应用级有界检查；这和上游 MAC 比较修正是两个问题，不能以其中一项通过替代另一项。

待回填：上游修正链接/commit、发布 module 版本、Hyphae 升级 PR、旧事件与跨语言样例结果。目前不声称差异已消除或已证明存在可利用的攻击。
