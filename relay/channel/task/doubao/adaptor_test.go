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
