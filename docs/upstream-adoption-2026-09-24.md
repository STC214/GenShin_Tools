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
