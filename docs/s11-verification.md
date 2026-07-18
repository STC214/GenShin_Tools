# S11 程序设置、语言与 UI 收尾验收记录

验收日期：2026-07-18  
版本：`0.10.0`  
上游审计基线：`b5a050ebd319341bddc4189491c90c22162d33fa`

## 1. 交付范围

- 简中、英文和系统语言选择；资源表缺失回退、键集合一致及格式化参数一致性测试。
- 简洁暗色、跟随 Windows 主题和高对比度系统色回退；不包含皮肤、在线背景或视频背景。
- 托盘、窗口记忆、安全最小尺寸、进程优先级及持续 CPU 异常提醒设置。
- 关于与构建身份、上游审计 SHA，以及后台生成的隐私过滤诊断导出。
- 全部保留页面的可见文案、状态和输入提示完成资源化；语言切换时同步刷新窗口、导航、托盘和 EDIT 提示。
- 设置、启动、截图/覆盖层、注入和插件配置统一遵守磁盘保存成功后再提交内存状态。
- 插件页和商店使用随客户区高度压缩的统一绘制、命中与子控件坐标。

## 2. 自动化验证

2026-07-18 执行：

```powershell
go test ./... -count=1
go vet -unsafeptr=false ./...
go test -race ./internal/localization ./internal/shellconfig ./internal/cpumonitor ./internal/diagnostics ./internal/shell -count=1
./scripts/build.ps1 -Configuration Both -Version 0.10.0
./scripts/verify-artifact.ps1 -Version 0.10.0
./scripts/test-s02-shell.ps1 -StressIterations 2
gofmt -l cmd internal tools
git diff --check
```

结果：

- 全仓测试、S11 相关竞态测试、标准 vet、源码格式及差异检查通过。
- 简中和英文资源 key 与 `fmt` 指令完全一致；Win32 壳层不再包含直接显示的单语中文字符串或硬编码输入提示。
- 主题、高对比度、配置规范化、CPU 持续阈值、诊断脱敏/原子替换，以及各类设置写盘失败回滚均有自动化覆盖。
- 单实例唤醒、最小化到托盘/恢复、损坏配置隔离、异常退出标记和 2 轮短压力通过。

Win32 SDK 通过 `LPARAM` 提供的结构及其他已审计边界需要使用 `unsafe.Pointer`；项目标准 vet 按 [构建说明](build.md)关闭 `unsafeptr` 分析器，其余分析器均通过。

## 3. 正式产物

| 文件 | PE subsystem | FileVersion | ProductVersion | 图标 |
|---|---:|---:|---:|---|
| `GenshinTools-debug.exe` | Console (3) | `0.10.0.0` | `0.10.0` | 通过 |
| `GenshinTools.exe` | Windows GUI (2) | `0.10.0.0` | `0.10.0` | 通过 |
| `GenshinTools-injector.exe` | Console (3) | `0.10.0.0` | `0.10.0` | 通过 |

产物校验同时确认便携数据目录和 `build-info.json`，构建提交为 `0055dba04dbb`。

## 4. 范围与稳定性复审

- 导航、页面、配置 schema 和后台初始化中没有登录、账号切换、米游社/BBS、抽卡、签到、资讯、数据中心、帮助、养成计算器、附加程序或内置浏览器入口。
- 首页范围声明及插件拒绝规则保留相关关键词，是用于告知排除范围和阻止插件引入排除能力，不是功能残留。
- 诊断导出测试确认不会输出账号字段、Token/Cookie、完整私人路径、目录 URL 或原始结构化日志字段。
- UI 线程不执行诊断文件写入、目录同步、下载或大目录扫描；后台结果只经有界消息通道回到 UI 线程。

## 5. 保留到 S13 的人工验证

- 125%/150%/200% DPI、跨不同缩放显示器拖动、显示器拔插和最小窗口的完整视觉/命中矩阵。
- Windows 浅色、暗色和真实高对比度模式下的全部页面与原生 EDIT 控件可读性。
- 反复切换主题、语言和 DPI 后的 GDI/USER 对象长期基线，以及休眠/唤醒、锁屏和 RDP 切换。
- 上述项目依赖真实桌面会话或较长运行时间，作为正式发布门禁保留到 S13；当前自动化已覆盖缩放算法、主题选择、高对比度 palette 和短壳层生命周期。

S11 开发阶段退出门禁通过，下一阶段为 `S12：本程序更新与上游自动对照`。
