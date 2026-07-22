# S00–S12 全量项目审计

审计日期：2026-07-22
最新复核提交：`f462d26506b157b360e3aad86ed4d7917de16725`
审计范围：需求边界、全部 Go/Win32 源码、配置与恢复数据、网络下载、注入/插件、自更新、构建脚本、依赖、发布产物和上游自动对照。

## 阶段结论

| 阶段 | 审计结果 | 主要证据 |
|---|---|---|
| S00 | 代码完成；上游新 head 等待人工处置 | `upstream.lock.json`、scope-v2 报告、严格分页/文件上限 |
| S01 | 通过 | 可重复清洁构建、版本资源、依赖校验、四个 PE 产物 |
| S02 | 通过 | 严格且有界配置、隔离恢复、单实例、托盘、DPI、诊断和关闭协调 |
| S03 | 通过自动门禁 | hook 队列握手、并发启停、成对 SendInput、UIPI、停止/焦点安全、race |
| S04 | 通过 | 只读发现、路径/版本/区服识别、PID+创建时间、可取消目录统计 |
| S05 | 通过 | Windows quoting、并发启动预留、关闭竞态、纯净启动回退 |
| S06 | 通过 | 响应写入硬上限、哈希、断点/分块、事务、日志恢复上限和严格 JSON |
| S07 | 通过 | 区服事务、HDR 类型/大小/JSON 门禁、备份恢复、BetterGI 固定协议联动 |
| S08 | 通过自动门禁 | 截图超时/空帧、覆盖层穿透、ETW 清理、callback panic containment |
| S09 | 通过自动门禁 | 固定 helper scope、reparse 拒绝、PE/导入审计、Job、超时、IO 上限和清理 |
| S10 | 通过 | 禁止能力、声明式 ZIP、哈希、配置 schema、安装/卸载/回滚事务、有界本地文件 |
| S11 | 通过 | 简洁暗色 UI、双语、设置原子保存、任务 ID、后台 panic 日志和关闭门闩 |
| S12 | 代码与自动门禁通过；发布外部条件未满足 | Ed25519、固定信任根、staging/commit/rollback、受跟踪重启确认、发布审计 |

## 本轮发现并修复

按风险顺序完成了以下修复：

1. S12 不再公开可信公钥覆盖字段，仓库其他包不能替换内置信任根。
2. updater 的确认等待可故障注入；新增确认超时真实回滚测试。
3. updater 保留新进程 PID+创建时间；确认超时后二次检查 journal，只终止路径和创建时间都匹配的未确认子进程，再回滚旧版本。
4. S06 普通和分块 HTTP 下载写入量限制为期望剩余长度加一，异常响应不能先填满磁盘再报错。
5. S03 在报告 hook ready 前用 `PeekMessage` 创建线程消息队列，并用生命周期锁关闭 Start/Close 竞态。
6. 输入 hook、覆盖层 WndProc、ETW 和后台任务补齐 panic containment；覆盖层异常路径仍以 defer 回收 HWND/GDI，taskrunner panic 写入诊断日志。
7. taskrunner 关闭门闩消除 `WaitGroup.Add/Wait` 竞态，Shutdown 开始后拒绝新任务。
8. S02 配置改为 1 MiB 流式上限、未知字段拒绝、尾随 JSON 拒绝和损坏隔离。
9. S05 增加启动预留，修复两个并发扫描各自启动游戏及扫描期间 Close 后仍启动的问题。
10. HDR 注册表值、备份 JSON、Sophon branches、资源事务 journal 改为严格且有界解析。
11. 注入 helper 请求/结果和模块 PE 改为读取时限长；输出只保留 4 KiB但持续排空；正常请求结束清理 staging。
12. 插件清单、状态、事务、目录缓存、schema 和 INI 统一为读取时硬上限。
13. 发布工具必填参数报错顺序确定化；manifest、签名密钥和 `build-info.json` 均在读取时限长，后者拒绝尾随 JSON。
14. build 每次验证固定 `dist` 路径后清洁重建；带后缀 SemVer 的 FileVersion 按数字四段精确验证。
15. 上游分类规则升为 `scope-v2`，报告目录包含策略版本，旧策略报告不会与新分类结果冲突。
16. FuFuPlugin 修复会迁移新旧 INI 中仍兼容的用户值，新字段保持上游默认值；动态配置编辑在导航、修复、注入切换和退出前同步落盘。
17. Fufu 商店依赖在消耗下载令牌前完成审计，且每个依赖必须用自己的令牌独立安装；同名同版本不同内容用修订指纹参与事务恢复和回滚。
18. FuFu 配置目录、配置文件、DLL 和通用插件配置更新统一拒绝 reparse point；注入配置页按钮与绘制共享水平 DPI 换算。
19. Windows 原子替换统一对杀毒/索引器造成的 sharing、lock、access 短暂错误执行最多 310ms 有界退避，永久错误立即返回。
20. SendInput 捕获测试创建同完整性前台窗口，保留生产 UIPI 门禁并吞掉测试注入事件，不再依赖用户当时的前台程序。

## 永久排除项

入口和源码扫描未发现 FufuLauncher 登录、账号切换、米游社/HoYoLAB/BBS、抽卡、签到、资讯、数据中心、帮助、养成计算器、附加程序、内置浏览器或 WebView 功能。`mihoyo`/`hoyoverse` 字符串只用于游戏资源、区服 SDK 和游戏注册表。插件能力校验继续拒绝账号、凭据、社区、抽卡、签到、资讯、浏览器和数据中心能力。

## Win32 ABI 与 unsafe 审计

默认 `go vet ./...` 仍有 8 个 `unsafeptr` 提示，均为系统在同步回调/API 调用期间拥有的 Win32 指针：`CommandLineToArgvW` 返回块、键鼠低级 hook 的 LPARAM、ETW `EVENT_RECORD`、`WM_DPICHANGED` RECT 和 `WM_GETMINMAXINFO`。这些指针不跨回调保存，不承载 Go 指针；相关结构在 amd64 构建、真实 Win32 测试和 race/checkptr 门禁中通过。项目门禁继续只关闭该单项分析器：`go vet -unsafeptr=false ./...`，其他 vet 分析器保持开启。

## 自动门禁结果

- `go mod verify`：通过。
- `scripts/test.ps1 -Race`：普通测试、race、gofmt、vet（仅关闭 unsafeptr）和 diff check 全部通过。
- `scripts/build.ps1 -Configuration Both`：通过。
- `scripts/verify-artifact.ps1`：Debug、GUI Release、injection helper、updater 的版本、图标、PE subsystem 和便携目录全部通过。
- 上游自动对照：逐项审查 `b5a050ebd319..5f6af35fcb90` 的 23 个变更文件与 21 个提交；disposition 通过工具校验，基线已原子提升到 `5f6af35fcb90807d5db390ed4af58ca09ddd381c`。

## 尚未关闭的发布条件

以下不是可由代码自行决定的问题：

1. S13 的真实游戏、休眠/锁屏、混合权限、多显示器/DPI 和长时间桌面矩阵由项目所有者执行；它不影响 S00–S12 自动代码门禁结论，但影响最终公开发布置信度。

因此，S00–S12 的可执行代码问题、最新上游人工处置和项目 MIT 许可证均已完成；最终公开发布结论仍等待项目所有者完成真实桌面/游戏矩阵。
