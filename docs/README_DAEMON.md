# Daemon 使用指南

## 手动启动

```bash
# 前台运行（适合测试）
agent-speaker daemon --identity alice

# 后台运行（使用 nohup 或 &）
nohup agent-speaker daemon --identity alice > ~/.agent-speaker/daemon.log 2>&1 &
```

## 配置自动启动（macOS）

创建 `~/Library/LaunchAgents/com.agent-speaker.daemon.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.agent-speaker.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/agent-speaker</string>
        <string>daemon</string>
        <string>--identity</string>
        <string>alice</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/Users/YOURNAME/.agent-speaker/daemon.log</string>
    <key>StandardErrorPath</key>
    <string>/Users/YOURNAME/.agent-speaker/daemon.error.log</string>
</dict>
</plist>
```

加载服务：
```bash
launchctl load ~/Library/LaunchAgents/com.agent-speaker.daemon.plist
```

## 检查 Daemon 状态

```bash
# 查看日志
tail -f ~/.agent-speaker/daemon.log

# 查看进程
ps aux | grep "agent-speaker daemon"
```
