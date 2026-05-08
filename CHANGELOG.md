# Changelog

## [0.4.0](https://github.com/Galvill/jitenv/compare/v0.3.0...v0.4.0) (2026-05-08)


### Features

* **chpwd:** JITENV_HOOK_DEBUG diagnostics ([4cf5614](https://github.com/Galvill/jitenv/commit/4cf5614cd17c51eb1d822c462cda1e279d472316))
* cwd_glob mappings via PATH-prepend wrappers ([78d4c39](https://github.com/Galvill/jitenv/commit/78d4c3952492239055aea65d74cca82741c4ff7f))
* cwd_glob mappings via PATH-prepend wrappers ([cdd4691](https://github.com/Galvill/jitenv/commit/cdd46913af4f03ddf2880ebb058c2d9b31d795e3))
* **hook:** "Press Enter to skip" during agent-down countdown ([2a59cf1](https://github.com/Galvill/jitenv/commit/2a59cf10949b20f12d01de59bfc593bec040fd8f))
* **hook:** re-reconcile symlinks when config.toml changes mid-session ([4b8fa0d](https://github.com/Galvill/jitenv/commit/4b8fa0d309ed086afe03362e5f18eb32e8485ab1))
* **shim:** warn + countdown when agent is locked ([655866a](https://github.com/Galvill/jitenv/commit/655866a27b9dedc2f83245a4fdd74af2ee4c6c69))


### Bug Fixes

* **chpwd:** read config directly, don't depend on a running agent ([240ad68](https://github.com/Galvill/jitenv/commit/240ad682b872c2006f5194d64cec89939a90e750))
* **hook:** drop 2&gt;/dev/null on chpwd calls so diagnostics surface ([c15d8f1](https://github.com/Galvill/jitenv/commit/c15d8f1358e032e8f9c73e8353357c53b64e4321))
* **hook:** only warn for actually-mapped scripts when agent is locked ([97dd7ef](https://github.com/Galvill/jitenv/commit/97dd7ef211d891e7b73e824a965fabba296ed096))


### Documentation

* Docs:  ([cdd4691](https://github.com/Galvill/jitenv/commit/cdd46913af4f03ddf2880ebb058c2d9b31d795e3))

## [0.3.0](https://github.com/Galvill/jitenv/compare/v0.2.0...v0.3.0) (2026-05-07)


### Features

* **aws:** UI-driven encrypted credentials for AWS Secrets Manager ([aa956a0](https://github.com/Galvill/jitenv/commit/aa956a01c492596f0bf2db319c1648c1df278913))
* **tui:** list-driven AWS secret picker (folded into PR [#23](https://github.com/Galvill/jitenv/issues/23)) ([414cfca](https://github.com/Galvill/jitenv/commit/414cfcaff363e5dd21c1724efa8a4a9e76c1dd3a))
* **tui:** list-driven AWS secret picker, skip redundant source step ([d48db91](https://github.com/Galvill/jitenv/commit/d48db91978a01e793fa353adb13590abe2f75cbf))


### Bug Fixes

* **ci:** gofmt + restore var_wizard.go //nolint markers ([fcae126](https://github.com/Galvill/jitenv/commit/fcae126b8b3e7d0ff2b383a6c05a685814ea688b))
* **hook:** also skip DEBUG trap inside command_not_found_handle ([4d5cd26](https://github.com/Galvill/jitenv/commit/4d5cd262f8b4a09bad8f19b410610e7468a341e1))
* **hook:** skip DEBUG trap during bash completion ([2e27963](https://github.com/Galvill/jitenv/commit/2e279634f9accc0668f813ebd62f58331b3ec141))
* **hook:** skip DEBUG trap during bash completion ([#30](https://github.com/Galvill/jitenv/issues/30)) ([9ffe287](https://github.com/Galvill/jitenv/commit/9ffe287501882538abccea03cfe8f92b2f973e7b))
* **tui:** wire remote-source variables into the mapping form ([c92cf7d](https://github.com/Galvill/jitenv/commit/c92cf7dd7427354bc3152e93f4056d5a32d9a597))
* **tui:** wire remote-source variables into the mapping form (squashed into AWS TUI work) ([1fed564](https://github.com/Galvill/jitenv/commit/1fed564a7dde4a59d566b6199a657f417d32e12a))

## [0.2.0](https://github.com/Galvill/jitenv/compare/v0.1.3...v0.2.0) (2026-05-07)


### Features

* **packaging:** postinstall/preremove hook reminder ([54f5b62](https://github.com/Galvill/jitenv/commit/54f5b62c43a13c1135d641d0da75acfae6d3efd9))
* **tui:** per-screen help overlay (?) and empty-state copy ([dcee529](https://github.com/Galvill/jitenv/commit/dcee52962e40ab631bfbe2a78b7fcc23bec43202))


### Bug Fixes

* **tui:** mark defaultListStatus //nolint:unused ([da0f6a0](https://github.com/Galvill/jitenv/commit/da0f6a0d79b0a99b8a0b26096990a70668981628))


### Documentation

* restructure README into focused docs/ pages ([3d5fa56](https://github.com/Galvill/jitenv/commit/3d5fa560de78399afea7168fb93a5a0103a3ec07))

## [0.1.3](https://github.com/Galvill/jitenv/compare/v0.1.2...v0.1.3) (2026-05-06)


### Documentation

* add release runbook (RELEASING.md) ([3c49ef5](https://github.com/Galvill/jitenv/commit/3c49ef5ced44588cc12b5dfe14459d0eaf4d5701))
* add RELEASING.md runbook ([7327c8c](https://github.com/Galvill/jitenv/commit/7327c8cf321f0470702dbdd4d24eeaea7100e466))

## [0.1.2](https://github.com/Galvill/jitenv/compare/v0.1.1...v0.1.2) (2026-05-06)


### Bug Fixes

* **ci:** authenticate release-please with PAT so tags fire release.yml ([50a467c](https://github.com/Galvill/jitenv/commit/50a467c76be4114bb09923dea8a1047ab130a947))
* **ci:** authenticate release-please with PAT so tags trigger releases ([c1914ea](https://github.com/Galvill/jitenv/commit/c1914eaa59b15582c333d45c578a5bf6cfeb8995))

## [0.1.1](https://github.com/Galvill/jitenv/compare/v0.1.0...v0.1.1) (2026-05-06)


### Bug Fixes

* build golangci-lint from source to match Go 1.25 toolchain ([0a3f5be](https://github.com/Galvill/jitenv/commit/0a3f5be51904228a570c7848abd118739440571c))
* **ci:** use .Version in snapshot template and tighten cosign identity anchor ([822f367](https://github.com/Galvill/jitenv/commit/822f367ae73e4e2553cab34f92cc5feadeaa4225))


### Documentation

* add v0.1 release pipeline design spec ([bd47aac](https://github.com/Galvill/jitenv/commit/bd47aacc7fe7d99eb77188429bf4a155be38b66e))
* add v0.1 release pipeline implementation plan ([bebc1b5](https://github.com/Galvill/jitenv/commit/bebc1b52ceeb181bf34669e2a42bc145ea88480b))
