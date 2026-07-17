# S06 resource pipeline verification

S06 is in progress. The first core milestone establishes the safety boundary used by the later live provider and UI work.

Implemented:

- strict, size-limited manifest JSON decoding with unknown-field and schema rejection;
- non-empty file lists, normalized unique Windows paths, URL and MD5/SHA-256 validation;
- path traversal, ADS, reserved device name and reparse-point rejection;
- bounded concurrent HTTP download with context cancellation, Range resume, timeouts, bounded retry, progress, speed and ETA;
- disk-space preflight and streaming size/hash verification;
- read-only repair plan preview;
- `data/staging/<transaction>/files` isolation, target-volume verified temporary copies, transaction journal, backup, rollback, crash recovery and successful residual cleanup.

Automated fault coverage currently includes:

- HTTP 503 followed by a valid resumed response;
- cancellation during a throttled response;
- impossible disk-space requirement;
- invalid schema, unknown fields, unsafe paths and hash validation;
- locked destination rollback;
- recovery after an original file was renamed but installation had not completed;
- race-enabled package tests.

Still required before S06 can be marked complete:

- the audited live Sophon manifest adapter and game/voice/pre-download selection;
- Win32 UI integration and user-visible repair-plan confirmation;
- end-to-end provider fixtures for API changes, offline operation and multi-file failure;
- final full-project build and artifact verification.
