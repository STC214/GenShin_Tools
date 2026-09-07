# Internal package boundaries

The implementation grows by the ordered stages in `docs/implementation-order.md`.

- `buildinfo`: build identity injected by the official build script.
- `capture`: bounded screenshot worker, hotkey configuration and atomic PNG capture.
- `config`: versioned, validated and atomically committed portable settings.
- `cpumonitor`: bounded logical-processor sampling used by diagnostics and the overlay.
- `game`: read-only game discovery, config parsing, cancellable size scan and process identity.
- `gamewindow`: PID plus creation-time validation and non-blocking top-level HWND discovery.
- `injection`: module audit, helper protocol, suspended launch, remote-load confirmation and bounded rollback.
- `launch`: pure launch arguments, process ownership state machine and native Shell shortcuts.
- `localenhance`: reversible server conversion, HDR registry snapshots, WAV startup sound and audited BetterGI protocol integration.
- `localization`: English/Chinese string catalogs and system-language selection.
- `overlay`: process CPU, GPU PDH and DXGI ETW sampling plus a no-activate click-through window.
- `diagnostics`: JSON Lines logger and abnormal-session marker.
- `input`: S03 state machine, dedicated low-level hook thread, waitable timer, integrity checks and `SendInput` boundary.
- `paths`: executable-local portable directory layout.
- `resources`: strict manifests, resumable verified downloads, repair planning and recoverable staging transactions.
- `platform/win32`: typed low-level Windows API boundary.
- `plugins`: declarative plugin state, store/catalog querying, Fufu target adaptation and transactional install/update.
- `selfupdate`: signed release staging, updater handshake, transactional commit, confirmation and rollback.
- `shell`: native navigation/pages, session and power handling, DPI, single-instance, tray and cross-feature lifetime coordinator.
- `shellconfig`: UI-facing key/value configuration helpers.
- `taskrunner`: cancellable background task IDs and bounded shutdown.
- `uitheme`: shared native colors, brushes and theme state.
- `upstreamaudit`: bounded read-only upstream difference classification and report generation.
- Shared Win32 calls belong in `platform/win32`; subsystem-owned ABI such as low-level hooks and `INPUT` stays isolated at that subsystem boundary. Business models remain independent from HWND values.

Directories are added only when their stage starts; empty speculative packages are intentionally avoided.
