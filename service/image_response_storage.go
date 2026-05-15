package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

type ImageResponsePersistOptions struct {
	UserId          int
	RequestId       string
	Model           string
	Prompt          string
	Size            string
	Quality         string
	Source          string
	StoreHistory    bool
	StoreRemoteURLs bool
}

func PersistImageResponse(ctx context.Context, imageResp *dto.ImageResponse, opts ImageResponsePersistOptions) (int, error) {
	if imageResp == nil || len(imageResp.Data) == 0 {
		return 0, nil
	}

	stored := 0
	for i := range imageResp.Data {
		if !shouldPersistImageData(&imageResp.Data[i], opts.StoreRemoteURLs) {
			continue
		}

		data, contentType, err := imageDataBytesFromItem(ctx, i, &imageResp.Data[i], opts.StoreRemoteURLs)
		if err != nil {
			return stored, err
		}
		key := imageResponseObjectKey(opts, i, contentType)
		imageObject, err := PutImageObject(ctx, key, contentType, data)
		if err != nil {
			return stored, err
		}

		imageResp.Data[i].Url = imageObject.URL
		imageResp.Data[i].ObjectKey = imageObject.Key
		imageResp.Data[i].B64Json = ""
		if opts.StoreHistory {
			imageResp.Data[i].ProxyUrl = ImageContentProxyURL(imageObject.Key)
			if err := rememberPersistedImageHistory(ctx, opts, imageObject); err != nil {
				return stored, err
			}
		} else {
			imageResp.Data[i].ProxyUrl = ""
		}
		stored++
	}
	return stored, nil
}

func ImageContentProxyURL(objectKey string) string {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return ""
	}
	return "/api/user/image-content?key=" + url.QueryEscape(objectKey)
}

func shouldPersistImageData(item *dto.ImageData, storeRemoteURLs bool) bool {
	if item == nil {
		return false
	}
	if strings.TrimSpace(item.B64Json) != "" {
		return true
	}
	rawURL := strings.TrimSpace(item.Url)
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		return true
	}
	return storeRemoteURLs && (strings.HasPrefix(strings.ToLower(rawURL), "http://") || strings.HasPrefix(strings.ToLower(rawURL), "https://"))
}

func imageDataBytesFromItem(ctx context.Context, index int, item *dto.ImageData, allowRemoteURL bool) ([]byte, string, error) {
	if item == nil {
		return nil, "", fmt.Errorf("generated image %d is empty", index+1)
	}
	if strings.TrimSpace(item.B64Json) != "" {
		data, err := decodeBase64ImagePayload(item.B64Json)
		if err != nil {
			return nil, "", fmt.Errorf("decode generated image %d: %w", index+1, err)
		}
		return data, http.DetectContentType(data), nil
	}

	rawURL := strings.TrimSpace(item.Url)
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		data, contentType, err := decodeImageDataURL(rawURL)
		if err != nil {
			return nil, "", fmt.Errorf("decode generated image %d data url: %w", index+1, err)
		}
		return data, contentType, nil
	}
	if rawURL != "" && allowRemoteURL {
		data, contentType, err := downloadImageTaskURL(ctx, rawURL)
		if err != nil {
			return nil, "", fmt.Errorf("download generated image %d: %w", index+1, err)
		}
		return data, contentType, nil
	}
	return nil, "", fmt.Errorf("generated image %d has no persistable url or b64_json", index+1)
}

func decodeImageDataURL(rawURL string) ([]byte, string, error) {
	commaIndex := strings.Index(rawURL, ",")
	if commaIndex < 0 {
		return nil, "", fmt.Errorf("missing comma separator")
	}
	metadata := strings.TrimSpace(rawURL[len("data:"):commaIndex])
	payload := strings.TrimSpace(rawURL[commaIndex+1:])
	if metadata == "" {
		return nil, "", fmt.Errorf("missing content type")
	}

	parts := strings.Split(metadata, ";")
	contentType := strings.ToLower(strings.TrimSpace(parts[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("unsupported content type %q", contentType)
	}

	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}

	var data []byte
	var err error
	if isBase64 {
		if payload, err = url.PathUnescape(payload); err != nil {
			return nil, "", err
		}
		data, err = decodeBase64ImagePayload(payload)
	} else {
		var unescaped string
		unescaped, err = url.PathUnescape(payload)
		data = []byte(unescaped)
	}
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

func decodeBase64ImagePayload(payload string) ([]byte, error) {
	normalized := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, payload)
	return base64.StdEncoding.DecodeString(normalized)
}

func imageResponseObjectKey(opts ImageResponsePersistOptions, index int, contentType string) string {
	requestId := strings.TrimSpace(opts.RequestId)
	if requestId == "" {
		requestId = model.GenerateTaskID()
	}
	return fmt.Sprintf("%s/%d/%s/%02d%s", imageTaskObjectPrefix, opts.UserId, safeImagePathSegment(requestId), index+1, imageExtension(contentType))
}

func safeImagePathSegment(value string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return model.GenerateTaskID()
	}
	return builder.String()
}

func rememberPersistedImageHistory(ctx context.Context, opts ImageResponsePersistOptions, imageObject *ImageObject) error {
	if opts.UserId <= 0 || imageObject == nil || imageObject.Key == "" {
		return nil
	}
	quality := opts.Quality
	if quality == "" {
		quality = "standard"
	}
	source := opts.Source
	if source == "" {
		source = "url"
	}

	deletedKeys, err := model.AddImageHistoryAndTrim(&model.ImageHistory{
		UserId:    opts.UserId,
		TaskID:    opts.RequestId,
		ObjectKey: imageObject.Key,
		Prompt:    opts.Prompt,
		Model:     opts.Model,
		Size:      opts.Size,
		Quality:   quality,
		Source:    source,
	}, model.ImageHistoryLimit)
	if err != nil {
		return fmt.Errorf("save image history: %w", err)
	}
	for _, key := range deletedKeys {
		if key == imageObject.Key {
			continue
		}
		if err := DeleteImageObject(ctx, key); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("delete old image history object %s failed: %v", key, err))
		}
	}
	return nil
}
