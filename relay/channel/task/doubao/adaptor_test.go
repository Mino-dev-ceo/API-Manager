package doubao

import "testing"

func TestBuildContentGenerationTaskURL(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		taskID string
		want   string
	}{
		{
			name: "root ark base",
			base: "https://ark.cn-beijing.volces.com",
			want: "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks",
		},
		{
			name: "sdk api v3 base",
			base: "https://ark.cn-beijing.volces.com/api/v3/",
			want: "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks",
		},
		{
			name:   "resource path base with task id",
			base:   "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks/",
			taskID: "task_123",
			want:   "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks/task_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildContentGenerationTaskURL(tt.base, tt.taskID)
			if got != tt.want {
				t.Fatalf("buildContentGenerationTaskURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewAPICompatibleVideoURL(t *testing.T) {
	if !isNewAPICompatibleVideoBaseURL("https://bobotoken.top/_cnapi") {
		t.Fatal("expected bobotoken upstream to use New API compatible video endpoint")
	}
	if isNewAPICompatibleVideoBaseURL("https://ark.cn-beijing.volces.com") {
		t.Fatal("expected official Ark endpoint to use content generation endpoint")
	}

	got := buildNewAPIVideoTaskURL("https://bobotoken.top/_cnapi/", "task_123")
	want := "https://bobotoken.top/_cnapi/v1/videos/task_123"
	if got != want {
		t.Fatalf("buildNewAPIVideoTaskURL() = %q, want %q", got, want)
	}
}

func TestParseCompatibleTaskResult(t *testing.T) {
	body := []byte(`{"id":"task_123","status":"completed","progress":100,"metadata":{"url":"https://example.com/video.mp4"}}`)
	result, ok := parseCompatibleTaskResult(body)
	if !ok {
		t.Fatal("expected compatible task result")
	}
	if result.Status != "SUCCESS" {
		t.Fatalf("status = %q, want SUCCESS", result.Status)
	}
	if result.Url != "" {
		t.Fatalf("url = %q, want empty stable proxy URL", result.Url)
	}
}

func TestResolveCompatibleUpstreamModelName(t *testing.T) {
	got := resolveCompatibleUpstreamModelName("seedance-2.0", "")
	want := "Seedance 2.0"
	if got != want {
		t.Fatalf("resolveCompatibleUpstreamModelName() = %q, want %q", got, want)
	}
}
