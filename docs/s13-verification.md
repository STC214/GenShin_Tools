# S13 发布回归验证记录

更新时间：2026-07-26
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
| 便携 ZIP 生成、S12 staging 重开、SHA-256 sidecar | 通过 |
| 所有子脚本恢复调用者进程环境 | 通过 |
| 测试后残留 Genshin Tools 进程 | 0 |

第一轮整链运行发现 `scripts/build.ps1` 会把 `CGO_ENABLED=0`、`GOOS/GOARCH` 和 Go 缓存变量遗留给调用者，导致后续 race 测试不可运行。构建及测试/捕获脚本现已统一保存并恢复自己修改的进程环境变量；S13 增加环境隔离断言，修复后从头执行全矩阵通过。

2026-07-22 重跑还暴露了杀毒/索引器短暂占用导致 `MoveFileEx` 返回 `ACCESS_DENIED` 的间歇窗口，以及高完整性前台游戏会使捕获测试误触 UIPI 门禁。原子替换现统一只对 sharing/lock/access 短暂错误做 310ms 有界退避；输入捕获测试创建自己的同完整性前台窗口并继续由全局 hook 吞掉事件。对应 race 重复测试及最终 S13 全矩阵均通过。

机器可读记录位于 `artifacts/s13/automated-verification.json`。

## 输入关键结果

12 组捕获均无粘键或丢失释放事件。30ms 组各产生 66 对事件，50ms 组 40 对，100ms 组 20 对，250ms 组 8 对。各模式最大观测间隔均保持在短时门禁容差内；本轮最长值为鼠标左键 250ms 组的 259.503ms。

## 当前桌面视觉检查

本机当前会话为单显示器、1920×1080、DPI 96、非高对比度。Release 首页截图在该环境下标题、导航、状态卡和底部资源栏完整，无裁切、重叠或明显错位；窗口退出后无残留进程。截图位于 `artifacts/s13/release-window.png`。此结果不能替代多显示器和 125%/150%/200% DPI 人工矩阵。

## 1.0.3 输入兼容候选包

- 文件：`artifacts/release/GenshinTools-1.0.3-windows-amd64-portable.zip`
- 大小：7,778,211 bytes
- SHA-256：`d0588362301f32fc2f687d86542048f6e16169f8f7d11d90b857232a1722b84f`
- ZIP 条目：14 个，仅包含 `release.json`、三个 Release EXE、`build-info.json`、项目 MIT 许可证、第三方通知、许可证政策和 `LICENSES/` 文本。
- 明确不含：Debug EXE、`data/`、日志、缓存、staging、测试夹具和源码。

便携 ZIP 使用固定条目顺序和时间戳。版本 `1.0.3` 保留带真实设备句柄过滤的 Windows Raw Input 物理键盘主通道以及 hook/轮询兼容兜底；键盘输出改为 AHK 运行端中已确认的目标窗口消息路径，向已核验游戏 HWND 分次投递 `WM_KEYDOWN/WM_KEYUP`，鼠标继续使用 QuickInput 兼容的单事件 `SendInput`，并在 down/up 之间保持 1～50ms。键盘模式还会仅在已核验游戏窗口中抑制原始物理连发键；其他键和其他窗口不拦截。快捷键轮询排除了鼠标虚拟键。运行日志新增输入状态变化、模式、最终输出计数和 Fault 信息。周期计时器锚定按下沿，因此保持时间不会额外累加到用户设置的连发间隔。30/50/100/250ms 三模式真实窗口/钩子捕获矩阵已验证 down/up 配平和节拍；真实原神游戏内效果仍属于人工门禁。

## 1.0.4 键盘后启动工作进程候选包

1.0.3 的实机日志确认物理 F 键、调度器、游戏目标核验和输出计数均正常，但目标窗口消息未被游戏消费。1.0.4 因此撤销该键盘输出路径：

- 鼠标继续保留已通过实机验证的 QuickInput 兼容单事件 `SendInput`。
- 键盘第一次实际输出时才启动同目录的输入工作进程，确保其创建晚于游戏和注入模块初始化。
- 工作进程使用虚拟键 `INPUT_KEYBOARD`，按下和抬起分别调用一次 `SendInput`；父进程通过匿名管道逐事件等待确认。
- 父进程退出或管道断开时工作进程退出；正常关闭会回收管道和子进程；单次确认超过 2 秒或发送失败会回收 helper，并进入既有 Fault 路径。
- 日志在首次成功输出时记录 `post-launch keyboard input worker active`、工作进程 PID 和后端名称，以支持下一轮实机判定。

自动化测试覆盖工作进程协议、分离的 down/up 命令和虚拟键 `INPUT` 内存形状。真实原神游戏内键盘效果仍属于人工门禁，不能以 UI 输出计数代替。

- 文件：`artifacts/release/GenshinTools-1.0.4-windows-amd64-portable.zip`
- SHA-256：`9198d0ed33270b0352871bb1958cc6705e10470ea09a2cf8c191d87fd9922671`
- 条目数：14
- PE 校验：主程序、输入/注入 helper、更新器均为 Windows GUI 子系统且版本统一为 `1.0.4`

## 1.0.5 AutoHotkey SendEvent 候选包

1.0.4 实机日志记录到后启动工作进程 PID 15324，并成功确认 476 组虚拟键 `SendInput`，但游戏仍未消费。该轮还确认了“游戏先运行、工具后冷启动”的时序已被覆盖。运行中的 `YuanShen.exe` 为 High 完整性，用户以管理员身份启动的工具及其子进程同为 High，因此不是 UIPI 向高完整性目标发送失败。

1.0.5 保留后启动独立工作进程和全部目标核验，将键盘边沿改为老版 AutoHotkey 默认 `SendEvent` 使用的 `keybd_event`：虚拟键、当前前台线程键盘布局映射出的扫描码、扩展键标志以及独立 key-up 标志分别传入。日志同时记录主进程/目标完整性、目标 PID 和是否被 UIPI 阻止。鼠标路径保持不变。

- 文件：`artifacts/release/GenshinTools-1.0.5-windows-amd64-portable.zip`
- SHA-256：`25b6abb71f4b9db86edff930d38299f43f931a3b8078d49583ab9d822e4aa8d0`
- 条目数：14
- PE 校验：主程序、输入/注入 helper、更新器均为 Windows GUI 子系统且版本统一为 `1.0.5`

## 1.0.6 AutoHotkey x86 标记事件候选包

1.0.5 实机日志确认工作进程、游戏目标和权限仍然正常：后启动 helper 输出 1274 组，主程序与游戏均为 High 完整性，但游戏未消费。对官方 Ahk2Exe 与 AutoHotkey 运行时源码以及用户提供的可用二进制继续核对后，确认此前的“裸 `keybd_event`”仍不等价：

- Ahk2Exe 只复制选定的 Base 运行时并嵌入脚本，实际发送逻辑属于 AutoHotkey 运行时。
- AutoHotkey `SendEvent` 的 `KeyEvent` 会写入非零 `KEY_IGNORE_LEVEL(0)`（`0xFFC3D44D`）到 `dwExtraInfo`。
- 用户提供的可用 `AHK_Space.exe` 与 `quickinput.exe` 均为 i386，而此前的输入 helper 为 x64。

1.0.6 将输入 worker 从 x64 注入 helper 中拆分为独立的 `GenshinTools-input.exe`：Windows GUI 子系统、x86 PE、独立 x86 manifest；事件使用已在用户运行时二进制中交叉确认的标记。x64 `GenshinTools-injector.exe` 恢复为只负责游戏注入。

- 文件：`artifacts/release/GenshinTools-1.0.6-windows-amd64-portable.zip`
- SHA-256：`8545e158fecad1adbd0298467eb75adba797ecb6ec6cbcb7735bad04230fd3a4`
- 条目数：15
- PE 校验：主程序/注入 helper/更新器为 x64，输入 helper 为 x86；全部为预期子系统且版本统一为 `1.0.6`

## 1.0.7 恢复已知可用键盘基线候选包

用户实机确认早期版本曾能在原神内产生键盘连发，缺陷只是按住连发键时再按其他键会中断。提交历史对照将 `0.9.5`（`81160c5`）确定为最可信的已知可用输出基线。1.0.7 不再继续模仿 AutoHotkey 运行时，而是精确恢复该版本的键盘输出边界，同时保留后来完成的多键状态管理、Raw Input 物理边沿、前台游戏进程核验和安全停止：

