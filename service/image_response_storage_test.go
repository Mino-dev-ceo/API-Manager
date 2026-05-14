package service

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestImageDataBytesFromItemB64JSON(t *testing.T) {
	item := &dto.ImageData{B64Json: base64.StdEncoding.EncodeToString(tinyPNG)}

	data, contentType, err := imageDataBytesFromItem(context.Background(), 0, item, false)
	if err != nil {
		t.Fatalf("imageDataBytesFromItem returned error: %v", err)
	}
	if string(data) != string(tinyPNG) {
		t.Fatalf("decoded data mismatch")
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
}

func TestImageDataBytesFromItemDataURL(t *testing.T) {
	item := &dto.ImageData{
		Url: "data:image/png;base64," + base64.StdEncoding.EncodeToString(tinyPNG),
	}

	data, contentType, err := imageDataBytesFromItem(context.Background(), 0, item, false)
	if err != nil {
		t.Fatalf("imageDataBytesFromItem returned error: %v", err)
	}
	if string(data) != string(tinyPNG) {
		t.Fatalf("decoded data mismatch")
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", contentType)
	}
}

func TestDecodeImageDataURLRejectsNonImage(t *testing.T) {
	_, _, err := decodeImageDataURL("data:text/plain;base64,aGVsbG8=")
	if err == nil {
		t.Fatalf("expected non-image data URL to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("unexpected error: %v", err)
	}
}
