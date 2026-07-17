# S10 插件管理、配置与商店设计

状态：实施基线  
日期：2026-07-17

## 1. 上游基线审计

审计提交固定为 `b5a050ebd319341bddc4189491c90c22162d33fa`。上游提供插件发现、DLL/`.disabled` 启停、目录重命名、删除、INI 配置、自动更新和远程商店，但以下行为不能直接移植：

- 本地发现只取每个目录第一个 `*.dll` 或 `*.dll.disabled`，缺少来源、许可证、PE、版本、哈希和游戏兼容校验。
- 添加插件会直接复制任意 DLL，并自动生成一个无可信来源的 `config.ini`。
- 启停通过重命名 DLL，删除通过递归删除目录；只依赖拼接字符串，没有逐级 reparse/junction 检查。
- 自动更新从一个明文 HTTP 代理或 GitHub `blob?raw=true` 下载 ZIP，不核验包哈希，先删除已安装目录再复制新文件。
- 商店安装会下载并执行远程 Lua。哈希字段为空时明确跳过校验；Lua “安全扫描”是字符串黑名单，不能构成沙箱。
- Lua 路径检查使用字符串 `StartsWith`，缺少目录分隔符边界；ZIP 使用直接 `ExtractToDirectory`，没有逐项 zip-slip、链接、数量、膨胀率和总大小检查。
- 2026-07-17 对上游当前公开列表端点做只读请求，响应为 `total=0`；分类端点只有 utility/gameplay/visuals/other。它没有提供可审计插件、源码或许可证，因此本项目不把该端点设为默认商店源。

上游仓库中的 `FPS.zip`、`FPS.disabled` 以及两个示例 `config.ini` 同样不进入本项目发布物；配置仅作为行为/schema 参考。

## 2. 本项目插件模型

插件是“可由 S09 helper 加载的、来源可追踪的单一 amd64 DLL 模块”，不是主程序扩展。插件代码永不加载进 Genshin Tools 主进程。安装后的活动目录仍使用 S09 已审计布局：

```text
data\injection\modules\<plugin-id>\
  module.json
  plugin.json
  plugin.dll
  config.ini                 # 可选
  config.schema.json         # 可选，声明可编辑字段

data\plugins\
  state.json                 # 启用、顺序、安全模式、活动/回滚版本
  catalog.json               # 最近一次完整校验通过的商店快照
  versions\<id>\<version>\  # 已校验版本/回滚副本
  staging\                   # 下载与解包事务，仅失败残留诊断
```

插件 ID、版本、来源、许可证、包 SHA-256、DLL SHA-256、PE/导入/导出和游戏兼容表均为必填门禁。活动模块继续由 `injection.AuditModule` 在主进程和 helper 中各核验一次。

## 3. 声明式插件包

商店只接受 ZIP 包，不接受 Lua、PowerShell、批处理、EXE、MSI 或任何安装脚本。包根必须包含严格 JSON `plugin.json`：

- `schemaVersion=1`、稳定 ID、名称、开发者、说明、SemVer、分类和标签。
- HTTPS 源码 URL、许可证标识、HTTPS 包 URL、包大小和包 SHA-256。
- 完整 S09 module manifest；ID、版本、DLL 和哈希必须与插件声明一致。
- 文件白名单及每个文件的相对路径、长度、SHA-256。
- 可选配置 schema；字段只支持 bool/int/float/string/key，且必须声明 section、key、默认值和边界。

ZIP 逐项检查：最多 256 项、压缩包最大 256 MiB、解压总量最大 512 MiB、单文件最大 256 MiB、拒绝绝对路径/`..`/ADS/空名/重复名/链接与 reparse、拒绝未声明文件和可执行脚本。所有内容先写 staging，完整哈希和 S09 审计通过后才进入版本目录。

## 4. 安装、更新、修复与回滚

1. HTTPS 下载使用独立 context、连接/响应/总超时、长度上限和流式 SHA-256。
2. staging 解包后逐文件复核，再构造活动目录候选并执行 S09 审计。
3. 当前版本先移动到 `versions`，候选再移动为活动目录；事务日志记录每一步。任一步失败按反向日志恢复。
4. 更新和修复走同一条安装事务；绝不在新版本完整校验前删除当前版本。
5. 回滚只选择已经完整审计的版本目录，并再次审计后切换。
6. 卸载先移动到 staging 隔离区，提交 state 后再清理；失败可恢复，不直接递归删除未知路径。

## 5. 启停、顺序和安全模式

- 启停保存在 `state.json`，不通过重命名 DLL，也不改变已校验字节。
- 注入启动按稳定顺序加载全部启用插件；任一插件预检失败则整次注入失败关闭，不跳过后继续。
- 安全模式是会话/配置级总开关：开启时 helper 收到空插件列表，纯净启动仍走 S05。
- 重命名只修改本地显示别名，不移动目录、不改变稳定 ID。
- 插件配置原子写入；损坏配置隔离并回到 schema 默认值。预设只是同一 schema 下的值集合，不能携带路径或命令。

## 6. 商店索引

本项目定义严格 `catalog.json` schema，支持分类、搜索、popular/newest/rating 排序和分页。远程源必须由用户明确配置为 HTTPS；默认不联网、默认目录为空。同步失败继续使用上一次完整校验快照，绝不清空已安装插件或阻止程序启动。

索引条目若命中 account/login/cookie/token/gacha/checkin/bbs/news/browser/data-center/calculator 等排除能力标记，或缺失源码/许可证/哈希/兼容信息，整条目拒绝进入 UI。S10 不采用上游当前 Lua 商店协议，也不连接其端点。

## 7. S10 退出门禁

1. 本地发现、启停、排序、别名、配置、预设和安全模式均不执行插件代码。
2. 路径遍历、同名前缀、junction、符号链接、ADS、zip-slip、重复项、压缩炸弹、错哈希和未声明文件 fixture 全部拒绝。
3. 安装/更新在下载、解包、审计、切换任一阶段取消或失败后，旧版本和 state 保持可用。
4. 商店断网、超时、损坏 JSON、未知 schema 和范围外插件不影响主程序、纯净启动或已安装插件。
5. helper 支持按顺序加载多个已审计插件；任一失败不会恢复状态不明的游戏进程。
6. 发布物不携带第三方插件；`0.9.0` 构建、测试、竞态和版本元数据通过。
