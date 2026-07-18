# S12 本程序更新与上游自动对照设计

制定日期：2026-07-18  
上游审计基线：`b5a050ebd319341bddc4189491c90c22162d33fa`  
状态：设计基线，按本文顺序实施

## 1. 目标与两条独立信任链

S12 包含两套互不授权的能力：

1. **本程序更新**：只接受由本项目离线发布密钥签名的更新清单，下载并校验本项目便携包，由独立 helper 在主程序退出后事务替换，失败时恢复旧版本。
2. **上游自动对照**：只读获取 `FufuLauncher/FufuLauncher` 在已审计基线之后的提交和文件变化，生成范围分类报告；它绝不能下载执行上游产物，也不能触发本程序更新。

上游仓库、GitHub API、更新下载站和 TLS 均不单独构成信任根。本程序更新的最终信任根是编译进程序的 Ed25519 公钥；上游基线提升的信任根是人工审计记录和单独的 Git 提交。

## 2. 唯一实施顺序

### S12.1 只读上游差异工具

- 新增 Go 命令和 `scripts/upstream-check.ps1` 包装器。
- 严格解析 `upstream.lock.json`，固定 owner/name/branch/base SHA。
- 通过 GitHub REST API 获取 head、compare、commit 和文件元数据；设置超时、响应大小上限和明确 User-Agent。
- 在本地 fixture 中覆盖 `in_scope`、`excluded`、`review_required`、`dependency_risk`。
- 报告写到新的时间戳目录；网络失败、限流、分页不完整或 schema 异常时不写基线。
- 第一版不提供自动合并代码能力。

### S12.2 更新清单与下载验证库

- 严格 JSON schema、未知字段拒绝、大小上限和单一 JSON 值。
- 清单包含 channel、SemVer、发布时间、最低兼容版本及每个平台包的 HTTPS URL、精确字节数和 SHA-256。
- `keyId` 选择编译期受信公钥；签名覆盖确定性 canonical payload，不信任清单携带的新公钥。
- 版本比较拒绝降级和相同版本；显式人工回滚只允许本地已验证备份，不重新信任旧网络清单。
- 下载使用有界超时、临时文件、流式 SHA-256、精确长度和原子提交。

正式发布公钥通过构建期配置写入；私钥永不进入仓库、构建产物、CI 日志或运行目录。未配置可信公钥时，程序明确显示更新不可用并且不发起包下载。

### S12.3 便携包审计与更新事务

- 更新载荷是 ZIP 便携包，不接受 MSI、脚本或自解压 EXE。
- ZIP 只允许固定相对路径；拒绝绝对路径、`..`、ADS、重复项、symlink、junction/reparse point、设备名、尾随点/空格和大小膨胀。
- staging 与版本备份位于 EXE 同卷的 `data\updates`；替换前重新核验 staged 文件清单和 SHA-256。
- 独立 `GenshinTools-updater.exe` 等待拥有明确 PID 与创建时间的主程序退出，不按进程名终止任何进程。
- journal 至少包含 `prepared`、`backed-up`、`committing`、`committed`、`restarting`；每个 phase 只允许预定义的根内路径。
- helper 先备份旧文件，再逐项替换；任一步失败按反向顺序恢复。启动时恢复未完成 journal。
- helper 只重启经过审计的主 EXE，不继承任意命令行，不请求管理员权限；目录不可写时失败关闭。

## 3. 更新清单草案

```json
{
  "schemaVersion": 1,
  "channel": "stable",
  "version": "0.11.0",
  "publishedUtc": "2026-07-18T00:00:00Z",
  "minimumVersion": "0.10.0",
  "artifacts": [
    {
      "os": "windows",
      "arch": "amd64",
      "url": "https://updates.example.invalid/GenshinTools-0.11.0-windows-amd64.zip",
      "size": 1,
      "sha256": "64 lowercase hexadecimal characters"
    }
  ],
  "keyId": "release-1",
  "signature": "base64 Ed25519 signature"
}
```

