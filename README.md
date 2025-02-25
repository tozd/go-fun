# Define functions with code, data, or natural language description

[![pkg.go.dev](https://pkg.go.dev/badge/gitlab.com/tozd/go/fun)](https://pkg.go.dev/gitlab.com/tozd/go/fun)
[![Go Report Card](https://goreportcard.com/badge/gitlab.com/tozd/go/fun)](https://goreportcard.com/report/gitlab.com/tozd/go/fun)
[![pipeline status](https://gitlab.com/tozd/go/fun/badges/main/pipeline.svg?ignore_skipped=true)](https://gitlab.com/tozd/go/fun/-/pipelines)
[![coverage report](https://gitlab.com/tozd/go/fun/badges/main/coverage.svg)](https://gitlab.com/tozd/go/fun/-/graphs/main/charts)

A Go package provides high-level abstraction to define functions with code (the usual way),
data (providing examples of inputs and expected outputs which are then used with an AI model),
or natural language description.
It is the simplest but powerful way to use large language models (LLMs) in Go.

Features:

- A common interface to support both code-defined, data-defined, and description-defined functions.
- Functions are strongly typed so inputs and outputs can be Go structs and values.
- Provides **unofficial** [OpenAI](https://openai.com/), [Groq](https://groq.com/),
  [Anthropic](https://www.anthropic.com/) and [Ollama](https://ollama.com/) integrations for AI (LLM) models.
- Support for tool calling which transparently calls into Go functions with Go structs and values
  as inputs and outputs. Recursion possible.
- Uses adaptive rate limiting to maximize throughput of API calls made to integrated AI models.
- Provides a CLI tool `fun` which makes it easy to run data-defined and description-defined functions on files.

## Installation

This is a Go package. You can add it to your project using `go get`:

```sh
go get gitlab.com/tozd/go/fun
```

It requires Go 1.23.6 or newer.

[Releases page](https://gitlab.com/tozd/go/fun/-/releases)
contains a list of stable versions of the `fun` tool.
Each includes:

- Statically compiled binaries.
- Docker images.

You should just download/use the latest one.

The tool is implemented in Go. You can also use `go install` to install the latest stable (released) version:

```sh
go install gitlab.com/tozd/go/fun/cmd/go/fun@latest
```

To install the latest development version (`main` branch):

```sh
go install gitlab.com/tozd/go/fun/cmd/go/fun@main
```

## Usage

### As a package

See full package documentation with examples on [pkg.go.dev](https://pkg.go.dev/gitlab.com/tozd/go/fun#section-documentation).

### `fun` tool

`fun` tool calls a function on files. You can provide:

- Examples of inputs and expected outputs as files (as pairs of files with same basename
  but different file extensions).
- Natural language description of the function, a prompt.
- Input files on which to run the function.
- Files with input and output JSON Schemas to validate inputs and outputs, respectively.

You have to provide example inputs and outputs or a prompt, and you can provide both.

`fun` has two sub-commands:

- `extract` supports extracting parts of one JSON into multiple files using
  [GJSON query](https://github.com/tidwall/gjson/blob/master/SYNTAX.md).
  Because `fun` calls the function on files this is useful to preprocess a large JSON
  file to create files to then call the function on.
  - The query should return an array of objects with ID and data fields
    (by default named `id` and `data`).
- `call` then calls the function on files in the input directory and writes results
  into files in the output directory.
  - Corresponding output files will have the same
    basename as input files but with the output file extension (configurable) so it is
    safe to use the same directory both for input and output files.
  - `fun` calls the function only for files which do not yet exist in the output directory
    so it is safe to run `fun` multiple times if previous run of `fun` had issues or was
    interrupted.
  - `fun` supports splitting input files into batches so one run of `fun` can operate
    only on a particular batch. Useful if you want to distribute execution across multiple
    machines.
  - If output fails to validate the JSON Schema, the output is stored into a file with
    additional suffix `.invalid`. If calling the function fails for some other reason,
    the error is stored into a file with additional suffix `.error`.
- `combine` combines multiple input directories into one output directory with only
  those files which are equal in all input directories.
  - Provided input directories should be outputs from different models or different
    configurations but all run on same input files.
  - This allows decreasing false positives at the expense of having less outputs overall.

For details on all CLI arguments possible, run `fun --help`:

```sh
fun --help
```

If you have Go available, you can run it without installation:

```sh
go run gitlab.com/tozd/go/fun/cmd/go/fun@latest --help
```

Or with Docker:

```sh
docker run -i registry.gitlab.com/tozd/go/fun/branch/main:latest --help
```

The above command runs the latest development version (`main` branch).
See [releases page](https://gitlab.com/tozd/go/fun/-/releases) for a Docker image for the latest stable version.

#### Example

If you have a [large JSON file](./testdata/exercises.json) with the following structure:

```yaml
{
  "exercises": [
    {
      "serial": 1,
      "text": "Ariel was playing basketball. 1 of her shots went in the hoop. 2 of the shots did not go in the hoop. How many shots were there in total?"
    },
    // ...
  ]
}
```

To create for each exercise a `.txt` file with filename based on the `serial` field
(e.g., `1.txt`) and contents based on the `text` field, in the `data` output directory,
you could run:

```sh
fun extract --input exercises.json --output data --out=.txt 'exercises.#.{id:serial,data:text}'
```

To solve all exercises, you can then run:

```sh
export ANTHROPIC_API_KEY='...'
echo "You MUST output only final number, nothing more." > prompt.txt
echo '{"model":"claude-3-haiku-20240307"}' > config.json
fun call --input data --output results --provider anthropic --config config.json --in .txt --out .txt --prompt prompt.txt
```

For the `data/1.txt` input file you should now get `results/1.txt` output file with contents `3`.

The issue is that sadly the function might sometimes output more than just the number.
We can detect those cases using [JSON Schema](https://json-schema.org/)
to validate outputs. We can use a JSON Schema to validate that the
[output is an integer](./testdata/number-schema.json). We will see warnings in cases when
outputs do not validate and corresponding output files will not be created.

```sh
echo '{"type": "integer"}' > schema.json
echo '{"model":"claude-3-haiku-20240307"}' > config.json
fun call --input data --output results --provider anthropic --config config.json --in .txt --out .txt --prompt prompt.txt --output-schema schema.json
```

We can also use a JSON Schema to validate that the
[output is a string matching a regex](./testdata/string-schema.json):

```sh
echo '{"type": "string", "pattern": "^[0-9]+$"}' > schema.json
echo '{"model":"claude-3-haiku-20240307"}' > config.json
fun call --input data --output results --provider anthropic --config config.json --in .txt --out .txt --prompt prompt.txt --output-schema schema.json
```

## Related projects

- [langchaingo](https://github.com/tmc/langchaingo) – Langchain exposes all different features of different models
  to you. But this package provides a higher-level abstraction. The idea is that everything looks like functions
  and that you can then compose them as necessary. Tools are just functions as well, so you can have tools which
  call into other LLMs. You can also implement functions which do not call into LLMs, e.g., to call into a REST API.
  Both can be used together. You can even have recursion.

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
