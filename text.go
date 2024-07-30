package fun

import (
	"bytes"
	"context"

	jsonschemaGen "github.com/invopop/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

var _ Callee[any, any] = (*Text[any, any])(nil)

const (
	StringToJSONStructurePrompt = `Be a parser of input strings into JSON. Match the structure of examples. Do not make up new JSON fields and do not add data not found in the input string. Keep data in original language and letter case. Use your knowledge to resolve ambiguousness. Output only JSON.`
	StringToJSONPrompt          = `Output only JSON.`
)

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

// Text implements Callee interface with its logic defined by given training inputs and
// outputs and a TextProvider.
type Text[Input, Output any] struct {
	Provider TextProvider

	InputJSONSchema  []byte
	OutputJSONSchema []byte

	Prompt string
	Data   []InputOutput[Input, Output]

	inputValidator  *jsonschema.Schema
	outputValidator *jsonschema.Schema
}

func (t *Text[Input, Output]) Init(ctx context.Context) errors.E {
	validator, errE := compileValidator[Input](t.InputJSONSchema)
	if errE != nil {
		return errE
	}
	t.inputValidator = validator

	validator, errE = compileValidator[Output](t.OutputJSONSchema)
	if errE != nil {
		return errE
	}
	t.outputValidator = validator

	messages := []ChatMessage{}
	if t.Prompt != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: t.Prompt,
		})
	}

	for _, data := range t.Data {
		errE := validate(t.inputValidator, data.Input)
		if errE != nil {
			return errE
		}
		input, errE := toString(data.Input)
		if errE != nil {
			return errE
		}

		errE = validate(t.outputValidator, data.Output)
		if errE != nil {
			return errE
		}
		output, errE := toString(data.Output)
		if errE != nil {
			return errE
		}

		messages = append(messages, ChatMessage{
			Role:    "user",
			Content: input,
		})
		messages = append(messages, ChatMessage{
			Role:    "assistant",
			Content: output,
		})
	}

	if len(messages) == 0 {
		return errors.New("prompt and training data are missing, at least one of them has to be provided")
	}

	errE = t.Provider.Init(ctx, messages)
	if errE != nil {
		return errE
	}

	return nil
}

func (t *Text[Input, Output]) Call(ctx context.Context, input Input) (Output, errors.E) {
	errE := validate(t.inputValidator, input)
	if errE != nil {
		return *new(Output), errE
	}

	i, errE := toString(input)
	if errE != nil {
		return *new(Output), errE
	}

	content, errE := t.Provider.Chat(ctx, ChatMessage{
		Role:    "user",
		Content: i,
	})
	if errE != nil {
		return *new(Output), errE
	}

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

	errE = validate(t.outputValidator, output)
	if errE != nil {
		return output, errE
	}

	return output, nil
}
