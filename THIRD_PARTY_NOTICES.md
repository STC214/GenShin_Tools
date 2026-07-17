# Third-party notices

## FufuLauncher

- Project: FufuLauncher/FufuLauncher
- Source: <https://github.com/FufuLauncher/FufuLauncher>
- Audited baseline: `b5a050ebd319341bddc4189491c90c22162d33fa`
- Role: behavioral reference for this independent Go + Win32 reimplementation
- License: MIT License
- License copy: `LICENSES/FufuLauncher-MIT.txt`

No FufuLauncher executable, DLL, image, sound, font or other binary asset is redistributed by the current project.

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
