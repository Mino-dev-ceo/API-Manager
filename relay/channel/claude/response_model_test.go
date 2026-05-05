package claude

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestClaudeHandlerUsesOriginModelWhenConvertingToOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-opus-4-7-max",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-opus-4-7",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	httpResp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	data := []byte(`{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-opus-4-7",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)

	if err := HandleClaudeResponseData(c, info, claudeInfo, httpResp, data); err != nil {
		t.Fatalf("HandleClaudeResponseData returned error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not json: %v", err)
	}
	if got := body["model"]; got != "claude-opus-4-7-max" {
		t.Fatalf("response model = %v, want claude-opus-4-7-max", got)
	}
}

func TestClaudeStreamUsesOriginModelWhenConvertingToOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-opus-4-7-max-thinking",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-opus-4-7-max",
		},
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	data := `{"type":"message_start","message":{"id":"msg_test","type":"message","model":"claude-opus-4-7-max","usage":{"input_tokens":1,"output_tokens":0}}}`

	if err := HandleStreamResponseData(c, info, claudeInfo, data); err != nil {
		t.Fatalf("HandleStreamResponseData returned error: %v", err)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"model":"claude-opus-4-7-max-thinking"`) {
		t.Fatalf("stream chunk = %q, want origin model", got)
	}
}

func TestClaudeFinalUsageUsesOriginModelWhenConvertingToOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		OriginModelName:    "claude-opus-4.6-thinking",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-opus-4.6",
		},
	}
	claudeInfo := &ClaudeResponseInfo{
		ResponseId: "msg_test",
		Created:    1777770000,
		Usage: &dto.Usage{
			PromptTokens:     1,
			CompletionTokens: 1,
			TotalTokens:      2,
		},
		Done: true,
	}

	HandleStreamFinalResponse(c, info, claudeInfo)

	got := w.Body.String()
	if !strings.Contains(got, `"model":"claude-opus-4.6-thinking"`) {
		t.Fatalf("usage chunk = %q, want origin model", got)
	}
}
