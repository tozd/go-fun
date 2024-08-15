package main

import (
	"context"
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
	defaultSeed       = 42
	progressPrintRate = 30 * time.Second
)

var errFileSkipped = errors.Base("file skipped")

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
	Model            string               `                                                   help:"AI model to use."                                                                                                                    required:"" short:"m"`
	Parallel         int                  `default:"1"                                        help:"How many input files to process in parallel. Default: ${default}."                                                placeholder:"INT"`
	Batches          int                  `default:"1"                                        help:"Split input files into batches. Default: ${default}."                                                             placeholder:"INT"              short:"B"`
	Batch            int                  `default:"0"                                        help:"Process only files in the batch with this 0-based index. Default: ${default}."                                    placeholder:"INT"              short:"b"`
}

func (c *CallCommand) Run(logger zerolog.Logger) errors.E { //nolint:maintidx
	// We stop the process gracefully on ctrl-c and TERM signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var provider fun.TextProvider
	switch c.Provider {
	case "ollama":
		if os.Getenv("OLLAMA_HOST") == "" {
			return errors.New("OLLAMA_HOST environment variable is missing")
		}
		provider = &fun.OllamaTextProvider{
			Client: nil,
			Base:   os.Getenv("OLLAMA_HOST"),
			Model: fun.OllamaModel{
				Model:    c.Model,
				Insecure: false,
				Username: "",
				Password: "",
			},
			MaxContextLength:  0,
			MaxResponseLength: 0,
			Seed:              defaultSeed,
			Temperature:       0,
		}
	case "groq":
		if os.Getenv("GROQ_API_KEY") == "" {
			return errors.New("GROQ_API_KEY environment variable is missing")
		}
		provider = &fun.GroqTextProvider{
			Client:            nil,
			APIKey:            os.Getenv("GROQ_API_KEY"),
			Model:             c.Model,
			MaxContextLength:  0,
			MaxResponseLength: 0,
			Tools:             nil, // TODO: How to make it configurable?
			Seed:              defaultSeed,
			Temperature:       0,
		}
	case "anthropic":
		if os.Getenv("ANTHROPIC_API_KEY") == "" {
			return errors.New("ANTHROPIC_API_KEY environment variable is missing")
		}
		provider = &fun.AnthropicTextProvider{
			Client:      nil,
			APIKey:      os.Getenv("ANTHROPIC_API_KEY"),
			Model:       c.Model,
			Tools:       nil, // TODO: How to make it configurable?
			Temperature: 0,
		}
	case "openai":
		if os.Getenv("OPENAI_API_KEY") == "" {
			return errors.New("OPENAI_API_KEY environment variable is missing")
		}
		provider = &fun.OpenAITextProvider{
			Client:                nil,
			APIKey:                os.Getenv("OPENAI_API_KEY"),
			Model:                 c.Model,
			MaxContextLength:      0,     // TODO: How to make it configurable?
			MaxResponseLength:     0,     // TODO: How to make it configurable?
			Tools:                 nil,   // TODO: How to make it configurable?
			ForceOutputJSONSchema: false, // TODO: How to make it configurable?
			Seed:                  defaultSeed,
			Temperature:           0,
		}
	}

	// TODO: We could use type:"filecontent" Kong's option on string field type instead?
	//       See: https://github.com/alecthomas/kong/issues/346#issuecomment-2266381258
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

	err = os.MkdirAll(c.OutputDir, 0o755) //nolint:gomnd
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

	logger.Info().Int("all", len(files)).Str("model", c.Model).
		Str("provider", c.Provider).Int("parallel", c.Parallel).
		Int("batches", c.Batches).Int("batch", c.Batch).
		Int("inputs", len(batch)).
		Msg("running")

	count := x.Counter(0)
	failed := x.Counter(0)
	invalid := x.Counter(0)
	skipped := x.Counter(0)
	done := x.Counter(0)
	ticker := x.NewTicker(ctx, &count, int64(len(files)), progressPrintRate)
	defer ticker.Stop()
	go func() {
		for p := range ticker.C {
			logger.Info().
				Int64("failed", failed.Count()).Int64("invalid", invalid.Count()).Int64("skipped", skipped.Count()).
				Int64("done", done.Count()).Int64("count", p.Count).Str("eta", p.Remaining().Truncate(time.Second).String()).
				Send()
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

	for i := 0; i < c.Parallel; i++ {
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

				errE := c.processFile(l.WithContext(ctx), fn, inputPath, outputPath) //nolint:govet
				if errE != nil {
					if errors.Is(errE, context.Canceled) || errors.Is(errE, context.DeadlineExceeded) {
						return errE
					}
					if errors.Is(errE, errFileSkipped) {
						skipped.Increment()
						continue
					}
					l.Warn().Err(errE).Msg("error processing file")
					if errors.Is(errE, fun.ErrJSONSchemaValidation) {
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
	logger.Info().Int64("failed", failed.Count()).Int64("invalid", invalid.Count()).Int64("skipped", skipped.Count()).
		Int64("done", done.Count()).Int64("count", count.Count()).Msg("done")
	return errE
}

func (c *CallCommand) processFile(ctx context.Context, fn fun.Callee[string, string], inputPath, outputPath string) (errE errors.E) { //nolint:nonamedreturns
	// Is output invalid?
	var invalidErrE errors.E
	defer func() {
		// Add invalidErrE to any existing error. If both errors are nil, this is still nil.
		errE = errors.Join(errE, invalidErrE)
	}()

	f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gomnd
	if errors.Is(err, fs.ErrExist) {
		// We skip files which already exist.
		return errors.Prefix(err, errFileSkipped)
	} else if err != nil {
		return errors.WithStack(err)
	}
	defer func() {
		errE2 := errors.WithStack(f.Close())
		var errE3 errors.E
		// We always remove this file if the output was invalid. We remove it on any other error as well (so that run can be redone).
		if errE != nil || invalidErrE != nil {
			// It is correct that we remove this file if we return errFileSkipped below. This means that .invalid
			// file already exist and our temporary data file we managed to create should be removed.
			errE3 = errors.WithStack(os.Remove(outputPath))
			if errE3 != nil {
				zerolog.Ctx(ctx).Error().Err(errE3).Msg("unable to remove data output file after error")
			}
		}
		// Combine any non-nil errors together.
		errE = errors.Join(errE, errE2, errE3)
	}()

	invalidOutputPath := outputPath + ".invalid"
	fInvalid, err := os.OpenFile(invalidOutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gomnd
	if errors.Is(err, fs.ErrExist) {
		// We skip files which already exist.
		return errors.Prefix(err, errFileSkipped)
	} else if err != nil {
		return errors.WithStack(err)
	}
	defer func() {
		errE2 := errors.WithStack(fInvalid.Close())
		var errE3 errors.E
		// We always remove this file if the output was valid. We remove it on any other error as well (so that run can be redone).
		if errE != nil || invalidErrE == nil {
			errE3 = errors.WithStack(os.Remove(invalidOutputPath))
			if errE3 != nil {
				zerolog.Ctx(ctx).Error().Err(errE3).Msg("unable to remove invalid flag output file after error")
			}
		}
		// Combine any non-nil errors together.
		errE = errors.Join(errE, errE2, errE3)
	}()

	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		return errors.WithStack(err)
	}

	ctx = fun.WithTextProviderRecorder(ctx)
	defer func() {
		e := zerolog.Ctx(ctx).Debug() //nolint:zerologlint
		if e.Enabled() {
			recorder := fun.GetTextProviderRecorder(ctx)
			hasData := false
			messages := recorder.Messages()
			if len(messages) > 0 {
				e = e.Interface("messages", messages)
				hasData = true
			}
			usedTime := recorder.UsedTime()
			if len(usedTime) > 0 {
				e = e.Interface("usedTime", usedTime)
				hasData = true
			}
			usedTokens := recorder.UsedTokens()
			if len(usedTokens) > 0 {
				e = e.Interface("usedTokens", usedTokens)
				hasData = true
			}
			if hasData {
				e.Str("model", c.Model).Str("provider", c.Provider).Msg("call")
			}
		}
	}()

	output, errE := fn.Call(ctx, string(inputData))
	if errors.Is(errE, fun.ErrJSONSchemaValidation) {
		invalidErrE, errE = errE, nil
		_, err = fInvalid.WriteString(output)
		return errors.WithStack(err)
	} else if errE != nil {
		if output != "" {
			errors.Details(errE)["output"] = output
		}
		return errE
	}

	_, err = f.WriteString(output)
	return errors.WithStack(err)
}
