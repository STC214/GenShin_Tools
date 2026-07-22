# S13 全量回归与发布候选设计

更新时间：2026-07-19

## 目标

S13 不增加产品功能，而是把 S00–S12 作为一个整体重新验证，生成可重复检查的候选 ZIP，并明确区分自动门禁、需要真实桌面/游戏的人工门禁和项目所有者才能决定的发布门禁。

## 自动门禁

`scripts/test-s13-release.ps1` 按固定顺序执行：

1. 全仓普通测试、race、gofmt、模块校验和 vet 门禁。
2. 清洁构建 Debug、GUI Release、注入 helper 和 updater。
3. 核验 VERSIONINFO、PE subsystem、图标和便携目录。
4. Win32 单实例、托盘、恢复和短时关闭压力。
5. 被吞掉的真实 `SendInput` 键盘/左键/右键矩阵和 hook 生命周期。
6. 纯启动真实子进程夹具与注入/helper 回归。
7. 只读 Sophon schema 在线审计。
8. 生成确定性候选 ZIP，重新通过 S12 staging 解析器打开，并输出 SHA-256 sidecar。

默认只执行短时压力，不启用按分钟计算的输入 soak。在线 provider 可在离线环境以 `-SkipOnlineProvider` 明确跳过，但跳过会记录在报告中。

## 候选包边界

`scripts/package-candidate.ps1` 只打包 Release 主程序、两个固定 helper、`build-info.json`、第三方通知和扁平许可证目录。Debug EXE、运行数据、缓存、日志、staging 和开发文件不会进入 ZIP。

ZIP 内含严格排序的 `release.json`，每个文件记录长度和 SHA-256。S13 使用 `SOURCE_DATE_EPOCH`（若提供）或 Git HEAD 提交时间作为确定性构建时间，打包工具使用固定 ZIP 时间戳和顺序，随后调用 S12 的 `StagePackage` 重新验证整个包。输出名称带 `candidate`；在项目许可证未选择前，不允许把它称为正式发布。

## 人工门禁

- 真实原神和反作弊环境下的输入、启动、截图、覆盖层及可选注入组合。
- 锁屏/解锁、休眠/唤醒、RDP、切换用户和混合完整性权限。
- 125%/150%/200% DPI、多显示器、拔插显示器、高对比度和主题切换。
- 杀毒软件隔离、UAC 人工取消、断网/磁盘空间不足和真实大资源恢复。
- 干净 Windows 10/11 x64 首次运行、自更新和回滚。
- 项目许可证选择。

人工门禁不能由自动脚本伪造为通过；每次自动运行都把它们写入 `artifacts/s13/automated-verification.json`。
