# FlairBloom 游戏模式实现路径审计

审计日期：2026-07-26

## 固定上游

- 项目：`x-wink/flair-bloom`
- 上游地址：<https://github.com/x-wink/flair-bloom>
- 默认分支：`main`
- 固定提交：`3873b4b90499457a0fe321f09a3a6d34c10462a3`
- 固定 tree：`92413e690b13b0d3e4c56ee59f4511b4b180d7b1`
- 提交时间：`2026-07-16T19:02:06+08:00`
- 本地研究副本：`research/upstream/flair-bloom-3873b4b90499457a0fe321f09a3a6d34c10462a3`
- 上游 `LICENSE` SHA-256：`55bd91093ea61efbc1fac3fb9780801c45e828421a927a0765f84def48728dc0`

研究目录被根 `.gitignore` 排除，不进入本项目提交、便携包或自动更新包。

## 许可证边界

FlairBloom 根许可证是 CC BY-NC-SA 4.0，Cargo workspace 元数据还标为
`UNLICENSED`。其游戏模式依赖的 Interception 对非商业用途采用 LGPL-3.0，
商业用途需要另购许可证。两者都不能直接并入本项目的 MIT 授权范围。

因此本项目可以：

- 阅读源码、验证行为和记录公开 Win32/Interception API 边界；
- 独立设计自身状态机、线程模型、安全检查和 UI；
- 在许可证允许的个人非商业环境中，对用户自行安装的 Interception 进行兼容。

未经额外授权，本项目不能：

- 复制 FlairBloom 的 Rust/TypeScript 实现进入本项目；
- 把其源码、图标、DD SDK 或成品二进制加入 MIT 便携包；
- 宣称 Interception 驱动或 API 可供商业用途；
- 把第三方的非商业限制改写成 MIT 权利。

## 已确认的完整输入路径

```text
物理键盘
  │
  ├─ WH_KEYBOARD_LL（只观察，不吞物理事件）
  │    └─ 物理 down/up 去重、Hold/Toggle 规则判断
  │
  └─ FlairBloom 高精度调度线程
       ├─ 首次触发立即安排 down
       ├─ down/up 分相位调度
       ├─ generation 围栏处理停止、切配置和切后端
       └─ InterceptionBackend::send_key
            ├─ MapVirtualKeyW(VK, MAPVK_VK_TO_VSC_EX)
            ├─ InterceptionKeyStroke{scan, DOWN/UP/E0, information}
            └─ interception_send(context, keyboard_device, stroke, 1)
                 └─ interception.dll
                      └─ \\.\interception00… 设备 + DeviceIoControl
                           └─ keyboard.sys 键盘类 UpperFilter
                                └─ kbdclass / Raw Input / 游戏输入队列
```

关键结论：游戏模式输出不是 `SendInput`、`keybd_event`、窗口消息或向游戏进程
注入 DLL。它通过已安装的 Interception 键盘类过滤驱动，把扫描码事件从输入设备
栈重新送入系统。游戏因此会看到接近硬件输入路径的事件，这解释了它能进入原神而
本项目此前的纯用户态键盘输出不被消费。

## 上游具体实现

### 物理触发

- `packages/burst-engine/src/lib.rs` 在专用消息循环线程安装
  `WH_KEYBOARD_LL` 和 `WH_MOUSE_LL`。
- Hook 只把物理 down/up 交给引擎，最终始终调用 `CallNextHookEx`，不会吞掉用户原始按键。
- `physical_pressed` 独立维护每个物理键的保持状态；按其他键不会清除仍按住的触发键。
- 全局停止在普通去重之前判断，避免物理账本残留导致急停失效。
- 后端切换期间禁止启动新规则，并先经旧后端释放所有模拟按键，避免 down 走旧后端、
  up 走新后端造成卡键。

### 调度

- `packages/burst-engine/src/scheduler.rs` 使用独立 worker、命令队列、generation
  围栏和 Windows 高精度 waitable timer。
- down/up 是两个明确相位。停止、切配置、切模式和退出都会清空规则并释放所有
  `target_holds`。
- 多条规则共享同一目标键时使用 owner 集合，只让第一个 owner 发送 down，最后一个
  owner 发送 up。
