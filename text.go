package fun

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	jsonschemaGen "github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
	"gitlab.com/tozd/identifier"
)

var _ Callee[any, any] = (*Text[any, any])(nil)

const (
	// Prompt to parse input string into the target struct.
	TextParserToJSONPrompt = `Be a parser of input strings into JSON. Match the structure of examples. Do not make up new JSON fields and do not add data not found in the input string. Keep data in original language and letter case. Use your knowledge to resolve ambiguousness. Output only JSON.` //nolint:lll

	// Prompt to request only JSON output, which is then converted into the target struct.
	TextToJSONPrompt = `Output only JSON.`
)

const (
	roleAssistant  = "assistant"
	roleUser       = "user"
	roleTool       = "tool"
	roleToolUse    = "tool_use"
	roleToolResult = "tool_result"

	typeText   = "text"
	stopReason = "stop"
)

func compileValidator[T any](jsonSchema []byte) (*jsonschema.Schema, []byte, errors.E) {
	if jsonSchema == nil {
		// We construct JSON Schema from Go value.
		schema := jsonschemaGen.Reflect(new(T))
		js, errE := x.MarshalWithoutEscapeHTML(schema)
		if errE != nil {
			return nil, nil, errE
		}
		jsonSchema = js
	}

	schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonSchema))
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	err = c.AddResource("schema.json", schema)
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}
	validator, err := c.Compile("schema.json")
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	return validator, jsonSchema, nil
}

func validateJSON(validator *jsonschema.Schema, data json.RawMessage) errors.E {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return errors.WithStack(err)
	}
	err = validator.Validate(v)
	if err != nil {
		errE := errors.Prefix(err, ErrJSONSchemaValidation)
		errors.Details(errE)["data"] = data
		return errE
	}
	return nil
}

func validate(validator *jsonschema.Schema, value any) errors.E {
	if validator == nil {
		return nil
	}

	var data []byte
	var errE errors.E
	// If type is a string, we want to support when string is a valid JSON that one can validate it as-is. At the same time we want to support using
	// JSON Schema to validate strings themselves (in which case we first have to marshal the string into JSON string with quotes). The issue with this
	// approach is if a) value is of string type b) user has a JSON Schema expecting non-string to validate JSON as string c) string is expected to be
	// JSON to validate, but d) input is not valid JSON. In that case instead of failing at UnmarshalJSON call below, we will marshal it into the JSON
	// string and then probably JSON Schema will fail to validate it. Hopefully we are not too smart here and this heuristic will work out well in practice.
	if v, ok := value.(string); ok && !slices.Equal(validator.Types.ToStrings(), []string{"string"}) && json.Valid([]byte(v)) {
		data = []byte(v)
	} else {
		data, errE = x.MarshalWithoutEscapeHTML(value)
		if errE != nil {
			return errE
		}
	}

	return validateJSON(validator, data)
}

func toInputString[T any](data []T) (string, errors.E) {
	if len(data) == 1 {
		// TODO: Use type assertion on type parameter.
		//       See: https://github.com/golang/go/issues/45380
		//       See: https://github.com/golang/go/issues/49206
		i, ok := any(data[0]).(string)
		if ok {
			return i, nil
		}
	}

	j, errE := x.MarshalWithoutEscapeHTML(data)
	if errE != nil {
		return "", errE
	}

	return string(j), nil
}

