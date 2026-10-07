# Hyphae 安装、relay 与邀请体验计划（2026-10-07）

状态：设计与 issue 排期，尚未实现理想体验。跨仓交接直接通过下列 issue 互链，不需要用户手工转发。

## 范围、编号与当前状态

当前优先项仍是 **M1 群聊加密通信与 TUI 离线发送（P0）**；下列安装、relay 与邀请体验排在该 P0 完成之后，不得抢占或冒充已完成的 M1 验收。

本文区分两套里程碑编号：Hyphae [`docs/milestones/roadmap-v2.md`](https://github.com/iDoris-ai/Hyphae/blob/main/docs/milestones/roadmap-v2.md) 的 **M1.5** 是 relay 自部署/花名册（当前文档记载代码任务已完成，但 `relay-khatru` 仓库仍待创建，deploy 脚本仍有已知问题）；**M2.5** 是 relay 间接力、漂流瓶与支付，尚未开始。Hyphae 单 relay QR/URL 邀请归 M1.5；跨 relay 邀请、发现或扩展归 M2.5。

Agent24 的 **C3 M1.5** 在 [`COMPONENT-ROADMAP.md`](https://github.com/iDoris-ai/Agent24/blob/main/docs/agent/COMPONENT-ROADMAP.md) 中专指个人记忆可控，不能改义或借用作 Hyphae M1.5。Agent24 工作使用自己的 **COMM / DEP** 编号；当前 [`COMM-HYPHAE.md`](https://github.com/iDoris-ai/Agent24/blob/main/docs/design/COMM-HYPHAE.md) 与 [`Deployment/TASKS.md`](https://github.com/iDoris-ai/Agent24/blob/main/docs/Deployment/TASKS.md) 已有 COMM-6/7、DEP-C8/C9 占位。

目前已有 Hyphae identity/contact、NIP-44 消息、TUI、daemon 和群聊基础；Agent24 `agent24d` 提供本机 token 鉴权的 `/api/v1/comm/*`，由它经 CLI 管理隔离 Hyphae HOME。它不是 Hyphae Go daemon 的 REST API。当前没有本文描述的可信安装向导、发行兼容 manifest、QR 邀请安全确认流程、官方 relay 运维交付，也没有已验收的私有 relay 授权邀请。Hyphae roadmap 记载默认 relay 行为，Agent24 的 COMM 设计仍把 `source=default` 当作未配置，不应混成“官方默认已确认可用”。

## 理想体验与安全边界

- 普通用户由 Agent24 安装/升级流程取得来自可信 release tag 的 Hyphae 发行物；机器可读兼容 manifest 列出版本、源码 SHA、协议版本、OS/架构、产物 SHA-256 与签名/校验根。首批至少 `darwin-arm64`、`linux-x64`。安装先下载到 staging，验证来源、签名/hash 和平台，再原子切换版本；升级失败可回滚，旧版不会被提前覆盖。
- 首次设置默认选官方 relay，也能随时替换；自建 relay 是可见的高级选项。URL 先规范化/验证，配置后显示 daemon 的真实状态、所用版本、relay 与 generation；配置变更经过校验后重启，启动失败清楚显示原因并允许回滚。口令只经系统 keychain 与 stdin，Hyphae 使用专用隔离 HOME；Agent24 只通过本机 token REST `/api/v1/comm/*` 协调，不直接读写该 HOME。
- 单 relay 邀请可用 QR/URL 分享；扫码只解析并展示预览，必须由用户明确确认后才加 relay。预览展示完整 canonical `wss://` host、issuer pubkey 指纹、联系人匹配/未知状态、有效期与数据暴露/保留提示。签名只证明 issuer 控制对应私钥，不证明现实身份、联系人可信或 relay 所有权。
- 邀请载荷使用 versioned、bounded、严格字段的格式。拒绝未知版本、重复字段、过期、篡改、userinfo、危险 scheme；不自动跟随 redirect、不探测任意 URL，避免 SSRF。公网 `wss://` relay 可走普通确认；私网/loopback 仅高级选项且再次显式确认。邀请绝不含私钥、Agent24 token、管理密钥。
- 公网 relay 邀请码不是 ACL。私有 relay 还需服务器端验证邀请者权限、短期可撤销且绑定 recipient/nonce 的授权并防重放；不得声称当前已有 NIP-42。官方 relay 的 TLS、容量/限流、滥用处置、元数据保留、监控、备份/恢复和隐私说明是独立运维交付；本计划不部署生产节点。

## 契约设计与验收边界

发行契约以 reviewed lock 为锚：固定 tag/source、OS/arch、产物 hash、CLI JSON/daemon 协议版本、兼容范围、来源与信任根。下载源自己的 checksum 只能证明一致性，不能独立证明真实性；邀请载荷无权改变安装源。Agent24 首发选择“发行包内置”还是“固定可信源下载”由 DEP-C9 记录决策，两者都要先校验、再原子激活，不接受浮动 latest。并发安装/中断/坏包/不支持平台必须保留上一已验证版本；macOS 签名/notarization/quarantine 依赖现有 DEP-B，未通过干净机器验收不能关闭。

邀请 v1 的设计候选是有界 URI/QR 编码的签名载荷：版本、canonical relay URL、issuer pubkey、issued_at/expires_at、公开/私有模式；私有模式另带目标公钥、nonce、scope 与非 bearer 的 grant 引用。编码上限、规范化字节规则、签名域隔离与算法必须在 H-INVITE PR 中冻结，不能用未冻结格式发布互不兼容客户端。私有服务器须独立验证签发权限和受邀者持钥证明，绑定节点/目标/有效期，并验证撤销与防重放；公开邀请可重复导入但配置幂等，不虚构服务器 ACL。

预览展示完整 hostname、公钥指纹及信息来源；IDN 同时显示 ASCII/punycode，控制字符安全渲染。TLS 有效不代表运营者可信；证书不可信不能悄悄跳过。确认前零网络副作用，确认后探测限定已授权目标，检查解析出的地址并防止重定向/DNS 重绑定绕过私网确认。用户同意添加 relay 不是同意切换默认、公开 profile、导入身份或运行任务。

## 当前实现与目标体验

| 事项 | 当前事实 | 本计划交付 |
|---|---|---|
| 发布 | v0.26.1 已正式发布；不含后合入的 #135 | 正式产物、版本/协议可查询、兼容与回滚契约 |
| Agent24 接入 | CLI JSON + 托管 daemon；本机 REST 在 agent24d | 无需 Go/终端的可信安装、向导与生命周期 UI |
| 版本锁 | 正式发布不等于生产锁已升级 | DEP-C8 使用正式产物双平台验证，再 DEP-C9 发包 |
| 接入与邀请 | 本地 relay 可启动；没有本计划完整邀请/运维验收 | 可替换官方默认、自建、扫码预览确认、私有授权 |
| M1 聊天 | #135 已合；新群协议/状态与离线闭环仍在 PR 收尾 | 按 M1 发布计划完成真实用户闭环后再进入本计划 |

“接入实现”是代码，不是已完成安装体验的文档；“发布待办”是未完成任务清单。本文是统一设计与执行索引，细分开发由两仓 issue 承担，不要求用户另行转发两个材料。

## 可执行任务与 GitHub issue（均保持 open，完成验收后关闭）

| ID / 所属里程碑 | 仓库 / 负责人 | 依赖 | 可执行验收 |
|---|---|---|---|
| **H-INSTALL**（Hyphae M1.5）发行兼容 | Hyphae | 当前 M1 P0 后；release pipeline | 可信 tag 产出兼容矩阵和签名/校验 manifest；至少 darwin-arm64、linux-x64 从干净环境下载并验 hash/signature；错误平台/损坏文件拒绝；发布与回滚步骤可重现。[139 · H-INSTALL](https://github.com/iDoris-ai/Hyphae/issues/139)。 |
| **H-INVITE**（Hyphae M1.5）单 relay 邀请协议 | Hyphae | 单 relay M1.5；邀请格式先冻结 | 编解码有大小/版本上限；有效邀请显示 canonical host、issuer 指纹/信任状态、expiry 与隐私提示；覆盖未知版、重复 key、过期、签名/内容篡改、userinfo、非 wss、redirect/SSRF 拒绝；scan/parse 本身不产生副作用。[142 · H-INVITE](https://github.com/iDoris-ai/Hyphae/issues/142)。 |
| **H-RELAY**（Hyphae M1.5；跨 relay 扩展归 M2.5）自建/私有接入 | Hyphae（relay 部署可独立仓库） | H-INSTALL；M1.5 relay-khatru 决策 | 普通用户可部署单 relay 并从客户端显式添加；私有 relay 需 server-side 权限、recipient/nonce 绑定、短期撤销和重放测试。文档清楚区分单 relay 与 M2.5 relay mesh；不把公开邀请当 ACL。[140 · H-RELAY](https://github.com/iDoris-ai/Hyphae/issues/140)。 |
| **H-OPS**（官方节点运维，不等于协议里程碑） | Hyphae 运维/relay 仓库 | 官方 relay owner 与运行预算决策 | TLS、容量与限流、滥用响应、日志/元数据保留策略、监控告警、备份恢复演练和用户隐私说明齐备；未完成前不宣称官方默认 relay 可用。[141 · H-OPS](https://github.com/iDoris-ai/Hyphae/issues/141)。 |
| **A-LOCK / DEP-C8**（Agent24） | Agent24 | H-INSTALL manifest/兼容承诺 | `hyphae.lock.json` 固定可信源码 SHA、平台构建配方/hash 与支持版本；lock verify 对不匹配产物失败。只在该任务中更新锁，不隐式扩大当前锁定二进制的验收范围。[751 · A-LOCK](https://github.com/iDoris-ai/Agent24/issues/751)。 |
| **A-INSTALL / DEP-C9**（Agent24） | Agent24 | A-LOCK；H-INSTALL 可信产物 | macOS/Linux 包含 Hyphae 或按 manifest 可信下载；staging 校验后原子安装；安装篡改、平台不兼容、半下载失败均不覆盖当前版本；回滚验证；干净机器启动 `agent24 comm`。[749 · A-INSTALL](https://github.com/iDoris-ai/Agent24/issues/749)。 |
| **A-SETUP / COMM 配置生命周期**（Agent24） | Agent24 | COMM-6 基本 UI、A-INSTALL | 首次配置清楚展示官方默认/自定义 relay 来源；默认值可替换；daemon start/stop/status、配置变更重启、generation/失败原因、升级失败回滚可经本机 token API/UI 验证；口令不进 argv/log/config，HOME 隔离。[752 · A-SETUP](https://github.com/iDoris-ai/Agent24/issues/752)。 |
| **A-INVITE / COMM 邀请 UI**（Agent24） | Agent24 | H-INVITE 稳定协议；群聊 P0 完成 | 扫码/打开 URL 仅显示受限预览；展示完整 host、issuer 指纹、联系人信任状态、expiry/隐私提示；只有显式确认才添加 relay，不自动切默认 relay、不发布 profile、不授予任务执行；恶意与私网 URL 按 H-INVITE 拒绝/二次确认。[750 · A-INVITE](https://github.com/iDoris-ai/Agent24/issues/750)。 |

**建议依赖顺序**：M1 群聊离线 P0 → H-INSTALL / H-INVITE / H-RELAY 与 A-LOCK → A-INSTALL / A-SETUP → A-INVITE；H-OPS 在选择正式官方节点后独立运行。M2.5 的 relay 间接力、跨 relay 邀请发现、漂流瓶与支付不得提前塞进 M1.5 的完成声明。每个新任务由对应仓库 issue 维护；Agent24 原编号 COMM/DEP、Hyphae 原编号 M1.5/M2.5 均保持不变。

初始官方节点运维 H-OPS 作为 Hyphae M1.5 的产品交付依赖；规模化/联邦运维归 M2.5。无需等待未来扩展版 manifest 才能验收现有正式发行：A-LOCK 可先根据当前正式 tag、manifest、生产产物完成严格 DEP-C8 升级，再补新的版本兼容契约。

**统一关闭标准**：issue 具备实现 PR、精确 head 测试、PR-Daemon approve、所需 CI 通过与已合入证据；涉及发布/安装的另附正式 tag 和两平台干净机器验收，涉及官方服务的另附 owner、获授权上线与运行/恢复演练证据。没有跨仓前置或只有候选 CI/文档不得算完成；本批 issue 均保持 open，本文提交不关闭任何开发 issue。

**下一阶段门槛**：先按 [M1 发布计划](https://github.com/iDoris-ai/Hyphae/pull/137) 完成群聊应用、历史/outbox、真实三用户与离线验收并走审核/发行；随后核对两仓 milestone/task 实际状态，启动发行兼容与 DEP-C8→DEP-C9，邀请协议设计可并行。M2 行为协议、M2.5 网络扩展、M3 任务委派、M4 长期自主、M5 支付信誉仍沿用原路线，不因本文提前宣告完成或启动。
