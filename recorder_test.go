package fun_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/tozd/go/fun"
)

func TestRecorderNil(t *testing.T) {
	t.Parallel()

	assert.Nil(t, (*fun.TextRecorder)(nil).Calls())
}
