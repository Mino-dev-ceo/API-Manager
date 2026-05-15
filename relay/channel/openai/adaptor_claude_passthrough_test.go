package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestClaudeMessagesPassthroughUsesAnthropicEndpointAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://windsurf.example",
			ApiKey:         "upstream-key",
			ChannelSetting: dto.ChannelSettings{
				PassThroughBodyEnabled: true,
			},
		},
	}
	adaptor := &Adaptor{}

	gotURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if want := "https://windsurf.example/v1/messages"; gotURL != want {
		t.Fatalf("GetRequestURL() = %q, want %q", gotURL, want)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "output-128k-2025-02-19")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	headers := http.Header{}
	if err := adaptor.SetupRequestHeader(c, &headers, info); err != nil {
		t.Fatalf("SetupRequestHeader returned error: %v", err)
	}
	if got := headers.Get("x-api-key"); got != "upstream-key" {
		t.Fatalf("x-api-key = %q, want upstream-key", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty for Anthropic passthrough", got)
	}
	if got := headers.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want 2023-06-01", got)
	}
	if got := headers.Get("anthropic-beta"); got != "output-128k-2025-02-19" {
		t.Fatalf("anthropic-beta = %q, want output-128k-2025-02-19", got)
	}
}

func TestClaudeMessagesDefaultStillUsesOpenAIChatCompletions(t *testing.T) {
	original := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = original
	}()

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://api.openai.example",
		},
	}
	adaptor := &Adaptor{}

	gotURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if want := "https://api.openai.example/v1/chat/completions"; gotURL != want {
		t.Fatalf("GetRequestURL() = %q, want %q", gotURL, want)
	}
}

func TestClaudeMessagesWindsurfBaseURLUsesAnthropicPassthrough(t *testing.T) {
	original := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = original
	}()

	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude,
		RelayMode:   relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "http://windsurfapi.zeabur.internal:3003",
		},
	}
	adaptor := &Adaptor{}

	gotURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if want := "http://windsurfapi.zeabur.internal:3003/v1/messages"; gotURL != want {
		t.Fatalf("GetRequestURL() = %q, want %q", gotURL, want)
	}
}

func TestClaudeOpusMessagesUseAnthropicPassthroughForOpenAIChannel(t *testing.T) {
	original := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	model_setting.GetGlobalSettings().PassThroughRequestEnabled = false
	defer func() {
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = original
	}()

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		RelayMode:       relayconstant.RelayModeChatCompletions,
		OriginModelName: "claude-opus-4-6",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "http://service.internal:3003",
			UpstreamModelName: "claude-opus-4.6",
		},
	}
	adaptor := &Adaptor{}

	gotURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if want := "http://service.internal:3003/v1/messages"; gotURL != want {
		t.Fatalf("GetRequestURL() = %q, want %q", gotURL, want)
	}
}

func TestWindsurfOpenAIChannelPreservesGpt5EffortSuffix(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://windsurfapi.minotoken.xyz",
			UpstreamModelName: "gpt-5.4-none",
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-5.4-none",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	gotRequest, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if gotRequest.Model != "gpt-5.4-none" {
		t.Fatalf("request.Model = %q, want gpt-5.4-none", gotRequest.Model)
	}
	if info.UpstreamModelName != "gpt-5.4-none" {
		t.Fatalf("info.UpstreamModelName = %q, want gpt-5.4-none", info.UpstreamModelName)
	}
	if info.ReasoningEffort != "" {
		t.Fatalf("info.ReasoningEffort = %q, want empty", info.ReasoningEffort)
	}
}

func TestOpenAIChannelStillExtractsGpt5EffortSuffixForOfficialUpstream(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://api.openai.com",
			UpstreamModelName: "gpt-5.4-none",
		},
	}
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-5.4-none",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	}

	got, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	gotRequest, ok := got.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIRequest returned %T, want *dto.GeneralOpenAIRequest", got)
	}
	if gotRequest.Model != "gpt-5.4" {
		t.Fatalf("request.Model = %q, want gpt-5.4", gotRequest.Model)
	}
	if gotRequest.ReasoningEffort != "none" {
		t.Fatalf("request.ReasoningEffort = %q, want none", gotRequest.ReasoningEffort)
	}
	if info.UpstreamModelName != "gpt-5.4" {
		t.Fatalf("info.UpstreamModelName = %q, want gpt-5.4", info.UpstreamModelName)
	}
}

func TestWindsurfResponsesChannelPreservesGpt5EffortSuffix(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeOpenAI,
			ChannelBaseUrl: "https://windsurfapi.minotoken.xyz",
		},
	}
	request := dto.OpenAIResponsesRequest{Model: "gpt-5.4-none"}

	got, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIResponsesRequest returned error: %v", err)
	}
	gotRequest, ok := got.(dto.OpenAIResponsesRequest)
	if !ok {
		t.Fatalf("ConvertOpenAIResponsesRequest returned %T, want dto.OpenAIResponsesRequest", got)
	}
	if gotRequest.Model != "gpt-5.4-none" {
		t.Fatalf("request.Model = %q, want gpt-5.4-none", gotRequest.Model)
	}
	if gotRequest.Reasoning != nil {
		t.Fatalf("request.Reasoning = %#v, want nil", gotRequest.Reasoning)
	}
}
