package fun

import (
	"context"
	"encoding/json"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/x"
)

type Tooler interface {
	Callee[json.RawMessage, string]

	GetDescription() string
	GetInputJSONSchema() []byte
}

type Tool[Input, Output any] struct {
	Description      string
	InputJSONSchema  []byte
	OutputJSONSchema []byte
	Fun              func(ctx context.Context, input Input) (Output, errors.E)

	inputValidator  *jsonschema.Schema
	outputValidator *jsonschema.Schema
}

var _ Tooler = (*Tool[any, any])(nil)

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

func (t *Tool[Input, Output]) GetDescription() string {
	return t.Description
}

func (t *Tool[Input, Output]) GetInputJSONSchema() []byte {
	return t.InputJSONSchema
}
