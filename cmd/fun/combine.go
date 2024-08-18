package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/rs/zerolog"
	"gitlab.com/tozd/go/errors"
)

//nolint:lll
type CombineCommand struct {
	InputDir       []string `arg:""               help:"Path to input directory."                                            placeholder:"PATH" required:""           type:"existingdir"`
	OutputDir      string   `                     help:"Path to output directory."                             name:"output" placeholder:"PATH" required:"" short:"o" type:"path"`
	InputExtension string   `       default:".in" help:"File extension of an input file. Default: ${default}." name:"in"     placeholder:"EXT"`
}

func (c *CombineCommand) Help() string {
	return "It outputs only those files which are equal in all input directories."
}

func (c *CombineCommand) Run(_ zerolog.Logger) errors.E {
	err := os.MkdirAll(c.OutputDir, 0o755) //nolint:gomnd
	if err != nil {
		return errors.WithStack(err)
	}

	files, err := filepath.Glob(filepath.Join(c.InputDir[0], "*"+c.InputExtension))
	if err != nil {
		return errors.WithStack(err)
	}
	slices.Sort(files)

FILE:
	for _, inputPath := range files {
		relPath, err := filepath.Rel(c.InputDir[0], inputPath)
		if err != nil {
			return errors.WithStack(err)
		}

		inputData, err := os.ReadFile(inputPath)
		if err != nil {
			return errors.WithDetails(err, "path", inputPath)
		}

		for _, inputDir := range c.InputDir[1:] {
			path := filepath.Join(inputDir, relPath)
			data, err := os.ReadFile(path) //nolint:govet
			if errors.Is(err, fs.ErrNotExist) {
				continue FILE
			} else if err != nil {
				return errors.WithDetails(err, "path", path)
			}

			if !bytes.Equal(inputData, data) {
				continue FILE
			}
		}

		outputPath := filepath.Join(c.OutputDir, relPath)
		err = os.WriteFile(outputPath, inputData, 0o644) //nolint:gosec,gomnd
		if err != nil {
			return errors.WithDetails(err, "path", outputPath)
		}
	}

	return nil
}
