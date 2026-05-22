package controller

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const defaultSeedanceVideoModel = "seedance-2.0"

func CreateUserVideoGenerationTask(c *gin.Context) {
	if err := setupUserImageTaskToken(c); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "access_denied",
			},
		})
		return
	}

	req, err := normalizeUserVideoRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	if !ensureUserVideoTaskChannel(c, req.Model) {
		return
	}

	common.SetContextKey(c, constant.ContextKeyOriginalModel, req.Model)
	c.Set("relay_mode", relayconstant.RelayModeVideoSubmit)
	c.Request.URL.Path = "/v1/video/generations"
	c.Request.RequestURI = "/v1/video/generations"

	RelayTask(c)
}

func GetUserVideoTask(c *gin.Context) {
	task, ok := getUserVideoTask(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, videoTaskResponse(task))
}

func GetUserVideoHistory(c *gin.Context) {
	var tasks []*model.Task
	err := model.DB.
		Where("user_id = ? AND platform != ? AND status = ?", c.GetInt("id"), constant.TaskPlatformOpenAIImage, model.TaskStatusSuccess).
		Order("id desc").
		Limit(60).
		Find(&tasks).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "server_error"}})
		return
	}

	items := make([]gin.H, 0, len(tasks))
	for _, task := range tasks {
		item := videoHistoryItem(task)
		if item != nil {
			items = append(items, item)
		}
	}
	common.ApiSuccess(c, gin.H{
		"items": items,
		"limit": 60,
	})
}

func normalizeUserVideoRequest(c *gin.Context) (*relaycommon.TaskSubmitReq, error) {
	contentType := c.GetHeader("Content-Type")
	var req relaycommon.TaskSubmitReq
	var err error

	if strings.Contains(contentType, gin.MIMEMultipartPOSTForm) {
		req, err = taskSubmitReqFromMultipart(c)
	} else {
		err = common.UnmarshalBodyReusable(c, &req)
	}
	if err != nil {
		return nil, err
	}

	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		req.Model = defaultSeedanceVideoModel
	}
	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{req.Image}
	}
	applyVideoRequestMetadata(&req)

	body, err := common.Marshal(req)
	if err != nil {
		return nil, err
	}
	storage, err := common.CreateBodyStorage(body)
	if err != nil {
		return nil, err
	}
	if oldStorage, exists := c.Get(common.KeyBodyStorage); exists {
		if oldBody, ok := oldStorage.(common.BodyStorage); ok {
			_ = oldBody.Close()
		}
	}
	c.Set(common.KeyBodyStorage, storage)
	c.Request.Body = io.NopCloser(storage)
	c.Request.Header.Set("Content-Type", gin.MIMEJSON)
	c.Request.ContentLength = int64(len(body))
	return &req, nil
}

func taskSubmitReqFromMultipart(c *gin.Context) (relaycommon.TaskSubmitReq, error) {
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return relaycommon.TaskSubmitReq{}, err
	}
	req := relaycommon.TaskSubmitReq{
		Prompt:         firstFormValue(form, "prompt"),
		Model:          firstFormValue(form, "model"),
		Mode:           firstFormValue(form, "mode"),
		Image:          firstFormValue(form, "image"),
		Size:           firstFormValue(form, "size"),
		Ratio:          firstFormValue(form, "ratio"),
		AspectRatio:    firstFormValue(form, "aspect_ratio"),
		Quality:        firstFormValue(form, "quality"),
		Resolution:     firstFormValue(form, "resolution"),
		NegativePrompt: firstFormValue(form, "negative_prompt"),
		Seconds:        firstFormValue(form, "seconds"),
		Metadata:       map[string]interface{}{},
	}
	if req.Seconds == "" {
		req.Seconds = firstFormValue(form, "duration")
	}
	if req.Seconds != "" {
		if duration, err := strconv.Atoi(req.Seconds); err == nil {
			req.Duration = duration
		}
	}
	if images := form.Value["images"]; len(images) > 0 {
		req.Images = append(req.Images, images...)
	}
	if req.Image != "" {
		req.Images = append(req.Images, req.Image)
	}
	if dataURL, err := firstMultipartImageDataURL(form); err != nil {
		return relaycommon.TaskSubmitReq{}, err
	} else if dataURL != "" {
		req.Image = dataURL
		req.Images = append(req.Images, dataURL)
	}
	return req, nil
}

func firstFormValue(form *multipart.Form, key string) string {
	if form == nil || form.Value == nil {
		return ""
	}
	values := form.Value[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func firstMultipartImageDataURL(form *multipart.Form) (string, error) {
	if form == nil || form.File == nil {
		return "", nil
	}
	for _, field := range []string{"image", "images", "input_reference"} {
		files := form.File[field]
		if len(files) == 0 {
			continue
		}
		file, err := files[0].Open()
		if err != nil {
			return "", err
		}
		data, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return "", readErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if len(data) == 0 {
			return "", nil
		}
		contentType := files[0].Header.Get("Content-Type")
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = http.DetectContentType(data)
		}
		return fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(data)), nil
	}
	return "", nil
}

func applyVideoRequestMetadata(req *relaycommon.TaskSubmitReq) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	if req.Ratio == "" && req.AspectRatio != "" {
		req.Ratio = req.AspectRatio
	}
	if req.Ratio != "" {
		req.Metadata["ratio"] = req.Ratio
	}
	if req.Resolution != "" {
		req.Metadata["resolution"] = req.Resolution
	}
	if req.Duration > 0 {
		req.Metadata["duration"] = req.Duration
	}
	if req.Seconds != "" {
		req.Metadata["duration"] = req.Seconds
	}
	if req.NegativePrompt != "" {
		req.Metadata["negative_prompt"] = req.NegativePrompt
	}
}

