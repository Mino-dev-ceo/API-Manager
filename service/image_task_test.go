package service

import (
	"bytes"
	"mime/multipart"
	"testing"
)

func TestImageTaskRequestRequiresUpscaleJSON(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"x","upscale":true}`)
	if !imageTaskRequestRequiresUpscale(body, "application/json") {
		t.Fatal("expected json upscale request to require upscale")
	}
}

func TestImageTaskRequestRequiresUpscaleMultipartFlag(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("model", "gpt-image-2"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("upscale", "true"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if !imageTaskRequestRequiresUpscale(body.Bytes(), writer.FormDataContentType()) {
		t.Fatal("expected multipart upscale flag to require upscale")
	}
}

func TestImageTaskRequestRequiresUpscaleMultipartTargetLongEdge(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("target_long_edge", "3840"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if !imageTaskRequestRequiresUpscale(body.Bytes(), writer.FormDataContentType()) {
		t.Fatal("expected multipart target_long_edge to require upscale")
	}
}
