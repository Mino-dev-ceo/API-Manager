package common

import "testing"

func TestGetModelContextWindow(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  int
	}{
		{
			name:  "gpt-5 family gets 1m context",
			model: "gpt-5.4",
			want:  1000000,
		},
		{
			name:  "codex family gets 1m context",
			model: "codex-mini-latest",
			want:  1000000,
		},
		{
			name:  "unknown model has no declared override",
			model: "gpt-4.1",
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetModelContextWindow(tt.model); got != tt.want {
				t.Fatalf("GetModelContextWindow(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}
