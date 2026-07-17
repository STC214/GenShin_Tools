# FufuLauncher 上游自动对照方案

## 1. 目标

自动对照的目标是“及时发现并可审计地分类上游变化”，不是自动复制 C# 代码、自动打开新功能或自动更新注入二进制。

本项目以 `upstream.lock.json` 保存最近一次审计基线。首次基线为：

- 仓库：`FufuLauncher/FufuLauncher`
- 分支：`master`
- 提交：`b5a050ebd319341bddc4189491c90c22162d33fa`
- 提交时间：2026-07-16T12:14:09Z
- 范围策略：`scope-v1`

## 2. 计划交付

Phase 7 实现以下命令；Phase 1 可先实现只读最小版本：

```powershell
./scripts/upstream-check.ps1
./scripts/upstream-check.ps1 -UpdateBaseline   # 仅在人工审计完成后允许
```

脚本/内部 Go 工具执行：

1. 读取 `upstream.lock.json`，验证仓库和基线提交。
2. 通过 GitHub API 获取 `master` 最新提交；不要求本机安装完整上游工具链。
3. 获取 `base...head` 提交、文件列表和补丁元数据。
4. 按路径、符号和资源键生成模块级变更，不做脆弱的纯文件名判断。
5. 使用 `docs/upstream-scope-matrix.md` 的规则分为：`in_scope`、`excluded`、`review_required`、`dependency_risk`。
6. 生成 Markdown 人读报告和 JSON 机器报告。
7. 若出现 `review_required`、API/schema、注入二进制或下载端点变化，以非零退出码阻止自动抬升基线。
8. 人工完成行为分析、本项目 issue/实现/测试链接后，才允许 `-UpdateBaseline` 写入新 SHA。

## 3. 报告格式

计划输出：

```text
artifacts/upstream-check/<UTC timestamp>/
  summary.md
  changes.json
  commits.json
  patches/
```

`changes.json` 每条至少包含：

- 上游提交和路径。
- 推断模块与置信度。
- 范围分类和命中的规则。
- 行为变化摘要。
- 是否需要本项目实现、测试或文档更新。
- 审计人、审计时间和处置链接（更新基线前必填）。

## 4. 分类规则

### 4.1 始终排除

命中以下概念的变化记录但不进入实现队列：

- account/login/cookie/token/QR/geetest/security web。
- checkin/daily note/cloud credential/community/hoyolab/mihoyo BBS。
- gacha/UIGF/achievement/inventory/player role/travelers diary。
- news/content feed/BBS/help/calculator/data center/browser/additional program，以及原仓库控制面板/工具箱全部功能。

若范围内代码开始依赖这些模块，分类为 `dependency_risk`，不能简单忽略。

### 4.2 范围内重点监控

- `GameLauncherService`、游戏路径/配置/EXE 名称和启动参数。
- `GenshinDownloader`、manifest/proto、下载/预下载、校验修复。
- 服务器识别和官服/B服切换文件。
- `AutoClickerService`、截图 hook、热键和 `SendInput`。
- 注入 Launcher、模块、插件商店、插件校验和更新。
- HDR、FPS overlay、快捷方式、托盘和本程序更新。

### 4.3 必须人工审计

- 新的二进制文件或二进制哈希变化。
- API URL、manifest/protobuf/schema、签名密钥、更新证书变化。
- 游戏文件删除/移动、注册表写入、进程注入或权限提升变化。
- 上游功能跨模块重构，导致路径规则无法可靠判断。
- 上游许可证或外部依赖许可证变化。

## 5. 安全与可重复性

- 默认只读访问上游；不执行上游构建脚本、安装器、EXE、DLL 或 PowerShell。
- API 响应和补丁落到带 SHA 的审计目录；报告记录生成工具版本。
- GitHub API 失败、限流或返回不完整时必须失败关闭，不能写新基线。
- 基线更新必须形成单独提交，并同时更新范围矩阵/路线（若行为边界变化）。
- CI 可定期运行只读检查并上传报告，但不自动创建发布、不自动替换依赖。

## 6. 版本策略

- 本项目拥有独立 SemVer，不沿用上游版本号。
- About/诊断中同时显示本项目版本、审计的上游 SHA 和范围策略版本。
- “已跟进上游”只表示所有 `in_scope` 变化已处置且测试通过，不表示包含上游被排除功能。
- 上游仍在基线之后并不阻止本项目启动；只在开发/CI 中提示审计滞后。

## 7. 首次实现的验收

- 在固定 fixture 上准确识别范围内、排除和交叉依赖三类变化。
- 网络失败和 GitHub 限流不改变 lock 文件。
- 未提供审计字段时 `-UpdateBaseline` 拒绝执行。
- 输出确定性：相同 base/head 生成相同排序和内容哈希。
- 不下载或运行任何上游二进制。
