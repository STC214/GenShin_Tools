# Genshin Tools

使用 Go + Win32 重构 FufuLauncher 的 Windows 原生轻量启动器。

当前正在按照既定顺序实施。项目不会实现米游社/HoYoLAB/BBS、登录与账号切换、抽卡统计、签到、资讯、数据中心、帮助文档、养成计算器、附加程序和内置浏览器等已排除功能。

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

## 当前结论

- 执行顺序：后续代码工作严格按照 `S01`～`S13` 推进；当前 `S00`～`S04` 已完成，下一步进入 `S05` 纯净启动与启动设置。S03 未执行的耗时/破坏性人工场景保留到 S13 发布回归。
- UI：纯 Windows 原生、简洁暗色、少动画；不复刻上游 WinUI 的复杂视觉层。
- 输入：键盘连按、鼠标左/右键连点是 P0 功能，先于启动器主体实现和验收。
- 更新：自动发现和生成上游差异报告，但不自动把上游代码或新功能合入本项目。
- 稳定性：所有后台工作与 Win32 UI 线程隔离，按专项风险清单逐阶段过门禁。
- 基线：上游 `master` 固定到 `b5a050ebd319341bddc4189491c90c22162d33fa`（2026-07-16 UTC）。
