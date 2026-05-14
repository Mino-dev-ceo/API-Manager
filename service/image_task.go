package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const (
	imageTaskObjectPrefix = "images"
	imageTaskDefaultLoop  = 2 * time.Second
)

func StartImageTaskWorker() {
	if !ImageTasksEnabled() {
		common.SysLog("image task worker disabled")
		return
	}
	if !common.IsMasterNode {
		common.SysLog("image task worker skipped on slave node")
		return
	}
	concurrency := common.GetEnvOrDefault("IMAGE_WORKER_CONCURRENCY", 2000)
	if concurrency <= 0 {
		concurrency = 1
	}
	common.SysLog(fmt.Sprintf("image task worker enabled, concurrency=%d", concurrency))
	go runImageTaskWorker(concurrency)
}

func runImageTaskWorker(concurrency int) {
	sem := make(chan struct{}, concurrency)
	for {
		freeSlots := concurrency - len(sem)
		if freeSlots > 0 {
			tasks := model.GetQueuedImageTasks(freeSlots)
			for _, task := range tasks {
				sem <- struct{}{}
				go func(t *model.Task) {
					defer func() { <-sem }()
					processImageTask(context.Background(), t)
				}(task)
			}
		}
		time.Sleep(imageTaskDefaultLoop)
	}
}

func processImageTask(ctx context.Context, task *model.Task) {
	oldStatus := task.Status
	now := time.Now().Unix()
	task.Status = model.TaskStatusInProgress
	task.Progress = "10%"
	if task.StartTime == 0 {
		task.StartTime = now
	}
	task.PrivateData.Attempts++
	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("claim image task %s failed: %v", task.TaskID, err))
		return
	}
	if !won {
		return
	}

	err = executeImageTask(ctx, task)
	if err == nil {
		return
	}

	maxRetries := common.GetEnvOrDefault("IMAGE_TASK_MAX_RETRIES", 2)
	if maxRetries < 0 {
		maxRetries = 0
	}
	task.FailReason = compactImageTaskError(err)
	if errors.Is(err, ErrImageStorageNotConfigured) {
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = time.Now().Unix()
		_ = task.Update()
		logger.LogError(ctx, fmt.Sprintf("image task %s failed because R2 storage is not configured", task.TaskID))
		return
	}
	if task.PrivateData.Attempts <= maxRetries {
		task.Status = model.TaskStatusQueued
		task.Progress = "0%"
		_ = task.Update()
		logger.LogWarn(ctx, fmt.Sprintf("image task %s failed, retry queued: %v", task.TaskID, err))
		return
	}

	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FinishTime = time.Now().Unix()
	_ = task.Update()
	logger.LogError(ctx, fmt.Sprintf("image task %s failed permanently: %v", task.TaskID, err))
}

