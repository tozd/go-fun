package main

import (
	"os"

	"gitlab.com/tozd/go/errors"
)

func writeFile(path, data string) errors.E {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gomnd
	if err != nil {
		return errors.WithStack(err)
	}
	_, err = f.WriteString(data)
	if err2 := f.Close(); err2 != nil && err == nil {
		err = err2
	}
	return errors.WithStack(err)
}