- 由主 x64 进程按当前前台线程布局映射扫描码。
- down/up 使用 `KEYEVENTF_SCANCODE`，保留扩展键身份和项目 `dwExtraInfo` 标记。
- 一组 down/up 在同一次 `SendInput(2, ...)` 中成对提交，不再经过 x86 worker、`keybd_event` 或分离管道。
- 不抑制原始物理连发键，保持与已知可用版本一致。
- 每个按住的连发键仍有独立状态；无关按键不会删除 `repeatHeld`，也不会取消当前 generation。

新增回归测试固定扫描码、扩展键、事件标记和物理键不拦截行为。真实 Windows 捕获中，键盘/左键/右键各 100 组 down/up 全部配平；30/50/100/250ms 三模式完整矩阵通过；`go test ./...` 和输入/外壳 race 测试通过。该结果证明 Windows 已接收预期事件，但真实原神消费结果仍必须由人工门禁确认。

- 文件：`artifacts/release/GenshinTools-1.0.7-windows-amd64-portable.zip`
- 大小：7,776,262 bytes
- SHA-256：`d92ade643652f056f19e9728b0b00c254ae58ae8c574ccc88b05ef82058cca9e`
- 条目数：14
- PE 校验：主程序/注入 helper/更新器均为 x64 Windows GUI 子系统，版本统一为 `1.0.7`；Debug 构建为 x64 Console 子系统且不进入便携包

实机结果：失败。日志确认 1.0.7 在游戏前台无 Win32 错误地产生 1129 组扫描码事件，但游戏仍未消费。这否定了“仅恢复扫描码成对提交即可恢复游戏效果”的假设。

## 1.0.8 物理触发键替换候选包

对可用 `SET_AHK` 目录继续核对后确认，实际生成的 `AHK_F.exe` 是要求管理员权限的 32 位 AutoHotkey 热键程序；其普通热键语义会拦截物理触发键，再以合成事件替代。1.0.7 则让真实 F 一直以 down 状态进入游戏，再叠加合成 F，游戏可能把后续 down 当作同一次持续保持而忽略。

1.0.8 首次组合此前没有共同测试过的两部分：

- 保留 1.0.7 的主进程扫描码 down/up 单次成对 `SendInput`。
- 仅当键盘连发启用、配置键匹配且已核验原神进程位于前台时，低级 hook 吞掉该物理键的 down/up；同一物理边沿仍进入多键状态机。
- 禁用状态、未核验窗口、其他进程和其他键均不拦截；项目合成事件继续按 injected 标志和项目 marker 排除，不会被自身再次拦截。
- hook 使用不可变原子策略快照，不在回调中等待状态机锁；多键状态仍保证无关按键不取消 generation。
- 状态日志新增 `keyboardBackend` 与 `physicalTriggerSuppressionArmed`，用于实机确认候选路径。

自动矩阵包括输入/外壳 race、全项目测试、真实 Windows 键盘/左右键捕获和 30/50/100/250ms 节拍，全部通过。真实原神消费仍是人工门禁。

- 文件：`artifacts/release/GenshinTools-1.0.8-windows-amd64-portable.zip`
- 大小：7,778,250 bytes
- SHA-256：`250d06dc23440c3395ee9151de0ec2de9ef4f196160f6dee7cfe0ac3f4cc11a7`
- 条目数：14
- PE 校验：主程序/注入 helper/更新器均为 x64 Windows GUI 子系统，版本统一为 `1.0.8`

实机结果：失败。日志确认物理触发键拦截已武装，两次保持分别生成 678 和 610 组，仍无 API/UIPI 错误，游戏未消费。这否定了“物理 down 与合成 down 冲突是唯一原因”的假设。

## 1.0.9 同进程 x86 输入 worker 候选包

对用户可用的 `SET_AHK` 产物做 PE 与机器码核对后，确认 `AHK_F.exe` 强制管理员权限、架构为 i386，并从同一进程同时持有低级热键 hook、物理按键状态和 `keybd_event(vk, scan, flags, extraInfo)` 调用。1.0.6 的 x86 helper 只接受父进程的代发命令，物理 hook 和 held 状态仍属于 x64 主进程，因此并不等价。

1.0.9 将该进程边界完整实现为原创 Go + Win32 worker：

- `GenshinTools-input.exe` 为 x86 Windows GUI 子系统，随高权限主进程继承相同完整性级别，不创建控制台窗口。
- worker 自己安装 `WH_KEYBOARD_LL`、保存每个配置键的独立 held 原子状态，并从自己的输出循环调用带 AHK ignore-level 标记的 `keybd_event`。
- worker 在游戏 PID 被发现后启动，因此同时覆盖游戏已运行后冷启动工具以及注入完成后的创建时序。
- 主程序通过有界 stdin/stdout JSON 协议同步启用状态、最多 16 个连发键、1～5000ms 间隔和已核验游戏 PID；2 秒无确认会终止 worker 并回退。
- worker 将物理事件继续传给主进程 hook，由主进程完成仅游戏前台的物理触发键拦截和 UI 状态计数；无关按键不会改变 worker held 状态。
- worker 缺失、启动失败或退出时，主程序明确退回扫描码成对 `SendInput`，日志记录实际 backend 与 worker PID。

自动验证包括：真实 x86 helper 启动/配置确认/管道关闭退出；同进程 held 循环的 Windows 低级 hook 捕获（至少 5 组且 down/up 配平）；原有三模式捕获矩阵；全项目测试；输入/外壳 race；PE 架构、版本、GUI 子系统和便携包审计。真实原神消费仍为人工门禁。

- 文件：`artifacts/release/GenshinTools-1.0.9-windows-amd64-portable.zip`
- 大小：8,946,265 bytes
- SHA-256：`147291cbb024be1e38e6dfcec8dc6b3e66f5e4dfb2144189cb6a65be1e81f4f3`
- 条目数：15
- PE 校验：主程序/注入 helper/更新器为 x64 GUI，输入 worker 为 x86 GUI，版本统一为 `1.0.9`

## 1.0.10 AHK 消息投递与注入后重装候选包

1.0.9 实机失败后，用专用探针在 `AHK_F.exe` 启动前安装 `WH_KEYBOARD_LL`，并以调试器监视其 `keybd_event`。有效报告捕获到 288 个物理键盘事件、0 次 `keybd_event` 调用：F 保持期间是一个真实 down、约 500ms 后按系统 typematic 重复 down、松开时一个 up，全部 `injected=false`。这确认此前把静态导入误判成实际输出路径。进一步核对同代 AutoHotkey 官方运行时，面向目标窗口的 ControlSend 分支使用 `PostMessage(WM_KEYDOWN/WM_KEYUP)`，不会进入低级 injected-input hook 链；Windows 11 已不支持 journal hook，因此不采用 SendPlay journal。

1.0.10 按报告修正：

- x86 worker 按 AutoHotkey ControlSend 的 Win32 形态向已核验前台原神窗口提交 `PostMessageW(WM_KEYDOWN/WM_KEYUP)`；扫描码、扩展键位和 key-up 状态位写入 `lParam`，该路径不进入低级 injected-input hook 链。
- 主 x64 hook 不再吞掉物理连发键，保留真实按键及系统 typematic；无关按键仍不会改变任何连发键的 held 状态。
- `PostMessageW` 失败时降级到原 `keybd_event`；worker 缺失或退出时再降级到主进程扫描码 `SendInput`。
- 注入成功并留出 2 秒初始化窗口后，内置 x86 worker 也会被重启并恢复原配置，使其低级物理 hook 安装在注入模块之后。
- 注入启动前精确记录正在运行的 `AHK_F.exe` 与 `quickinput.exe` 的 PID、进程创建时间和完整路径。注入成功并留出 2 秒初始化窗口后，只终止匹配的原进程生命期并按原路径重启，使外部工具在注入模块之后重装 hook。不会匹配 `AutoHotkey.exe`、`AHK_Space.exe` 或其他同类工具。
- 版本提升为 `1.0.10`。`go test ./...`、输入/外壳 race、真实 Win32 消息投递捕获、30/50/100/250ms 三模式短矩阵以及 PE 版本/架构/GUI 子系统审计均通过；真实原神消费仍是人工门禁。

