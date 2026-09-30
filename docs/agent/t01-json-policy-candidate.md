# T01-B JSON 输入策略候选 v1

本文件与共享 fixtures 定义一个供不同语言实现对照的保守输入 profile。
它是测试规范和 Go 可执行参考，不接入生产入口，不冻结 wire，也不实现 T07。
它不解除 T01；Agent24 consumer 由用户推进。

## 候选规则

- 输入按原始 UTF-8 字节检查，不允许 BOM，正文上限为 32,768 bytes。
- 字节上限先判；超长输入返回 `JSON_SIZE`。
- 接着检查原始 UTF-8 和字符串中的 Unicode scalar 与转义。
- 原始 UTF-8 必须合法；孤立 UTF-16 surrogate 和 Unicode noncharacter 均拒绝。
- noncharacter 范围为 U+FDD0..U+FDEF，以及每个平面的 U+xxFFFE/U+xxFFFF。
- 合法 U+FFFD 接受；合法 surrogate pair 解码为对应 Unicode scalar 后接受。
- 再要求恰好一个完整 JSON 值；值后只允许 SP、HT、LF、CR。
- 根必须是 object。有效但根类型不是 object 时返回 `JSON_ROOT`。
- object 和 array 都计嵌套层；根 object 是第 1 层，最多 16 层。
- string、number、boolean 和 null 等标量不计入嵌套层数。
- 每个 object 内，按解码后的 Unicode key 字符串判重。
- 因此 `"a"` 与 `"\u0061"` 是重复 key；不同 object 中同名 key 合法。
- 不做 Unicode normalization；预组合字符和组合序列可作为不同 key。
- 数字只允许规范十进制整数，范围为 `[-9007199254740991, 9007199254740991]`。
- 拒绝 `-0`、小数和指数；负的非零整数有效。
- 将来如需小数，应使用另行定义的 decimal 字符串；本 profile 不猜业务字段。
- `true`、`false`、`null`、string 和 array 均可用。
- 必填字段、未知字段、消息类型和业务 schema 留待后续定义。

## 错误码与优先级

错误码限定为：

- `JSON_SIZE`：原始输入字节数超过 32,768。
- `JSON_UNICODE`：非法 UTF-8、孤立 surrogate 或 noncharacter。
- `JSON_INVALID`：JSON 语法错误、BOM、多个值或非空白尾随内容。
- `JSON_ROOT`：语法有效，但根值不是 object。
- `JSON_DUPLICATE_KEY`：同一个 object 出现解码后相同的 key。
- `JSON_INTEGER_FORMAT`：整数位置使用 `-0`、小数或指数。
- `JSON_INTEGER_RANGE`：整数超出安全整数范围。
- `JSON_DEPTH`：object / array 深度超过 16。

优先级固定为：字节上限、Unicode 检查、完整 JSON 语法、根类型、结构遍历。
通过大小和 Unicode 检查后，任何 JSON 语法错误均返回 `JSON_INVALID`，
即使文本前部已有重复 key；孤立 surrogate 仍由更早的 Unicode 检查返回 `JSON_UNICODE`。
结构遍历按输入顺序返回首次发现的重复 key、整数错误或深度错误。

## 共享 fixtures

`tests/contracts/testdata/json-policy-v1.json` 的 profile 标识是
`hyphae-json-input-candidate/1`。每例有唯一非空 `id`、一种输入来源和 `expected`。
`expected` 只能是 `accept` 或上列错误码。

输入来源在 `input`、`input_base64`、`recipe` 中必须且只能出现一个。
`input` 是 JSON 字符串，解码后其 UTF-8 字节就是待测输入。
`input_base64` 解码为原始字节，可表达非法 UTF-8；空 Base64 也是一个来源。
`recipe` 按 UTF-8 字节串接 `prefix`、重复 `fill` 共 `repeat` 次、再接 `suffix`。
recipe 字符串不做换行、trim、字符计数换算或 Unicode normalization。

recipe 边界例的 `expected_bytes` 校验展开后的精确字节数。
例如中文 `界` 每个字符占 3 bytes；长度不能按 Unicode 字符数或 UTF-16 code unit 算。

## RFC 依据与消费端验收

[RFC 7493 §2.1](https://www.rfc-editor.org/rfc/rfc7493#section-2.1) 涉及 UTF-8、surrogate
和 noncharacter；[§2.2](https://www.rfc-editor.org/rfc/rfc7493#section-2.2) 讨论数字精度；
[§2.3](https://www.rfc-editor.org/rfc/rfc7493#section-2.3) 禁止解码后重复的 object member name。
本候选对整数格式施加更严格限制，也增加 32 KiB 上限、深度 16 和项目错误码；这些不是 RFC 要求。

Agent24 consumer 应读取同一 fixtures，并逐例得到相同 accept/error 结果。
验收还应从实际入口证明限制与错误码传递，并记录 consumer 版本和命令。
不能用复制 Go 参考代码的实现假设替代共享 fixtures。

Go 测试参考只验证测试规范层，不证明生产入口受限、验签、授权或 Go/TS 互操作。
它不是完整 JCS 实现；canonicalization 与授权 digest 仍待单独固定。
