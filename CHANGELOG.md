# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