- 文件：`artifacts/release/GenshinTools-1.0.10-windows-amd64-portable.zip`
- SHA-256：`2e699836801c4a104098380c081d2dd92761e2faa385cec9c0d5cae21abf4bf2`
- 条目数：15
- PE 校验：主程序/注入 helper/更新器为 x64 GUI，输入 worker 为 x86 GUI，版本统一为 `1.0.10`

## 1.0.11 实测 AHK 事件形态与前台托管候选包

1.0.10 实机确认内置键盘连发仍未被游戏消费，且注入后外部 `AHK_F.exe` 的重启日志不能证明替代进程持续存活。新版 schema 3 输入探针在原神前台捕获到 1092 条 F 事件，其中 749 条为注入事件：375 个 down、374 个 up，扫描码均为 33，标志分别为 `LLKHF_INJECTED` 和 `LLKHF_INJECTED|LLKHF_UP`，`dwExtraInfo` 均为 AutoHotkey `KEY_IGNORE_LEVEL(0)` 的 `0xFFC3D44D`。这否定了 1.0.10 的纯 `PostMessage` 推断，并确认可用 AHK_F 会用自身 hook 吞掉物理热键，再以注入式平衡 down/up 替换。

1.0.11 按实测修正：

- x86 worker 在同一进程中持有物理 hook、每键 held 状态和 `keybd_event` 输出；仅在已核验原神前台吞掉配置的物理触发键，其他按键与其他窗口放行。
- 切出游戏只暂停输出，不销毁 held 循环；重新回到游戏时仍按住的键可以继续输出，不会因其他按键或短暂失焦永久中止。
- 注入进入 Running 后不再固定等待两秒，而是要求已核验游戏窗口连续位于前台 1.5 秒，再重启内置 worker 和注入前捕获的外部输入工具。
- 外部工具按精确完整路径分组，终止并等待每个捕获生命期；AHK_F 使用 `/restart`，新 PID 必须持续存活一秒、映像路径和创建时间一致，否则记录真实失败。
- 重启后的 AHK_F 由其标准 Suspend Hotkeys 命令管理：游戏前台时启用并显示绿色，离开前台时暂停并显示红色。未使用进程冻结，避免冻结低级 hook、锁或半完成按键状态。
- 若用户设置注入后退出启动器但存在待托管 AHK_F，则改为隐藏驻留；启动器退出前会恢复用户 AHK，避免遗留暂停状态。
- 输入探针 schema 3 增加 `SendInput`、`keybd_event`、`PostMessage`、`SendMessage` 与线程消息候选 API 记录，并跳过不适合按 user32 基址换算的转发导出。
- 版本提升为 `1.0.11`。全项目普通与 race 测试、格式、依赖和 vet 门禁通过；真实 Win32 短捕获中 worker 的五组 AHK 标记 down/up 完全配平，键盘/左键/右键在 30/50/100/250ms 矩阵中均配平且节拍处于容差内。PE、GUI 子系统、版本、图标、注入 helper 协议和便携布局审计通过；真实原神消费仍须人工门禁确认。

- 文件：`artifacts/release/GenshinTools-1.0.11-windows-amd64-portable.zip`
- 大小：8,963,285 bytes
- SHA-256：`ce6ea1c2dfede9f8300de89f44e4781946ba654b4a95b724b8bf45876dfdc9e7`
- 条目数：15
- PE 校验：主程序/注入 helper/更新器为 x64 GUI，输入 worker 为 x86 GUI，版本统一为 `1.0.11`

## 1.1.0 外部 AHK 权限与可靠重启候选包

1.0.11 实测日志确认注入前的高权限 `AHK_F.exe` PID 34164 已被终止，但中权限主程序随后以普通 `CreateProcess` 重启时收到 `ERROR_ELEVATION_REQUIRED`，因此没有新 PID。相同完整性差异还会使 UIPI 拦截后续 `WM_COMMAND/Suspend Hotkeys`，所以只增加一次重试不足以完成前台托管。

1.1.0 冻结内置键盘连发的现状，不再继续修改其行为；本轮只处理外部 AHK：

- 主程序清单改为 `requireAdministrator`，启动时进行一次 UAC，使主程序、AHK_F 和游戏输入管理处于相同完整性级别。
- 注入 helper、x86 输入 helper 和更新 helper 继续使用 `asInvoker`，按调用方权限继承，不单独重复请求 UAC。
- AHK_F 使用 `/restart` 先启动替代进程；新 PID 必须持续存活一秒且映像路径、创建时间一致，之后才清理仍存在的旧 PID。替代启动失败、UAC 取消或健康检查失败时不会先结束原 AHK。
- 普通创建明确返回 `ERROR_ELEVATION_REQUIRED` 时仍提供 `ShellExecuteExW(runas)` 回退，并保留返回的进程句柄/PID用于同一健康检查。
- AHK 成功替换后继续按已核验游戏是否位于前台，通过同权限 `Suspend Hotkeys` 切换绿色激活与红色暂停状态。
- 版本提升为 `1.1.0`；全项目普通/race、格式、依赖、vet、AHK 原实例保留顺序、ShellExecute ABI、外壳和输入回归测试通过。PE 审计确认主程序为 `requireAdministrator`，三个辅助程序为 `asInvoker`，版本、架构、GUI 子系统、图标、注入 helper 协议和便携布局均符合预期。

- 文件：`artifacts/release/GenshinTools-1.1.0-windows-amd64-portable.zip`
- 大小：8,965,121 bytes
- SHA-256：`dece6160ccec8d6a9b55ae9ed9c0d0a7937f038cfa6540e56acd1f44abdc5ffc`
- 条目数：15
- PE 校验：主程序为 x64 GUI/`requireAdministrator`；注入 helper、更新器为 x64 GUI/`asInvoker`；输入 helper 为 x86 GUI/`asInvoker`；版本统一为 `1.1.0`

## 1.2.0 AHK 随游戏共生命周期

- 输入增强页新增持久化选项“AHK随游戏启动/关闭”，配置 schema 提升到 10；旧配置迁移后默认关闭。
- 启用后，只使用游戏发现层已核验路径、PID 和创建时间的原神进程作为生命周期目标。检测到游戏即启动内置 AHK，不再等待游戏先成为前台；所有目标 PID 退出后脚本自行退出。
- 游戏仍运行而 AHK 被手动关闭或异常退出时，管理任务一秒后重新拉起；关闭选项只结束本项目记录的内置 PID 与完整路径，不按进程名模糊结束用户工具。
- 项目脚本只在已核验游戏窗口处于前台时启用热键，离开前台立即暂停；即使主程序按启动后行为退出，脚本仍独立核验游戏 PID 并随游戏退出。
- 便携包内置官方 AutoHotkey v1.1.37.02 x86 运行时并重命名为 `AHK_F.exe`，同时携带项目脚本、GPL-2.0 文本及完整对应源码 ZIP。用户提供、追加脚本不透明且再分发权限不明的旧 `AHK_F.exe` 不进入仓库或成品。
- 运行时、脚本、源码在构建、成品审计和每次实际启动前均校验固定 SHA-256，避免管理员主程序执行被本地替换的同名脚本。
- 自动测试覆盖 schema 迁移/持久化、独立 UI 命中区域、篡改拒绝、升级包必需文件与确定性 ZIP；真实运行时冒烟验证通过脚本解析、进程存活以及目标进程退出后 AHK 自动退出。

- 文件：`artifacts/release/GenshinTools-1.2.0-windows-amd64-portable.zip`
- 大小：11,464,634 bytes
- SHA-256：`89035204fbdc0019d7d117bbadc0e0e3672f1be61eaba75f923d217330c07e24`
- 条目数：19

- PE 校验：主程序为 x64 GUI/`requireAdministrator`；注入 helper、更新器为 x64 GUI/`asInvoker`；输入 helper 与内置 AHK 运行时为 x86 GUI；项目程序版本统一为 `1.2.0`

## 1.3.0 Interception 游戏模式键盘连发

- 根据 FlairBloom 游戏模式的已审计行为和 Interception 公开设备协议，独立实现 x86
  驱动键盘后端；未复制 FlairBloom 源码，也未打包 Interception DLL、安装器或驱动。
- 输入增强页固定提示“需要安装 Interception 驱动，安装后必须重启电脑，且仅在原神内
  生效”；驱动不可用时提供固定到官方 v1.0.1 release 的下载入口。
