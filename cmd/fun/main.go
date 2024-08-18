package main

import (
	"github.com/alecthomas/kong"
	"gitlab.com/tozd/go/cli"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/zerolog"
)

type App struct {
	zerolog.LoggingConfig

	Version kong.VersionFlag `help:"Show program's version and exit." short:"V" yaml:"-"`

	Extract ExtractCommand `cmd:"" help:"Extract data from JSON into files."`
	Call    CallCommand    `cmd:"" help:"Call function on files defined with data and/or natural language description."`
	Combine CombineCommand `cmd:"" help:"Combine multiple input directories into one output directory."`
}

func main() {
	var app App
	cli.Run(&app, kong.Vars{}, func(ctx *kong.Context) errors.E {
		return errors.WithStack(ctx.Run(app.Logger))
	})
}
