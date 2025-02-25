package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"golang.org/x/sync/errgroup"

	"gitlab.com/tozd/go/fun"
)

const (
	progressPrintRate = 30 * time.Second
)

var errFileSkipped = errors.Base("file skipped")

type errorAndCalls struct {
	Err   errors.Formatter       `json:"error"`
	Calls []fun.TextRecorderCall `json:"calls,omitempty"`
}

//nolint:lll
type CallCommand struct {
	InputDir         string               `                                                   help:"Path to input directory."                                                                    name:"input"         placeholder:"PATH" required:"" short:"i" type:"existingdir"`
	OutputDir        string               `                                                   help:"Path to output directory."                                                                   name:"output"        placeholder:"PATH" required:"" short:"o" type:"path"`
	DataDir          string               `                                                   help:"Path to data directory. It should contains pairs of files with inputs and expected outputs." name:"data"          placeholder:"PATH"             short:"d" type:"existingdir"`
	PromptPath       string               `                                                   help:"Path to a file with the prompt, a natural language description of the function."             name:"prompt"        placeholder:"PATH"             short:"P" type:"path"`
	InputExtension   string               `default:".in"                                      help:"File extension of an input file. Default: ${default}."                                       name:"in"            placeholder:"EXT"`
	OutputExtension  string               `default:".out"                                     help:"File extension of an output file. Default: ${default}."                                      name:"out"           placeholder:"EXT"`
	InputJSONSchema  kong.FileContentFlag `                                                   help:"Path to a file with JSON Schema to validate inputs."                                         name:"input-schema"  placeholder:"PATH"`
	OutputJSONSchema kong.FileContentFlag `                                                   help:"Path to a file with JSON Schema to validate outputs."                                        name:"output-schema" placeholder:"PATH"`
	Provider         string               `               enum:"ollama,groq,anthropic,openai" help:"AI model provider. Possible: ${enum}."                                                                                               required:"" short:"p"`
	Config           kong.FileContentFlag `                                                   help:"Path to a file with AI model configuration in JSON."                                                                                 required:"" short:"c"`
	Parallel         int                  `default:"1"                                        help:"How many input files to process in parallel. Default: ${default}."                                                placeholder:"INT"`
	Batches          int                  `default:"1"                                        help:"Split input files into batches. Default: ${default}."                                                             placeholder:"INT"              short:"B"`
	Batch            int                  `default:"0"                                        help:"Process only files in the batch with this 0-based index. Default: ${default}."                                    placeholder:"INT"              short:"b"`
}

