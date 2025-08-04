# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Go 1.24.5 or newer is required.
- Update Ollama dependency to 0.10.1.

## [0.8.0] - 2025-02-26

### Added

- Support for reasoning models in `AnthropicTextProvider` and `OpenAITextProvider`.
- Support limiting the maximum number of exchanges with the model to prevent infinite loops.
- Support forcing output JSON Schema in `OllamaTextProvider`.
  [#2](https://gitlab.com/tozd/go/fun/-/issues/2)

### Changed

- Improve prompt caching when using tools for `AnthropicTextProvider`.
- `fun` tool uses now a JSON config file instead of just a model name.
- Go 1.23.6 or newer is required.
- Update Ollama dependency to 0.5.11.

### Fixed

- Rate limiting logic for `GroqTextProvider`.

## [0.7.0] - 2024-12-10

### Changed

- Update Ollama dependency to 0.5.1.

### Fixed

- Update rate limiting logic to new rate limiting used by Anthropic.

## [0.6.0] - 2024-09-07

### Added

- Get recorder notifications when messages are received or send.
- Record various end-to-end durations.
- Support for prompt caching in `AnthropicTextProvider`.

### Changed

- Go 1.23 or newer is required.

## [0.5.1] - 2024-08-21

### Fixed

- Allow tool result to be the last message in `AnthropicTextProvider`.

## [0.5.0] - 2024-08-19

### Changed

- Store recorded AI model calls into `.error` files as well.

### Fixed

- Correctly send empty text content in `AnthropicTextProvider`.
- Rate limit handling in `AnthropicTextProvider`.

## [0.4.0] - 2024-08-18

### Added

- `combine` sub-command for the `fun` tool which combines multiple input
  directories into one output directory with only those files which are equal in
  all input directories.
- Create files with `.error` suffix for outputs with errors and skip them on followup
  runs of the `fun` tool.
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

[unreleased]: https://gitlab.com/tozd/go/fun/-/compare/v0.8.0...main
[0.8.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.7.0...v0.8.0
[0.7.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.6.0...v0.7.0
[0.6.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.5.1...v0.6.0
[0.5.1]: https://gitlab.com/tozd/go/fun/-/compare/v0.5.0...v0.5.1
[0.5.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.4.0...v0.5.0
[0.4.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.3.0...v0.4.0
[0.3.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.2...v0.3.0
[0.2.2]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.1...v0.2.2
[0.2.1]: https://gitlab.com/tozd/go/fun/-/compare/v0.2.0...v0.2.1
[0.2.0]: https://gitlab.com/tozd/go/fun/-/compare/v0.1.0...v0.2.0
[0.1.0]: https://gitlab.com/tozd/go/fun/-/tags/v0.1.0

<!-- markdownlint-disable-file MD024 -->
