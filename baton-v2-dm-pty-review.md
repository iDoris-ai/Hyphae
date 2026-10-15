# Baton v2 Wave1：DM PTY 验收工装源码审查（只读）

- task_id: `v2-wave1-dm-pty-20261015`
- attempt_id: `a67c88e5-ecc2-4f30-8087-9e1a3a83761e`
- base_sha: `01294a52ec597eec1c213b97a2ca8de7fc5b9795`（clone 后 `git rev-parse HEAD` 与 `origin/main` 均等于该值）
- branch: `baton/grokbot-v2-20261015-dm-pty`
- 审查范围（只读）：`scripts/tui_offline_pty_acceptance.py`（863 行）、`scripts/test_tui_offline_pty_acceptance.py`（67 行）、`docs/agent/m1-tui-offline-pty-acceptance-20261007.md`（71 行）。该 runner 最后一次改动为 `652fd12`（#143）。
- 方法：一次源码阅读 + `rg` 检索。**没有**运行 harness、自测、仓库脚本或 hook，没有构建产品，没有探测运行时。下文“证据”指源码行；标为“推断”的部分依赖 CPython 标准库行为，本次未执行验证。

## 结论摘要

发现 3 个真实缺陷（1 个隐私/脱敏，1 个清理，1 个 fork 安全），外加 1 个信号路径清理缺口。均可用小补丁 + 不启动产品/PTY/relay 的单元测试覆盖。PID 归属（只对自己创建的精确 PID 发信号）、临时目录权限、evidence 文件防覆盖/不落 HOME 这几条不变量在正常路径上成立。

---

## D1（高）失败路径脱敏不成立：`str(exc)` 原样写入 evidence 和 stdout

**证据**
- `scripts/tui_offline_pty_acceptance.py:808-811`：注释写“never the raw exception payload”，但实际执行 `evidence.update({"status": "fail", "failure": str(exc)[:240]})`，接着 `write_evidence(...)` 和 `print(json.dumps(evidence))`。
- 可能抛出非 runner 自写异常的调用点：
  - `:293-295` `run_cli` 使用 `subprocess.run(..., timeout=timeout)`，超时抛 `subprocess.TimeoutExpired`；二进制不可执行时抛 `PermissionError`（`:606` 只检查 `is_file()`，不检查可执行）。
  - `:413-415` `verify_event_signature` 把**完整签名 event JSON** 当作 argv（`[str(binary), "verify", event_json]`），`timeout=15`。
  - `:528-529` `independent_inbox_probe` 调 `run_cli(... "--relay", relay_url ...)`，但 `:540` 只捕获 `(RuntimeError, json.JSONDecodeError, AttributeError)`，`TimeoutExpired`/`OSError` 会逃出。这个 probe 是在 `:753` 的 `except RuntimeError` 块里调用的（`:775-776`），逃出的异常会**替换掉** `:756-763` 精心构造的 `safe_diag`。
- 文档承诺（`docs/agent/m1-tui-offline-pty-acceptance-20261007.md:34`）：summary 不写 npub、密文、raw relay/PTY 日志，event ID 只写截断 SHA-256，失败输出“不透传” CLI 细节。`:664`、`:698` 也特意只写了 relay URL 和 event ID 的 hash。

**影响（推断，依据 CPython `TimeoutExpired.__str__` = `"Command '<argv repr>' timed out after N seconds"`）**
- verify 超时时，failure 字段会包含二进制绝对路径（`--bin-dir` 由调用者提供，可能是私有路径/含用户名），以及 event JSON 开头部分（通常是明文 `id`，可能还有 `pubkey`），违反“event ID 只写 hash”。
- inbox probe 超时时会写出 loopback relay URL 和端口（其他地方都只写 hash），同时丢掉 receiver 诊断结论。
- `PermissionError` 会写出调用者提供的 `--bin-dir` 路径。

**最小补丁**
```python
def redacted_failure(exc: BaseException) -> str:
    # Runner-authored RuntimeError messages are fixed, generic stage text.
    if type(exc) is RuntimeError:
        return str(exc)[:240]
    return f"{type(exc).__name__} (details suppressed)"
```
- `:809` 改为 `"failure": redacted_failure(exc)`。
- `:540` 的捕获元组加上 `subprocess.TimeoutExpired, OSError`，让 probe 按设计返回全 False 的字典，保住 `safe_diag`。

