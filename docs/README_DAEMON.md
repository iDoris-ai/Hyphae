# Daemon 使用指南

## 手动启动

```bash
# 前台运行（适合测试）
hyphae daemon --identity alice

# 后台运行（使用 nohup 或 &）
nohup hyphae daemon --identity alice > ~/.hyphae/daemon.log 2>&1 &
```

## 配置自动启动（macOS）

**从 agent-speaker 升级的用户请先做这一步。** launchd 的 `Label`/plist 属于系统状态，不在本仓库控制范围内，改名 PR 改不到它；旧 plist 的 `RunAtLoad`+`KeepAlive` 都是 `true`，旧二进制改名/搬走之后它会以 launchd 的节流间隔不断重启、日志无限追加，不会自愈：

```bash
launchctl unload -w ~/Library/LaunchAgents/com.agent-speaker.daemon.plist
rm ~/Library/LaunchAgents/com.agent-speaker.daemon.plist
rm /usr/local/bin/agent-speaker   # 如果 install.sh 之前装过旧二进制
```

确认旧任务已经卸载（`launchctl list | grep agent-speaker` 应该没有输出）之后，再继续下面的步骤装新的 `com.hyphae.daemon.plist`。

创建 `~/Library/LaunchAgents/com.hyphae.daemon.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.hyphae.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/hyphae</string>
        <string>daemon</string>
        <string>--identity</string>
        <string>alice</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/Users/YOURNAME/.hyphae/daemon.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/YOURNAME/.hyphae/daemon.error.log</string>
</dict>
</plist>
```

加载服务：
```bash
launchctl load ~/Library/LaunchAgents/com.hyphae.daemon.plist
```

## 检查 Daemon 状态

```bash
# 查看日志
tail -f ~/.hyphae/daemon.log

# 查看进程
ps aux | grep "hyphae daemon"
```
