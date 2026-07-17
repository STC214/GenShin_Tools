# Internal package boundaries

The implementation grows by the ordered stages in `docs/implementation-order.md`.

- `buildinfo`: build identity injected by the official build script.
- `config`: versioned, validated and atomically committed portable settings.
- `game`: read-only game discovery, config parsing, cancellable size scan and process identity.
- `launch`: pure launch arguments, process ownership state machine and native Shell shortcuts.
- `localenhance`: reversible server conversion, HDR registry snapshots, WAV startup sound and audited BetterGI protocol integration.
- `diagnostics`: JSON Lines logger and abnormal-session marker.
- `input`: S03 state machine, dedicated low-level hook thread, waitable timer, integrity checks and `SendInput` boundary.
- `paths`: executable-local portable directory layout.
- `resources`: strict manifests, resumable verified downloads, repair planning and recoverable staging transactions.
- `platform/win32`: typed low-level Windows API boundary.
- `shell`: S02/S03 window, navigation, input page, session/power handling, DPI, single-instance, tray and lifetime coordinator.
- `taskrunner`: cancellable background task IDs and bounded shutdown.
- Shared Win32 calls belong in `platform/win32`; subsystem-owned ABI such as low-level hooks and `INPUT` stays isolated at that subsystem boundary. Business models remain independent from HWND values.

Directories are added only when their stage starts; empty speculative packages are intentionally avoided.