- 主程序只读探测完整 20 设备 context 和当前完整性级别。驱动已安装但程序不是 High
  完整性时明确提示管理员运行，不把“设备可打开/IOCTL 成功”误报为真实可输出。
- `GenshinTools-input.exe` 仅在游戏发现层给出路径、PID、创建时间均已核验的进程后启动，
  游戏全部退出时立即关闭；注入完成且游戏前台稳定后重启 worker，以保证 hook 安装顺序。
- worker 同时持有低级键盘 hook、每键独立 held 状态和唯一 Interception context。
  输出 down/hold/up 在互斥边界内串行提交；1ms 间隔不额外持有，其他间隔采用三分之一且
  最长 30ms 的持有时间。
- 所有驱动扫描码写入 `ExtraInformation=0x51485844`，x86/x64 两层 hook 都在物理状态
  处理前过滤该标记；无关按键不会清除任何正在保持的连发键。
- 游戏外不吞配置键也不输出。每一组发送前检查当前前台 HWND 是否属于已核验游戏 PID；
  切出游戏暂停，回到游戏且物理键仍保持时恢复。
- 普通 Go、x86 编译和竞态门禁覆盖驱动 ABI 大小、IOCTL、扫描码状态、1ms 持有、worker
  请求与前台隔离。真实驱动输出必须在管理员成品进程和真实原神环境继续完成手工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.0-windows-amd64-portable.zip`
- 大小：11,481,101 bytes
- SHA-256：`e2cbf1472a65b02f8626e8a8c2fd7af0fdc48f197ab51ef1f166a2ace402755e`
- 条目数：19；确认不含 FlairBloom/Interception 源码、DLL、安装器或驱动
- PE 校验：主程序为 x64 GUI/`requireAdministrator`；输入 worker 为 x86 GUI/`asInvoker`；
  注入 helper、更新器为 x64 GUI/`asInvoker`；版本统一为 `1.3.0`

## 1.3.1 项目所有者 AHK_F 成品替换

> 本节记录当时的候选包。其“所有者授权足以覆盖整个成品、无需附带 GPL
> 材料”的判断已被 1.3.2 的二进制审计纠正，不再作为当前许可证结论。

- 按项目所有者确认，不再使用 1.2.0 引入的官方 AutoHotkey v1.1.37.02
  解释器、独立 `AHK_F.ahk`、GPL 副本和源码 ZIP；这些内容已从源码树、构建、升级白名单
  和便携包必需文件中移除。
- 便携包改为直接分发项目所有者约十年前制作并明确授权复制的旧版编译成品
  `AHK_F.exe`。构建、成品审计和每次启动均固定校验其 422,139 字节大小与
  SHA-256 `09ae8c2a0eb2a5636231a4a228f89502bcce5c682d52b10ca803b8fef9cad2f5`。
- 程序不再把该文件当作解释器，也不传入脚本路径或游戏 PID；仅用其兼容的 `/restart`
  开关直接启动完整成品。
- “AHK随游戏启动/关闭”仍只接受发现层已核验的游戏进程作为启动条件。管理器按准确
  AHK PID/完整路径随游戏结束而停止，异常退出时重新拉起；游戏前台时启用，离开前台时
  通过 AHK 标准暂停命令失活。
- 新增 `LICENSES/User-AHK_F-NOTICE.md` 记录所有者授权、二进制身份和许可证边界；
  不把该成品纳入仓库根 MIT，也不声称它对应当前 AutoHotkey 仓库版本。
- 普通全量测试及 `internal/input`、`internal/shell`、`internal/selfupdate` 竞态测试通过；
  PE、GUI 子系统、清单权限、版本、图标、注入 helper 协议、便携布局和 AHK 固定身份审计
  通过。

- 文件：`artifacts/release/GenshinTools-1.3.1-windows-amd64-portable.zip`
- 大小：9,313,056 bytes
- SHA-256：`978bd811557906f80099e7c1acf05b2453e8aafd87861f87a9a63db14ea08281`
- 条目数：17；只含一个 `AHK_F.exe`，不含 `.ahk`、AutoHotkey 源码 ZIP 或旧 GPL 副本
- PE 校验：主程序为 x64 GUI/`requireAdministrator`；输入 worker 与 `AHK_F.exe` 为
  x86 GUI；注入 helper、更新器为 x64 GUI/`asInvoker`；项目程序版本统一为 `1.3.1`

## 1.3.2 完整审计与顺序修复

- 键盘连发仅在已核验游戏进程存在后创建独立 x86 worker，并通过用户安装的
  Interception 驱动提交扫描码；驱动写入失败会保留首个根因、使 helper 非零退出并由
  主程序显示，不再静默变成“输出计数增长但游戏无输入”。每个连发键另有独立 generation
  围栏，配置切换后快速重新按下时，旧睡眠循环不能加入新一轮输出。
- AHK 管理改为记录 PID、规范化完整路径和进程创建时间，避免 PID 复用误判。便携
  AHK 被纳入 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`，启动器异常退出也不会长期遗留；
  启动前等待游戏前台，启动后每 100ms 按前台状态切换 AHK Suspend。
- 旧版成品中确认存在 `AutoHotkey v1.0.48.05` 运行时标记。所有者授权继续覆盖其
  成品/脚本部分，运行时则按 GPL-2.0 随包提供许可证
  `LICENSES/AutoHotkey-v1.0-GPL-2.0.txt` 和对应官方标签源码
  `SOURCES/AutoHotkey-v1.0.48.05-source.zip`；构建、成品校验、升级白名单及测试夹具均
  把二者列为必需文件并固定核验 SHA-256。
- 构建结束无论成功失败都删除四个生成的 `.syso`，避免管理员清单污染后续
  `go test`。S02/S05 自动化改用独立 asInvoker GUI harness 和独立单实例命名，正式
  `requireAdministrator` 清单由 PE 审计覆盖，测试不再弹 UAC、遗留高权限进程或锁住
  `dist`。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 自动化短矩阵
  于 2026-07-26 通过：全量测试、`-race`、格式、vet、确定性构建、PE/图标/清单、
  S02 生命周期、S05 真实进程参数、S09 helper/注入边界、升级包和环境隔离均通过。
  当前执行终端为 Medium integrity，Interception 真驱动输出因此明确跳过；自动化桌面
  抢占前台时鼠标捕获也明确跳过。这两项以及真实原神消费仍属于下方人工门禁，未误标为
  实机通过。

- 文件：`artifacts/release/GenshinTools-1.3.2-windows-amd64-portable.zip`
- 大小：10,876,460 bytes
- SHA-256：`8b3cfc21e1fad7b2ceba5c445326c26655b0369c50f2077c5e81d49d8475c1cc`
- 条目数：19；不含 Debug EXE、运行数据、日志、缓存、`.ahk`、驱动、Interception
  DLL/安装器或 FlairBloom 源码
- AHK_F SHA-256：`09ae8c2a0eb2a5636231a4a228f89502bcce5c682d52b10ca803b8fef9cad2f5`
- AutoHotkey v1.0.48.05 源码 SHA-256：
  `fd9d629dbd742cbe1e14c530dd092e32d4ba2a058b97d69d935ea340b61b8c39`
- GPL-2.0 文本 SHA-256：
  `2f37ec8a6e912402a7d79ea03e5e33eacf54d1bf1fc7e3b0eab3a69bd8b23252`

## 1.3.3 注入完成后的最终 Hook 时序

- 游戏扫描层现在只登记并核验进程生命周期，不能直接创建内置键盘 worker，也不能提前
  启动便携 AHK。游戏进程集合变化时，程序立即关闭内置键盘后端、停止项目托管的 AHK，
  并使所有输入 Hook 重新进入等待状态。
- 注入启动前精确捕获并停止受支持的外部 `AHK_F.exe`/`quickinput.exe`；匹配同时核验
  文件名、绝对路径、PID 和创建时间，避免误停同名或 PID 复用后的进程。注入失败、取消
  或最终化中断时按原路径恢复已捕获工具。
- 注入启动采用双完成屏障：只有注入 helper 已完成所有 DLL 的 `LoadLibrary`，且启动
  引擎已确认游戏进入 Running，才允许进入输入最终化阶段；两个事件无论先后到达都不会
  提前放行。
