package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestOpenaiHandlerUsesOriginModelInOpenAIResponse(t *testing.T) {
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
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1777770000,
			"model":"claude-opus-4-7",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)),
	}

	if _, err := OpenaiHandler(c, info, resp); err != nil {
		t.Fatalf("OpenaiHandler returned error: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not json: %v", err)
	}
	if got := body["model"]; got != "claude-opus-4-7-max" {
		t.Fatalf("response model = %v, want claude-opus-4-7-max", got)
	}
}

func TestSendStreamDataUsesOriginModelInOpenAIChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-opus-4.6-thinking",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-opus-4.6",
		},
	}
	data := `{"id":"chatcmpl-test","object":"chat.completion.chunk","created":1777770000,"model":"claude-opus-4.6","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`

	if err := sendStreamData(c, info, data, false, false); err != nil {
		t.Fatalf("sendStreamData returned error: %v", err)
	}

	got := w.Body.String()
	if !strings.Contains(got, `"model":"claude-opus-4.6-thinking"`) {
		t.Fatalf("stream chunk = %q, want origin model", got)
	}
	if strings.Contains(got, `"model":"claude-opus-4.6"`) {
		t.Fatalf("stream chunk leaked upstream model: %q", got)
	}
}

func TestHandleFinalResponseUsesOriginModelInUsageChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		RelayFormat:        types.RelayFormatOpenAI,
		ShouldIncludeUsage: true,
		OriginModelName:    "claude-opus-4-7-max-thinking",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "claude-opus-4-7-max",
		},
	}

	HandleFinalResponse(c, info, "", "chatcmpl-test", 1777770000, "claude-opus-4-7-max", "", &dto.Usage{
		PromptTokens:     1,
		CompletionTokens: 1,
		TotalTokens:      2,
	}, false)

	got := w.Body.String()
	if !strings.Contains(got, `"model":"claude-opus-4-7-max-thinking"`) {
		t.Fatalf("usage chunk = %q, want origin model", got)
	}
}
