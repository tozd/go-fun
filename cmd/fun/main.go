package main

import (
	"github.com/alecthomas/kong"
	"gitlab.com/tozd/go/cli"
	"gitlab.com/tozd/go/errors"
	"gitlab.com/tozd/go/zerolog"
)

type App struct {
	zerolog.LoggingConfig

	Extract ExtractCommand `cmd:"" help:"Extract data from JSON into files."`
	Call    CallCommand    `cmd:"" help:"Call data and/or natural language description function on files."`
}

func main() {
	var app App
	cli.Run(&app, kong.Vars{}, func(ctx *kong.Context) errors.E {
		return errors.WithStack(ctx.Run(app.Logger))
	})
}
