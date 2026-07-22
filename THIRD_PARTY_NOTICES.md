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

## PresentMon

- Project: GameTechDev/PresentMon
- Source: <https://github.com/GameTechDev/PresentMon>
- Audited commit: `de4b9c40bc97d237a77e539d1bd2835b743b33f0`
- Role: technical reference for documented DXGI ETW provider/event identifiers and trace-consumer behavior
- License: MIT License
- Redistribution: no PresentMon source code or binary is linked, copied or packaged by this project

This notice is a living document. Injection modules and plugins cannot enter a release until their source, license, version, architecture and SHA-256 have been recorded here or in a generated dependency manifest.
