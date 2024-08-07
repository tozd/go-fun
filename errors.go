package fun

import "gitlab.com/tozd/go/errors"

var (
	ErrAlreadyInitialized           = errors.Base("already initialized")
	ErrMultipleSystemMessages       = errors.Base("multiple system messages")
	ErrGaveUpRetry                  = errors.Base("gave up retrying")
	ErrAPIRequestFailed             = errors.Base("API request failed")
	ErrAPIResponseError             = errors.Base("API response error")
	ErrModelNotActive               = errors.Base("model not active")
	ErrUnexpectedNumberOfMessages   = errors.Base("unexpected number of messages")
	ErrNotTextMessage               = errors.Base("not text message")
	ErrNotDone                      = errors.Base("not done")
	ErrUnexpectedNumberOfTokens     = errors.Base("unexpected number of tokens")
	ErrModelMaxContextLength        = errors.Base("unable to determine model max context length")
	ErrMaxContextLengthOverModel    = errors.Base("max context length over what model supports")
	ErrMaxResponseLengthOverContext = errors.Base("max response length over max context length")
	ErrJSONSchemaValidation         = errors.Base("JSON Schema validation error")
)
