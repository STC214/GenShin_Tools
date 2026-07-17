# S02 verification record

Date: 2026-07-17  
Stage: `S02 - Stable Win32 shell`  
Version: `0.2.0`  
Result: passed

## Implemented shell capabilities

- UI goroutine locked to its Windows OS thread with a checked `GetMessageW` loop.
- Native dark title bar and custom-drawn dark client area.
- Sidebar navigation, page heading, status card and diagnostic status bar.
- Per-Monitor V2 DPI declaration, `WM_DPICHANGED` handling and scaled fonts/layout.
- Persisted/clamped window position and minimum tracking size.
- Named-mutex single instance and acknowledged activation of the first window.
- Minimize-to-tray, double-click restore, context menu show/exit and `TaskbarCreated` recovery.
- Versioned portable config with atomic `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)` commits.
- Corrupt/future config quarantine and schema-0 migration.
- JSON Lines logging, previous-unclean-exit marker and top-level/WndProc panic recovery.
- Cancellable background task IDs with bounded shutdown.
- Live goroutine, thread, handle, USER and GDI object counts.

No input hook, game process, download, account, WebView or injection behavior exists in S02.

## Source verification

```powershell
./scripts/test.ps1 -Race
```

Passed: `go mod verify`, unit tests, race tests, `go vet -unsafeptr=false`, `gofmt` and `git diff --check`.

The `unsafeptr` vet analyzer is disabled because `WM_DPICHANGED` and `WM_GETMINMAXINFO` synchronously expose SDK-owned structures through `LPARAM`. Those conversions are isolated to the WndProc boundary; Go pointers are not retained by Win32.

## GUI lifecycle and recovery verification

```powershell
./scripts/test-s02-shell.ps1 -StressIterations 1000
```

Passed observations:

- First and second instance exit codes: `0`
- Second-instance activation acknowledged by first instance: passed
- Minimize-to-tray then second-instance restore: passed
- Tray registration: passed
- Corrupt config quarantine and default recovery: passed
- Previous unclean exit detection: passed
- Clean session marker removal: passed
- Real process create/show/tray/task/config/shutdown cycles: `1000/1000`
- Total 1000-cycle stress duration: approximately 103 seconds

The window was captured and visually inspected: title bar, icon, Chinese navigation text, selected state, status card and resource counters rendered correctly at the active DPI.

## Formal artifact verification

- Release: `dist/GenshinTools.exe`
- Debug: `dist/GenshinTools-debug.exe`
- ProductVersion: `0.2.0`
- FileVersion: `0.2.0.0`
- Release subsystem: Windows GUI (`2`)
- Debug subsystem: Windows CUI (`3`)
- Embedded icon, portable layout and build information: verified

## Stability checklist coverage

S02 addresses applicable shell items in groups A, B, C, E, F, G and L, including callback panic containment, GetMessage error handling, UI/background separation, stale task cancellation, GDI ownership, COM lifetime, tray recovery, atomic config writes and visible GUI-build diagnostics.

## Stage boundary

S02 is complete. The next stage is `S03 - Mouse auto-click and keyboard auto-repeat P0`.