func toOutputString(data any) (string, errors.E) {
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

// InputOutput describes one example (variadic) input with corresponding output.
type InputOutput[Input, Output any] struct {
	Input  []Input
	Output Output
}

// Text implements [Callee] interface with its logic defined by given example data inputs
// and outputs, or a natural language description, or both.
//
// It uses a text-based AI model provided by a [TextProvider].
//
// For non-string Input types, it marshals them to JSON before
// providing them to the AI model, and for non-string Output types,
// it unmarshals model outputs from JSON to Output type.
// For this to work, Input and Output types should have a
// JSON representation.
type Text[Input, Output any] struct {
	// Provider is a text-based AI model.
	Provider TextProvider

	// InputJSONSchema is a JSON Schema to validate inputs against.
	// If not provided it is automatically generated from the Input type.
	InputJSONSchema []byte

	// OutputJSONSchema is a JSON Schema to validate outputs against.
	// If not provided it is automatically generated from the Output type.
	OutputJSONSchema []byte

	// Prompt is a natural language description of the logic.
	Prompt string

	// Data is example inputs with corresponding outputs for the function.
	Data []InputOutput[Input, Output]

	// Tools can be called by the AI model.
	Tools map[string]Tooler

	inputValidator  *jsonschema.Schema
	outputValidator *jsonschema.Schema
}

// Init implements [Callee] interface.
func (t *Text[Input, Output]) Init(ctx context.Context) errors.E {
	if t.inputValidator != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}

	callID := identifier.New().String()
	if recorder := GetTextProviderRecorder(ctx); recorder != nil {
		recorder.pushCall(callID)
		defer recorder.popCall()
	}
	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	validator, inputSchema, errE := compileValidator[Input](t.InputJSONSchema)
	if errE != nil {
		return errE
	}
	t.inputValidator = validator
	if t.InputJSONSchema == nil {
		t.InputJSONSchema = inputSchema
	}

	validator, outputSchema, errE := compileValidator[Output](t.OutputJSONSchema)
	if errE != nil {
		return errE
	}
	t.outputValidator = validator
	if t.OutputJSONSchema == nil {
		t.OutputJSONSchema = outputSchema
	}

	messages := []ChatMessage{}
	if t.Prompt != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: t.Prompt,
		})
	}

	for _, data := range t.Data {
		for _, i := range data.Input {
			errE = validate(t.inputValidator, i)
			if errE != nil {
				return errE
			}
		}
		input, errE := toInputString(data.Input) //nolint:govet
		if errE != nil {
			return errE
		}

		errE = validate(t.outputValidator, data.Output)
		if errE != nil {
			return errE
		}
		output, errE := toOutputString(data.Output)
		if errE != nil {
			return errE
		}

		messages = append(messages, ChatMessage{
			Role:    roleUser,
			Content: input,
		})
		messages = append(messages, ChatMessage{
			Role:    roleAssistant,
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

	if p, ok := t.Provider.(WithOutputJSONSchema); ok {
		errE = p.InitOutputJSONSchema(ctx, outputSchema)
		if errE != nil {
			return errE
		}
	}

	if len(t.Tools) > 0 {
		if p, ok := t.Provider.(WithTools); ok {
			errE = p.InitTools(ctx, t.Tools)
			if errE != nil {
				return errE
			}
		} else {
			return errors.New("provider does not support tools, but tools are provided")
		}
	}

	return nil
}

// Call implements [Callee] interface.
func (t *Text[Input, Output]) Call(ctx context.Context, input ...Input) (Output, errors.E) { //nolint:ireturn
	callID := identifier.New().String()
	if recorder := GetTextProviderRecorder(ctx); recorder != nil {
		recorder.pushCall(callID)
		defer recorder.popCall()
	}
	logger := zerolog.Ctx(ctx).With().Str("fun", callID).Logger()
	ctx = logger.WithContext(ctx)

	for _, i := range input {
		errE := validate(t.inputValidator, i)
		if errE != nil {
			return *new(Output), errE
		}
	}

	i, errE := toInputString(input)
	if errE != nil {
		return *new(Output), errE
	}

	content, errE := t.Provider.Chat(ctx, ChatMessage{
		Role:    roleUser,
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
		output = any(content).(Output) //nolint:errcheck,forcetypeassert
	default:
		errE = x.UnmarshalWithoutUnknownFields([]byte(content), &output)
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

// Variadic implements [Callee] interface.
func (t *Text[Input, Output]) Variadic() func(ctx context.Context, input ...Input) (Output, errors.E) {
	return func(ctx context.Context, input ...Input) (Output, errors.E) {
		return t.Call(ctx, input...)
	}
}

// Unary implements [Callee] interface.
func (t *Text[Input, Output]) Unary() func(ctx context.Context, input Input) (Output, errors.E) {
	return func(ctx context.Context, input Input) (Output, errors.E) {
		return t.Call(ctx, input)
	}
}