签名 payload 不包含 `signature` 字段，字段顺序和字符串编码由项目代码固定。解析后先规范化再重新编码，签名验证通过之前不使用 URL 下载载荷。

## 4. 上游报告与分类

输出目录：

```text
artifacts/upstream-check/<base-short>_<head-short>/
  summary.md
  changes.json
  commits.json
  disposition.template.json
  patches/
```

同一 base/head 始终生成同一路径、相同排序和稳定 JSON；`generatedUtc` 只放在人读摘要，不参与机器报告内容哈希。

分类优先级从高到低：

1. `dependency_risk`：范围内文件同时依赖账号、BBS、WebView、工具箱等排除模块。
2. `review_required`：二进制、依赖/许可证、API/URL、schema/protobuf、注册表、删除/移动、注入、提权或无法可靠识别的重构。
3. `excluded`：只命中永久排除能力且没有范围内依赖。
4. `in_scope`：游戏启动、路径、资源、区服、输入、截图、覆盖层、HDR、插件、托盘或更新。

报告只能提出“需要人工处理”，不能把上游 C#、Lua、DLL、EXE 或脚本复制到本仓库。

## 5. 基线提升

默认命令永远只读。`-UpdateBaseline` 还必须接收一个本地审计处置 JSON，其中每个 `in_scope`、`review_required` 和 `dependency_risk` 项均包含：

- 审计人和 UTC 时间；
- 处置结论；
- 本项目实现/测试/文档或明确不适用的链接；
- 对二进制、许可证、API/schema 和永久排除范围的复核结论。

只有报告 base/head 与当前 lock 完全匹配、所有必填项关闭、工作树中的 lock 仍是读取时内容时，才通过原子替换更新 `commit`、`commitTimeUtc` 和 `checkedAtUtc`。工具不执行 `git add`、commit 或 push。

## 6. 崩溃、卡死与安全对照

| 风险 | S12 处理 |
|---|---|
| UI 线程进行网络、哈希、解压或等待进程 | 全部后台执行；UI 只接收有界进度/终态消息 |
| 仅靠 HTTPS 或 SHA-256 信任更新 | Ed25519 签名清单 + 包 SHA-256 + 固定公钥 |
| 运行中的 EXE 自覆盖 | 独立 helper 等待精确进程身份退出 |
| 断电留下新旧文件混合 | 同卷 staging、journal、逐项备份和启动恢复 |
| updater 被诱导覆盖任意路径 | 固定安装根、路径 allowlist、根边界和 reparse point 拒绝 |
| helper 永久等待主程序 | 有界等待和明确错误文件；绝不按名称杀进程 |
| 更新成功但新版本无法启动 | 保留上一完整版本；启动确认超时后允许 helper 回滚 |
| GitHub API 限流/分页不完整 | 失败关闭，不生成“无变化”结论，不修改 lock |
| 分类规则漏掉跨模块依赖 | dependency_risk 优先于 excluded |
| 自动对照恢复排除功能 | 只生成报告；基线提升要求人工处置，不自动改代码 |
| 私钥泄漏 | 仓库和运行程序只含公钥；签名离线完成 |

对应稳定性清单重点为 C02/C05/C06、I01～I10、J02/J05/J09、K04/K05、L01～L08。

## 7. 测试与退出门禁

- fixture 下报告分类、排序和内容哈希确定；网络错误、403/429、截断分页和 schema 变化失败关闭。
- 未处置或处置报告不匹配时拒绝提升基线；任何失败不改变 `upstream.lock.json`。
- 更新清单未知字段、错误签名、错误 key、降级、过期/不兼容、错误长度和哈希全部拒绝。
- ZIP 路径逃逸、链接、重复文件、膨胀和未声明文件全部拒绝。
- 替换任一步故障、helper 被杀和模拟未完成 phase 均能恢复旧版本；恢复逻辑幂等。
- Debug、Release、updater helper 使用同一 SemVer/VERSIONINFO；便携 ZIP 重开后与发布清单完全一致。
- 主程序更新不可用或失败不影响纯净游戏启动、输入增强和其他离线功能。

S12 完成后才进入 S13 全量回归与首个正式版本。
