package openai

import (
	"encoding/json"
	"testing"
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