- 对已成功发送但未成功释放的键另有模拟键账本；worker panic、停止超时或正常清理时
  都会补发 up。
- 上游虽然数据结构允许 `1ms`，生产路径实际把单规则下限钳到 `10ms`；多条活跃规则时
  每条规则的有效下限为 `10ms × 活跃规则数`。因此 FlairBloom 的实测成功不能证明
  Interception 在 `1ms` 连续多规则条件下仍稳定。

### 驱动输出

- `packages/win-input/src/interception.rs` 创建一个 Interception context。
- 键盘 VK 通过 `MapVirtualKeyW(..., MAPVK_VK_TO_VSC_EX)` 转为扫描码；`E0` 前缀转成
  Interception 扩展键状态。
- 键盘、鼠标按钮和滚轮分别构造 `InterceptionKeyStroke` /
  `InterceptionMouseStroke`，要求 `interception_send` 返回 `1` 才算成功。
- `information` 写入 `0x51485844`。模拟事件回到低级 Hook 时按该标记过滤，避免
  自身输出再次触发规则。
- 当前实现把第一个逻辑键盘设备 ID（1）和第一个逻辑鼠标设备 ID（11）作为发送目标，
  没有按硬件 ID 选择真实活动设备。此做法在当前测试机器可用，但多键盘、设备热插拔和
  特殊虚拟设备环境需要额外诊断。

### 安装、提权和启动

- “安装游戏模式”调用固定资源 `install-interception.exe /install`。
- 安装器通过 `ShellExecuteExW` 的 `runas` 提权并等待退出；安装完成要求重启。
- 安装前校验安装器固定大小和 SHA-256。
- 切换游戏模式时，如果当前不是管理员实例，应用用 `runas` 携带
  `--switch-mode=interception --await-pid=<old>` 重启；新实例先等旧实例释放单实例锁。
- 启动时先落到 `SendInput`，由前端根据持久化配置和权限状态恢复 Interception，
  避免未提升实例直接打开驱动通道。

## 二进制与本机核验

FlairBloom 携带的 `install-interception.exe`：

- 大小：`470528`
- SHA-256：`E137863A79DA797F08E7A137280FF2A123809044A888FD75CE9C973198915ABE`
- VERSIONINFO：Interception，`1.00 built by: WinDDK`
- Authenticode：未签名

它与 Interception 官方 v1.0.1 release 中的安装器逐字节一致。官方 x64 用户态
`interception.dll` SHA-256 为
`AB88164C11B1B48488772D4C3BFAA4509D5B0AE9DBC5A691DC4F96F0260443C8`；
x86 版本为
`9E1DEF27B804DF9BA97FD07F9DE835C70660AE568C00950102F70034E293A684`。
两个 DLL 都未签名。

当前测试机器已经安装并加载 Interception：

- 键盘类 `UpperFilters = keyboard, kbdclass`
- 鼠标类 `UpperFilters = mouse, mouclass`
- `keyboard`、`mouse` 两个内核服务均为 `RUNNING`
- `C:\Windows\System32\drivers\keyboard.sys` 签名有效，签名者为 Francisco Lopes da Silva
- `C:\Windows\System32\drivers\mouse.sys` 签名有效，签名者相同

本次审计只读取状态，没有安装、卸载、停止或修改任何驱动和注册表项。

## 审计发现的风险

1. Interception 是键盘和鼠标类 UpperFilter。安装/卸载或过滤链损坏可能导致整机键鼠
   不工作，恢复通常需要官方 `/uninstall`、重启、系统还原或恢复环境。
2. 上游驱动/安装器版本为 2017 年的 v1.0.1，官方说明仅测试到 Windows 10。新 Windows
   的 HVCI、Smart App Control、驱动阻止列表或反作弊可以阻止它，未来系统更新也可能
   改变兼容性。
3. `is_driver_installed()` 只判断能否创建 context，没有复核服务状态、UpperFilters
   顺序、驱动文件哈希/签名、重启待处理状态，也没有执行无副作用的真实发送健康检查。
4. FlairBloom 静态导入 `interception.dll`，再由安装器把未签名 DLL 放到 EXE 同级；
   DLL 在应用代码运行前加载，资源完整性表又没有钉住该 DLL。安装在受 ACL 保护的
   Program Files 时风险较低，但不适合直接照搬到管理员便携程序。
