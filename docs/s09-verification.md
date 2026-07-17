# S09 注入适配层验收记录

状态：已完成  
日期：2026-07-17  
版本：`0.8.0`

## 1. 验收环境

- Windows 11 专业工作站版 `10.0.26200` x64。
- Go `1.26.1 windows/amd64`，`CGO_ENABLED=0`。
- 上游审计基线：`b5a050ebd319341bddc4189491c90c22162d33fa`。
- 本轮起点：`547dcbbc107a`；验收时工作树包含待提交的 S09 变更。

## 2. 实现结果

- 主 UI 不加载外部 DLL；`GenshinTools-injector.exe` 独立完成提权、重复预检、suspended 创建、远程加载、恢复和结果回传。
- 模块 manifest 严格校验来源 HTTPS、许可证、SHA-256、amd64 PE、DLL 标志、VERSIONINFO/显式无版本、导出、普通/延迟导入和明确游戏版本/EXE 兼容表。
- 请求只能位于便携目录固定 staging，模块只能位于固定 modules 根；主进程和 helper 各审计一次，逐级拒绝 reparse point。helper 锁定 DLL 后复算哈希并保持句柄到加载结束，游戏 EXE 也在检查到创建之间保持只读锁，关闭预检后的替换竞态。
- helper/远程线程分别限时。自有 suspended child 先加入 kill-on-close Job；任何失败、取消或 helper 异常都不会留下挂起子进程。
- 远程加载不用会截断 x64 HMODULE 的线程退出码判定，而是按目标进程模块完整路径确认。
- UI 注入页默认关闭并要求风险确认；纯净启动按钮始终复用 S05 启动路径。

## 3. 自动验证

执行并通过：

```powershell
./scripts/test-s09-injection.ps1
go test -race ./internal/injection ./internal/launch ./internal/config ./internal/taskrunner -count=1
./scripts/build.ps1 -Configuration Both -Version 0.8.0
./scripts/verify-artifact.ps1 -Version 0.8.0
./scripts/capture-s04-game.ps1 -NavigationY 448 -OutputPath build\s09-injection.png
```

真实 Windows 集成 fixture 已覆盖 `CreateProcessW(CREATE_SUSPENDED)`、Job 所有权、`VirtualAllocEx`、`WriteProcessMemory`、远程 `LoadLibraryW`、模块路径确认、`ResumeThread` 和进程退出观察。测试只复制本项目测试 EXE 并加载 Windows `System32` DLL，不启动或注入用户游戏。

拒绝矩阵覆盖：SHA-256 不符、未知游戏版本、缺导出、文件版本不符、伪装无版本、候选 EXE 名称不符、相邻依赖劫持、越界 request/modules 路径、协议/结果异常和非法超时。初次全量 `go test ./...` 唯一失败是既有本地增强测试的瞬时临时文件 `Access is denied`；同一用例单独立即重跑通过，S09 相关包和竞态测试均通过。

构建产物验证结果：

| 产物 | 子系统 | FileVersion | ProductVersion | 图标 |
|---|---:|---|---|---|
| `GenshinTools-debug.exe` | Console (3) | `0.8.0.0` | `0.8.0` | 通过 |
| `GenshinTools.exe` | Windows GUI (2) | `0.8.0.0` | `0.8.0` | 通过 |
| `GenshinTools-injector.exe` | Console (3) | `0.8.0.0` | `0.8.0` | 通过 |

## 4. 门禁结论与保留项

S09 自动化退出门禁已关闭，稳定性清单新增 `J13`～`J19` 并在实现中逐项对照。项目未复制、执行或打包上游 `Launcher.dll`/`Launcher_2.exe`，也没有随包第三方注入模块。

真实原神、真实反作弊环境、UAC 人工取消、杀毒软件隔离 helper，以及未来具体第三方模块的兼容/许可证验证保留到 S13；这不影响当前 fail-closed 行为，因为无模块、未知版本或任何预检失败时注入均不可用，纯净启动仍可用。
