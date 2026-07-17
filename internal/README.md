# Internal package boundaries

The implementation grows by the ordered stages in `docs/implementation-order.md`.

- `buildinfo`: build identity injected by the official build script.
- `paths`: executable-local portable directory layout.
- Future packages must keep Win32 calls in `platform/win32` and keep business models independent from HWND values.

Directories are added only when their stage starts; empty speculative packages are intentionally avoided.

