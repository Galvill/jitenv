# Changelog

## [0.10.2](https://github.com/Galvill/jitenv/compare/v0.10.1...v0.10.2) (2026-05-28)


### Bug Fixes

* **release:** decouple macOS sign/notarize + cask from the main release ([#13](https://github.com/Galvill/jitenv/issues/13)) ([#212](https://github.com/Galvill/jitenv/issues/212)) ([38862fc](https://github.com/Galvill/jitenv/commit/38862fcbb310752004e8cdd89c8d615cec67583d))

## [0.10.1](https://github.com/Galvill/jitenv/compare/v0.10.0...v0.10.1) (2026-05-27)


### Bug Fixes

* **release:** raise macOS notarization wait to 45m ([#13](https://github.com/Galvill/jitenv/issues/13)) ([#211](https://github.com/Galvill/jitenv/issues/211)) ([f98e247](https://github.com/Galvill/jitenv/commit/f98e247766895160412d73c7d2639bfcb65f3e03))

## [0.10.0](https://github.com/Galvill/jitenv/compare/v0.9.1...v0.10.0) (2026-05-27)


### Features

* **clone:** jitenv clone &lt;url&gt; — encrypted bag + GIT_ASKPASS injection ([#179](https://github.com/Galvill/jitenv/issues/179)) ([#181](https://github.com/Galvill/jitenv/issues/181)) ([7b72d8f](https://github.com/Galvill/jitenv/commit/7b72d8ff2a02cf0aa33811d4cd0cafa6e96b182a))
* **release:** Apple Developer ID code-sign + notarize macOS binaries ([#13](https://github.com/Galvill/jitenv/issues/13)) ([#176](https://github.com/Galvill/jitenv/issues/176)) ([6927ebb](https://github.com/Galvill/jitenv/commit/6927ebb22d4f408b5b80257a4087c4209ec49b02))
* **release:** Chocolatey package for Windows installs ([#180](https://github.com/Galvill/jitenv/issues/180)) ([#197](https://github.com/Galvill/jitenv/issues/197)) ([2c157ec](https://github.com/Galvill/jitenv/commit/2c157ececf68c7fa4915eca71d1e9cd43f7583bd))


### Bug Fixes

* **agent/windows:** authenticate pipe peer via impersonation, not PID ([#132](https://github.com/Galvill/jitenv/issues/132)) ([#200](https://github.com/Galvill/jitenv/issues/200)) ([42e1358](https://github.com/Galvill/jitenv/commit/42e1358f84624c1a55548a861d0ac2bccb27df13))
* **chpwd:** rehash shell command table when the cwd_glob wrapper set changes ([#201](https://github.com/Galvill/jitenv/issues/201)) ([adb6dbb](https://github.com/Galvill/jitenv/commit/adb6dbb0318a479cf66700414db219d6c76de810))

## [0.9.1](https://github.com/Galvill/jitenv/compare/v0.9.0...v0.9.1) (2026-05-27)


### Bug Fixes

* **tui:** scroll the var-selection tree to keep the cursor visible ([#194](https://github.com/Galvill/jitenv/issues/194)) ([#195](https://github.com/Galvill/jitenv/issues/195)) ([bb3e98e](https://github.com/Galvill/jitenv/commit/bb3e98e8918bf3991d54e505ee5d170e4fb18a99))

## [0.9.0](https://github.com/Galvill/jitenv/compare/v0.8.1...v0.9.0) (2026-05-26)


### Features

* **release:** add manual RC tagger workflow ([#192](https://github.com/Galvill/jitenv/issues/192)) ([910a898](https://github.com/Galvill/jitenv/commit/910a898a33093bd34d6a6b4d3f31ace12f79046c))


### Bug Fixes

* **cli:** mkdir config parent before TUI lockfile acquire ([#190](https://github.com/Galvill/jitenv/issues/190)) ([#191](https://github.com/Galvill/jitenv/issues/191)) ([e23465f](https://github.com/Galvill/jitenv/commit/e23465f969ad50274c684ee42be8d0cada4799e5))

## [0.8.1](https://github.com/Galvill/jitenv/compare/v0.8.0...v0.8.1) (2026-05-26)


### Bug Fixes

* **release:** use index .Env for homebrew cask token ([#188](https://github.com/Galvill/jitenv/issues/188)) ([42b25e1](https://github.com/Galvill/jitenv/commit/42b25e19693540f23194f97351f4a2f4007ae976))

## [0.8.0](https://github.com/Galvill/jitenv/compare/v0.7.0...v0.8.0) (2026-05-26)


### Features

* **release:** Homebrew cask for macOS/Linux installs ([#13](https://github.com/Galvill/jitenv/issues/13)) ([#175](https://github.com/Galvill/jitenv/issues/175)) ([f2d941e](https://github.com/Galvill/jitenv/commit/f2d941e80c875c9d5b96b9b66c6452a9c3eb5b39))
* **versioncheck:** daily background check + one-line upgrade notice ([#136](https://github.com/Galvill/jitenv/issues/136)) ([#178](https://github.com/Galvill/jitenv/issues/178)) ([21e963d](https://github.com/Galvill/jitenv/commit/21e963d0b573c6615570a9b0e7e94e8d0748816b))


### Bug Fixes

* close all symptoms of [#182](https://github.com/Galvill/jitenv/issues/182) (TUI binary split + injection-marker lifecycle) ([#187](https://github.com/Galvill/jitenv/issues/187)) ([95851c4](https://github.com/Galvill/jitenv/commit/95851c485cedf6f51a10b6d743553a2e7358f654))

## [0.7.0](https://github.com/Galvill/jitenv/compare/v0.6.0...v0.7.0) (2026-05-18)


### Features

* **agent:** hidden-console daemon spawn + key-handle handoff on Windows ([#87](https://github.com/Galvill/jitenv/issues/87)) ([#96](https://github.com/Galvill/jitenv/issues/96)) ([5b4909b](https://github.com/Galvill/jitenv/commit/5b4909bed75d8bea4dca1a9f63f93330637b64f5))
* **agent:** named-pipe transport + token-SID peer auth for Windows ([#86](https://github.com/Galvill/jitenv/issues/86)) ([#93](https://github.com/Galvill/jitenv/issues/93)) ([6da2672](https://github.com/Galvill/jitenv/commit/6da2672756286140b1769562a1d536ebd7e767db))
* **chpwd,shim:** .ps1 wrapper shims + Windows-aware chpwd reconcile ([#89](https://github.com/Galvill/jitenv/issues/89)) ([#99](https://github.com/Galvill/jitenv/issues/99)) ([7c3f603](https://github.com/Galvill/jitenv/commit/7c3f60398ea3e49fceda58edd83a827f4ac8d72d))
* **powershell:** intercept path/glob commands via PSReadLine AcceptLine ([#103](https://github.com/Galvill/jitenv/issues/103)/[#104](https://github.com/Galvill/jitenv/issues/104)) ([#105](https://github.com/Galvill/jitenv/issues/105)) ([3c5f458](https://github.com/Galvill/jitenv/commit/3c5f458fc108fc4d3575504d4320a83479ec0362))
* **run,shim:** spawn-and-wait Windows runtime for jitenv run + shim ([#88](https://github.com/Galvill/jitenv/issues/88)) ([#95](https://github.com/Galvill/jitenv/issues/95)) ([174fd7b](https://github.com/Galvill/jitenv/commit/174fd7bd19a9a9fb30a381268e7393aaf11d3bdb))
* **windows:** PowerShell 7 hook + passphrase prompt over CONIN$/CONOUT$ ([#101](https://github.com/Galvill/jitenv/issues/101)) ([dda360f](https://github.com/Galvill/jitenv/commit/dda360f47957b2515742b2e102081773dba56ca4))
* **windows:** release gate — %APPDATA% config, GoReleaser zip targets, docs ([#91](https://github.com/Galvill/jitenv/issues/91)) ([#102](https://github.com/Galvill/jitenv/issues/102)) ([9c17290](https://github.com/Galvill/jitenv/commit/9c17290f49909f23e0773266bedecda58d40f1a4))


### Bug Fixes

* **shim:** use PATHEXT lookup for .exe candidates on Windows ([#97](https://github.com/Galvill/jitenv/issues/97)) ([#98](https://github.com/Galvill/jitenv/issues/98)) ([7853496](https://github.com/Galvill/jitenv/commit/785349684c707c22fb93d87212e583b8a6e848fd))

## [0.6.0](https://github.com/Galvill/jitenv/compare/v0.5.0...v0.6.0) (2026-05-13)


### Features

* **sources:** add Vault source plugin with token + AppRole auth ([#81](https://github.com/Galvill/jitenv/issues/81)) ([#85](https://github.com/Galvill/jitenv/issues/85)) ([9097092](https://github.com/Galvill/jitenv/commit/9097092be6fc0964f4bbd3a5924514cfd5e7b5b0))
* **tui:** bulk-import KEY=VALUE pairs into a local bag ([#70](https://github.com/Galvill/jitenv/issues/70)) ([#75](https://github.com/Galvill/jitenv/issues/75)) ([94edf95](https://github.com/Galvill/jitenv/commit/94edf9554e6fdc8d211889ad9f99af89e0d9cd00))


### Bug Fixes

* **hook:** bake paths into snippet, sidecar nanosecond mtime ([#69](https://github.com/Galvill/jitenv/issues/69)) ([1fb7cab](https://github.com/Galvill/jitenv/commit/1fb7cab8fd6d42e3783ff0c3a456801146f9232d))
* **shim:** collapse duplicate execReal after [#79](https://github.com/Galvill/jitenv/issues/79)/[#80](https://github.com/Galvill/jitenv/issues/80) merge race ([#82](https://github.com/Galvill/jitenv/issues/82)) ([08dfabf](https://github.com/Galvill/jitenv/commit/08dfabf83f1847eaee0e369aab3238593983e29c))
* **shim:** suppress duplicate agent-down warning on execve chains ([#71](https://github.com/Galvill/jitenv/issues/71)) ([#74](https://github.com/Galvill/jitenv/issues/74)) ([7ae916e](https://github.com/Galvill/jitenv/commit/7ae916e15f23c87469020748af11b1cf3f3e2c5c))
* **shim:** suppress duplicate env injection on execve chains ([#77](https://github.com/Galvill/jitenv/issues/77)) ([#79](https://github.com/Galvill/jitenv/issues/79)) ([4ce1e16](https://github.com/Galvill/jitenv/commit/4ce1e166c591faeee892f9b1c66abc8405b822aa))
* **tui:** drop dup help-text + remove unsafe AWS profile field ([#76](https://github.com/Galvill/jitenv/issues/76)) ([#78](https://github.com/Galvill/jitenv/issues/78)) ([c06e704](https://github.com/Galvill/jitenv/commit/c06e7041b4d5c35a9b0f325ab622d7f90f82d211))

## [0.5.0](https://github.com/Galvill/jitenv/compare/v0.4.0...v0.5.0) (2026-05-11)


### ⚠ BREAKING CHANGES

* configs containing `[sources.<name>]` of type `github` will fail `jitenv config validate`. Remove the entry (and any mappings that referenced it) from your `config.toml`.

* remove the github Variables source backend ([3dc2804](https://github.com/Galvill/jitenv/commit/3dc28045c1d50351668010d1290a60c62b48d39a))


### Features

* display version via -v, --help footer, and TUI ([f02cad1](https://github.com/Galvill/jitenv/commit/f02cad1aff43ba477e64847a404f1071e0db2f70))
* **e2e:** containerised harness with two distros + LocalStack ([cbd1848](https://github.com/Galvill/jitenv/commit/cbd18488dc52deff53517881935ea0e130387c18))
* **e2e:** containerised harness with two distros + LocalStack ([fad6bff](https://github.com/Galvill/jitenv/commit/fad6bffdbddd159c08edf423252dddc28258466f))
* **e2e:** install jitenv from release artefacts ([ac6b6e6](https://github.com/Galvill/jitenv/commit/ac6b6e6ba8db5985a754d8cf7923e4e3dbe2a35f))
* **e2e:** install jitenv from release artefacts (closes [#53](https://github.com/Galvill/jitenv/issues/53)) ([1aad675](https://github.com/Galvill/jitenv/commit/1aad67530342a99107c050023d7a3d0e96e45210))
* macOS port (darwin/amd64 + darwin/arm64) ([2b104a8](https://github.com/Galvill/jitenv/commit/2b104a85a5206ea74c9b3ec0bfe7be184d5d2265))
* **run:** opt-in pre-run notice on stderr ([#45](https://github.com/Galvill/jitenv/issues/45)) ([4a23d5a](https://github.com/Galvill/jitenv/commit/4a23d5a5997d1ff1afc0832cfde2d30087aae034))
* **run:** opt-in pre-run notification of injected variable count ([2b399ba](https://github.com/Galvill/jitenv/commit/2b399badbb279be562464db6bcd3ad081f13eda0))
* **run:** turn on the pre-run notice by default ([e6bb499](https://github.com/Galvill/jitenv/commit/e6bb499aa9df7a2ead1b9c4a87569bfeeadcd1ac))
* **site:** add jitenv.com landing page served from /docs ([3b89f2e](https://github.com/Galvill/jitenv/commit/3b89f2e334438d496333a5cbd30e24dc87c54a14))
* **site:** add jitenv.com landing page served from /docs ([8a71e04](https://github.com/Galvill/jitenv/commit/8a71e04d09daf1c074ad40b2ae63a48d77950570))
* surface jitenv version via -v/--version, --help, and TUI footer ([f14db37](https://github.com/Galvill/jitenv/commit/f14db37c2fc8bcc1de0611bb8226a7cf8398a026))
* **tui:** align Save/Back/Apply/Quit nav conventions across screens ([2b1b0ea](https://github.com/Galvill/jitenv/commit/2b1b0ea7200c71d5bc0160915a6bce5ae0687ec9))
* **tui:** align Save/Back/Apply/Quit nav conventions across screens ([bf7a8a9](https://github.com/Galvill/jitenv/commit/bf7a8a9ad4697c8574090c4306677accca6bd5d0))
* **tui:** switch cwd_glob commands editor to a list page ([350f8fb](https://github.com/Galvill/jitenv/commit/350f8fbbc1a0dbea8778345c95971ad14db73443))
* **tui:** switch cwd_glob commands editor to a list page (closes [#38](https://github.com/Galvill/jitenv/issues/38)) ([f4421fc](https://github.com/Galvill/jitenv/commit/f4421fc702789133aba625967ccb565c6b9f0997))


### Bug Fixes

* **hook:** non-interactive UX, stale docs, dead code ([#65](https://github.com/Galvill/jitenv/issues/65)) ([59774b3](https://github.com/Galvill/jitenv/commit/59774b32b0bef3bb8f3097b114b59c0776860e5b))
* **hook:** strip trailing slash from XDG_RUNTIME_DIR ([566d667](https://github.com/Galvill/jitenv/commit/566d667278888fab697c0e9ef7317ba8fdf1e119))
* **hook:** strip trailing slash from XDG_RUNTIME_DIR / TMPDIR ([5571308](https://github.com/Galvill/jitenv/commit/557130899f420f673fae89d5ddc6226c2e169ec4))
* **shim:** emit pre-run notice for cwd_glob commands too ([47e12f6](https://github.com/Galvill/jitenv/commit/47e12f68df6f814ccee886669e8d512cedacd153))
* **shim:** only inject env vars for direct shell invocations ([#52](https://github.com/Galvill/jitenv/issues/52)) ([e3fbf4e](https://github.com/Galvill/jitenv/commit/e3fbf4e115bca3be5a50d72d3c68b53f000182c8))
* **shim:** only inject env vars for direct shell invocations ([#52](https://github.com/Galvill/jitenv/issues/52)) ([5db4466](https://github.com/Galvill/jitenv/commit/5db4466318e96aade96cbf791ead3523dec26290))
* **tui,cli:** rework footer layout and --help trailing newline ([924a6fd](https://github.com/Galvill/jitenv/commit/924a6fdaeb8624374aa40db68225f836ee2027fd))
* **tui:** align footer's left padding with the status bar ([29844bd](https://github.com/Galvill/jitenv/commit/29844bd550a3226c26cc883b46a9635ae5f4d64a))
* **tui:** left-align global footer ([539b824](https://github.com/Galvill/jitenv/commit/539b824a2a6252997676c5ba5773df50335c7ed4))

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
