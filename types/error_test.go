package types

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToOpenAIErrorSanitizesInternalImplementationTerms(t *testing.T) {
	err := NewErrorWithStatusCode(
		errors.New("kiro upstream aws channel relay failed"),
		ErrorCodeBadResponseStatusCode,
		http.StatusBadGateway,
	)

	openAIError := err.ToOpenAIError()

	require.Equal(t, "api_error", openAIError.Type)
	require.Equal(t, "The model service is temporarily unavailable. Please retry later.", openAIError.Message)
	require.NotContains(t, strings.ToLower(openAIError.Message), "kiro")
	require.NotContains(t, strings.ToLower(openAIError.Message), "upstream")
	require.NotContains(t, strings.ToLower(openAIError.Message), "aws")
	require.NotContains(t, strings.ToLower(openAIError.Message), "channel")
	require.NotContains(t, strings.ToLower(openAIError.Message), "relay")
}

func TestToClaudeErrorSanitizesInternalImplementationTerms(t *testing.T) {
	err := WithClaudeError(
		ClaudeError{Type: "upstream_error", Message: "反代上游渠道超时"},
		http.StatusServiceUnavailable,
	)

	claudeError := err.ToClaudeError()

	require.Equal(t, "api_error", claudeError.Type)
	require.Equal(t, "The model service is temporarily unavailable. Please retry later.", claudeError.Message)
	require.NotContains(t, claudeError.Message, "反代")
	require.NotContains(t, claudeError.Message, "上游")
	require.NotContains(t, claudeError.Message, "渠道")
}

func TestToOpenAIErrorPreservesSafeValidationMessage(t *testing.T) {
	err := NewErrorWithStatusCode(
		errors.New("field messages is required"),
		ErrorCodeInvalidRequest,
		http.StatusBadRequest,
	)

	openAIError := err.ToOpenAIError()

	require.Equal(t, "invalid_request_error", openAIError.Type)
	require.Equal(t, "field messages is required", openAIError.Message)
}