func ensureUserVideoTaskChannel(c *gin.Context, modelName string) bool {
	for _, candidate := range videoChannelModelCandidates(modelName) {
		if selected, done := trySelectUserVideoTaskChannel(c, candidate); done {
			return selected
		}
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": gin.H{
			"message": fmt.Sprintf("当前分组下模型 %s 的可用渠道不存在", modelName),
			"type":    "service_unavailable",
		},
	})
	return false
}

func videoChannelModelCandidates(modelName string) []string {
	switch modelName {
	case "seedance-2.0":
		return []string{modelName, "doubao-seedance-2-0-260128"}
	case "seedance-2.0-fast":
		return []string{modelName, "doubao-seedance-2-0-fast-260128"}
	default:
		return []string{modelName}
	}
}

func trySelectUserVideoTaskChannel(c *gin.Context, modelName string) (bool, bool) {
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	channel, selectGroup, err := service.CacheGetRandomSatisfiedChannel(&service.RetryParam{
		Ctx:        c,
		ModelName:  modelName,
		TokenGroup: usingGroup,
		Retry:      common.GetPointer(0),
	})
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("获取分组 %s 下模型 %s 的可用渠道失败: %s", selectGroup, modelName, err.Error()),
				"type":    "service_unavailable",
			},
		})
		return false, true
	}
	if channel == nil {
		_ = selectGroup
		return false, false
	}
	if newAPIError := middleware.SetupContextForSelectedChannel(c, channel, modelName); newAPIError != nil {
		writeImageTaskChannelError(c, newAPIError)
		return false, true
	}
	return true, true
}

func getUserVideoTask(c *gin.Context) (*model.Task, bool) {
	taskID := c.Param("task_id")
	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error(), "type": "server_error"}})
		return nil, false
	}
	if !exists || task.Platform == constant.TaskPlatformOpenAIImage {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "video task not found", "type": "not_found_error"}})
		return nil, false
	}
	return task, true
}

func videoTaskResponse(task *model.Task) gin.H {
	resp := gin.H{
		"id":           task.TaskID,
		"task_id":      task.TaskID,
		"object":       "video.task",
		"status":       videoTaskPublicStatus(task.Status),
		"progress":     videoTaskProgress(task.Progress),
		"created_at":   task.SubmitTime,
		"started_at":   task.StartTime,
		"completed_at": task.FinishTime,
		"model":        task.Properties.OriginModelName,
	}
	if task.FailReason != "" && task.Status == model.TaskStatusFailure {
		resp["error"] = gin.H{"message": task.FailReason}
	} else if task.FailReason != "" {
		resp["last_error"] = gin.H{"message": task.FailReason}
	}
	if url := videoTaskURL(task); url != "" {
		resp["url"] = url
		resp["video_url"] = url
		resp["data"] = []gin.H{videoHistoryItem(task)}
		resp["videos"] = []gin.H{{"url": url, "video_url": url}}
	}
	return resp
}

func videoHistoryItem(task *model.Task) gin.H {
	url := videoTaskURL(task)
	if url == "" {
		return nil
	}
	modelName := task.Properties.OriginModelName
	if modelName == "" {
		modelName = task.Properties.UpstreamModelName
	}
	return gin.H{
		"id":         task.TaskID,
		"task_id":    task.TaskID,
		"src":        url,
		"url":        url,
		"video_url":  url,
		"prompt":     task.Properties.Input,
		"model":      modelName,
		"created_at": videoTaskCreatedAt(task),
	}
}

func videoTaskURL(task *model.Task) string {
	if task == nil {
		return ""
	}
	if url := strings.TrimSpace(task.GetResultURL()); url != "" {
		return url
	}
	var payload map[string]interface{}
	if err := common.Unmarshal(task.Data, &payload); err != nil {
		return ""
	}
	return firstStringFromVideoPayload(payload)
}

func firstStringFromVideoPayload(payload map[string]interface{}) string {
	for _, key := range []string{"url", "video_url"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if content, ok := payload["content"].(map[string]interface{}); ok {
		if value, ok := content["video_url"].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if metadata, ok := payload["metadata"].(map[string]interface{}); ok {
		if value := firstStringFromVideoPayload(metadata); value != "" {
			return value
		}
	}
	for _, key := range []string{"data", "videos"} {
		rows, ok := payload[key].([]interface{})
		if !ok {
			continue
		}
		for _, row := range rows {
			if rowMap, ok := row.(map[string]interface{}); ok {
				if value := firstStringFromVideoPayload(rowMap); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func videoTaskPublicStatus(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "succeeded"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusQueued, model.TaskStatusSubmitted, model.TaskStatusNotStart:
		return "queued"
	default:
		return "running"
	}
}

func videoTaskProgress(progress string) int {
	value := strings.TrimSuffix(strings.TrimSpace(progress), "%")
	if value == "" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func videoTaskCreatedAt(task *model.Task) int64 {
	if task.FinishTime > 0 {
		return task.FinishTime
	}
	if task.UpdatedAt > 0 {
		return task.UpdatedAt
	}
	return time.Now().Unix()
}