func (c *CallCommand) Run(logger zerolog.Logger) errors.E { //nolint:maintidx
	// We stop the process gracefully on ctrl-c and TERM signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var model string
	var provider fun.TextProvider
	switch c.Provider {
	case "ollama":
		var p fun.OllamaTextProvider
		errE := x.UnmarshalWithoutUnknownFields(c.Config, &p)
		if errE != nil {
			return errE
		}
		if host := os.Getenv("OLLAMA_HOST"); host != "" {
			p.Base = host
		}
		provider = &p
		model = p.Model
	case "groq":
		var p fun.GroqTextProvider
		errE := x.UnmarshalWithoutUnknownFields(c.Config, &p)
		if errE != nil {
			return errE
		}
		if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
			p.APIKey = apiKey
		}
		provider = &p
		model = p.Model
	case "anthropic":
		var p fun.AnthropicTextProvider
		errE := x.UnmarshalWithoutUnknownFields(c.Config, &p)
		if errE != nil {
			return errE
		}
		if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
			p.APIKey = apiKey
		}
		provider = &p
		model = p.Model
	case "openai":
		var p fun.OpenAITextProvider
		errE := x.UnmarshalWithoutUnknownFields(c.Config, &p)
		if errE != nil {
			return errE
		}
		if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
			p.APIKey = apiKey
		}
		provider = &p
		model = p.Model
	}

	// TODO: We could use type:"filecontent" Kong's option on string field type instead?
	//       See: https://github.com/alecthomas/kong/issues/482
	prompt := ""
	if c.PromptPath != "" {
		promptData, err := os.ReadFile(c.PromptPath)
		if err != nil {
			return errors.WithStack(err)
		}
		prompt = string(promptData)
	}

	data := []fun.InputOutput[string, string]{}
	if c.DataDir != "" {
		files, err := filepath.Glob(filepath.Join(c.DataDir, "*"+c.InputExtension))
		if err != nil {
			return errors.WithStack(err)
		}
		slices.Sort(files)

		for _, inputPath := range files {
			outputPath := strings.TrimSuffix(inputPath, c.InputExtension) + c.OutputExtension
			inputData, err := os.ReadFile(inputPath)
			if err != nil {
				return errors.WithStack(err)
			}
			outputData, err := os.ReadFile(outputPath)
			if err != nil {
				return errors.WithStack(err)
			}
			data = append(data, fun.InputOutput[string, string]{
				Input:  []string{string(inputData)},
				Output: string(outputData),
			})
		}
	}

	fn := &fun.Text[string, string]{
		Provider:         provider,
		InputJSONSchema:  c.InputJSONSchema,
		OutputJSONSchema: c.OutputJSONSchema,
		Prompt:           prompt,
		Data:             data,
		Tools:            nil, // TODO: How to make it configurable?
	}

	errE := fn.Init(logger.WithContext(ctx))
	if errE != nil {
		return errE
	}

	files, err := filepath.Glob(filepath.Join(c.InputDir, "*"+c.InputExtension))
	if err != nil {
		return errors.WithStack(err)
	}
	slices.Sort(files)

	err = os.MkdirAll(c.OutputDir, 0o755) //nolint:mnd
	if err != nil {
		return errors.WithStack(err)
	}

	batch := files
	if c.Batches > 1 {
		if c.Batch < 0 || c.Batch >= c.Batches {
			errE = errors.New("invalid batch index")
			errors.Details(errE)["batch"] = c.Batch
			errors.Details(errE)["batches"] = c.Batches
			return errE
		}
		batchSize := int(math.Ceil(float64(len(files)) / float64(c.Batches)))
		batch = files[c.Batch*batchSize : min((c.Batch+1)*batchSize, len(files))]
	}

	logger.Info().Int("all", len(files)).Str("model", model).
		Str("provider", c.Provider).Int("parallel", c.Parallel).
		Int("batches", c.Batches).Int("batch", c.Batch).
		Int("inputs", len(batch)).
		Msg("running")

	count := x.Counter(0)
	failed := x.Counter(0)
	errored := x.Counter(0)
	invalid := x.Counter(0)
	skipped := x.Counter(0)
	done := x.Counter(0)
	ticker := x.NewTicker(ctx, &count, int64(len(files)), progressPrintRate)
	defer ticker.Stop()
	go func() {
		for p := range ticker.C {
			logger.Info().
				Int64("failed", failed.Count()).Int64("errored", errored.Count()).Int64("invalid", invalid.Count()).
				Int64("skipped", skipped.Count()).Int64("done", done.Count()).Int64("count", p.Count).
				Str("eta", p.Remaining().Truncate(time.Second).String()).Send()
		}
	}()

	filesChan := make(chan string, c.Parallel)
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer close(filesChan)
		for _, inputPath := range batch {
			select {
			case <-ctx.Done():
				// Context has been canceled.
				return errors.WithStack(ctx.Err())
			case filesChan <- inputPath:
			}
		}
		return nil
	})

	for range c.Parallel {
		g.Go(func() error {
			// Loop ends when filesChan is closed, which happens when context is cancelled, too.
			for inputPath := range filesChan {
				if ctx.Err() != nil {
					return errors.WithStack(ctx.Err())
				}

				relPath, err := filepath.Rel(c.InputDir, inputPath)
				if err != nil {
					return errors.WithStack(err)
				}
				outputPath := filepath.Join(c.OutputDir, strings.TrimSuffix(relPath, c.InputExtension)+c.OutputExtension)

				l := logger.With().Str("file", relPath).Logger()

				count.Increment()

				hasErrored, errE := c.processFile(l.WithContext(ctx), fn, inputPath, outputPath) //nolint:govet
				if errE != nil {
					if errors.Is(errE, context.Canceled) || errors.Is(errE, context.DeadlineExceeded) {
						return errE
					}
					if errors.Is(errE, errFileSkipped) {
						skipped.Increment()
						continue
					}
					l.Warn().Err(errE).Msg("error processing file")
					if hasErrored {
						errored.Increment()
					} else if errors.Is(errE, fun.ErrJSONSchemaValidation) {
						invalid.Increment()
					} else {
						failed.Increment()
					}
					continue
				}
				done.Increment()
			}
			return nil
		})
	}

	errE = errors.WithStack(g.Wait())
	logger.Info().Int64("failed", failed.Count()).Int64("errored", errored.Count()).Int64("invalid", invalid.Count()).
		Int64("skipped", skipped.Count()).Int64("done", done.Count()).Int64("count", count.Count()).Msg("done")
	return errE
}

