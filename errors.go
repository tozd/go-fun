package fun

import "gitlab.com/tozd/go/errors"

var (
	ErrAlreadyInitialized           = errors.Base("already initialized")
	ErrMultipleSystemMessages       = errors.Base("multiple system messages")
	ErrGaveUpRetry                  = errors.Base("gave up retrying")
	ErrAPIRequestFailed             = errors.Base("API request failed")
	ErrAPIResponseError             = errors.Base("API response error")
	ErrMissingRequestID             = errors.Base("missing request ID")
	ErrModelNotActive               = errors.Base("model not active")
	ErrUnexpectedRole               = errors.Base("unexpected role")
	ErrUnexpectedNumberOfMessages   = errors.Base("unexpected number of messages")
	ErrUnexpectedMessageType        = errors.Base("unexpected message type")
	ErrUnexpectedStop               = errors.Base("unexpected stop")
	ErrUnexpectedNumberOfTokens     = errors.Base("unexpected number of tokens")
	ErrModelMaxContextLength        = errors.Base("unable to determine model max context length")
	ErrMaxContextLengthOverModel    = errors.Base("max context length over what model supports")
	ErrMaxResponseLengthOverContext = errors.Base("max response length over max context length")
	ErrJSONSchemaValidation         = errors.Base("JSON Schema validation error")
	ErrRefused                      = errors.Base("refused")
	ErrInvalidJSONSchema            = errors.Base("invalid JSON Schema")
	ErrToolNotFound                 = errors.Base("tool not found")
	ErrToolCallsWithoutCalls        = errors.Base("tool calls without calls")
	ErrMaxExchangesReached          = errors.Base("reached max allowed exchanges")
)
