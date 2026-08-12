# Daemon 使用指南

## 手动启动

```bash
# 前台运行（适合测试）
hyphae daemon --identity alice

# 后台运行（使用 nohup 或 &）
nohup hyphae daemon --identity alice > ~/.hyphae/daemon.log 2>&1 &
```

## 配置自动启动（macOS）

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
