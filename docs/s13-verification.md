# S13 发布回归验证记录

更新时间：2026-07-22
状态：全量自动发布矩阵、上游 disposition 和项目 MIT 许可证已完成；真实桌面/游戏人工门禁未关闭。

## 自动矩阵结果

`scripts/test-s13-release.ps1 -ShellIterations 10` 于本机 Windows x64 完整通过，未跳过在线 provider：

| 门禁 | 结果 |
|---|---|
| 全仓普通测试、race、gofmt、模块和 vet 门禁 | 通过 |
| Debug、GUI Release、injector、updater 清洁构建 | 通过 |
| PE subsystem、VERSIONINFO、图标、便携目录 | 通过 |
| 单实例、托盘、损坏配置恢复、10 次短关闭压力 | 通过 |
| 200 次 hook 安装/卸载、1000-trigger/200-toggle | 通过 |
| 键盘/鼠标左键/鼠标右键，30/50/100/250ms 捕获矩阵 | 通过，全部 down/up 成对 |
| 带 Unicode/空格路径和复杂参数的真实子进程纯启动 | 通过 |
| 注入 manifest、PE、helper 协议和 owned-process 夹具 | 通过 |
| Sophon 在线只读 schema（game、zh-cn） | 通过 |
| 候选 ZIP 生成、S12 staging 重开、SHA-256 sidecar | 通过 |
| 所有子脚本恢复调用者进程环境 | 通过 |
| 测试后残留 Genshin Tools 进程 | 0 |

第一轮整链运行发现 `scripts/build.ps1` 会把 `CGO_ENABLED=0`、`GOOS/GOARCH` 和 Go 缓存变量遗留给调用者，导致后续 race 测试不可运行。构建及测试/捕获脚本现已统一保存并恢复自己修改的进程环境变量；S13 增加环境隔离断言，修复后从头执行全矩阵通过。

2026-07-22 重跑还暴露了杀毒/索引器短暂占用导致 `MoveFileEx` 返回 `ACCESS_DENIED` 的间歇窗口，以及高完整性前台游戏会使捕获测试误触 UIPI 门禁。原子替换现统一只对 sharing/lock/access 短暂错误做 310ms 有界退避；输入捕获测试创建自己的同完整性前台窗口并继续由全局 hook 吞掉事件。对应 race 重复测试及最终 S13 全矩阵均通过。

机器可读记录位于 `artifacts/s13/automated-verification.json`。

## 输入关键结果

12 组捕获均无粘键或丢失释放事件。30ms 组各产生 66 对事件，50ms 组 40 对，100ms 组 20 对，250ms 组 8 对。各模式最大观测间隔均保持在短时门禁容差内；本轮最长值为鼠标左键 250ms 组的 259.503ms。

## 当前桌面视觉检查

本机当前会话为单显示器、1920×1080、DPI 96、非高对比度。Release 首页截图在该环境下标题、导航、状态卡和底部资源栏完整，无裁切、重叠或明显错位；窗口退出后无残留进程。截图位于 `artifacts/s13/release-window.png`。此结果不能替代多显示器和 125%/150%/200% DPI 人工矩阵。

## 候选包

- 文件：`artifacts/release/GenshinTools-0.9.0-windows-amd64-candidate.zip`
- 大小：7,421,040 bytes
- SHA-256：`c153d089a4031824d491c2c89059bce0a269bacf260ac08b4b67eadb08ffde4c`
- ZIP 条目：14 个，仅包含 `release.json`、三个 Release EXE、`build-info.json`、项目 MIT 许可证、第三方通知、许可证政策和 `LICENSES/` 文本。
- 明确不含：Debug EXE、`data/`、日志、缓存、staging、测试夹具和源码。

候选 ZIP 使用固定条目顺序和时间戳；本轮构建时间固定为 `2026-07-22T15:23:25Z`（Git HEAD `15d5af70feb1a4ff164748f522b532850e15e4d0` 的提交时间）。清洁重建四个 EXE 后再次打包得到相同大小和 SHA-256，可确认相同源码、提交身份和工具链可复现。源码、提交或显式构建时间改变会按设计改变 `build-info.json` 和包哈希。

## 尚未关闭的人工门禁

1. 真实原神/反作弊环境下的输入、截图、覆盖层、启动和可选插件/注入组合。
2. 锁屏/解锁、休眠/唤醒、RDP、切换用户、UAC 取消和杀毒软件隔离。
3. 125%/150%/200% DPI、多显示器、显示器拔插、主题和高对比度。
4. 干净 Windows 10/11 x64 首次运行、自更新和真实回滚。

在以上门禁关闭前，当前 ZIP 只能称为候选包，不能称为正式公开版本。
