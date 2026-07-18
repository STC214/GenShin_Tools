# S12 完成前全量项目审计

审计日期：2026-07-19  
审计范围：仓库源码、构建脚本、依赖、入口导航、永久排除项、Go/Win32 稳定性清单和 S12 产物链。

## 范围审计

- 当前 Win32 导航只有首页、游戏、资源、区服、本地增强、截图/性能、输入、注入、插件、插件商店和设置。
- 未发现登录、账号切换、米游社/HoYoLAB/BBS、抽卡、签到、资讯、数据中心、帮助、养成计算器、附加程序或内置浏览器入口。
- 插件 manifest 能力字段对上述排除能力执行拒绝；上游审计只生成报告，不复制或执行上游代码/二进制。
- `mihoyo`/`hoyoverse` URL 和注册表命中仅用于游戏资源/区服配置，不属于账号或社区功能；未发现 Cookie、Token、登录状态采集链。

## 稳定性审计

- UI 网络、下载、哈希、解包、进程等待均通过 taskrunner 或 updater 后台执行。
- 输入引擎使用 generation、context cancel、waitable timer、SendInput 成对 down/up、注入标记过滤和故障停机。
- Win32 窗口回调有最外层 recover；关闭路径使用 `sync.Once`；后台结果带 task ID。
- 更新事务覆盖 prepared/backing-up/backed-up/committing/committed/restarting，并验证备份哈希、staged 哈希和安装目标。
- 所有更新删除/替换路径固定在安装根和 update 子树，拒绝 reparse point；helper 不按进程名终止进程。

## 依赖与发布审计

- `go mod verify` 通过；运行依赖为 x/sys、protobuf 和 klauspost/compress，许可证和通知文件存在。
- 发布 manifest 工具不写入私钥；release-audit 会重新打开 ZIP、复验 manifest、SHA-256、release.json、必需 EXE、许可证文件和 build-info 版本。
- Debug/Release/updater/injector 版本信息、图标和 PE subsystem 构建审计通过。

## 已知审计提示

默认 `go vet ./...` 的 `unsafe.Pointer` 提示集中于 Win32 ABI 边界：窗口 LPARAM 结构、低级输入 hook、ETW record 和命令行 UTF-16。项目门禁使用 `go vet -unsafeptr=false`，并保留这些位置供人工 ABI 对照；本审计未发现新增的非 Win32 unsafe 用法。

## 结论

S12 的代码、自动门禁、故障注入、发布工具和全量静态审计已闭合。真实桌面/游戏长时间矩阵明确留给 S13；在用户要求下，本项目当前不进入 S13。
