# FufuLauncher scope-v2 上游处置（2026-07-22）

审计区间：`b5a050ebd319341bddc4189491c90c22162d33fa..5f6af35fcb90807d5db390ed4af58ca09ddd381c`

本次逐项检查了 23 个变更文件、21 个提交，并复核了二进制、源码与许可证、API/schema 以及永久排除边界。机器可验证的完整处置记录见 `docs/upstream-disposition-2026-07-22.json`。

## 结论

- FuFuPlugin 新增自由视角布尔、按键和字符串字段由动态 INI schema 自动生成 UI；修复时兼容字段保留用户值，新字段使用上游默认值。
- 插件列表新增的 `visibility`、`update_type`、`dependencies`、最小启动器版本和直接 ZIP 下载字段已被正规化、校验；依赖必须先以其自己的验证令牌独立安装，禁止令牌跨插件转发。
- Fufu 下载验证继续使用系统默认浏览器，`dl_token` 只驻留内存；不引入上游 WebView2 验证窗口。
- 上游远程 Lua 安装/卸载/测试解释器仍属于永久排除。项目只下载带 SHA-256 的官方 ZIP，并经过路径、链接、数量、体积、PE 与依赖门禁。
- 私密插件的 `access_key`/`access_token` 和非公开制品不纳入当前公开商店合同。项目不保存或转发这类凭据；若未来明确纳入，必须另做凭据生命周期、来源/许可证和每个依赖独立授权设计。
- 商店统计/排行榜端点不参与插件发现、安装、更新或配置，不需要进入简化 UI。
- 成就、账号/BBS、工具箱、资讯、数据中心和对应 SQLite 迁移仍为永久排除；安装器版本号、原项目自更新器和花哨 UI 不映射到本项目独立版本线。

## 核对依据

- 范围合同：`docs/upstream-scope-matrix.md`
- 商店与安全设计：`docs/s10-design.md`、`docs/s10-verification.md`
- 动态配置与安全安装：`internal/plugins/config_schema.go`、`internal/plugins/fufu_package.go`、`internal/plugins/catalog.go`
- 回归覆盖：`internal/plugins/fufu_package_test.go`、`internal/plugins/catalog_test.go`、`internal/shell/fufu_config_ui_windows_test.go`

