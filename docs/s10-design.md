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
- 2026-07-22 重新核对上游当前公开端点：商店已经提供插件条目、分类、ZIP/Lua SHA-256、下载验证令牌与私有插件访问协议。此前 `total=0` 的一次性观测已失效，不能继续作为自建商店的依据。

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

## 6. FufuLauncher 商店适配（2026-07-22 决策覆盖）

插件模块包和商店数据统一使用 FufuLauncher 的公开官方来源，本项目不再设计、托管或运营另一套插件商店：

- 商店客户端固定连接 `https://fu1.fun/api/v1/plugins`，不再提供用户可编辑的目录 URL，也不接受第三方镜像把来源切离 Fufu。
- 本地 `data\plugins\catalog.json` 只是把 Fufu API 的 snake_case 响应正规化后的原子缓存；同步失败继续保留上次完整缓存，不影响纯净启动或已安装状态。
- 适配字段以 Fufu 当前 `PluginStoreItem` 为准，包括 ZIP/Lua URL 与 SHA-256、最小应用版本、DLL 文件名、可见性和更新类型。上游协议变化由 S12 自动对照报告提示，再经人工审计更新适配器。
- Fufu 的下载令牌需要网页人机验证。由于项目永久排除内置浏览器，UI 使用系统默认浏览器打开 Fufu 官方验证页；用户可粘贴 `dl_token` 或完整返回 JSON，令牌不写配置、日志或诊断包。
- 主程序不执行远程 Lua。受限安装器只追加 Fufu `dl_token`、下载官方 ZIP、复核长度/SHA-256、定位最浅层 `config.ini` 和 `[General] File=*.dll`，再生成本地审计 manifest 并走原有事务安装。
- 目录约定以 `FufuLauncher.UnlockerIsland@cb6ce2112dada8ce7856469b21720eedc7c044f1` 为行为参考，但不复制上游 Launcher 二进制。只兼容单一根 amd64 DLL；多入口、额外可执行格式、本地依赖遮蔽、路径逃逸、reparse、压缩炸弹或当前游戏版本审计失败均拒绝。
- FufuLauncher 仓库的 MIT 许可证不自动覆盖商店内每个插件；插件作者、许可证和风险必须分别展示与审计。本项目不随发布物再分发 Fufu 插件包。

本节覆盖原 S10 的“自定义 `catalog.json` 远程协议”决定。旧的本地声明式 ZIP 代码只作为安全回归 fixture 和离线审计入口保留，不构成本项目要运营的插件生态或商店格式。

## 7. S10 退出门禁

1. 本地发现、启停、排序、别名、配置、预设和安全模式均不执行插件代码。
2. 路径遍历、同名前缀、junction、符号链接、ADS、zip-slip、重复项、压缩炸弹、错哈希和未声明文件 fixture 全部拒绝。
3. 安装/更新在下载、解包、审计、切换任一阶段取消或失败后，旧版本和 state 保持可用。
4. Fufu 商店断网、超时、损坏 JSON、协议变化和范围外插件不影响主程序、纯净启动或已安装插件。
5. helper 支持按顺序加载多个已审计插件；任一失败不会恢复状态不明的游戏进程。
6. 发布物不携带第三方插件；README、第三方声明和 UI 明确致谢并引用 FufuLauncher；`0.9.0` 构建、测试、竞态和版本元数据通过。
