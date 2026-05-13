package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTrustedAsyncRelayTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	c.Request = req
	return c
}

func TestTrustedAsyncImageRelayChannelIDIgnoresTaskHeaderWithoutChannel(t *testing.T) {
	t.Setenv("IMAGE_INTERNAL_RELAY_SECRET", "")
	c := newTrustedAsyncRelayTestContext()
	c.Request.Header.Set("X-Mino-Async-Image-Task", "task_test")

	channelID, pinned, err := trustedAsyncImageRelayChannelID(c)
	if err != nil {
		t.Fatalf("trustedAsyncImageRelayChannelID returned error: %v", err)
	}
	if pinned {
		t.Fatal("expected missing channel id to fall back to normal channel selection")
	}
	if channelID != 0 {
		t.Fatalf("channelID = %d, want 0", channelID)
	}
}

func TestTrustedAsyncImageRelayChannelIDRejectsChannelWithoutTask(t *testing.T) {
	t.Setenv("IMAGE_INTERNAL_RELAY_SECRET", "")
	c := newTrustedAsyncRelayTestContext()
	c.Request.Header.Set("X-Mino-Async-Channel-Id", "123")

	_, pinned, err := trustedAsyncImageRelayChannelID(c)
	if !pinned {
		t.Fatal("expected channel id header to request pinning")
	}
	if err == nil {
		t.Fatal("expected channel id without task header to be rejected")
	}
}

func TestTrustedAsyncImageRelayChannelIDPinsWithBothHeaders(t *testing.T) {
	t.Setenv("IMAGE_INTERNAL_RELAY_SECRET", "")
	c := newTrustedAsyncRelayTestContext()
	c.Request.Header.Set("X-Mino-Async-Image-Task", "task_test")
	c.Request.Header.Set("X-Mino-Async-Channel-Id", "123")

	channelID, pinned, err := trustedAsyncImageRelayChannelID(c)
	if err != nil {
		t.Fatalf("trustedAsyncImageRelayChannelID returned error: %v", err)
	}
	if !pinned {
		t.Fatal("expected both headers to pin the selected channel")
	}
	if channelID != 123 {
		t.Fatalf("channelID = %d, want 123", channelID)
	}
}
