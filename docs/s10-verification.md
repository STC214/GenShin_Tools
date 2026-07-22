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
- FufuLauncher 官方商店适配、固定来源缓存校验、搜索、分类、排序、分页及已安装状态展示。
- 插件活动目录固定为 `data\injection\modules`；状态、缓存、版本和 staging 固定为 `data\plugins`。

2026-07-22 的决策复审已覆盖最初结论：插件商店唯一连接 FufuLauncher 官方公开端点，不再接受自定义目录 URL，也不随包发布第三方插件。Fufu 下载验证使用系统浏览器；程序不执行远程 Lua，而是使用内存令牌下载并受限转换官方 ZIP。

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

## 7. Fufu 商店迁移复审（2026-07-22）

- `internal/plugins/catalog.go` 改为 Fufu `/api/v1/plugins/list` 的固定适配器；分页拉取后才原子替换缓存，拒绝跨 `fu1.fun` origin 的 ZIP/Lua URL及缺失 SHA-256 的条目。
- 配置中的旧 `catalogUrl` 仅为 schema-v9 兼容读取保留，正规化时强制清空；Win32 界面已删除自定义商店 URL 输入框。
- 商店页明确显示 Fufu 来源；验证只用系统默认浏览器，没有加入 WebView2。密码样式输入框接受 `dl_token` 或完整 JSON，启动后台任务后立即清空且从不持久化。
- Fufu ZIP 适配器复核官方包长度和 SHA-256，只接受可唯一映射的 `config.ini`/根 amd64 DLL，生成本地 `plugin.json`/`module.json` 后再次执行 S09 当前游戏兼容审计和崩溃可恢复事务。
- 上游插件加载约定引用 `FufuLauncher.UnlockerIsland` 开源仓库及 MIT 许可证；不复制、执行或发布其 Launcher 二进制。商店未给出插件许可证时明确记录 `UNSPECIFIED-FUFU-STORE`，不推断再分发权。
- README 与 `THIRD_PARTY_NOTICES.md` 已加入致敬、MIT 引用、非官方关系和插件独立许可证声明。
- Fufu 实时契约、令牌/完整 JSON 解析、跨域令牌重定向拒绝、ZIP 根目录、zip-slip、DLL 元数据不符和未解析依赖 fixture 均通过。
- `go test ./... -count=1`、`go test -race ./internal/plugins ./internal/config ./internal/shell -count=1`、双配置构建、`scripts/verify-artifact.ps1 -Version 0.9.0` 与 `git diff --check` 均通过。
- `scripts/test-s02-shell.ps1 -StressIterations 1` 通过单实例、激活、托盘恢复、损坏配置恢复、异常退出检测及短压力检查。

## 8. FuFuPlugin 配置目标补审（2026-07-22）

- 人工商店安装失败暴露出两个缺口：插件后台错误此前只显示在状态栏而不落日志；配置目标中的主插件也未独立于商店适配。现已将插件任务成功/失败写入诊断日志（不记录下载令牌）。
- 按当前 Fufu 源码核实：主插件下载/修复使用 `CodeCubist/FufuLauncher--Plugins/FuFuPlugin.zip`，启停通过 `FufuLauncher.UnlockerIsland.dll` 与 `.disabled` 改名；本项目固定使用其 HTTPS GitHub 原始线路，不采用上游明文 HTTP 代理线路。
- 新增通用 Fufu INI 目标解析：读取 `[General]` 元数据和各节 `Name/Type/Value/help`，支持 `bool/int/float/string/key`，原子修改 `Value` 并保留注释、未知字段和顺序。
- 注入页新增配置目标、下载/修复和启用注入组合区；插件页根据完整 INI 一次性生成 FuFuPlugin 配置列表，布尔项直接切换，数字、文本和按键项使用各自输入框，校验后自动保存；配置、已安装插件和商店插件列表共用自适应 Win32 原生滚动条，支持拖动滑块、点击轨道、逐行/整页和鼠标滚轮，并统一保留 8 个逻辑像素左右的行间距。同时保留切换回通用插件管理的入口。Avatar 的 HWID/私有授权功能明确排除；FPS 因当前仅来自上游安装包内置 ZIP，暂不伪造在线来源。
- 主插件下载限制为 GitHub HTTPS 允许列表、最多 5 次跳转和 64 MiB；每次记录包 SHA-256，解压后仍经过路径、膨胀、amd64 PE、依赖、当前游戏与事务恢复审计。实时官方 ZIP 下载与安装链测试通过。
- 主窗口 GDI 改为内存 DC 双缓冲后一次 `BitBlt` 提交，并移除窗口类不必要的横纵尺寸强制重绘标志；`WM_ERASEBKGND` 仍直接确认，降低频繁状态刷新造成的闪烁。
- 全量补审后的修复：下载/修复会按物理 INI 字段迁移仍合法的用户值；商店依赖在下载前核验已审计安装状态及精确版本；FuFuPlugin 配置目录、配置文件和 DLL 均拒绝重解析点；插件状态增加内容修订指纹，使上游未递增版本号时仍可区分、恢复和回滚；动态编辑框在离页、启停、修复和退出前统一提交；配置页与注入页共用按水平 DPI 缩放的按钮命中逻辑。
