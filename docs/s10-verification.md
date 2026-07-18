# S10 插件管理、配置与商店验收记录

验收日期：2026-07-18  
版本：`0.9.0`  
上游审计基线：`b5a050ebd319341bddc4189491c90c22162d33fa`

## 1. 交付范围

- 声明式 `plugin.json`、S09 `module.json` 和逐文件 SHA-256 门禁；不执行 Lua、PowerShell、EXE、MSI 或远程安装脚本。
- 本地插件发现、启停、别名、稳定排序、多插件有序注入和默认开启的安全模式。
- schema 控制的 INI 配置、预设、原子写入和损坏配置隔离。
- 本地 ZIP 与 HTTPS 商店包的下载、ZIP 路径/链接/膨胀限制、PE/版本/导出和当前游戏兼容复核。
- 安装、修复、更新、版本归档、回滚、二次确认卸载和崩溃事务恢复。
- 显式 HTTPS 插件目录、来源缓存校验、搜索、分类、排序、分页及已安装/修复/更新状态。
- 插件活动目录固定为 `data\injection\modules`；状态、缓存、版本和 staging 固定为 `data\plugins`。

上游当前 Lua 商店协议和公开端点没有被采用；默认目录 URL 为空，程序不会自动联网，也不随包发布第三方插件。

## 2. 安全与故障门禁

| 场景 | 结果 |
|---|---|
| `..`、绝对路径、ADS、重复 ZIP 项、symlink、junction/reparse point | 拒绝 |
| 文件大小、包大小、展开总量、膨胀比例或 SHA-256 不符 | 拒绝且不修改活动插件 |
| manifest、来源、许可证、能力范围、DLL PE 或游戏兼容不符 | 拒绝 |
| account/login/token/gacha/checkin/BBS/news/browser/data-center/calculator 等排除能力 | 目录或包校验拒绝 |
| 目录断网、HTTP 错误、损坏 JSON、跨来源缓存 | 保留既有缓存和本地插件，不阻止主程序 |
| HTTPS 跳转降级或目录跳转改变来源 origin | 拒绝 |
| 安装/更新/回滚中断 | transaction journal 恢复旧活动版本或完成已提交清理 |
| 卸载中断 | 状态提交前恢复隔离目录，提交后完成版本和 staging 清理 |
| 手工放入但没有受管安装状态的插件 | 可发现和停用，但拒绝自动删除 |
| 任一启用插件的 helper 复核或注入失败 | 不恢复游戏主线程，终止该次挂起进程 |
| 安全模式开启 | 跳过插件注入；主程序和 S05 纯净启动仍可用 |

## 3. 自动化验证

2026-07-18 执行：

```powershell
go test ./... -count=1
go test -race ./internal/plugins ./internal/injection ./internal/config ./internal/shell -count=1
./scripts/build.ps1 -Configuration Both -Version 0.9.0
./scripts/verify-artifact.ps1 -Version 0.9.0
./scripts/test-s09-injection.ps1
./scripts/test-s02-shell.ps1 -StressIterations 2
```

结果：

- 全仓测试通过；插件、注入、配置和 shell 关键竞态测试通过。
- 自有进程/DLL fixture 的 manifest、PE、helper 协议、多模块远程加载和清理路径通过。
- 单实例激活、最小化到托盘、托盘恢复、损坏配置隔离、上次异常退出检测和 2 轮短压力测试通过。
- `git diff --check` 通过。

## 4. 正式产物

| 文件 | PE subsystem | FileVersion | ProductVersion | 图标 |
|---|---:|---:|---:|---|
| `GenshinTools-debug.exe` | Console (3) | `0.9.0.0` | `0.9.0` | 通过 |
| `GenshinTools.exe` | Windows GUI (2) | `0.9.0.0` | `0.9.0` | 通过 |
| `GenshinTools-injector.exe` | Console (3) | `0.9.0.0` | `0.9.0` | 通过 |

`scripts/verify-artifact.ps1` 同时确认 `data\plugins\versions`、`data\plugins\staging`、注入目录和基础便携目录存在，`build-info.json` 为 `windows/amd64`、版本为 `0.9.0`。

## 5. 保留到 S13 的人工验证

- 未在真实原神进程中加载任何第三方 DLL，也未连接或安装来源不明插件。
- 真实游戏版本、反作弊环境、多插件组合、长时间运行、休眠/唤醒和杀软隔离场景，只能在用户明确选择可信插件后进行。
- 以上人工项不削弱默认安全路径：插件安全模式默认开启，注入默认关闭，纯净启动不依赖插件系统。

S10 退出门禁通过，下一阶段为 `S11：程序设置、语言与 UI 收尾`。

## 6. 完成后复审修正

2026-07-18 对 S00～S10 逐阶段复审后，补充关闭以下两项真实问题：

- transaction journal 现在按 install/uninstall 分别限制合法 phase；卸载备份只能是当前 `staging\<stage>\removed`，安装备份只能是对应版本目录或当前 staging 的 `previous`。篡改备份位置和混用 phase 均有拒绝测试。
- 插件页、插件商店和侧栏导航改用当前客户区可用高度；绘制、点击命中区和 EDIT 子控件共享同一压缩坐标，最小窗口及高 DPI 下不再因固定逻辑坐标裁掉入口。

修正后重新执行项目标准门禁以及 `go test -race ./internal/plugins ./internal/shell -count=1`，均通过。