**受限回归测试**（只用 mock，不启动任何进程）
- `redacted_failure(subprocess.TimeoutExpired(["/private/x/hyphae", "verify", '{"id":"' + "a"*64 + '"}'], 15))` 的结果不包含 `/private`、`"id"`、`aaaa`；`redacted_failure(RuntimeError("local relay did not start listening"))` 原样保留。
- `mock.patch.object(runner, "run_cli", side_effect=subprocess.TimeoutExpired(["x"], 20))` 后，`independent_inbox_probe(...)` 返回 `command_succeeded=False, exact_event_count=0`，不抛异常。

---

## D2（中）teardown 没有隔离异常：一个子进程停不掉，其余子进程就泄漏

**证据**
- `:802-807`：
  ```python
  finally:
      if sender_tui: sender_tui.stop()
      if receiver_tui: receiver_tui.stop()
      relay.stop()
  ```
- `:223-229` `PtyProcess.stop()` 在 SIGKILL 后 2 秒仍未回收时会故意 `raise RuntimeError("owned TUI child did not stop after SIGKILL")`；自测 `scripts/test_tui_offline_pty_acceptance.py:35-48` 专门锁定了这种“失败即关闭”的行为。
- `:271` `Relay.stop()` 里的 `self.proc.wait(timeout=2)` 在 kill 之后也可能抛 `TimeoutExpired`。

**影响**：如果 `sender_tui.stop()` 抛异常，`receiver_tui.stop()` 和 `relay.stop()` 都不会执行。随后 `TemporaryDirectory`（`:592`）会在 receiver TUI 和 relay（`:249` `start_new_session=True`，不会收到终端信号）仍在运行时删除它们的 HOME/data dir。relay 会占住端口，成为孤儿进程。这与文档 `:36`“仅清理它自己创建的 PTY 子进程和 relay 子进程”的承诺相反。触发条件少见（例如子进程卡在 D 状态），但这正是作者已经写测试覆盖的路径。

**最小补丁**：抽一个辅助函数，逐个尝试停止，最后再统一抛错。
```python
def stop_owned(*children) -> None:
    errors = []
    for child in children:
        if child is None:
            continue
        try:
            child.stop()
        except Exception as exc:  # keep stopping the rest
            errors.append(exc)
    if errors:
        raise RuntimeError("owned child cleanup failed") from errors[0]
```
`:802-807` 改为 `finally: stop_owned(sender_tui, receiver_tui, relay)`。

**受限回归测试**：三个 `mock.Mock()`，第一个的 `stop.side_effect = RuntimeError`；断言三个的 `stop` 都被调用了一次，并且抛出的是 `RuntimeError("owned child cleanup failed")`。

---

## D3（中，可达性低）`pty.fork()` 子进程 exec 失败后没有 `os._exit`，会在子进程里执行父进程的清理

**证据**
- `:178-184`：`self.pid, self.fd = pty.fork()`；子进程分支里执行 `os.execvpe(argv[0], argv, env)`，没有 `try/finally: os._exit(127)`。

**影响（推断）**：如果 exec 抛出 `OSError`（例如在 `:625` 那次 `subprocess.run` 成功之后二进制被替换、ENOMEM/E2BIG），fork 出来的 Python 副本会继续 unwind 父进程的栈：
1. 在 `:647` 启动 receiver 时，子进程的 `finally`（`:803-804`）会对 sender TUI 的 PID 发 SIGTERM/SIGKILL，而这个 PID 并不属于子进程；之后 `waitpid` 会得到 ECHILD。
2. 子进程里 `TemporaryDirectory.__exit__` 会删除父进程仍在使用的整个临时根目录（HOME、relay data、构建出的二进制）。
3. `:808-810` 在子进程里先用 `"x"` 模式创建 evidence 文件，写入的 failure 可能带路径。父进程随后自己调用 `write_evidence` 时会遇到 `FileExistsError`。这发生在 `except` 块内，没有被捕获，结果是 traceback 退出，磁盘上留下的是子进程那份 evidence。

**最小补丁**
```python
if self.pid == 0:
    try:
        os.environ.clear(); os.environ.update(env)
        os.execvpe(argv[0], argv, env)
    finally:
        os._exit(127)
```

**受限回归测试**：`mock.patch.object(runner.pty, "fork", return_value=(0, -1))`，`os.execvpe` 设为抛 `OSError`，`os._exit` 设为抛一个哨兵异常。断言 `runner.PtyProcess(["x"], {})` 抛出哨兵异常，且 `_exit` 收到参数 127。不会真的 fork。