- 屏障放行后仍要求已核验游戏窗口连续位于前台 5 秒。周期性游戏扫描不会反复取消或重置
  这段稳定窗口，并在整个窗口持续枚举确认全部注入 DLL 仍驻留于准确游戏 PID。最终加载
  顺序固定为：主程序键盘/鼠标观察 Hook、内置 Interception 键盘 worker、注入前捕获的
  外部 AHK/QuickInput、便携 AHK。这样负责拦截的 worker 和所有外部连发 Hook 都安装在
  插件注入完成以后。
- 最终化使用代际号和提交互斥保护待处理工具所有权。等待期间出现新游戏生命周期时，旧
  任务退出前不能启动新任务，外部工具保持停止；若取消发生在工具重启之后，新 PID/创建
  时间会先被精确停止并转交下一轮。失败且没有游戏进程时才恢复用户工具，不再从取消
  defer 提前恢复。
- 输入增强页在等待期间显示“等待启动/插件完成”，便携 AHK 的随游戏启动逻辑也受同一
  硬门禁约束，不能从游戏发现消息或普通配置刷新路径绕过。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race 测试、格式、vet、确定性 Debug/Release/helper 构建、
  PE/图标/清单、S02 生命周期、S03 鼠标实捕与节奏矩阵、S05 真实进程启动、S09 注入
  helper 边界以及确定性便携包均通过。当前测试进程为 Medium integrity，要求 High
  integrity 的 Interception 真驱动输出捕获明确跳过；真实原神与插件组合仍保留为人工
  门禁。

- 文件：`artifacts/release/GenshinTools-1.3.3-windows-amd64-portable.zip`
- 大小：10,896,019 bytes
- SHA-256：`655ec4848ec47408c7749caf8e933505515bbcfc3e2539a94c7ff64d65c6c083`
- 条目数：19

## 1.3.4 最终化状态机完整修复

- 外部 AHK/QuickInput 不再在最终化任务开始时脱离应用状态。每轮捕获、重启和恢复都由
  generation 标识准确所有权；旧任务只能提交或清理自己创建的 PID/路径/创建时间，
  不能清空新一轮待处理资产。Hook epoch 的提交与 `hold` 使用同一互斥边界，消除“检查
  通过后、提交前又发生游戏生命周期变化”的窗口。
- 等待被取消时不再立即恢复用户工具。若外部工具尚未重启，则继续保持待处理；若已经
  重启，则精确停止新实例并把新生命周期重新排队。游戏退出或确定启动失败且没有游戏
  进程时才异步恢复，恢复任务为单飞状态；恢复期间禁止开始新启动，generation 改变时
  会停止本次错误创建的替代实例。
- 注入 helper 与 Running 双屏障通过后，连续 8 秒同时要求准确游戏窗口位于前台，并
  逐次枚举确认本次审计的全部注入 DLL 仍驻留于准确游戏 PID。模块消失或枚举失败时
  保持关闭，不会按固定延时盲目放行。
- 主程序的 `WH_KEYBOARD_LL`/`WH_MOUSE_LL` 观察 Hook 现在也在注入前由其所属消息线程
  卸载，并在最终阶段首先重装；随后才安装 x86 Interception worker、重启外部工具并
  启动便携 AHK。Raw Input 和键盘轮询备用路径受同一门禁约束，不会在观察 Hook 卸载
  期间提前触发 Engine、产生输出或把输入模块推进 Fault。
- 新增测试覆盖：准确模块驻留复核、观察 Hook 反复卸载/重装、门禁前键盘事件隔离、
  外部工具成功/失败后的生命周期转换、旧 generation 拒绝覆盖及重启后交叠提交、
  恢复期间拒绝新启动，以及双屏障和连续前台窗口。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race 测试、格式、vet、确定性构建、PE/图标/清单、
  200 次主 Hook 安装/卸载、S02、S05、S09 和确定性 ZIP 均通过。矩阵运行时桌面焦点
  被其他进程抢占，鼠标实捕按设计跳过；随后单独重跑
  `TestCapturedNativeMousePressReleasePairs` 已通过。Interception 真驱动捕获仍因当前
  测试进程为 Medium integrity 而明确跳过，真实原神消费仍属于人工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.4-windows-amd64-portable.zip`
- 大小：10,896,980 bytes
- SHA-256：`a0d9e8d1f5ef50a95697a0892a5546e80c12f45816c4022a29771fb8ba8cbd78`
- 条目数：19

## 1.3.5 最终化退出、进程回滚与插件就绪协议

- 启动器退出会先标记 shutdown、收束后台任务，再在独立恢复互斥区内恢复仍由当前 generation
  持有的 AHK/QuickInput。最终化取消与退出恢复不能并发重复拉起，也不会因为游戏仍存在而把
  用户原有工具永久留在停止状态。
- 外部工具替代进程一旦取得 PID 就立即记录创建时间。健康验证失败仍按准确路径、PID 和创建
  时间清理；错误结果不再被回滚逻辑跳过，避免留下未托管的 AHK/QuickInput。
- 注入模块清单新增向后兼容的可选 `readyEvent`：
  `Local\GenshinTools.PluginReady.<id>.{pid}`。主动采用该协议的模块必须在异步初始化和
  Hook 安装结束后置位手动复位事件；现有 Fufu 模块无须修改，使用“目标 DLL 驻留 + 完整模块
  集合不再变化 + 前台连续 8 秒”的兼容回退。
- 主观察 Hook 的跨线程控制使用 pending/claimed/canceled/done 状态确认。只允许取消尚未领取的
  超时请求；执行线程已经领取时调用方等待明确完成或线程退出，杜绝调用方收到超时后旧请求又
  迟到安装/卸载 Hook。
- 外部工具停止、替代启动、generation 所有权转移、取消清理与退出恢复共享同一生命周期互斥
  边界，关闭过程中不会与后台最终化交错创建重复或迟到的替代进程。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race、格式、vet、确定性双配置构建、PE/图标/权限、200 次 Hook
  重装、鼠标八组节拍、S02、S05、S09 和确定性 ZIP 均通过。当前自动化进程为 Medium
  integrity，真实 Interception 驱动捕获按设计跳过，真实原神消费仍属于人工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.5-windows-amd64-portable.zip`
- 大小：10,904,433 bytes
- SHA-256：`096c93f1025f4d344843a053e5920b63dcc40af89f436cc3f64516688f40b45d`
- 条目数：19

## 1.3.6 发布后复审修复

- 内置 AHK 在进程创建成功但健康检查、创建时间核验或 Job 绑定失败时，统一进入精确替代进程
  清理路径；清理错误并入原始根因并保留 PID 身份供调用方重试。仍无法安全停止时终止本轮管理
  任务，不再每秒创建新的未托管实例。
- 旧插件兼容门禁的完整模块指纹由“规范化路径”提升为“路径 + 映射基址 + 映像大小”。同一路径
  DLL 卸载后重新加载也会被识别为新模块生命周期并重新开始连续稳定计时。
- 主观察 Hook 改为逐句柄安装和卸载。`UnhookWindowsHookEx` 失败时不再遗忘真实句柄，而是保留
  并标记 dirty；后续 hold 会重试。注入启动和最终化都必须再次确认全部旧观察 Hook 已卸载，
  否则失败关闭，不允许带着残留旧 Hook 进入插件与输入 Hook 排序。
