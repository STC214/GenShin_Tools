# S06 resource pipeline verification

S06 is complete. The safety pipeline, live current/pre-download branch provider and Win32 workflow are implemented and verified.

Implemented:

- strict, size-limited manifest JSON decoding with unknown-field and schema rejection;
- non-empty file lists, normalized unique Windows paths, URL and MD5/SHA-256 validation;
- path traversal, ADS, reserved device name and reparse-point rejection;
- bounded concurrent HTTP download with context cancellation, Range resume, timeouts, bounded retry, progress, speed and ETA;
- disk-space preflight and streaming size/hash verification;
- read-only repair plan preview;
- `data/staging/<transaction>/files` isolation, target-volume verified temporary copies, transaction journal, backup, rollback, crash recovery and successful residual cleanup.
- audited live Sophon Build JSON, bounded zstd manifests, minimal protobuf wire parsing and game/voice asset selection;
- zstd chunk assembly with per-chunk MD5, final-file MD5 and restart from the last verified chunk boundary;
- separate Win32 resource page for read-only plan generation, language selection, double-click confirmation, progress and cancellation;
- startup recovery of interrupted resource transactions.
- official branch discovery with explicit `pre_download: null` handling; future pre-downloads remain verified under `data/staging` and never modify the installed game before release;
- transactional `gid_ver` and `config.ini` updates when a verified version is committed.

Automated fault coverage currently includes:

- HTTP 503 followed by a valid resumed response;
- cancellation during a throttled response;
- impossible disk-space requirement;
- invalid schema, unknown fields, unsafe paths and hash validation;
- locked destination rollback;
- recovery after an original file was renamed but installation had not completed;
- race-enabled package tests.
- full fixture chain from Build JSON through zstd/protobuf and resumed chunk assembly;
- unknown Build JSON fields and unknown protobuf fields fail closed;
- live read-only `game,zh-cn` audit on 2026-07-17: version `6.7.0`, 2,752 files, 114,102 chunks, 132,591,238,107 bytes.
- offline refusal and multi-file mid-transaction rollback;
- release/debug artifact verification and the `build/s06-resource.png` packaged GUI smoke capture.