---

## D4（低）没有 SIGTERM 处理：外部超时终止 runner 时会留下孤儿进程和私有临时数据

**证据**：`rg "signal\.signal|atexit"` 在 runner 中没有任何结果。清理只依赖 `:592` 的 `with TemporaryDirectory` 和 `:802` 的 `finally`。

**影响（推断）**：SIGTERM 的默认动作是直接终止 Python，不执行 `finally` 和 `__exit__`。比如 runner 被 `timeout`/CI 取消时：relay 因为 `:249` 在独立 session 里会继续运行；临时 HOME（含新建 identity 的私钥材料，权限 0700）和 relay data 会留在 `/tmp`。TUI 子进程大概率会在 PTY master 关闭后收到 SIGHUP 退出，但这一点没有被保证。

**最小补丁**：在 `run_offline_acceptance` 开头加上 `signal.signal(signal.SIGTERM, lambda signum, frame: (_ for _ in ()).throw(SystemExit(128 + signum)))`，或者用一个等价的具名函数抛 `SystemExit`，让现有的 `finally` 和临时目录清理照常运行。`SystemExit` 不是 `Exception`，所以不会写一份误导性的 fail evidence。

**受限回归测试**：`mock.patch.object(runner.signal, "signal")`，并把 `run_offline_acceptance` 内部第一个耗时调用（如 `tempfile.TemporaryDirectory`）mock 掉。断言安装了 `SIGTERM` handler，且调用该 handler 会抛 `SystemExit(143)`。

---

## 已核对且成立的不变量（无需改动）

- **PID 归属**：`PtyProcess.stop`（`:217-234`）只对 `pty.fork` 返回的精确 PID 发信号；`Relay.stop`（`:262-271`）只对自己的 `Popen` 发信号，`:269` 有注释说明。没有 `killpg` 或按名字杀进程，也不触碰外部服务或 daemon。
- **进程组**：`pty.fork` 会让子进程成为新 session 的 leader；relay 使用 `start_new_session=True`。teardown 只对 leader PID 发信号，如果 TUI 再派生孙进程，不会被显式清理。这需要读产品源码才能确认，超出本次 read scope，因此**不列为缺陷**，仅作为残余风险。
- **临时目录权限与寿命**：`TemporaryDirectory` 本身就是 0700，`:594` 再次 chmod 0700；`:596`、`:599`、`:641` 的子目录都用 `mode=0o700`。子进程都在 `with` 块内停止（`:802`），D2–D4 的路径除外。
- **evidence 文件**：`:578` 用 `"x"`（O_EXCL）防覆盖和防最终一层 symlink；`:579` 在写入内容之前 chmod 0600；`:830-842` 拒绝写到调用者的 HOME 下或已存在的文件。小建议是把 `os.chmod(output, ...)` 改成 `os.fchmod(stream.fileno(), ...)`，但当前写法没有泄漏窗口，不算缺陷。
- **正常路径脱敏**：成功路径的 evidence（`:790-801`）和 pre-send/queued 摘要只包含 hash、布尔值和计数；`run_cli` 失败只暴露子命令名和退出码（`:296-298`）。
- **opt-in 门控**：不带 `--execute` 时返回 77 / `expected_gap`（`:847-851`）；`--bin-dir` 要求 40 位 hex commit 和两个 64 位 hex SHA（`:852-858`），并在启动前复核（`:617-622`）。

## 次要观察（不是缺陷，不建议本轮处理）

- `wait_screen`（`:315`）只在 `proc.pid == 0` 时判定 TUI 已退出，而 `pid` 只有 `wait()` 会清零。TUI 提前退出时，`drain` 遇到 EIO/EOF 会立即返回，循环空转到超时，报出的是“timed out”而不是“exited”。这只影响诊断精度。
- `free_port()`（`:302-305`）到 relay bind 之间存在端口竞争；`Relay.start`（`:252-258`）的就绪判定只做 connect，不验证监听者就是自己的子进程。概率很低，后续的签名、event 精确匹配会失败关闭。

## 建议的下一步

需要协调者授予新的 scoped 实现权限后，才能在新分支上应用 D1–D4 的补丁，并把上面的 mock 测试加到 `scripts/test_tui_offline_pty_acceptance.py`。只运行 `python3 scripts/test_tui_offline_pty_acceptance.py`，不加 `--execute`。本次没有改动任何源码。
