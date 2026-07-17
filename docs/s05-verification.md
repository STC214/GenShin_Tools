# S05 纯净启动与启动设置验收记录

状态：**完成。**

## 行为边界

- 只使用游戏主 EXE、显式工作目录和用户确认的参数启动。
- 不加载 DLL、不扫描插件、不启动附加程序、不调用 BetterGI，也不请求管理员权限。
- 启动前按完整 EXE 路径检查既有进程；已运行时拒绝重复创建，不接管进程。
- 只有本次成功创建的进程标记为 `Owned`；启动器关闭只停止观察并回收句柄，绝不终止游戏。
- 桌面快捷方式直接指向游戏 EXE，包含相同参数、工作目录和游戏图标。

## 参数依据

Windows 启动遵守 [CreateProcessW](https://learn.microsoft.com/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw) 的完整应用路径、可写命令行和显式当前目录约束。分辨率、显示器和窗口模式采用 [Unity Player command-line arguments](https://docs.unity3d.com/Manual/PlayerCommandLineArguments.html) 中的 `-screen-width`、`-screen-height`、`-screen-fullscreen`、`-monitor`、`-popupwindow` 和 `-window-mode`。快捷方式使用 [Windows Shell Links](https://learn.microsoft.com/windows/win32/shell/links) 指定的 `IShellLinkW` 与 `IPersistFile`。

上游锁定提交中的分辨率、弹出窗口、显示器、自定义参数和启动后行为得到保留；没有照搬基于正则替换和空格切分的参数处理。

## 已实现

- `Idle / Starting / Running / Exited / Failed` 单一启动状态机和 generation 隔离。
- 普通启动失败、访问拒绝、快速退出、非零退出代码和重复点击均恢复为可再次操作状态。
- 路径带 Unicode/空格时使用绝对 EXE 路径和显式游戏工作目录。
- 自定义参数经 `CommandLineToArgvW` 解析；最终 argv 交给 Go/Windows 安全转义。
- 游戏默认、独占全屏、窗口、无边框四种模式。
- 1280×720、1920×1080、2560×1440、3840×2160和游戏默认分辨率。
- 默认显示器或实际显示器 1～N，Unity 显示器编号保持 1 起始。
- 启动后保持、最小化到托盘、保存状态并退出。
- 原生暗色 `EDIT` 自定义参数框，最大 8192 字符。
- 原生 `.lnk` 桌面快捷方式；目标、参数、工作目录、图标和描述均写入。
- schema 3 → 4 迁移；同时修复 UTF-8 BOM 配置被误隔离的问题。

## 自动化结果（2026-07-17）

```powershell
./scripts/test.ps1 -Race
./scripts/test-s05-launch.ps1
./scripts/capture-s04-game.ps1 -OutputPath build/s05-launch.png
```

| 门禁 | 结果 |
|---|---:|
| Windows 引号、空格、反斜杠、内嵌引号 | 通过 |
| Unicode/空格 EXE 路径和显式工作目录 | 通过 |
| 四种窗口模式、分辨率和显示器参数 | 通过 |
| 已运行、重复启动、启动失败、快速退出 | 通过 |
| 启动器关闭不终止游戏 | 通过 |
| Unicode `.lnk` 创建 | 通过 |
| GUI → 真实夹具进程 13 个 argv 端到端 | 通过，夹具退出代码 23 后启动器正常 |
| schema 4 与 UTF-8 BOM 配置 | 通过 |
| 全项目普通测试与 race | 通过 |

页面截图：[s05-launch.png](../build/s05-launch.png)（`build/` 是本地忽略目录，可由脚本重新生成）。

## 发布前复查

- 在真实游戏上分别抽检全屏、窗口、无边框和多显示器落点。
- 对必须管理员运行的游戏副本确认显示 `ERROR_ELEVATION_REQUIRED` 类错误，不自动提权。
- 登录到路径重定向的桌面环境，确认 `.lnk` 写入 `FOLDERID_Desktop` 返回位置。

这些实机组合保留到 S13；S05 的开发门禁已关闭。
