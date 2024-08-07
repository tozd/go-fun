package fun

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"gitlab.com/tozd/go/errors"
)

const (
	retryWaitMin = 100 * time.Millisecond //nolint:revive
	retryWaitMax = 5 * time.Second
)

const applicationJSONHeader = "application/json"

func retryErrorHandler(resp *http.Response, err error, numTries int) (*http.Response, error) {
	var body []byte
	if resp != nil {
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	var errE errors.E
	if err != nil {
		errE = errors.WrapWith(err, ErrGaveUpRetry)
	} else {
		errE = errors.WithStack(ErrGaveUpRetry)
	}
	errors.Details(errE)["attempts"] = numTries
	if body != nil {
		if resp.Header.Get("Content-Type") == applicationJSONHeader && json.Valid(body) {
			errors.Details(errE)["body"] = json.RawMessage(body)
		} else {
			errors.Details(errE)["body"] = string(body)
		}
	}
	return resp, errE
}
