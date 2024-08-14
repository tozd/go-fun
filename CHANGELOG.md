# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Context-based recorder for information about internal calls made to a model.
- Transparent tool calling into Go code.
- OpenAI provider.

## [0.3.0] - 2024-08-07

### Added

- Create files with `.invalid` suffix for outputs which fail JSON Schema validation
  and skip them on followup runs of the `fun` tool.

### Changed

- Retry communication errors for Ollama provider.

## [0.2.2] - 2024-08-04

### Added

- Show final summary in `fun` tool.

## [0.2.1] - 2024-08-04

### Fixed

- Fixed Go package.

## [0.2.0] - 2024-08-04

### Added

- Added `fun` CLI tool.
- Logging using [zerolog](https://github.com/rs/zerolog).

## [0.1.0] - 2024-07-31

### Added

- First public release.

[unreleased]: https://gitlab.com/tozd/go/fun/-/compare/v0.3.0...main
[0.3.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.2...v0.3.0
[0.2.2]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.1...v0.2.2
[0.2.1]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.0...v0.2.1
[0.2.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.1.0...v0.2.0
[0.1.0]: https://gitlab.com/tozd/go/fun/-/tags/v0.1.0

<!-- markdownlint-disable-file MD024 -->