5. 用户态 context 被 `unsafe impl Send + Sync`，但实际通过全局 `Mutex` 串行使用。
   本项目不应依赖未经证明的并发调用，应把 context 固定在单一输出线程。
6. 输出设备固定取逻辑 ID 1/11；多设备、设备重连、RDP、虚拟 HID 和休眠恢复都可能使
   固定设备失效。
7. 驱动路径是系统全局的，不天然知道“原神是否前台”。FlairBloom 本身全局生效；
   本项目必须继续保留已核验游戏 PID/创建时间/前台 HWND 门禁。
8. 驱动输入仍可能被游戏规则或反作弊判定为违规；技术上能被游戏消费不代表使用安全。

## 对本项目的建议实现顺序

1. 不复制 FlairBloom 代码；新增独立的 Interception 兼容设计和许可证门禁。
2. 第一阶段只适配当前机器已经安装的官方 v1.0.1 驱动，不自动改系统 UpperFilters。
3. 把输出后端放进现有 x86 `GenshinTools-input.exe` worker，使用官方 x86
   `interception.dll` 的绝对路径动态加载；加载前校验固定大小/SHA-256，限制 DLL 搜索
   路径，逐个解析所需导出。不要静态导入同级 DLL。
4. context 只归单一输出线程所有；所有 down/up、停止、崩溃清理和后端切换都走同一线程。
5. 在现有低级 Hook 最前面过滤专用 `information` 标记，再维护每个物理键的独立保持状态。
6. 继续只在已核验原神窗口前台时输出；切出、游戏退出、锁屏、休眠、worker 断连和程序退出
   都阻塞释放所有已按下键。
7. 首版只替换键盘输出。当前鼠标连点已经在原神中生效，不同时引入鼠标驱动路径，减少回归面。
8. 提供只读诊断：服务状态、UpperFilters、驱动签名/哈希、context 创建、逻辑设备列表、
   当前后端、最后发送结果和故障降级原因。
9. 驱动安装/卸载另立高风险阶段：必须有明确许可告知、系统还原/恢复说明、固定官方哈希、
   UAC、重启状态、事务日志以及失败后回滚路径；不得静默安装。
10. 保留 `1ms` 配置能力，但先做单键/多键真实压力矩阵。FlairBloom 实际使用 `10ms`
    下限，不能把其成功经验直接外推到本项目的 `1ms` 要求。

## 当前结论

可以确认：值得适配的是 Interception 驱动输出边界，而不是 FlairBloom 的 UI、配置格式
或整套 Rust 调度器。现有 Go 项目的多键规则、独立保持状态、前台游戏核验和停止清理可以
保留，只需把键盘 worker 的最终输出端新增为经过审计的 Interception backend。

## 1.3.0 独立适配落地

- 没有复制 FlairBloom 的 Rust/TypeScript 源码，也没有打包其任何资源。
- 没有随包再分发 Interception DLL、安装器或驱动；设置页只打开固定的官方 v1.0.1 release。
- x86 输入 worker 独立实现公开设备协议边界：游戏进程出现后建立 20 设备 context，
  为每个设备登记事件句柄，并向逻辑键盘 1 提交 `KEYBOARD_INPUT_DATA`。
- 键盘扫描码事件使用 `ExtraInformation=0x51485844`；x86 worker hook 与 x64 主 hook
  均在物理状态处理前过滤该值，阻断自身输出递归。
- 游戏进程出现时只登记目标 PID，不创建 worker。注入启动必须同时确认 helper 完成和
  游戏 Running，并持续确认全部注入 DLL 仍驻留于准确游戏 PID，再等待游戏连续前台
  8 秒；主程序观察 Hook 在注入前卸载、最终阶段先重装，随后才首次创建 worker，使负责
  拦截的 worker 处于最新顺序；游戏全部退出后关闭 hook、线程、事件和驱动句柄。
- 每次输出前重新核对前台 HWND 的 PID；切到其他窗口不吞触发键，也不发送驱动事件。
- 客户端必须为 High 完整性。Medium 完整性下即使设备打开和 IOCTL 返回成功，官方 DLL
  与独立实现都可能不产生输入；因此状态探测和 worker 初始化会明确失败，不再静默计数。
