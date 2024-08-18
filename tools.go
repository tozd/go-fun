package fun

import (
	"context"
	"encoding/json"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

// Tooler extends [Callee] interface with additional methods needed to
// define a tool which can be called by AI models through [TextProvider].
type Tooler interface {
	Callee[json.RawMessage, string]

	// GetDescription returns a natural language description of the tool which helps
	// an AI model understand when to use this tool.
	GetDescription() string

	// GetInputJSONSchema return the JSON Schema for parameters passed by an AI model
	// to the tool. Consider using meaningful property names and use "description"
	// JSON Schema field to describe to the AI model what each property is.
	// Depending on the provider and the model there are limitations on the JSON Schema
	// (e.g., only "object" top-level type can be used, all properties must be required,
	// "additionalProperties" must be set to false).
	GetInputJSONSchema() []byte
}

type Tool[Input, Output any] struct {
	// Description is a natural language description of the tool which helps
	// an AI model understand when to use this tool.
	Description string

	// InputJSONSchema is the JSON Schema for parameters passed by an AI model
	// to the tool. Consider using meaningful property names and use "description"
	// JSON Schema field to describe to the AI model what each property is.
	// Depending on the provider and the model there are limitations on the JSON Schema
	// (e.g., only "object" top-level type can be used, all properties must be required,
	// "additionalProperties" must be set to false).
	//
	// It should correspond to the Input type parameter.
	InputJSONSchema []byte

	// InputJSONSchema is the JSON Schema for tool's output. It is used to validate
	// the output from the tool before it is passed on to the AI model.
	//
	// It should correspond to the Output type parameter.
	OutputJSONSchema []byte

	// Fun implements the logic of the tool.
	Fun func(ctx context.Context, input Input) (Output, errors.E)

	inputValidator  *jsonschema.Schema
	outputValidator *jsonschema.Schema
}

var _ Tooler = (*Tool[any, any])(nil)

// Init implements [Callee] interface.
func (t *Tool[Input, Output]) Init(_ context.Context) errors.E {
	if t.inputValidator != nil {
		return errors.WithStack(ErrAlreadyInitialized)
	}

	validator, schema, errE := compileValidator[Input](t.InputJSONSchema)
	if errE != nil {
		return errE
	}
	t.inputValidator = validator
	if t.InputJSONSchema == nil {
		t.InputJSONSchema = schema
	}

	validator, schema, errE = compileValidator[Output](t.OutputJSONSchema)
	if errE != nil {
		return errE
	}
	t.outputValidator = validator
	if t.OutputJSONSchema == nil {
		t.OutputJSONSchema = schema
	}

	return nil
}

// Call takes the raw JSON input from an AI model and converts it a value of
// Input type, calls Fun, and converts the output to the string to be passed
// back to the AI model as result of the tool call.
//
// Call also validates that inputs and outputs match respective JSON Schemas.
//
// Call implements [Callee] interface.
func (t *Tool[Input, Output]) Call(ctx context.Context, input ...json.RawMessage) (string, errors.E) {
	if len(input) != 1 {
		return "", errors.New("invalid number of inputs")
	}

	errE := validateJSON(t.inputValidator, input[0])
	if errE != nil {
		return "", errE
	}

	var i Input
	errE = x.UnmarshalWithoutUnknownFields(input[0], &i)
	if errE != nil {
		return "", errE
	}

	output, errE := t.Fun(ctx, i)
	if errE != nil {
		return "", errE
	}

	errE = validate(t.outputValidator, output)
	if errE != nil {
		return "", errE
	}

	return toOutputString(output)
}

// Variadic implements [Callee] interface.
func (t *Tool[Input, Output]) Variadic() func(ctx context.Context, input ...json.RawMessage) (string, errors.E) {
	return func(ctx context.Context, input ...json.RawMessage) (string, errors.E) {
		return t.Call(ctx, input...)
	}
}

// Unary implements [Callee] interface.
func (t *Tool[Input, Output]) Unary() func(ctx context.Context, input json.RawMessage) (string, errors.E) {
	return func(ctx context.Context, input json.RawMessage) (string, errors.E) {
		return t.Call(ctx, input)
	}
}

// GetDescription implements [Tooler] interface.
func (t *Tool[Input, Output]) GetDescription() string {
	return t.Description
}

// GetInputJSONSchema implements [Tooler] interface.
func (t *Tool[Input, Output]) GetInputJSONSchema() []byte {
	return t.InputJSONSchema
}
