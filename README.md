# Define functions with code, data, or natural language description

[![pkg.go.dev](https://pkg.go.dev/badge/gitlab.com/tozd/go/fun)](https://pkg.go.dev/gitlab.com/tozd/go/fun)
[![Go Report Card](https://goreportcard.com/badge/gitlab.com/tozd/go/fun)](https://goreportcard.com/report/gitlab.com/tozd/go/fun)
[![pipeline status](https://gitlab.com/tozd/go/fun/badges/main/pipeline.svg?ignore_skipped=true)](https://gitlab.com/tozd/go/fun/-/pipelines)
[![coverage report](https://gitlab.com/tozd/go/fun/badges/main/coverage.svg)](https://gitlab.com/tozd/go/fun/-/graphs/main/charts)

A Go package that allows you to define functions with code (the usual way), data
(providing examples of inputs and expected outputs which are then used with an AI model),
or natural language description.

Features:

- A common interface to support both code-defined, data-defined, and description-defined functions.
- Functions are strongly typed so inputs and outputs can be Go structs and values.
- Provides [Groq](https://groq.com/), [Anthropic](https://www.anthropic.com/) and
  [Ollama](https://ollama.com/) integrations for AI models.
- Uses adaptive rate limiting to maximize throughput of API calls made to integrated AI models.

## Installation

This is a Go package. You can add it to your project using `go get`:

```sh
go get gitlab.com/tozd/go/fun
```

It requires Go 1.22 or newer.

## Usage

See full package documentation with examples on [pkg.go.dev](https://pkg.go.dev/gitlab.com/tozd/go/fun#section-documentation).

## GitHub mirror

There is also a [read-only GitHub mirror available](https://github.com/tozd/go-fun),
if you need to fork the project there.

## Acknowledgements

The project gratefully acknowledge the [HPC RIVR consortium](https://www.hpc-rivr.si) and
[EuroHPC JU](https://eurohpc-ju.europa.eu) for funding this project by providing computing
resources of the HPC system Vega at the
[Institute of Information Science](https://www.izum.si).

Funded by the European Union. Views and opinions expressed are however those of the author(s) only
and do not necessarily reflect those of the European Union or European Commission.
Neither the European Union nor the granting authority can be held responsible for them.
Funded within the framework of the [NGI Search](https://www.ngisearch.eu/)
project under grant agreement No 101069364.

<!-- markdownlint-disable MD033 -->

<img src="EN_FundedbytheEU_RGB_POS.png" alt="Funded by the European Union emblem" height="60" />
<img src="NGISearch_logo.svg" alt="NGI Search logo" height="60" />

<!-- markdownlint-enable MD033 -->