- 外部工具生命周期互斥改为 defer 释放，即使后台任务 panic 也不会把退出恢复永久锁死。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 再次通过：全量普通/race、格式、vet、确定性双配置构建、PE/图标/权限、200 次
  Hook 重装、鼠标八组节拍、S02、S05、S09 和确定性 ZIP 全部通过。真实 Interception 驱动
  捕获仍因自动化进程为 Medium integrity 而明确跳过，真实原神消费仍保留为人工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.6-windows-amd64-portable.zip`
- 大小：10,910,586 bytes
- SHA-256：`f5d9b5b48aa2f893888909e347912ffb4b62130cf5a25ab8e26328ddf638e295`
- 条目数：19；无重复条目

## 1.3.7 受保护进程兼容与注入后五秒硬时序

- 对实机便携目录日志的复核确认：三次注入均已完成 helper/Running 屏障，插件心跳也持续存在，
  但恢复运行后的模块快照验证均返回 `Access is denied.`。这会永久阻塞最终化任务，因而既没有
  启动内置 Interception worker，也没有尝试重新拉起 AHK。
- 模块驻留验证只保留在 helper 的挂起进程注入阶段；helper 必须等待每个远程
  `LoadLibrary` 返回并验证目标 DLL 后才能恢复游戏。恢复后的受保护游戏进程不再执行
  `TH32CS_SNAPMODULE` 枚举，也不再把其访问结果用作输入最终化门禁。
- 启动顺序固定为：全部 DLL 注入完成 → helper 成功并进入 Running → 等待模块声明的可选
  `readyEvent` → 在所有内外部连发均未加载的状态下单独等待 5 秒 → 游戏处于前台时依次恢复
  主观察 Hook、载入内置 Interception worker、重启捕获到的外部 AHK/QuickInput，最后启动
  随游戏管理的内置 AHK。
- 五秒计时只由注入/插件就绪状态控制；期间切换前台不会重新计时。计时结束时游戏不在前台，
  最终化任务继续等待，直到游戏重新成为前台才加载连发。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race、格式、vet、确定性双配置构建、PE/图标/权限、200 次 Hook
  重装、鼠标八组节拍、S02、S05、S09 和确定性 ZIP 全部通过。真实 Interception 驱动捕获
  因自动化进程为 Medium integrity 而明确跳过，真实原神消费仍保留为人工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.7-windows-amd64-portable.zip`
- 大小：10,906,316 bytes
- SHA-256：`da0ede8b37fe665ee8bb100302cbdf37bf5eaa82088d18e80f35170bd0519dbc`
- 条目数：19；无重复条目

## 1.3.8 内置连发分层诊断

- x86 worker 的请求/响应协议新增只读诊断快照，不会因查询而重置配置、held 状态或
  generation。主进程每两秒记录配置键事件、前台拒绝、触发 down/up、循环启停、当前 held
  键、输出组数、驱动失败、扫描码、逻辑设备和合成 Hook 回流计数。
- 诊断协议超时会关闭已经失去帧边界的 worker，并经既有注入后门禁允许的刷新路径重新创建，
  避免遗留读取协程与后续响应串帧。
- 已确认界面“触发/待触发”高速切换来自 Interception 合成输入携带真实设备句柄回到
  Raw Input，而该路径无法读取低级 Hook 的 `ExtraInformation` 标记。worker 活跃时，主状态机
  不再消费配置连发键的这份回流；真实物理保持与输出仍完全由 x86 worker 负责。
- `go test -race ./internal/input ./internal/shell -count=1` 于 2026-07-28 通过。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race、格式、vet、双配置构建、PE/图标/权限、Hook 生命周期、
  S02、S05、S09 和确定性 ZIP 均通过。真实 Interception 捕获因自动化进程为 Medium
  integrity 而明确跳过，真实原神消费仍属于人工门禁。
- 最终复审又补齐诊断超时后的配置保留与 worker 重建；随后重新通过 input/shell race、
  Debug/Release 双构建、PE/权限/便携布局核验并重打候选包。

- 文件：`artifacts/release/GenshinTools-1.3.8-windows-amd64-portable.zip`
- 大小：10,916,363 bytes
- SHA-256：`39cf1d6dc976349b79b04acbc54c8c694d55d0a8f51bf4f65a25ca13bd52fa2b`
- 条目数：19；无重复条目

## 1.3.9 物理触发边沿透传

- 1.3.8 实机日志确认：F 对应身份码 `582`、扫描码 `0x21`、逻辑键盘 1 和游戏前台 PID
  均正确；3543 组驱动输出全部成功，7086 个合成 Hook 事件与 down/up 数严格相等，排除了
  worker 未启动、前台门禁、扫描码、直接 IOCTL ABI 和驱动写入失败。
- 与 FlairBloom 当前公开实现逐项复核后，保留的关键差异是物理 Hook 返回值：FlairBloom
  始终调用 `CallNextHookEx`，旧 worker 则在匹配游戏前台配置键后返回 `1`。新版完全取消
  worker 的吞键路径；物理 down/up 先原样进入游戏，worker 只维护 held/generation 并附加
  带 marker 的 Interception 重复。
- 主进程仍在 worker 活跃时忽略配置键的 Raw Input 合成回流，因此物理边沿透传不会恢复
  1.3.7 的 UI 状态振荡。
- `go test -race ./internal/input ./internal/shell -count=1` 于 2026-07-28 通过。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过：全量普通/race、格式、vet、双配置构建、PE/图标/权限、Hook 生命周期、
  S02、S05、S09 和确定性 ZIP 均通过。真实 Interception 捕获因自动化进程为 Medium
  integrity 而明确跳过，真实原神消费仍属于人工门禁。

- 文件：`artifacts/release/GenshinTools-1.3.9-windows-amd64-portable.zip`
- 大小：10,916,334 bytes
- SHA-256：`cf1d801bf5be620a13422781d5f858ae537717a41d64260946b3407b05631ace`
- 条目数：19；无重复条目

## 1.3.10 Raw Input 物理保持状态

- 1.3.9 已由实机确认 10ms 连发能够被原神消费，但用户保持 F 时按下其他键，worker 仍收到
  一次无 Interception marker 的 F-up 并错误结束循环；中断记录中的 `triggerUps` 与
  `repeatStops` 同步增加，而 `outputFailures` 和前台拒绝始终为零。
- 低级 Hook 不再写入 held/generation，只保留合成回流诊断并始终调用 `CallNextHookEx`。
  主窗口已注册的设备级 Raw Input 现在把配置键 down/up 按顺序转发给 x86 worker，作为
  物理保持状态的唯一依据。带 Interception 或项目 SendInput marker 的设备栈回流在转发前
  丢弃；插件链仅在低级 Hook 层复制出的无标记 F-up 因而不能再终止循环。
- Raw Input 到 worker 使用有界串行队列和有确认的 IPC；队列溢出时禁用输入并明确报错，
  不允许漏掉 key-up 后继续输出。worker 停止或重配会清空 held/generation。
- 新增回归用例覆盖“F down → G down/up → F 仍保持”，并验证两类合成 marker 都不会进入
  Raw Input 物理队列。
- `go test -race ./internal/input ./internal/shell -count=1` 和
  `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过；S13 包含全量普通/race、格式、vet、确定性双配置构建、PE/图标/权限、
  200 次 Hook 生命周期、鼠标八组短节拍、S02、S05、S09 和 ZIP 复核。真实 Interception
  捕获因自动化进程为 Medium integrity 而明确跳过，本次“其他键不打断”仍需真实原神复验。

- 文件：`artifacts/release/GenshinTools-1.3.10-windows-amd64-portable.zip`
- 大小：10,919,471 bytes
- SHA-256：`ca4d738a7cf7a332471aab559590b04b552fcc55a6ed6fc234f152fde0908393`
- 条目数：19；无重复条目

## 1.4.0 Interception 驱动物理账本与启动窗口保持

- 1.3.10 实机日志再次确认 Raw Input 不是物理真值：10ms 输出期间仍出现无 marker 的
  F-up，`triggerUps`、`repeatStops` 同步增长且 `outputFailures=0`。因此低级 Hook 和
  Raw Input 均不再具有修改 held/generation 的权限。
- x86 worker 对十个 Interception 键盘设备事务式设置 `FILTER_KEY_ALL`，等待设备事件后
  通过 `IOCTL_READ` 取得驱动 stroke，先向同一逻辑设备原样 `IOCTL_WRITE`，再把匹配的
  配置键交给 held 状态机。其他键只透明转发，不访问配置键状态；合成重复的 WRITE 不会
  重新进入同一读取队列。
- 过滤器部分安装失败会回滚已经设置的设备；READ/WRITE 失败会终止 worker，进程和设备
  句柄关闭负责解除过滤。正常关闭以 50ms 有界等待观察 shutdown，避免 worker 已退出但
  全局键盘仍被过滤。
- 纯净启动和注入启动都把本次 launch snapshot 的 `PostBehavior` 固定为 `PostKeep`；
  无论持久化设置曾为最小化还是退出，点击这两个显式启动按钮后都保持启动器当前窗口状态。
- 新增驱动协议 ABI/IOCTL、F down 期间 G down/up 不修改 held，以及三种旧 PostBehavior
  均归一为 PostKeep 的回归测试。
- `go test -race ./internal/input ./internal/shell -count=1` 和
  `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过；S13 覆盖全量普通/race、格式、vet、确定性双配置构建、x86 worker、
  PE/图标/权限、200 次 Hook 生命周期、托盘生命周期、鼠标八组短节拍、S02、S05、S09
  和 ZIP 复核。真实驱动 READ 与原神组合仍属于本次人工复验门禁。

