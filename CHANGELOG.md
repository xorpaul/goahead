# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.2.0] - 2026-11-09

### Added
- `reboot_completion_panic_actions` `mail` is now implemented: a mail is sent via the local `sendmail` (`sendmail_path`, `mail_from`) containing the re-executed `reboot_completion_check` and panic script command lines, exit codes and output for every host that did not come back, plus a link to the cluster config file (`cluster_config_url_base`)
- Support for the list format `reboot_completion_panic_action` (singular, `- mail: [...]`, `- script1: [...]`) that all goaheadmw cluster configs use; it was silently ignored before
- Background timer that fires the reboot completion panic actions without needing an incoming request

### Changed
- The test suite no longer hard-codes port 8443 and `/tmp/goahead/`: the port defaults to `listen_port` of `config.yml` and can be set with `-goahead-test-port` or `GOAHEAD_TEST_PORT` (`0` picks a free port); state and logs go to a private temporary directory. `main()` was split into `main()` and `run(configFile)` for this
- Clusters with `reboot_completion_check_offset` larger than `reboot_completion_panic_threshold` are automatically disabled at startup and an ERROR is logged, because the panic would fire before the completion check starts polling
- Panic actions now run asynchronously and once per host that is still offline, with `{:%fqdn%:}` being the offline host instead of the host that sent the triggering request
- A `reboot_completion_panic_threshold` of `0` or unset no longer triggers a panic on every request
- Updated vendor dependencies (`golang.org/x/sys` v0.50.0, `github.com/mattn/go-colorable` v0.1.16)

### Fixed
- `-version` printed a hard-coded `0.0.8`; it now prints the version injected at build time by `build_release.sh` (`-X main.buildversion=...`), or `dev` for a plain `go build`

## [v0.1.0] - 2026-09-18

### Added
- Auto-generate a self-signed ECDSA P-256 TLS certificate and key when the configured `ssl_private_key` or `ssl_certificate_file` paths do not exist on disk, so the server (and test suite) starts without any manual certificate setup. The generated certificate includes SANs for `127.0.0.1`, `::1`, and `localhost`.

### Fixed
- Panic when the cluster state JSON file exists but is empty (contributed by linxside, PR #11)
- `ssl_client_cert_ca_file` existence check is now conditional on `ssl_require_and_verify_client_cert: true`; it was previously checked unconditionally even though it is only loaded in that branch

### Changed
- Updated vendor dependencies

## [v0.0.9] - 2026-01-21

### Added
- Support for `.yaml` file extension besides `.yml`
- Added build_release submodule

### Changed
- Updated vendor dependencies
- Code cleanup using gofmt, golint, go vet
- Fixes to make code work with Go 1.24.6
- Logger now outputs to both file and stderr for better error visibility

### Fixed
- Fixed YAML format issues
- Fixed tests

## [v0.0.8] - 2025-07-18

### Added
- Reboot completion panic threshold and actions feature

### Changed
- Updated vendor dependencies (2025)

### Fixed
- Test fixes
- Use newer build_release.sh

## [v0.0.7] - 2024-09-20

### Added
- OBS (Open Build Service) spec file support
- Added 'golang' as dependency in specfile

### Changed
- Use `os.ReadFile` instead of deprecated ioutil
- Bumped version number to 0.0.7
- Updated specfile to use default go (any version) or go1.19

### Fixed
- Fixed `/v1/inquire/` request when middleware found a reason for client to restart
- Prevent goahead if the cluster state JSON file can't be written (fixes #3)

## [v0.0.6] - 2020-01-10

### Changed
- Version bump to v0.0.6

## [v0.0.5] - 2020-01-10

### Fixed
- Hotfix for always mismatching request id

## [v0.0.4] - 2020-01-09

### Fixed
- Save previous goahead state in ackFile even when a new restart is triggered

## [v0.0.3] - 2020-01-09

### Fixed
- Make sure mutex is unlocked properly

## [v0.0.2] - 2020-01-09

### Changed
- Switch from channel to simple sleepingClusterChecks global map
- Switch to Info logging level

### Fixed
- Fix missing cluster issue
- Add .zip support

## [v0.0.1] - 2019-12-20

### Added
- Initial release

### Fixed
- Fix trigger to start check for rebooted systems