func executeImageTask(ctx context.Context, task *model.Task) error {
	body, err := base64.StdEncoding.DecodeString(task.PrivateData.RequestBody)
	if err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if strings.TrimSpace(task.PrivateData.RelayTokenKey) == "" {
		return fmt.Errorf("missing relay token")
	}
	if _, err := getImageStorage(); err != nil {
		return err
	}
	requestPath := task.PrivateData.RequestPath
	if requestPath == "" {
		requestPath = "/v1/images/generations"
	}
	method := task.PrivateData.RequestMethod
	if method == "" {
		method = http.MethodPost
	}

	timeout := time.Duration(common.GetEnvOrDefault("IMAGE_TASK_TIMEOUT_SECONDS", 1800)) * time.Second
	if timeout <= 0 {
		timeout = 1800 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, method, imageInternalRelayBaseURL()+requestPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build internal image request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer sk-"+strings.TrimPrefix(task.PrivateData.RelayTokenKey, "sk-"))
	if task.PrivateData.RequestType != "" {
		req.Header.Set("Content-Type", task.PrivateData.RequestType)
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Mino-Async-Image-Task", task.TaskID)
	if task.ChannelId > 0 {
		req.Header.Set("X-Mino-Async-Channel-Id", strconv.Itoa(task.ChannelId))
	}
	if secret := strings.TrimSpace(os.Getenv("IMAGE_INTERNAL_RELAY_SECRET")); secret != "" {
		req.Header.Set("X-Mino-Async-Relay-Secret", secret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("call internal image relay: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read internal image response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("internal image relay returned HTTP %d: %s", resp.StatusCode, compactResponseText(responseBody))
	}

	var imageResp dto.ImageResponse
	if err := common.Unmarshal(responseBody, &imageResp); err != nil {
		return fmt.Errorf("parse image response: %w", err)
	}
	if len(imageResp.Data) == 0 {
		return fmt.Errorf("image response contains no data")
	}

	imageObjects := make([]*ImageObject, 0, len(imageResp.Data))
	for i := range imageResp.Data {
		imageObject, err := existingImageTaskObject(ctx, &imageResp.Data[i])
		if err != nil {
			return err
		}
		if imageObject == nil {
			imageObject, err = persistImageTaskItem(ctx, task, i, &imageResp.Data[i])
			if err != nil {
				return err
			}
		}
		imageObjects = append(imageObjects, imageObject)
		imageResp.Data[i].Url = imageObject.URL
		imageResp.Data[i].ObjectKey = imageObject.Key
		imageResp.Data[i].B64Json = ""
		if i == 0 {
			task.PrivateData.ResultURL = imageObject.URL
		}
	}
	for _, imageObject := range imageObjects {
		if err := rememberImageHistory(ctx, task, imageObject); err != nil {
			return err
		}
	}

	task.SetData(imageResp)
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	task.FinishTime = time.Now().Unix()
	task.FailReason = ""
	return task.Update()
}

func existingImageTaskObject(ctx context.Context, item *dto.ImageData) (*ImageObject, error) {
	if item == nil || strings.TrimSpace(item.B64Json) != "" {
		return nil, nil
	}
	objectKey := strings.TrimSpace(item.ObjectKey)
	if objectKey == "" {
		return nil, nil
	}
	objectURL := strings.TrimSpace(item.Url)
	if objectURL == "" || !strings.HasPrefix(strings.ToLower(objectURL), "http://") && !strings.HasPrefix(strings.ToLower(objectURL), "https://") {
		url, err := ImageObjectURL(ctx, objectKey)
		if err != nil {
			return nil, err
		}
		objectURL = url
	}
	return &ImageObject{
		Key: objectKey,
		URL: objectURL,
	}, nil
}

func persistImageTaskItem(ctx context.Context, task *model.Task, index int, item *dto.ImageData) (*ImageObject, error) {
	data, contentType, err := imageDataBytesFromItem(ctx, index, item, true)
	if err != nil {
		return nil, err
	}

	ext := imageExtension(contentType)
	key := fmt.Sprintf("%s/%d/%s/%02d%s", imageTaskObjectPrefix, task.UserId, task.TaskID, index+1, ext)
	obj, err := PutImageObject(ctx, key, contentType, data)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func rememberImageHistory(ctx context.Context, task *model.Task, imageObject *ImageObject) error {
	if imageObject == nil || imageObject.Key == "" {
		return nil
	}
	quality := task.Properties.ImageQuality
	if quality == "" {
		quality = "standard"
	}
	deletedKeys, err := model.AddImageHistoryAndTrim(&model.ImageHistory{
		UserId:    task.UserId,
		TaskID:    task.TaskID,
		ObjectKey: imageObject.Key,
		Prompt:    task.Properties.Input,
		Model:     task.Properties.OriginModelName,
		Size:      task.Properties.ImageSize,
		Quality:   quality,
		Source:    "url",
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

func downloadImageTaskURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	limitMB := common.GetEnvOrDefault("IMAGE_DOWNLOAD_MAX_MB", 64)
	if limitMB <= 0 {
		limitMB = 64
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limitMB)<<20))
	if err != nil {
		return nil, "", err
	}
	contentType := strings.Split(resp.Header.Get("Content-Type"), ";")[0]
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func imageInternalRelayBaseURL() string {
	if raw := strings.TrimRight(strings.TrimSpace(os.Getenv("IMAGE_INTERNAL_RELAY_BASE_URL")), "/"); raw != "" {
		return raw
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" && common.Port != nil {
		port = strconv.Itoa(*common.Port)
	}
	if port == "" {
		port = "3000"
	}
	return "http://127.0.0.1:" + port
}

func imageExtension(contentType string) string {
	if contentType == "" {
		return ".png"
	}
	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	switch strings.ToLower(contentType) {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		ext := filepath.Ext(contentType)
		if ext != "" {
			return ext
		}
		return ".png"
	}
}

func compactImageTaskError(err error) string {
	if err == nil {
		return ""
	}
	return compactResponseText([]byte(err.Error()))
}

func compactResponseText(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 1000 {
		return text[:1000] + "..."
	}
	return text
}