- 文件：`artifacts/release/GenshinTools-1.4.0-windows-amd64-portable.zip`
- 大小：10,920,001 bytes
- SHA-256：`4b7814041c70b486198efbe736e6a74e7b8f8c6d89407559e95e1da533301d5e`
- 条目数：19；无重复条目

## 1.4.1 首次 down 立即输出与跨键伪 up 隔离

- 1.4.0 实机确认驱动读取路径能够在其他键打断后恢复，但恢复依赖 Windows 再次发送 F 的
  长按重复 down，延迟明显。日志同时显示停止边沿来自逻辑设备 4，而持续 down 和旧输出
  设备显示为逻辑设备 1，证明输入链存在跨设备重路由。
- 删除 repeat goroutine 的 1ms 启动等待；Interception 捕获路径已经先原样回写物理 down，
  因此首次 F-down 可立即发送第一组输出，不存在点按/长按判定。
- 每次按下记录其真实 Interception 输入设备，重复 down/up 写回同一设备，不再固定使用
  逻辑键盘 1。诊断拆分 `lastDevice`、`lastOutputDevice` 和与 `heldKeys` 对齐的
  `heldDevices`。
- F-up 使用 3ms settle 和 25ms 跨键邻接历史判断；若它与其他键 stroke 紧邻，则记为
  插件/虚拟设备切换副产物并丢弃，不清 held、不推进 generation、不停止原循环。独立真实
  F-up 结束本次按下；后续每个 F-down 无论点按、连按或间隔按都会立即开始新一轮。
- 新增 `releaseChecks`、`releaseSuppressed` 日志，并增加首次 down 无长按等待、物理输出
  设备保持和“F down → G down/up → 伪 F-up 不停止”的回归测试。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过；覆盖全量普通/race、格式、vet、x86/x64 双构建、PE/图标/权限、托盘、
  200 次 Hook 生命周期、鼠标八组短节拍、S02、S05、S09 和确定性 ZIP。

- 文件：`artifacts/release/GenshinTools-1.4.1-windows-amd64-portable.zip`
- 大小：10,923,411 bytes
- SHA-256：`e78ff89061f4af79f0f4b96ed9d7c8a974f97b5358860068c18d56e582ad0f34`
- 条目数：19；无重复条目

## 1.4.2 硬件式 make 重复与跨键零重启

- **实机结论：失败，不应作为候选版本继续使用。** 驱动日志显示 make 持续输出且无
  IOCTL 故障，但原神不消费缺少配对 break 的重复 stroke；该路径已在 1.4.3 撤销。
- 实机日志确认 1.4.1 的邻接过滤无法可靠识别伪 F-up：20 次候选中仅过滤 9 次，其余
  候选均结束 repeat goroutine；后续恢复依赖 Windows 的长按重复延时，因此出现约两秒
  的跨键中断。
- 内置连发改为标准键盘 typematic 形态：物理 F-down 原样透传后，每个间隔只向同一
  Interception 逻辑设备发送一个 make（down）stroke，不再为每次重复合成 break（up）。
  用户的真实物理 F-up 是本次保持会话的唯一正常结束边沿。
- 删除 3ms settle、25ms 邻接历史及其异步判断。第一次物理 down 立即建立会话并立即
  输出；G/WASD 等其他键只执行原样 READ/WRITE 转发，不访问 F 的 held/generation，
  也不会触发新的长按判断。
- 重复输出不再在驱动互斥锁外休眠。真实释放、配置切换和 worker 退出时，使用与输出
  共用的短临界区串行发送最终安全 break，保证最后一个在途 make 不会造成粘键，同时
  不阻塞其他物理键。
- 诊断字段由 `outputPairs` 改为 `outputMakes`，准确表示驱动收到的连续 make 数；
  `releaseChecks` 继续记录真实驱动读取路径观察到的配置键 break，供下一次实机日志
  区分保持会话和输出故障。
- `./scripts/test-s13-release.ps1 -ShellIterations 1 -SkipOnlineProvider` 于
  2026-07-28 通过；覆盖全量普通/race、格式、vet、x86/x64 双构建、PE/图标/权限、
  Hook 生命周期、S02、S03、S05、S09 和确定性 ZIP。

- 文件：`artifacts/release/GenshinTools-1.4.2-windows-amd64-portable.zip`
- 大小：10,921,186 bytes
- SHA-256：`87264ee109ef3224ff9c0cf0bd3a9c486038994d01be6a1b76e61491aced6510`
- 条目数：19；无重复条目

## 1.4.3 配对输出恢复与释放边沿前置隔离

- 1.4.2 实机日志确认约 16 秒内成功写入 1240 个 make，前台门禁和驱动错误均为零，
  但原神内没有产生连发效果，因此恢复已由实机验证可用的 Interception down/up 配对。
- 捕获线程不再先把配置键 F-up 交给 Windows 再猜测其来源。F-up 在驱动读取边界先进入
  50ms 释放确认区，期间 held/generation 和原连发 goroutine 完全不变，配对输出持续。
- 确认窗口内观察到后续物理 F-down 时，候选 up 被判为跨键/设备切换产生的伪释放并
  丢弃；不会停止旧循环、不会创建新循环，也不会等待 Windows 的长按重复延迟。确认无
  后续 down 才停止循环，并把暂存的真实 up 按原逻辑设备顺序写回。
- G/WASD 等非配置键不进入确认状态机，仍在 Interception READ 后同步原样 WRITE。
  输出互斥只包围单个配对发送及最终释放；`PressDevice` 在 down/up 间不会持有驱动锁，
  因此其他物理键可在连发周期中即时穿过。
- 新增回归用例验证“F-up 确认期间输出计数持续增长”和“后续 F-down 取消候选但不重启
  会话”；input/shell race、S02、S03、S05、S09、双架构构建、PE/图标/权限及确定性
  ZIP 均于 2026-07-28 通过。

- 文件：`artifacts/release/GenshinTools-1.4.3-windows-amd64-portable.zip`
- 大小：10,925,329 bytes
- SHA-256：`2f2958df660f76511a6ab1c0e3c2f78c5df2027fbda0b8bffd3f72a07fb53aa8`
- 条目数：19；无重复条目

## 1.4.4 物理触发抑制与合成脉冲独占

- **实机结论：失败，不应作为候选版本继续使用。** 日志确认驱动高速输出和物理抑制
  都已生效，但游戏消费频率没有恢复；进一步与 FlairBloom 源码逐行对照后发现合成输出
  仍错误地跟随物理设备 4，而参考实现固定使用逻辑键盘 1。
- 1.4.3 实机日志证明用户感知降频时 repeat 会话没有停止：每两秒仍稳定新增约
  190–194 组 down/up，`repeatStarts/repeatStops`、前台 PID 和设备号不变，驱动失败
  为零。因此根因不是调度暂停，而是游戏在“物理 F 保持 + 合成 F 脉冲 + 其他键”
  的组合状态下只消费极少数合成脉冲。
- 游戏前台时，Interception 捕获的物理配置键 F down/up 现在只控制 held 会话，不再
  回写到游戏输入栈；游戏只收到同一逻辑键盘设备上的完整、平衡合成 down/up。该行为
  对齐 AHK 热键用物理触发替换合成输出的语义，避免物理 typematic 与合成脉冲竞争。
- 非配置键不进入抑制路径，仍在驱动 READ 后立即原样 WRITE；游戏不在前台时配置键
  也正常透传。焦点切换后收到的真实 F-up 会清理旧会话，不会在其他程序中继续输出。
- 新增 `physicalSuppressed` 诊断计数，可直接确认物理 F 被替换而非送入游戏；
  `outputPairs` 继续表示游戏实际收到的平衡合成组数。
- 全量普通测试、input/shell race、Debug/Release 与 x86 worker 构建、PE/图标/权限和
  便携包复核于 2026-07-28 通过。

- 文件：`artifacts/release/GenshinTools-1.4.4-windows-amd64-portable.zip`
- 大小：10,924,750 bytes
- SHA-256：`530575891eedbf0c420a58a6615dd30b76e75a67939dcdef3287f3a2706dda4b`
- 条目数：19；无重复条目

