# S09 注入适配层设计

状态：已实施并通过验收  
日期：2026-07-17

## 1. 上游静态审计结论

锁定提交 `b5a050ebd319341bddc4189491c90c22162d33fa` 同时携带 `Launcher.dll` 和 `Launcher_2.exe`，但仓库没有对应源代码、来源仓库、构建说明或版本协议。两者均为 x64 PE，均未签名且没有文件版本资源：

> 2026-07-22 补充：上游后来公开了独立的 `FufuLauncher/FufuLauncher.UnlockerIsland` 源码仓库。该事实不改变锁定提交内二进制不可复现、哈希不符的审计结论。本项目只引用新仓库的 `Plugins/config.ini`/`File=*.dll` 目录约定，不切换回其 Launcher 二进制或无限等待/递归直接注入实现。

| 文件 | 大小 | SHA-256 | 上游调用方式 |
|---|---:|---|---|
| `Launcher.dll` | 74240 | `BE35BACE23ED16CCE99E80F40A67ECFB7EE5CB6B87BA3CD06A643F613263E230` | 启动器进程静态 `LoadLibrary` 后调用四个 C ABI 导出 |
| `Launcher_2.exe` | 80896 | `9F2D3323384A96C432E845EA681CD4B1D40AC44F12BCA135CEA75ABB8182CC5C` | 以 `runas` 启动并传入游戏路径 |

仓库 `Assets/Launcher/hash.txt` 的两个 SHA-512 均不匹配上述文件。静态导入表显示两者使用 `CreateProcessW`、`VirtualAllocEx`、`WriteProcessMemory`、`CreateRemoteThread`、`ResumeThread` 等 API。上游 DLL 路径还存在以下生命周期问题：

- DI/静态初始化会把不透明 DLL 加载进主 UI 进程，模块崩溃可直接带走启动器。
- 修改进程级当前目录和 DLL 搜索目录，影响其他并发功能。
- 提权子进程 `WaitForExitAsync` 没有超时或取消。
- `--elevated-inject` 解析会忽略调用方传入的 DLL 路径，重新取默认模块。
- 插件目录只取第一个 DLL，没有架构、版本、来源、哈希或游戏兼容校验。

因此本项目不复制、不执行、不打包上述两个二进制，也不声称其 MIT 仓库许可证足以证明未知二进制的可再分发来源。

## 2. 本项目架构

```text
Win32 UI / launch engine
  ├─ pure launch ───────────────> S05 NativeStarter（始终可用）
  └─ injection launch task
       ├─ load + validate module manifest
       ├─ PE/hash/version/game compatibility preflight
       └─ start GenshinTools-injector.exe with bounded request
             ├─ repeat all trust-boundary validation
             ├─ CreateProcessW(CREATE_SUSPENDED)
             ├─ bounded LoadLibraryW remote thread
             ├─ success: ResumeThread + return PID
             └─ failure/cancel: terminate only its suspended child
```

主程序和 helper 共享版本化 JSON request/result schema，但不共享外部 DLL 地址或进程内指针。helper 崩溃、卡死或被杀毒软件隔离只会形成明确失败；主程序 UI 和纯净启动不受影响。

## 3. 模块 manifest

模块位于 EXE 同级 `data\injection\modules\<id>\`。`module.json` 必须包含：

- `schemaVersion=1`、稳定 `id`、名称、来源 HTTPS URL 和许可证标识。
- `adapterApi=1`、相对且不越界的 DLL 文件名、精确 SHA-256、`architecture=amd64`。
- 文件版本；没有 VERSIONINFO 的模块必须显式声明 `allowUnversioned=true`。
- 明确的游戏版本列表、允许的 `YuanShen.exe`/`GenshinImpact.exe` 列表。
- 可选的必需导出名；声明后逐项核验 PE export table。
- 普通和延迟导入表必须可有界解析；同目录/游戏目录旁加载副本一律拒绝，非 API-set 依赖只接受 System32 实物。

manifest 只提供完整性和兼容声明，不等同于代码签名或可信背书。S10 才负责从审计来源安装/更新模块；S09 不联网下载任何 DLL。

## 4. 启动与失败规则

- 默认关闭注入，且需要单独确认反作弊、游戏崩溃和账号风险。
- 未选择模块、来源/许可证缺失、路径越界、哈希错误、错架构、文件版本错误、导出缺失或未知游戏版本均失败关闭。
- helper 最长运行时间和远程线程等待时间均有硬上限；取消时只结束 helper 及其尚未恢复的自有 suspended child。
- `LoadLibraryW` 成功后才恢复游戏主线程；失败不会自动恢复一个状态不明的进程。
- helper 成功返回的 PID 还要由主程序按游戏 EXE 路径重新核验，再进入 S05 的运行/退出观察。
- 注入失败不自动重试、不改用别的 DLL、不隐藏检测行为；UI 提供明确的“纯净启动”动作。

## 5. S09 退出门禁

1. 主进程导入表和运行路径均不加载外部模块。
2. 缺失、损坏、错架构、错哈希、错版本、缺导出和未知游戏版本 fixture 全部拒绝。
3. helper 超时、崩溃、UAC 取消和结果 JSON 损坏不会卡住 UI，也不会留下 suspended 游戏进程。
4. 仅在本项目自有无害 fixture 上验证远程加载；不为验收启动或注入用户真实游戏。
5. 关闭注入时与 S05 `NativeStarter` 路径一致；0.8.0 Debug/GUI/helper 构建和版本元数据通过。