func (c *CallCommand) processFile( //nolint:nonamedreturns
	ctx context.Context, fn fun.Callee[string, string], inputPath, outputPath string,
) (errored bool, errE errors.E) {
	// Was there an output error?
	var errorErrE errors.E
	// Is output invalid?
	var invalidErrE errors.E
	defer func() {
		// Add outputErrE or invalidErrE to any existing error. If all errors are nil, this is still nil.
		errE = errors.Join(errE, errorErrE, invalidErrE)
	}()
	defer func() {
		errored = errE == nil && errorErrE != nil && invalidErrE == nil
	}()

	f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:mnd
	if errors.Is(err, fs.ErrExist) {
		// We skip files which already exist.
		return false, errors.Prefix(err, errFileSkipped)
	} else if err != nil {
		return false, errors.WithStack(err)
	}
	defer func() {
		errE2 := errors.WithStack(f.Close())
		var errE3 errors.E
		// We always remove this file if the output was invalid or there was output error.
		// We remove it on any other error as well (so that run can be redone).
		if errE != nil || invalidErrE != nil || errorErrE != nil {
			// It is correct that we remove this file if we return errFileSkipped below. This means that .invalid
			// or .error file already exist and our temporary data file we managed to create should be removed.
			errE3 = errors.WithStack(os.Remove(outputPath))
			if errE3 != nil {
				zerolog.Ctx(ctx).Error().Err(errE3).Msg("unable to remove output file after error")
			}
		}
		// Combine any non-nil errors together.
		errE = errors.Join(errE, errE2, errE3)
	}()

	errorOutputPath := outputPath + ".error"
	fError, err := os.OpenFile(errorOutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:mnd
	if errors.Is(err, fs.ErrExist) {
		// We skip files which already exist.
		return false, errors.Prefix(err, errFileSkipped)
	} else if err != nil {
		return false, errors.WithStack(err)
	}
	defer func() {
		errE2 := errors.WithStack(fError.Close())
		var errE3 errors.E
		// We always remove this file if the output was valid or if the output was invalid.
		// We remove it on any other error as well (so that run can be redone).
		if errE != nil || (errorErrE == nil && invalidErrE == nil) || invalidErrE != nil {
			errE3 = errors.WithStack(os.Remove(errorOutputPath))
			if errE3 != nil {
				zerolog.Ctx(ctx).Error().Err(errE3).Msg("unable to remove output error file after error")
			}
		}
		// Combine any non-nil errors together.
		errE = errors.Join(errE, errE2, errE3)
	}()

	invalidOutputPath := outputPath + ".invalid"
	fInvalid, err := os.OpenFile(invalidOutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:mnd
	if errors.Is(err, fs.ErrExist) {
		// We skip files which already exist.
		return false, errors.Prefix(err, errFileSkipped)
	} else if err != nil {
		return false, errors.WithStack(err)
	}
	defer func() {
		errE2 := errors.WithStack(fInvalid.Close())
		var errE3 errors.E
		// We always remove this file if the output was valid or if there was an output error.
		// We remove it on any other error as well (so that run can be redone).
		if errE != nil || (errorErrE == nil && invalidErrE == nil) || errorErrE != nil {
			errE3 = errors.WithStack(os.Remove(invalidOutputPath))
			if errE3 != nil {
				zerolog.Ctx(ctx).Error().Err(errE3).Msg("unable to remove invalid output file after error")
			}
		}
		// Combine any non-nil errors together.
		errE = errors.Join(errE, errE2, errE3)
	}()

	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		return false, errors.WithStack(err)
	}

	ctx = fun.WithTextRecorder(ctx)
	defer func() {
		e := zerolog.Ctx(ctx).Debug()
		if e.Enabled() {
			calls := fun.GetTextRecorder(ctx).Calls()
			if len(calls) > 0 {
				e.Interface("calls", calls).Msg("call")
			}
		}
	}()

	output, errE := fn.Call(ctx, string(inputData))
	if errors.Is(errE, context.Canceled) || errors.Is(errE, context.DeadlineExceeded) {
		return false, errE
	} else if errors.Is(errE, fun.ErrJSONSchemaValidation) {
		invalidErrE, errE = errE, nil
		_, err = fInvalid.WriteString(output)
		return false, errors.WithStack(err)
	} else if errE != nil {
		if output != "" {
			errors.Details(errE)["output"] = output
		}
		errorErrE, errE = errE, nil
		errJSON, errE := x.MarshalWithoutEscapeHTML(errorAndCalls{
			Err:   errors.Formatter{Error: errorErrE},
			Calls: fun.GetTextRecorder(ctx).Calls(),
		})
		if errE != nil {
			return false, errE
		}
		out := new(bytes.Buffer)
		err = json.Indent(out, errJSON, "", "  ")
		if err != nil {
			return false, errors.WithStack(err)
		}
		_, err = out.WriteTo(fError)
		return false, errors.WithStack(err)
	}

	_, err = f.WriteString(output)
	return false, errors.WithStack(err)
}
