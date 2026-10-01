# FuFuPlugin 1.7.0 focused upstream adoption (2026-09-24)

## Reviewed upstream state

- FufuLauncher release: `1.7.0.0`, tag commit
  `a87b9b42c1c9d21f9f7c23002027736b673c3d6f`.
- FuFuPlugin bundle repository commit:
  `cfca899984aa1c276a9836fb79d9108e55348d33`.
- Bundle metadata: game `7.1`, plugin `1.7.0`, build description `1.7.0.0`.
- Observed bundle SHA-256:
  `f551255c0558f28b0d8d5365d03a2e34f595674ee2c9d50b1d7247728a9084d9`.
- The next upstream source version `1.7.0.1` had no tag, release or replacement
  plugin bundle at review time and is not treated as a released dependency.

## Adopted behavior

- The public main-plugin download uses the repository's GitHub Contents API
  raw media response. Redirect targets remain restricted to official GitHub
  hosts, and the existing bounded download, ZIP layout, PE, dependency and
  SHA-256 checks remain mandatory.
- The dynamic INI adapter accepts the released 1.7.0 fields without hard-coded
  UI replication. Existing compatible user values remain migrated during
  repair and new fields retain upstream defaults.
- Plugin repair, compatibility audit and injection launch re-inspect the game
  root before using the candidate. This closes the observed window where the
  launcher started on game 7.0, the game updated to 7.1, and the elevated
  helper rejected the stale request before plugin audit.

## Scope disposition

- Upstream launcher UI, account, browser, updater and unrelated product code
  remain excluded by the existing scope policy.
- The general `upstream.lock.json` baseline remains unchanged because the
  repository-wide comparison exceeds GitHub's 300-file compare response. This
  record adopts only the release tag, public plugin bundle contract and the
  locally verified candidate-refresh behavior; it does not claim a full
  repository baseline review.

## Verification

- Offline plugin, injection, game and shell tests cover the 1.7 INI schema and
  game 7.0 to 7.1 candidate refresh.
- `GENSHINTOOLS_LIVE_FUFU_MAIN=1` validates download and installation of the
  current official bundle without executing its DLL.

## Follow-up: FuFuPlugin 1.7.0.1 bundle (2026-09-25)

- FufuLauncher release `1.7.0.1` was published at
  `https://github.com/FufuLauncher/FufuLauncher/releases/tag/1.7.0.1`.
- The plugin bundle changed at repository commit
  `8c14463fba916c03a6bd6f1d1f8b1ef61392e16e`; its ZIP is 194,717 bytes with
  SHA-256 `c655ef59c01e151285947a5151a47ff95e1c7cc41609df34283346785e934ed4`.
- Bundle metadata is game `7.1`, plugin `1.7.0`, build description `1.7.0.1`.
  The ten new INI sections cover camera offset (enable and X/Y/Z) and free
  camera (enable, toggle/lock keys, movement speed, sprint multiplier, and
  mouse sensitivity).
- No production code change was needed: `LoadFufuTargetConfig` derives the UI
  schema from the package INI, and the existing typed editor supports the new
  `bool`, `float`, `key`, and `string` fields. A regression test now covers all
  ten sections; the live package-download/install test also passed against the
  current official ZIP without loading or executing its DLL.
- The launcher release's background-media download UI is not part of this
  project's FuFuPlugin integration and was not copied into the project.

## Follow-up: FuFuPlugin 1.7.0.2 bundle (2026-09-28)

- FufuLauncher released `1.7.0.2` on 2026-09-26 to address plugin performance
  and monitor ordering. The public plugin ZIP changed at commit
  `400e52343a0435c379c9c539fc10f7f01febb9fd` and is 194,972 bytes,
  SHA-256 `2d92b407b1eff7020e9d4df628d4af4bc127358770f36bcd7c082189325864f6`.
- The ZIP still contains only `config.ini` and
  `FufuLauncher.UnlockerIsland.dll`. Its 53 INI sections and plugin version
  `1.7.0` are unchanged from build `1.7.0.1`; the description now identifies
  build `1.7.0.2`. The DLL SHA-256 changed from
  `ed7b7a6c8d938188152ea887d7a84b816efc07a54f3eb9244f6a86c68646eaea`
  to `fd2f0b2686c165a4a9b8acd2d8862ed59e18102937090e5d53afc5c5c52bcf69`.
- The existing official-URL downloader already acquires the replacement ZIP;
  the transactional installer keys rollback by content revision rather than
  only by the unchanged plugin version. A live regression test pins both
  released ZIP hashes and verifies install, same-version replacement, and
  rollback without executing either DLL.
- Source commits after the release add `PreventDetectionPopup` to the launcher
  repository's INI, but this field is absent from the published ZIP. The
  dynamic INI adapter will accept it when a bundle includes it; no unreleased
  default is introduced locally.
- Upstream's separate Yae injector now enumerates the full x64 remote module
  path rather than using a truncated thread exit code. This project already
  verifies the loaded module by ToolHelp path under its injection deadline, so
  no injection code is copied. Launcher UI, account, and monitor-ordering code
  remain outside the FuFuPlugin package contract.

## Follow-up: FuFuPlugin 1.7.0.3 bundle (2026-10-01)

- FufuLauncher published release `1.7.0.3` on 2026-10-01. The public plugin
  repository's `main` commit `254961f42c34362e4557315052b40b7375fc71d2`
  replaced the four published ZIP variants; this project continues to download
  only the official `FuFuPlugin.zip` at the existing Contents API URL.
- The new ZIP is 196,336 bytes, SHA-256
  `a0ade0ba0d75f02bfbf84d9ad157f36315b25815aef7c5d65ec30c8724fa601a`.
  It still contains only `config.ini` and `FufuLauncher.UnlockerIsland.dll`.
  The DLL SHA-256 is
  `690485c04321c18913a04b88e2b2e7a30f939f105c45f068d033efc57fb14b83`.
- The INI retains plugin version `1.7.0` and game version `7.1`, but its build
  description is `1.7.0.3`. It has 59 sections (58 settings): six new default-on
  `bool` fields are `PreventDetectionPopup` and five `ResinItem*` options.
  The existing dynamic INI adapter and same-version content-revision rollback
  require no runtime change. The live regression now checks the new bundle hash,
  installation, all six new fields, replacement, and rollback without loading
  the DLL.
- The launcher tag comparison from `1.7.0.2` to `1.7.0.3` also includes UI,
  account/community, installer, and update-notice changes outside this
  project's FuFuPlugin integration. The repository-wide baseline is unchanged:
  its older compare still exceeds GitHub's 300-file response cap.
