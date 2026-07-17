# S01 verification record

Date: 2026-07-17  
Stage: `S01 - Engineering skeleton and reproducible build`  
Result: passed

## Source checks

Command:

```powershell
./scripts/test.ps1 -Race
```

Passed checks:

- `gofmt -l .`
- `go mod verify`
- `go test -count=1 ./...`
- `go test -race -count=1 ./...`
- `go vet ./...`
- Non-Markdown trailing-whitespace audit
- Local Markdown link audit: 0 broken links
- `app.manifest` XML parse and `upstream.lock.json` JSON parse

Packages passing tests:

- `genshintools/cmd/genshin-tools`
- `genshintools/internal/buildinfo`
- `genshintools/internal/paths`
- `genshintools/tools/icon`

## Formal build and PE verification

Commands:

```powershell
./scripts/build.ps1
./scripts/verify-artifact.ps1
```

Verified for both executables:

- ProductVersion: `0.1.0`
- FileVersion: `0.1.0.0`
- Embedded application icon: present
- Debug PE subsystem: `3` (`Windows CUI`)
- Release PE subsystem: `2` (`Windows GUI`)
- Target: `windows/amd64`
- Portable directories: `data/logs`, `data/cache`, `data/staging`
- `build-info.json` matches the requested version and target

## Deterministic rebuild check

Two consecutive builds used identical inputs:

```powershell
$env:SOURCE_DATE_EPOCH = '1784260800'
$env:BUILD_COMMIT = 's01-reproducible'
./scripts/build.ps1
```

The SHA-256 values matched exactly between build 1 and build 2:

| Artifact | Reproducible SHA-256 |
|---|---|
| `GenshinTools-debug.exe` | `F1753DFA733B40A3CDFA2D2600855B4138E685C34E1E52A009D2ED14D7DA58B3` |
| `GenshinTools.exe` | `D9D54D667110AC683A7A508F3AD1BE95B325B461D3237C03C6C1F20FF0E6EF20` |

The checked-in source does not contain those generated binaries; `dist/`, the generated ICO and `app.syso` are intentionally ignored.

## Runtime smoke check

- `GenshinTools-debug.exe --version-json` returned complete build identity.
- Normal Debug launch created/validated the portable layout and exited with code 0.
- Normal Release GUI launch exited with code 0.
- A Git repository with no first commit builds successfully and records commit identity as `uncommitted`.

## Final local artifacts

The final non-deterministic local build uses its real UTC build time:

| Artifact | Size | SHA-256 |
|---|---:|---|
| `dist/GenshinTools-debug.exe` | 3,095,040 bytes | `F627C04F928596BCD920AEB5A7C585E0692755B4DE9E41AA71598D921322E2B0` |
| `dist/GenshinTools.exe` | 2,136,064 bytes | `304AAE3A6DBA8864E0578BA0B8EE3AAD2EF21F8379A19742D84C22EADA0A29A7` |

## Stage boundary

S01 contains no Win32 window, hook, game discovery, network download or injection implementation. The next stage is `S02 - Stable Win32 shell`.

