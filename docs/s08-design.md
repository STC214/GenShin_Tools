# S08 截图与性能覆盖层设计

状态：实施基线  
日期：2026-07-17

## 1. 上游审计结论

锁定版 FufuLauncher 为截图单独安装第二个 `WH_KEYBOARD_LL`，每次命中快捷键后临时创建 WinRT/D3D 捕获对象；FPS 覆盖层另起窗口、ETW、性能计数器三个线程，并要求管理员权限。该结构存在重复 Hook、按托管线程 ID 发送 `WM_QUIT`、无界 `Task.Run`、窗口句柄只在失效后更新、多个线程互相调用停止以及 ETW 会话异常残留风险。

本项目不复制这些生命周期。截图使用主窗口 `RegisterHotKey`，不增加低级键盘 Hook；捕获编码使用容量为 1 的 single-flight worker；覆盖层窗口和采样器均由一个可停止的 session 所有，游戏 PID、创建时间和窗口句柄必须同时核验。

DXGI ETW provider、Present_Start 事件及消费结构另以 PresentMon `de4b9c40bc97d237a77e539d1bd2835b743b33f0` 和 Windows SDK ABI 为技术核对来源；项目未复制或打包 PresentMon 代码/二进制。

## 2. 线程和所有权

```text
Win32 UI thread
  ├─ RegisterHotKey / WM_HOTKEY ──> screenshot queue (capacity 1)
  ├─ game process snapshots ──────> session target reconciliation
  └─ settings page ───────────────> enable/disable/configure

screenshot worker
  └─ revalidate PID generation + HWND -> capture -> PNG temp+rename

overlay session
  ├─ sampler worker: CPU / GPU / FPS source, 1 Hz bounded publication
  └─ dedicated locked OS thread: click-through layered HWND + message loop
```

任何 worker 都不直接修改主窗口控件。UI 只接收不可变快照；停止顺序为取消采样、关闭覆盖层窗口、等待有界退出、注销热键。

## 3. 截图边界

- 默认快捷键为 `Ctrl+Shift+F10`，避开 S03 默认触发键 F8 和停止键 F12。
- 配置保存前检查与输入触发键、输出键和停止键的虚拟键冲突；即使修饰键不同也拒绝，因为 S03 低级 Hook 按物理键处理停止逻辑。
- 每次捕获重新核验进程创建时间并重新枚举该 PID 的可见顶层窗口，处理 Unity 窗口重建。
- 最小化、零尺寸、超过像素/内存上限、受保护或返回全空帧时明确失败，不写“成功”文件。
- 首选带 1500ms 上限的 `WM_PRINT`；调用失败或成功但全空时，只在目标窗口可见且未最小化时使用窗口 DC `BitBlt`，不会在 UI/Hook 线程编码 PNG。
- 默认目录持久化为 EXE 相对的 `data\screenshots`，便携目录移动后仍可用；用户显式选择的外部目录保留绝对路径。
- 文件名包含毫秒和无碰撞序号；先写同目录临时文件、`Sync`，再原子改名。

## 4. 性能采样边界

- CPU 使用率来自目标进程 kernel/user time 增量，并按逻辑处理器数归一化。
- GPU 使用率通过英文 PDH `GPU Engine` wildcard 读取目标 PID 的 3D engine；不可用、超时或实例消失时显示 `N/A`，不阻塞窗口线程。
- FPS 使用目标 PID 的 DXGI Present ETW 事件计数。会话名包含本程序 PID 和随机 generation；启动前不停止其他程序会话；权限不足或 provider 不可用时只将 FPS 降级为 `N/A`，CPU/GPU 继续工作。
- 不把桌面合成刷新率伪装成游戏 FPS，也不通过注入获得帧率。

## 5. 覆盖层窗口

- 独立 OS 线程拥有 HWND、字体、画刷和消息循环。
- `WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE`，置顶但不激活、不进入 Alt-Tab、不接收鼠标。
- 每次刷新重新核验目标 HWND 和客户区屏幕坐标；最小化、隐藏、进程退出或 generation 改变时立即隐藏/销毁。
- 多显示器和 DPI 变化时按目标窗口当前 DPI 重算尺寸；不缓存主窗口 DPI。

## 6. S08 退出门禁

1. 快捷键与 S03 同时启用，不自触发、不重复安装 Hook，冲突配置被拒绝。
2. 连续快捷键只保留一个执行中和一个待处理请求；退出时不再写新截图。
3. 窗口重建后能重新定位；最小化/空帧不生成假成功 PNG。
4. 覆盖层样式确认 click-through/no-activate，游戏退出后 HWND、计时器、PDH、ETW 和 goroutine 回到基线。
5. FPS/GPU 数据源失败时显示 `N/A`，不影响 CPU、截图或纯净游戏启动。
6. 自动测试、竞态测试、锁文件/只读目录/窗口失效故障注入、Win32 GUI 截图和构建元数据检查通过。
