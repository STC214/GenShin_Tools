# S04 游戏发现、路径与只读状态验收记录

状态：**完成。**

## 上游对照

锁定基线：FufuLauncher `b5a050ebd319341bddc4189491c90c22162d33fa`。

本阶段审计了上游 `GamePathFinder.cs`、`GameExeManager.cs`、`GameConfigService.cs` 和 `GameLauncherService.cs` 中的本地游戏管理行为，保留：

- `HKCU\Software\miHoYo\HYP\1_1\hk4e_cn` 的 `GameInstallPath` 只读提示；
- `YuanShen.exe`、`GenshinImpact.exe` 和自定义 EXE 文件名；
- `config.ini` 的 `game_version`、`channel`、`cps` 区服判定；
- 游戏目录、版本、区服、目录大小和运行状态。

没有接入上游登录、账号、附加程序、网络内容、注入或启动行为。上游“找到第一个即返回”和同步不可取消的目录递归未照搬。

## 已实现

- 已保存路径、HoYoPlay 注册表提示和所有固定盘常见目录的有序发现。
- 保存路径失效时自动回退到其他来源；多个有效安装时不静默猜测，要求手动选择。
- 原生 Windows EXE 选择器；选择文件必须真实存在，支持 Unicode、空格和长缓冲区。
- 自定义 EXE 只接受当前游戏目录内的文件名，拒绝绝对路径和 `..` 路径穿越。
- `config.ini` 限制为 1 MiB 只读解析，支持 UTF-8 BOM、注释和大小写不敏感键。
- 官服、B 服和国际服识别；配置缺失时以标准 EXE 名称提供保守回退。
- 目录大小后台计算、每 256 个文件更新一次、可立即取消、单项无权限计入跳过数。
- 进程快照按 EXE 完整路径核验并记录 PID+创建时间；查询权限不足时只显示“可能运行中”，不冒充已核验。
- schema 3 原子保存游戏目录和自定义 EXE；扫描不写游戏目录和注册表。
- 暗色游戏管理页：自动扫描、手动选择、取消、路径、程序、版本、区服、大小、跳过数和运行状态。

## 自动化结果（2026-07-17）

```powershell
./scripts/test.ps1 -Race
./scripts/capture-s04-game.ps1
```

结果：`PASS`

| 门禁 | 结果 |
|---|---:|
| 官服/B服/国际服与 BOM 配置 | 通过 |
| Unicode、空格、父目录和自定义 EXE | 通过 |
| 自定义 EXE 路径穿越拒绝 | 通过 |
| 多候选拒绝静默选择、候选去重 | 通过 |
| 目录大小、文件计数和取消 | 通过 |
| 扫描前后目录元数据快照一致 | 通过 |
| 当前进程完整路径、PID、创建时间核验 | 通过 |
| schema 2 → 3 迁移与原子配置 | 通过 |
| 全项目普通测试、race、vet、diff check | 通过 |
| 实际 Win32 页面截图与 6 秒干净退出 | 通过 |

页面截图：[s04-game.png](../build/s04-game.png)（`build/` 为本地忽略目录，可用脚本重新生成）。

## 只读边界

`internal/game` 不包含创建、写入、删除、移动游戏文件或写注册表的 API。测试通过扫描前后树快照验证游戏目录未发生变化。唯一写入是本程序 EXE 旁 `data/config.json` 的用户选择，沿用原子配置提交。

S04 不启动游戏；普通启动和启动参数从 S05 开始实现。
