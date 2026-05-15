package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCPAAsyncImageCreateURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "generations",
			in:   "https://cpa.example.com/v1/images/generations",
			want: "https://cpa.example.com/v1/images/generations/async",
			ok:   true,
		},
		{
			name: "edits with query",
			in:   "https://cpa.example.com/v1/images/edits?foo=bar",
			want: "https://cpa.example.com/v1/images/edits/async?foo=bar",
			ok:   true,
		},
		{
			name: "not image endpoint",
			in:   "https://cpa.example.com/v1/chat/completions",
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, err := cpaAsyncImageCreateURL(tc.in)
			if err != nil {
				t.Fatalf("cpaAsyncImageCreateURL returned error: %v", err)
			}
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("url = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCPAAsyncImageTaskURL(t *testing.T) {
	got, err := cpaAsyncImageTaskURL("https://cpa.example.com/v1/images/generations/async?foo=bar", "task_abc/123")
	if err != nil {
		t.Fatalf("cpaAsyncImageTaskURL returned error: %v", err)
	}
	want := "https://cpa.example.com/v1/images/tasks/task_abc%2F123"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestCPAAsyncExtractImageResponsePrefersWrappedResponse(t *testing.T) {
	body := []byte(`{"status":"succeeded","response":{"created":123,"data":[{"url":"https://example.com/a.png"}],"usage":{"total_tokens":1}},"data":[{"url":"ignored"}]}`)
	got, err := cpaAsyncExtractImageResponse(body)
	if err != nil {
		t.Fatalf("cpaAsyncExtractImageResponse returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("final response is not json: %v", err)
	}
	if payload["created"].(float64) != 123 {
		t.Fatalf("created = %#v, want 123", payload["created"])
	}
	data := payload["data"].([]any)
	if data[0].(map[string]any)["url"] != "https://example.com/a.png" {
		t.Fatalf("unexpected data: %#v", data)
	}
}

func TestCPAAsyncExtractImageResponseBuildsOpenAIShapeFromTaskFields(t *testing.T) {
	body := []byte(`{"status":"succeeded","created":456,"data":[{"url":"https://example.com/a.png"}],"usage":{"total_tokens":1}}`)
	got, err := cpaAsyncExtractImageResponse(body)
	if err != nil {
		t.Fatalf("cpaAsyncExtractImageResponse returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("final response is not json: %v", err)
	}
	if payload["created"].(float64) != 456 {
		t.Fatalf("created = %#v, want 456", payload["created"])
	}
	if _, ok := payload["usage"]; !ok {
		t.Fatal("usage missing")
	}
}

func TestCPAAsyncLooksLikeUpstreamUsesChannelName(t *testing.T) {
	if !looksLikeCPAAsyncUpstream("http://127.0.0.1:8317", "CLIProxy Codex 本地上游") {
		t.Fatal("expected CLIProxy channel name to enable CPA async relay")
	}
}

func TestCPAAsyncUpscaleStillPendingAfterSourceGeneration(t *testing.T) {
	body := []byte(`{"status":"succeeded","upscale":true,"upscale_job":{"id":"job_1","status":"queued"}}`)
	if !cpaAsyncUpscaleStillPending(body) {
		t.Fatal("expected queued upscale job to keep polling")
	}
}

func TestCPAAsyncUpscaleStillPendingEvenWithSourceResponse(t *testing.T) {
	body := []byte(`{"status":"succeeded","upscale":true,"response":{"created":123,"data":[{"url":"https://example.com/source-2k.png"}]},"upscale_job":{"id":"job_1","status":"running"}}`)
	if !cpaAsyncUpscaleStillPending(body) {
		t.Fatal("expected running upscale job to keep polling even when source response is present")
	}
}

func TestCPAAsyncUpscaleFailureMessage(t *testing.T) {
	body := []byte(`{"status":"succeeded","upscale":true,"upscale_job":{"id":"job_1","status":"failed","error_message":"worker failed"}}`)
	message, failed := cpaAsyncUpscaleFailureMessage(body)
	if !failed {
		t.Fatal("expected failed upscale job")
	}
	if message != "worker failed" {
		t.Fatalf("message = %q, want worker failed", message)
	}
}

func TestCPAAsyncExtractImageResponseBuildsFromFinalURL(t *testing.T) {
	body := []byte(`{"status":"succeeded","created":789,"final_url":"https://example.com/final.png"}`)
	got, err := cpaAsyncExtractImageResponse(body)
	if err != nil {
		t.Fatalf("cpaAsyncExtractImageResponse returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("final response is not json: %v", err)
	}
	data := payload["data"].([]any)
	if data[0].(map[string]any)["url"] != "https://example.com/final.png" {
		t.Fatalf("unexpected data: %#v", data)
	}
}

func TestCPAAsyncExtractImageResponsePrefersUpscaleJobResult(t *testing.T) {
	body := []byte(`{"status":"succeeded","upscale":true,"response":{"created":123,"data":[{"url":"https://example.com/source-2k.png"}]},"upscale_job":{"id":"job_1","status":"succeeded","result_image_url":"https://example.com/final-4k.png"}}`)
	got, err := cpaAsyncExtractImageResponse(body)
	if err != nil {
		t.Fatalf("cpaAsyncExtractImageResponse returned error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("final response is not json: %v", err)
	}
	data := payload["data"].([]any)
	if data[0].(map[string]any)["url"] != "https://example.com/final-4k.png" {
		t.Fatalf("unexpected data: %#v", data)
	}
}

func TestCPAAsyncDisablesSyncFallbackForUpscale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	c.Request.Header.Set("X-Mino-Async-Image-Upscale", "true")

	if cpaAsyncFallbackToSyncAllowed(c) {
		t.Fatal("upscale requests must not fall back to sync image generation")
	}
}
