# Third-party notices

## FufuLauncher

- Project: FufuLauncher/FufuLauncher
- Source: <https://github.com/FufuLauncher/FufuLauncher>
- Audited baseline: `5f6af35fcb90807d5db390ed4af58ca09ddd381c`
- Live store contract rechecked: 2026-07-22 (`fu1.fun/api/v1/plugins` and current upstream store client sources)
- Role: behavioral reference for this independent Go + Win32 reimplementation, plus the fixed upstream source for plugin-store metadata and official download verification pages
- License: MIT License
- License copy: `LICENSES/FufuLauncher-MIT.txt`

No FufuLauncher executable, DLL, plugin package, image, sound, font or other binary asset is redistributed by the current project. At runtime the plugin-store page reads FufuLauncher's public store API and may open its official verification page in the user's system browser. Store content and individual plugins remain third-party material governed by their respective authors and terms.

The audited upstream baseline contains opaque `Launcher.dll` and `Launcher_2.exe` files whose bytes do not match the repository's adjacent SHA-512 list and have no reproducible source-to-binary linkage, signature or VERSIONINFO. They are not executed, copied or packaged. The later UnlockerIsland source repository is credited separately below; it does not retroactively authenticate those exact binaries. See `docs/s09-design.md` for recorded SHA-256 values and the independent helper decision.

## FufuLauncher.UnlockerIsland

- Project: FufuLauncher/FufuLauncher.UnlockerIsland
- Source: <https://github.com/FufuLauncher/FufuLauncher.UnlockerIsland>
- Audited commit: `cb6ce2112dada8ce7856469b21720eedc7c044f1`
- Role: reference for Fufu's `Plugins/config.ini` discovery convention and `File=*.dll` loading behavior
- License: MIT License
- License copy: `LICENSES/FufuLauncher-UnlockerIsland-MIT.txt`
- Redistribution: no upstream Launcher executable or DLL is copied or packaged; this project retains its independently audited Go/helper boundary

Individual packages delivered through the Fufu store are not covered by the launcher's or UnlockerIsland repository's MIT license unless their own authors say so. A missing store license is recorded as `UNSPECIFIED-FUFU-STORE`; runtime installation is not permission to redistribute that package.

## golang.org/x/sys

- Module: `golang.org/x/sys`
- Version: `v0.47.0`
- Role: typed Windows system calls and UTF-16 helpers
- License: BSD 3-Clause
- License copy: `LICENSES/golang-x-sys-BSD-3-Clause.txt`

## Build tools

The Go toolchain and GNU `windres` are build-time tools and are not redistributed in the portable output.

## github.com/klauspost/compress

- Version: `v1.19.0`
- Role: bounded streaming zstd decoding for Sophon manifests and resource chunks
- License: BSD 3-Clause for the used zstd package
- License copy: `LICENSES/klauspost-compress-BSD-3-Clause.txt`

## google.golang.org/protobuf

- Version: `v1.36.11`
- Role: protobuf wire primitives for the minimal audited Sophon schema adapter
- License: BSD 3-Clause
- License copy: `LICENSES/google-protobuf-BSD-3-Clause.txt`

## BetterGI

- Project: BetterGI / better-genshin-impact
- Source: <https://github.com/babalae/better-genshin-impact>
- Role: optional external `bettergi://start` protocol target
- License: GPL-3.0
- Redistribution: no BetterGI source code or binary is linked, copied or packaged by this project

The integration audits the user-installed URL handler and asks Windows to open the protocol. It never terminates a process merely because its executable name is `BetterGI`.

## QuickInput and AutoHotkey

- QuickInput project: ChiyukiGana/Quickinput
- Source: <https://github.com/ChiyukiGana/Quickinput>
- Source commit inspected: `95fb003efead7c54d4678949bed765e395f5ccb2`
- QuickInput license: GPL-3.0
- Role: behavioral and Win32 API-shape reference for separated mouse down/up events
- AutoHotkey project: AutoHotkey/AutoHotkey
- Source and releases: <https://github.com/AutoHotkey/AutoHotkey>
- Pinned release: `v1.1.37.02`
- License: GPL-2.0
- Redistributed runtime: official release file `AutoHotkeyU32.exe`, renamed to `AHK_F.exe`
- Runtime SHA-256: `ba35b8b4346b79b8bb4f97360025cb6befaf501b03149a3b5fef8f07bdf265c7`
- Complete corresponding source: `SOURCES/AutoHotkey-v1.1.37.02-source.zip`
- Source archive SHA-256: `2b1d94e5d9b94b6a6dc3a2565bc65e74fef93ac2c34bb57fe182ffb4ab20fe92`
- License copy: `LICENSES/AutoHotkey-GPL-2.0.txt`
- Project-owned script: `AHK_F.ahk`; this script is covered by the repository's MIT license
- Project-owned script SHA-256: `ce1e29cf5ca21dd0fa99840db895c9eea66e76721c0238d33bcb1e072d17ea4b`

The portable package redistributes the pinned official AutoHotkey interpreter under
GPL-2.0 and includes its complete corresponding source and license text. The
previously inspected user-provided compiled `AHK_F.exe`/`AHK_Space.exe` tools have
opaque appended scripts and unclear redistribution permission, so they are not
committed or packaged. QuickInput remains a behavioral reference only; none of its
source code or binaries is redistributed.

## PresentMon

- Project: GameTechDev/PresentMon
- Source: <https://github.com/GameTechDev/PresentMon>
- Audited commit: `de4b9c40bc97d237a77e539d1bd2835b743b33f0`
- Role: technical reference for documented DXGI ETW provider/event identifiers and trace-consumer behavior
- License: MIT License
- Redistribution: no PresentMon source code or binary is linked, copied or packaged by this project

## FlairBloom and Interception research reference

- FlairBloom project: x-wink/flair-bloom
- Source: <https://github.com/x-wink/flair-bloom>
- Audited commit: `3873b4b90499457a0fe321f09a3a6d34c10462a3`
- FlairBloom license: CC BY-NC-SA 4.0 (workspace metadata also marks the crates `UNLICENSED`)
- Interception project: oblitum/Interception
- Source and release: <https://github.com/oblitum/Interception>
- Audited Interception release: `v1.0.1`
- Interception license: LGPL-3.0 for non-commercial use; separate commercial licenses are offered upstream
- Role: behavioral and API-boundary research for an independently designed optional driver input backend
- Redistribution: no FlairBloom or Interception source, driver, DLL, installer, UI asset, or binary is packaged by this project

The complete FlairBloom source is present only in the Git-ignored local research
directory documented in `docs/flair-bloom-interception-analysis.md`. Its code is
not incorporated into this repository's MIT implementation.

This notice is a living document. Injection modules and plugins cannot enter a release until their source, license, version, architecture and SHA-256 have been recorded here or in a generated dependency manifest.
