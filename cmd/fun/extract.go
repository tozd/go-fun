package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/tidwall/gjson"
	"gitlab.com/tozd/go/errors"
)

func init() { //nolint:gochecknoinits
	gjson.DisableEscapeHTML = true
}

//nolint:lll
type ExtractCommand struct {
	InputFile       *os.File `                      help:"Path to input JSON file."                                                                   name:"input"  placeholder:"PATH"   required:"" short:"i"`
	OutputDir       string   `                      help:"Path to output directory."                                                                  name:"output" placeholder:"PATH"   required:"" short:"o" type:"path"`
	OutputExtension string   `                      help:"File extension of an output file."                                                          name:"out"    placeholder:"EXT"`
	IDField         string   `       default:"id"   help:"Name of the field used for ID in results from the GJSON query."                             name:"id"     placeholder:"STRING"`
	DataField       string   `       default:"data" help:"Name of the field used for data in results from the GJSON query."                           name:"data"   placeholder:"STRING"`
	Query           string   `arg:""                help:"GJSON query to extract data. It should return an array of objects with ID and data fields."`
}

func (c *ExtractCommand) Run(_ zerolog.Logger) errors.E {
	err := os.MkdirAll(c.OutputDir, 0o755) //nolint:mnd,gosec
	if err != nil {
		return errors.WithStack(err)
	}

	data, err := io.ReadAll(c.InputFile)
	if err != nil {
		return errors.WithStack(err)
	}

	result := gjson.GetBytes(data, c.Query)
	if !result.IsArray() {
		return errors.New("results from the GJSON query are not an array")
	}

	var errE errors.E
	result.ForEach(func(_, value gjson.Result) bool {
		id := value.Get(c.IDField).String()
		if id == "" {
			errE = errors.New("ID field is empty")
			return false
		}
		data := value.Get(c.DataField).String()

		errE = writeFile(filepath.Join(c.OutputDir, id+c.OutputExtension), data)
		return errE == nil
	})

	return errE
}
