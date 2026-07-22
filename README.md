# Genshin Tools

使用 Go + Win32 重构 FufuLauncher 的 Windows 原生轻量启动器。

当前正在按照既定顺序实施。项目不会实现米游社/HoYoLAB/BBS、登录与账号切换、抽卡统计、签到、资讯、数据中心、帮助文档、养成计算器、附加程序和内置浏览器等已排除功能。

## FufuLauncher 致敬与引用

本项目是对 [FufuLauncher/FufuLauncher](https://github.com/FufuLauncher/FufuLauncher) 的独立 Go + Win32 重构。衷心感谢 FufuLauncher Dev Team 及其贡献者公开项目、产品思路和插件生态；本项目的范围梳理、上游行为对照以及插件商店协议适配都以该项目为重要参考。

- FufuLauncher 采用 [MIT License](https://github.com/FufuLauncher/FufuLauncher/blob/master/LICENSE)；本仓库保留了对应许可证副本和第三方声明。
- 插件模块包与商店数据固定使用 FufuLauncher 当前公开的官方来源；本项目不自建、不运营另一套插件商店，也不发布自有商店目录协议。
- `data\plugins\catalog.json` 只是 Fufu 商店 API 响应的本地校验缓存，不是独立商店或可供第三方托管的目录格式。
- 插件目录约定参考 Fufu 的开源 [FufuLauncher.UnlockerIsland](https://github.com/FufuLauncher/FufuLauncher.UnlockerIsland)：识别 `Plugins\<id>\config.ini` 的 `File=*.dll`。本项目不复制其 Launcher 二进制，而由现有隔离 helper 在加载前增加路径、ZIP、SHA-256、PE、依赖和当前游戏版本复核。
- “配置目标”中的 FuFuPlugin 主插件按 Fufu 当前源码声明的 [FuFuPlugin.zip 官方 GitHub 路径](https://github.com/CodeCubist/FufuLauncher--Plugins/blob/main/FuFuPlugin.zip)按需下载，本仓库不随包再分发该二进制。界面读取上游 `config.ini`，一次性生成全部 `bool/int/float/string/key` 配置控件；配置、已安装插件和商店插件列表均使用按条目数量自适应的原生滚动条，并在行间保留小间距。下载/修复、`.dll/.disabled` 启停和注入开关保持功能兼容，但不复刻原界面。
- 主插件包没有随 Fufu 商店 API 提供固定 SHA-256；本项目会记录每次实际下载的 SHA-256，并在激活前执行受限 ZIP、amd64 PE、依赖和当前游戏兼容审计。其二进制授权不能由 FufuLauncher 主仓库的 MIT 许可证推定，本地标记为 `UNSPECIFIED-FUFU-BUNDLE`。
- FuFuPlugin 修复会迁移新旧 INI 中仍兼容的用户配置，新字段采用上游默认值；同一语义版本的不同包内容使用独立修订指纹参与事务恢复和回滚。商店依赖必须先使用依赖插件自己的验证令牌完成审计安装，程序不会把某个插件的令牌转发给其他依赖包。
- 人机验证使用系统默认浏览器打开 Fufu 官方验证页；项目仍不嵌入浏览器。用户可粘贴验证页返回的 `dl_token` 或完整 JSON，令牌只在内存中使用且不会保存。
- 安装器不执行 Fufu 的远程 Lua，只实现官方 ZIP 下载、哈希校验和受限目录/INI 转换。无法映射为单一根 DLL、含额外可执行格式或无法通过审计的插件会失败关闭。
- Fufu 商店中的插件由各插件作者提供，插件自身的许可证、支持与风险不等同于 FufuLauncher 的 MIT 许可证，也不由本项目背书。商店未提供许可证字段时，本地记录为 `UNSPECIFIED-FUFU-STORE`，这不代表授予再分发权限。
- 本项目不是 FufuLauncher 官方版本，与 FufuLauncher Dev Team 没有隶属或官方合作关系；名称与链接仅用于说明重构对象、兼容来源和致谢。

## 开工前文档

- [需求总表与唯一代码执行顺序](docs/implementation-order.md)
- [重构与实施路线](docs/refactor-roadmap.md)
- [功能范围矩阵](docs/upstream-scope-matrix.md)
- [Go + Win32 稳定性风险与验收清单](docs/go-win32-stability-checklist.md)
- [上游自动对照方案](docs/upstream-sync-plan.md)
- [上游基线锁定文件](upstream.lock.json)
- [构建与验证](docs/build.md)
- [S01 验收记录](docs/s01-verification.md)
- [S02 验收记录](docs/s02-verification.md)
- [S03 输入增强验收记录](docs/s03-verification.md)
- [S04 游戏发现与只读状态验收记录](docs/s04-verification.md)
- [S05 纯净启动与启动设置验收记录](docs/s05-verification.md)
- [S06 资源管理验收记录](docs/s06-verification.md)
- [S07 区服与本地增强验收记录](docs/s07-verification.md)
- [S08 截图与性能覆盖层设计](docs/s08-design.md)
- [S08 截图与性能覆盖层验收记录](docs/s08-verification.md)
- [S09 注入适配层设计与上游二进制审计](docs/s09-design.md)
- [S09 注入适配层验收记录](docs/s09-verification.md)
- [S10 插件管理、配置与商店设计](docs/s10-design.md)
- [S10 插件管理、配置与商店验收记录](docs/s10-verification.md)
- [S11 程序设置、语言与 UI 收尾设计](docs/s11-design.md)
- [S11 程序设置、语言与 UI 收尾验收记录](docs/s11-verification.md)
- [S12 本程序更新与上游自动对照设计](docs/s12-design.md)

## 当前结论

- 执行顺序：后续代码工作严格按照 `S01`～`S13` 推进；当前 `S00`～`S12` 已完成，最新 scope-v2 上游差异已逐项处置并提升基线，`S13` 全量自动发布矩阵及候选 ZIP 已通过。项目级许可证和真实游戏/桌面矩阵仍需人工处置。
- UI：纯 Windows 原生、简洁暗色、少动画；不复刻上游 WinUI 的复杂视觉层。
- 输入：键盘连按、鼠标左/右键连点是 P0 功能，先于启动器主体实现和验收。
- 更新：自动发现和生成上游差异报告，但不自动把上游代码或新功能合入本项目。
- 稳定性：所有后台工作与 Win32 UI 线程隔离，按专项风险清单逐阶段过门禁。
- 基线：上游 `master` 固定到已审查的 `5f6af35fcb90807d5db390ed4af58ca09ddd381c`（2026-07-22 UTC）；逐项结论见 `docs/upstream-disposition-2026-07-22.md`。
- 注入：默认关闭；只接受 `data\injection\modules` 中来源、许可证、SHA-256、PE、文件版本和游戏版本全部匹配的模块，并由独立管理员 helper 重复核验。FuFuPlugin 仅按需下载，不打包进本项目发布物。
- 插件生态：商店来源固定为 FufuLauncher 官方服务，不提供自定义目录 URL，也不建设或运营独立插件商店。
