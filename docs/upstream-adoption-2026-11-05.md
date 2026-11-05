# FufuLauncher 1.7.2.2 / game 7.2 focused adoption

## Reviewed inputs

- Launcher release: [1.7.2.2](https://github.com/FufuLauncher/FufuLauncher/releases/tag/1.7.2.2),
  published 2026-11-04T15:50:58Z (2026-11-04 23:50:58 Asia/Shanghai).
- Launcher tag commit: `af192350c8b07b5c01b18adefa62971964309c80`.
- Compared locally with tag `1.7.0.3`, rather than relying on GitHub's truncated
  compare response. Source is held only in Git-ignored `.tmp/upstream-fufu-1722`;
  upstream executables, DLLs and build scripts were not run.
- Plugin repository commit: `06da24fcf988d0da41e11fe1d764c32fc573e332`.
- `FuFuPlugin.zip`: 206,924 bytes; SHA-256
  `2e40fdbde2d065734fbcdcd95eb4fc23a0573adb803ab4a06d8400a110cd7bdd`.
- ZIP entries: `config.ini`, `FufuLauncher.UnlockerIsland.dll` only.
- DLL SHA-256: `6424fae1c6fb7f490f9058db5b944efc860e23aceada2ba82fda8647773711c0`.
- INI plugin version `1.7.2`, description build `1.7.2.2`, game `7.2`,
  63 sections including General / 62 typed settings.
- Baseline project commit: `f5b483975c3b9e9d3206395073118da440cbba73`.
  Original source archive SHA-256:
  `cec6a29b4c1ef7897556f6fc380d8eec2f6cabef754baf98bb5a6b7d5b2a10a0`.

## Ordered disposition

1. **Main package / configuration — adopted.** Existing typed INI controls
   already represent the new fields. A pinned live regression verifies all 62
   settings, the new camera-distance/collision and domain-map defaults,
   migration of a user's target FPS, install, upgrade and rollback. Old 7.1
   binaries are rejected for game 7.2; rollback succeeds with the original 7.1
   candidate. Neither live test executes the downloaded plugin DLL.
2. **Declared game compatibility — adopted.** Read the upstream General keys
   `GameVersion`, `SupportedGameVersions`, `GameVersions`, `游戏版本`, and
   `支持游戏版本`, in that order. Numeric versions, trailing wildcards,
   comparisons, inclusive ranges, OR/AND and exclusions are supported with
   bounded input. Invalid suffixes are rejected rather than interpreted as
   numeric prefixes. For the main bundle only, its published `游戏7.2`
   description supplies the rule when no explicit key exists. Store-plugin
   descriptions are never treated as compatibility declarations. The stored
   rule and existing exact installation-time version allowlist are both
   enforced by the common audit path, including helper launch and rollback.
3. **Signature verification — adopted independently.** Embedded-signed modules
   and main-plugin versions from 1.7.1 require Windows Authenticode validation
   at staged install and every later common module audit. Use `WinVerifyTrustEx`
   with no UI/network retrieval; close trust state on both success and failure.
   No certificate import, trust-root redistribution, or ignore-check switch is
   added. A clean computer missing the signer's trust chain receives an explicit
   trust error and retains the prior installation; pure launch is unchanged.
   Offline validation does not assert current revocation status. Unsigned
   legacy modules retain the existing explicit source/hash/version audit.
4. **Download sources / store — verified.** Upstream `ApiEndpoints.cs` now
   prefers `cdn.fufu.skin/dll/FuFuPlugin.zip` and declares a GitHub fallback.
   This project's existing organization-repository Contents API raw endpoint
   still serves the pinned official package, so its bounded GitHub-only
   downloader is retained rather than expanding trusted hosts unnecessarily.
   The store remains `https://fu1.fun/api/v1/plugins/list`; the existing live
   store regression verifies the current catalog without installing packages.
5. **Resources / updates — compatible, reuse reporting improved.** Live CN
   Sophon schema audit reads game 7.2.0, 2,974 game + zh-cn files and 129,387
   chunks. The current full-file/chunk reconstruction remains the independent
   update/repair path; upstream delta-patch execution is not imported. Verified
   complete staged files and verified partial chunk prefixes are reused.
   Complete cached files are excluded from download-space preflight; UI shows
   reused bytes and network speed excludes those verified reused bytes.
   Hash-corrupted cached files are downloaded again. Callback snapshots are
   serialized to avoid stale/out-of-order progress reports.
6. **Server switching — adopted.** Add both CN/global `PluginEOSSDK.dll`
   legacy paths to the existing transactional deletion list. Returning to the
   official CN server no longer requires a live Bilibili SDK metadata request.
   Bilibili metadata identity/hash/size checks and file transactions are retained;
   the live metadata endpoint still reports SDK 5.0.4. Unrelated files and
   `game_version` remain unchanged.
7. **Input stop fix — independently covered.** Keep the existing independent
   input state machine and release/cancellation tests. Replace a flaky
   five-second cmd process-exit test with an owned Go test subprocess, bounded
   context, explicit wait, and cleanup; no orphan ping subprocess remains.
8. **Release-matrix follow-up — fixed.** The first complete matrix caught a
   PowerShell/modern .NET distinction between absent and empty environment
   variables: restoring null through a string parameter left `GOOS` empty.
   Restore actual null with `NullString.Value` in shared helpers and the build
   script. The source gate now exercises absent, empty, and nonempty snapshots.

## Scope preserved

This is a compatibility adoption, not a clone of every launcher release feature.
Account/community/player-data/background/animation changes remain excluded.
MCP server, third-party UI widgets, certificate installation, lightweight-mode
switching, online translation and additional archive decoders are not introduced.
The existing store installer continues to accept only its audited ZIP/INI/DLL
contract, without running upstream Lua installers. New store entries remain
subject to the same source/hash/dependency/license and package-layout checks.
HDR and input retain the current independent implementations and regressions.

`upstream.lock.json` deliberately remains the last fully reviewed repository
baseline. This record pins the newer reviewed release contracts; it does not
claim review/adoption of every change since July or automatic code imports.

## Verification and evidence

Run from the project root using the documented Go/Win32 toolchain:

```powershell
./scripts/test.ps1 -Race
$env:GENSHINTOOLS_LIVE_FUFU_MAIN = '1'
$env:GENSHINTOOLS_LIVE_FUFU_STORE = '1'
go test -count=1 -v ./internal/plugins -run 'TestLiveFufu|TestFufuCatalogLiveIntegration'
./scripts/test-s06-provider.ps1
./scripts/test-s13-release.ps1
```

Local raw package, original source archive and literal command transcripts are
under `artifacts/upstream-2026-11-05/`. The complete release matrix separately
records real-desktop/game gates that automation has not exercised; successful
offline/live-package tests do not assert real-game stability or account outcomes.
