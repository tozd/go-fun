package fun

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/hashicorp/go-cleanhttp"
	jsonschemaGen "github.com/invopop/jsonschema"
	"github.com/ollama/ollama/api"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

const StringToJSONPrompt = `Be a parser of input strings into JSON. Match the structure of examples. Do not make up new JSON fields and do not add data not found in the input string. Keep data in original language and letter case. Use your knowledge to resolve ambiguousness. Output only JSON.`

var _ Callee[any, any] = (*Ollama[any, any])(nil)

func compileValidator[T any](jsonSchema []byte) (*jsonschema.Schema, errors.E) {
	if jsonSchema == nil {
		// TODO: Use type assertion on type parameter.
		//       See: https://github.com/golang/go/issues/45380
		//       See: https://github.com/golang/go/issues/49206
		dummy := new(T)
		switch any(*dummy).(type) {
		case string:
			// Nothing. One can provide InputJSONSchema if they want to validate input strings.
			return nil, nil
		default:
			// We construct JSON Schema from Go struct.
			schema := jsonschemaGen.Reflect(dummy)
			js, errE := x.MarshalWithoutEscapeHTML(schema)
			if errE != nil {
				return nil, errE
			}
			jsonSchema = js
		}
	}

	schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonSchema))
	if err != nil {
		return nil, errors.WithStack(err)
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	err = c.AddResource("schema.json", schema)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	validator, err := c.Compile("schema.json")
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return validator, nil
}

func validate(validator *jsonschema.Schema, value any) errors.E {
	if validator == nil {
		return nil
	}

	data, errE := x.MarshalWithoutEscapeHTML(value)
	if errE != nil {
		return errE
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return errors.WithStack(err)
	}
	err = validator.Validate(v)
	return errors.WithStack(err)
}

func toString(data any) (string, errors.E) {
	i, ok := data.(string)
	if ok {
		return i, nil
	}

	j, errE := x.MarshalWithoutEscapeHTML(data)
	if errE != nil {
		return "", errE
	}

	return string(j), nil
}

type InputOutput[Input, Output any] struct {
	Input  Input
	Output Output
}

type OllamaModel struct {
	Model    string
	Insecure bool
	Username string
	Password string
}

// Ollama implements Callee interface with its logic defined by given training inputs and outputs.
type Ollama[Input, Output any] struct {
	Client *http.Client
	Base   string
	Model  OllamaModel

	InputJSONSchema  []byte
	OutputJSONSchema []byte

	Prompt string
	Data   []InputOutput[Input, Output]

	Seed        int
	Temperature float64

	client           *api.Client
	inputValidator   *jsonschema.Schema
	outputValidator  *jsonschema.Schema
	messages         []api.Message
	maxContextLength int
}

func (o *Ollama[Input, Output]) Init(ctx context.Context) errors.E {
	if o.client != nil {
		return errors.New("already initialized")
	}

	base, err := url.Parse(o.Base)
	if err != nil {
		return errors.WithStack(err)
	}
	client := o.Client
	if client == nil {
		client = cleanhttp.DefaultPooledClient()
	}
	o.client = api.NewClient(base, client)

	validator, errE := compileValidator[Input](o.InputJSONSchema)
	if errE != nil {
		return errE
	}
	o.inputValidator = validator

	validator, errE = compileValidator[Output](o.OutputJSONSchema)
	if errE != nil {
		return errE
	}
	o.outputValidator = validator

	o.messages = []api.Message{}
	if o.Prompt != "" {
		o.messages = append(o.messages, api.Message{
			Role:    "system",
			Content: o.Prompt,
		})
	}

	for _, data := range o.Data {
		errE := validate(o.inputValidator, data.Input)
		if errE != nil {
			return errE
		}
		input, errE := toString(data.Input)
		if errE != nil {
			return errE
		}

		errE = validate(o.outputValidator, data.Output)
		if errE != nil {
			return errE
		}
		output, errE := toString(data.Output)
		if errE != nil {
			return errE
		}

		o.messages = append(o.messages, api.Message{
			Role:    "user",
			Content: input,
		})
		o.messages = append(o.messages, api.Message{
			Role:    "assistant",
			Content: output,
		})
	}

	if len(o.messages) == 0 {
		return errors.New("prompt and training data are missing, at least one of them has to be provided")
	}

	stream := false
	err = o.client.Pull(ctx, &api.PullRequest{
		Model:    o.Model.Model,
		Insecure: o.Model.Insecure,
		Username: o.Model.Username,
		Password: o.Model.Password,
		Stream:   &stream,
	}, func(pr api.ProgressResponse) error { return nil })
	if err != nil {
		return errors.WithStack(err)
	}

	resp, err := o.client.Show(ctx, &api.ShowRequest{
		Model: o.Model.Model,
	})
	if err != nil {
		return errors.WithStack(err)
	}

	arch := resp.ModelInfo["general.architecture"].(string)
	contextLength := int(resp.ModelInfo[fmt.Sprintf("%s.context_length", arch)].(float64))

	if contextLength == 0 {
		return errors.New("unable to determine max context length")
	}
	o.maxContextLength = contextLength

	return nil
}

func (o *Ollama[Input, Output]) Call(ctx context.Context, input Input) (Output, errors.E) {
	errE := validate(o.inputValidator, input)
	if errE != nil {
		return *new(Output), errE
	}

	i, errE := toString(input)
	if errE != nil {
		return *new(Output), errE
	}

	messages := slices.Clone(o.messages)
	messages = append(messages, api.Message{
		Role:    "user",
		Content: i,
	})

	responses := []api.ChatResponse{}

	stream := false
	err := o.client.Chat(ctx, &api.ChatRequest{
		Model:    o.Model.Model,
		Messages: messages,
		Stream:   &stream,
		Options: map[string]interface{}{
			"num_ctx":     o.maxContextLength,
			"num_predict": o.maxContextLength,
			"seed":        o.Seed,
			"temperature": o.Temperature,
		},
	}, func(resp api.ChatResponse) error {
		responses = append(responses, resp)
		return nil
	})
	if err != nil {
		return *new(Output), errors.WithStack(err)
	}

	if len(responses) != 1 {
		return *new(Output), errors.New("unexpected number of responses")
	}
	if !responses[0].Done {
		return *new(Output), errors.New("not done")
	}

	if responses[0].Metrics.EvalCount >= o.maxContextLength {
		return *new(Output), errors.New("response hit max context length")
	}
	if responses[0].Metrics.PromptEvalCount >= o.maxContextLength {
		return *new(Output), errors.New("prompt hit max context length")
	}

	// TODO: Log/expose responses[0].Metrics.

	content := responses[0].Message.Content

	var output Output

	// TODO: Use type assertion on type parameter.
	//       See: https://github.com/golang/go/issues/45380
	//       See: https://github.com/golang/go/issues/49206
	switch any(output).(type) {
	case string:
		output = any(content).(Output)
	default:
		errE := x.UnmarshalWithoutUnknownFields([]byte(content), &output)
		if errE != nil {
			return output, errE
		}
	}

	errE = validate(o.outputValidator, output)
	if errE != nil {
		return output, errE
	}

	return output, nil
}
