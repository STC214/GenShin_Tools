# S12 验证记录

更新时间：2026-07-19  
状态：S12 代码与自动门禁已完成；许可证和上游人工 disposition 尚未完成；后续 S13 短时自动矩阵已通过。

## 通过项

- S12.1：上游只读审计、分页/限流/响应上限、分类报告、人工处置后基线更新。
- S12.2：严格 manifest、Ed25519 canonical payload、固定公钥、版本门禁、HTTPS 同源、长度和 SHA-256 校验。
- S12.3：ZIP 路径/链接/设备名/重复项/压缩炸弹门禁；staging；备份；journal；逐文件原子替换；回滚；启动恢复。
- updater：固定 runner、PID＋创建时间＋进程路径校验、ready 握手、有界等待、固定主 EXE 重启。
- 启动确认：`restarting` phase；新版本确认后清理；确认超时后二次检查 journal，终止身份和路径匹配的未确认子进程，再恢复并启动上一版本。
- 设置页：检查更新、后台下载/校验/staging、task ID 防旧结果回写、ready 后安全退出。
- 发布工具：签名 manifest 生成和 release ZIP 一致性审计。

## 自动门禁

```text
go mod verify
scripts/test.ps1
scripts/test.ps1 -Race
go vet ./... -unsafeptr=false
gofmt -l (仓库 Go 源码)
scripts/build.ps1 -Configuration Both
scripts/verify-artifact.ps1
```

以上代码/构建门禁在本记录日期均通过。上游检查生成 scope-v2 报告后按设计因一个 `review_required` 项返回非零，等待人工处置。默认 `go vet ./...` 仍会报告 Win32 ABI callback/LPARAM 边界的 `unsafe.Pointer` 提示；这些位置已由 `scripts/test.ps1` 使用 `-unsafeptr=false` 隔离，并保留在全量审计报告中供人工复核。

## 发布配置边界

仓库不保存发布私钥。Release 构建通过 `GENSHINTOOLS_UPDATE_MANIFEST_URL` 与 `GENSHINTOOLS_UPDATE_PUBLIC_KEYS_BASE64` 注入 URL 和公钥；只配置一项、空 key、非法 key ID 或非 32 字节 key 会在构建前拒绝。未配置时更新功能安全关闭，不下载任何载荷。

## 未纳入 S12 的验证

真实 Windows 多显示器/DPI 长时间运行、真实游戏输入 soak、休眠唤醒和干净机器首次安装属于 S13 人工门禁；短时自动矩阵已通过，但这些场景尚未执行。