## 1.4.5 FlairBloom 逻辑键盘路由对齐

- **实机结论：频率恢复，但仍会被其他按键产生的 F-up 打断，因此不能作为最终候选。**
  日志确认设备 1 路由有效，同时 9 次 release 候选仅 1 次被取消，其余 8 次推进了
  `repeatStops`；生产路径额外安装的 Interception 全量物理过滤仍与参考实现不同。
- 复核固定上游提交
  `research/upstream/flair-bloom-3873b4b90499457a0fe321f09a3a6d34c10462a3`：
  `packages/win-input/src/interception.rs` 的 `find_keyboard()` 从 1 开始并返回第一个
  Interception 键盘，因此其所有合成键盘输出固定走逻辑设备 1；物理 Hook 最终始终
  `CallNextHookEx`，不吞物理触发。
- 本机日志持续显示物理设备与旧合成设备均为 4。1.4.5 撤销 1.4.4 的物理 F 抑制，
  配置键 make 先原样回到物理设备再建立 held 会话；合成 down/up 和清理 up 则严格
  固定写入设备 1。`lastDevice` 与 `heldDevices` 保留物理设备，`lastOutputDevice`
  必须显示 1。
- 调度相位继续与上游 `scheduler.rs` 一致：10ms 请求使用 3ms DownPhase 和 7ms
  rest；1ms 请求在同一拍发送 down/up。新增回归断言确保物理设备 4 不会再次把合成
  路由从设备 1 带偏。
- 全量普通测试、input/shell race、Debug/Release 与 x86 worker 构建、PE/图标/权限和
  便携包复核于 2026-07-28 通过。

- 文件：`artifacts/release/GenshinTools-1.4.5-windows-amd64-portable.zip`
- 大小：10,925,291 bytes
- SHA-256：`6d75efd4afc65b029d7f5adb4842ac5808c08faef867b61814019c1339c2df68`
- 条目数：19；无重复条目

## 1.4.6 参考 Hook 链路完整对齐

- 删除生产 worker 对十个键盘设备的 `FILTER_KEY_ALL`、`IOCTL_READ` 和物理 stroke
  READ→WRITE 回灌；Interception context 不再接管物理输入，只向固定逻辑键盘 1 发送
  合成 down/up。这与 FlairBloom `InterceptionBackend` 的职责边界一致。
- 物理保持状态只由最终阶段安装的 `WH_KEYBOARD_LL` 管理；标记为
  `0x51485844` 的自身输出先过滤，非注入物理 down/up 分别更新各自键位，回调最终始终
  `CallNextHookEx`。G/WASD 的状态不访问 F 的 held/generation。
- 删除驱动捕获专用的 50ms release 猜测、设备 stroke 映射和 suppression 状态。新增
  Hook 级回归用例验证 `F down → G down/up → F 仍 held → F up → F released`，
  并将 Hook 事件处理提取为类型安全方法，避免测试使用不安全 uintptr 伪造系统指针。
- 合成输出继续固定设备 1，10ms 的 3ms down/7ms rest 相位保持不变。
- 全量普通测试、input/shell race、Debug/Release 与 x86 worker 构建、PE/图标/权限和
  便携包复核于 2026-07-28 通过。

- 文件：`artifacts/release/GenshinTools-1.4.6-windows-amd64-portable.zip`
- 大小：10,918,863 bytes
- SHA-256：`70b3c6b2b014f088b43900fb354cc625b0e6bd73c249f7f4e9960874ff9049bc`
- 条目数：19；无重复条目

## 1.4.7 驱动物理触发隔离与设备 1 输出合并

- 根据 1.4.6 实机日志，低级 Hook 在按住 F 并操作其他键时收到无
  `ExtraInformation` 标记的 F-up；该事件与 `repeatStops` 一一对应，而驱动输出、
  前台门禁和输出频率均正常。低级 Hook 因此降为纯诊断来源，不再具有改变
  held/generation 的权限。
- 恢复生产 worker 的 Interception `FILTER_KEY_ALL`/`IOCTL_READ` 链路。非配置键同步
  写回原物理设备；游戏前台的配置键物理 down/up 被消费，只用于建立或结束与来源设备
  绑定的 held 会话。跨设备配置键 up 被抑制且不会推进 generation。
- 保留已由 1.4.5 实机确认频率正常的逻辑设备 1 输出：第一次物理 down 立即创建循环，
  游戏只接收设备 1 上带 `0x51485844` marker 的平衡 down/up，不再同时接收物理 F 和
  合成 F。切出游戏时配置键恢复物理透传，匹配来源设备的 release 仍会清理旧会话。
- 新增 `physicalSuppressed`、`hookTriggerUpsIgnored` 和
  `crossDeviceUpsIgnored` 诊断字段；回归覆盖无标记 Hook F-up、其他键透传、跨设备
  F-up、同设备真实 F-up、立即首发、设备 1 路由和游戏外透传。
- 2026-07-29 自动门禁通过：全项目测试与 race、格式/vet 策略、三轮 Win32 shell
  生命周期、S03 短矩阵、S05 真实进程启动、S09 有界注入、Debug/Release/x86 worker
  构建、PE/图标/权限和确定性 ZIP 均通过。执行终端为 Medium integrity，因此真实
  Interception 驱动捕获按设计跳过，真实原神内同时按住 F 和其他键仍是人工门禁。

- 文件：`artifacts/release/GenshinTools-1.4.7-windows-amd64-portable.zip`
- 大小：10,923,377 bytes
- SHA-256：`105340f41fb8e145cb88c33fef0c02e013680431f3c88943d269870f9a4cfc58`
- 条目数：19；无重复条目

## 1.4.8 内置键盘连发退休

- 根据连续实机结论，内置键盘连发不再作为产品功能提供；键盘连发唯一产品路径为便携包
  `AHK_F.exe` 及其“随游戏启动/关闭”生命周期托管。鼠标左/右键连点继续保留。
- 输入增强页删除键盘模式、连发键列表、添加/删除入口、键盘模式开关热键设置和
  Interception 驱动状态/下载入口；只显示左键连点、右键连点、各自开关、全局停止、
  间隔、AHK 生命周期选项及 AHK 激活状态。
- 旧配置若保存 `ModeKeyboard`，启动时迁移为 `ModeMouseLeft + Enabled=false` 并持久化。
  Native `Configure` 重复执行同一失败关闭转换，旧键盘开关热键在 Hook/Raw
  Input/轮询路径均被忽略，worker 同步请求固定 `Enabled=false`。即使游戏 PID 和
  注入后五秒门禁都已放行，也不会创建 `GenshinTools-input.exe` 进程。
- 注入最终化仍释放鼠标热键门禁，但明确跳过内置键盘 worker；顺序收敛为
  “主观察 Hook → 捕获的外部输入工具 → 便携 AHK”，AHK 仍保持最后加载。
- 新增回归覆盖产品模式列表仅含左右鼠标、旧键盘配置迁移、键盘热键 Hook/轮询无效，
  以及最终化门禁后 worker PID 仍为零。
- 2026-07-29 自动门禁通过：全项目测试与 race、格式/vet 策略、三轮 Win32 shell
  生命周期、S05 真实进程启动、S09 有界注入、Debug/Release 构建、PE/图标/权限和
  确定性 ZIP 均通过。S03 的真实桌面鼠标捕获因前台被其他桌面进程抢占而明确跳过；
  鼠标单元/race 与全项目回归通过，未把跳过写成实机成功。

- 文件：`artifacts/release/GenshinTools-1.4.8-windows-amd64-portable.zip`
- 大小：10,912,037 bytes
- SHA-256：`4d9d6332ffcc75ac1749892657c22a4d14fb54c63f2206e1b8b4bd2ca9870629`
- 条目数：19；无重复条目

## 尚未关闭的人工门禁

1. 真实原神/反作弊环境下的输入、截图、覆盖层、启动和可选插件/注入组合。
2. 锁屏/解锁、休眠/唤醒、RDP、切换用户、UAC 取消和杀毒软件隔离。
3. 125%/150%/200% DPI、多显示器、显示器拔插、主题和高对比度。
4. 干净 Windows 10/11 x64 首次运行、自更新和真实回滚。

在以上门禁关闭前，当前 ZIP 只能称为候选包，不能称为正式公开版本。
