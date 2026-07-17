# S08 截图与性能覆盖层验收记录

状态：已完成  
日期：2026-07-17

## 已实现范围

- 截图：主窗口使用 `RegisterHotKey`，默认 `Ctrl+Shift+F10`，不安装第二个低级键盘 Hook；与 S03 的触发、输出和停止物理键冲突时拒绝配置。
- 捕获：每次按 PID 与创建时间重新核验进程并枚举当前可见顶层 HWND；容量 1 的队列最多保留一个执行中和一个待处理请求。
- 输出：1500ms 有界 `WM_PRINT`、失败或全空时的可见窗口 DC 回退、空帧和 1 亿像素上限检查；PNG 同目录临时写入、`Sync` 后原子替换。
- 便携目录：默认保存位置为 EXE 相对的 `data\screenshots`；用户选择项目外目录时保留绝对路径。
- 性能数据：进程 CPU 时间增量、目标 PID 的英文 PDH `GPU Engine` 3D 实例、目标 PID 的 DXGI Present ETW；单项不可用时独立显示 `N/A`。
- 覆盖层：独立锁定 OS 线程和消息循环，使用 topmost/layered/transparent/toolwindow/noactivate 样式；500ms 重新核验目标 HWND、位置和 DPI，窗口连续失效 5 秒后自毁。
- 生命周期：游戏 PID/创建时间改变、设置切换、窗口自毁或程序退出时统一取消 session，并释放进程 HANDLE、PDH query、ETW trace、timer、HWND、字体和画刷。

## 自动化验证

- 真实 Win32 顶层夹具：正确拒绝伪造的旧进程代际，重新发现当前 HWND，并生成可解码、尺寸为 `360x220` 的 PNG。
- 截图突发测试：执行中请求被阻塞时仅接受一个待处理请求，第三个请求立即丢弃；释放后恰好完成两次。
- 覆盖层夹具：自动检查五项扩展样式、`WM_NCHITTEST=HTTRANSPARENT`、`WM_MOUSEACTIVATE=MA_NOACTIVATE`，并在 1 秒内退出。
- 原生采样器：实际打开当前进程 HANDLE，启动或安全降级 DXGI ETW/PDH，取得有效 CPU 样本并验证关闭后所有本地所有权清零。
- ETW ABI：x64 下 `WNODE_HEADER`、`EVENT_TRACE_PROPERTIES`、`EVENT_HEADER`、`EVENT_RECORD` 和 `EVENT_TRACE_LOGFILEW` 的尺寸及关键回调偏移均有断言。
- `scripts/test.ps1 -Race`：普通测试、竞态测试、`go vet`、格式和 diff whitespace 门禁全部通过。
- Win32 界面截图：`build/s08-media.png` 在 DPI 96 下导航、八行设置和状态栏完整，无裁切或重叠。

## 本阶段发现并关闭的真实稳定性问题

- 枚举窗口时同步读取跨进程标题会被挂起的目标线程拖死；现只读取窗口管理器维护的可见性、style、owner 和 rectangle。
- 把 Go 指针经 `LPARAM` 传给 `EnumWindows` 在 race/checkptr 下会 fatal；现使用整数 token 与并发上下文表。
- 覆盖层启动超时后 HWND 可能晚到并泄漏；现设置取消标志，晚到窗口立即销毁。
- UI 已退出或结果通道饱和时，覆盖层任务不再阻塞发送；未交付的新 session 会被有界关闭。

## 构建与人工边界

- `scripts/build.ps1` 与 `scripts/verify-artifact.ps1` 验证 `0.7.0` Debug/GUI EXE、版本资源、子系统、图标和便携布局。
- 本机夹具覆盖 Win32 捕获、样式、采样源启动/降级和资源释放；真实原神独占全屏、无边框、多显示器、DPI 热切换以及实际 DXGI 帧率/多 GPU 数值对照保留到 S13 发布回归，避免为验收主动启动或操作用户游戏。

## 对应稳定性清单

- `D13`：不新增截图 Hook，使用 `RegisterHotKey` 并检查物理键冲突。
- `E01`～`E03`、`E08`：GDI/DC 所有权和覆盖层穿透。
- `H05`：PID 与创建时间共同确定进程代际。
- `K01`～`K12`：捕获、队列、窗口重建、ETW/PDH、超时、回调指针和资源退出。
