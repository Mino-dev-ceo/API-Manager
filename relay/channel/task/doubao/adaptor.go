package doubao

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int    `json:"seed"`
	Resolution      string `json:"resolution"`
	Duration        int    `json:"duration"`
	Ratio           string `json:"ratio"`
	FramesPerSecond int    `json:"framespersecond"`
	ServiceTier     string `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

type compatibleTaskResponse struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
	Progress any    `json:"progress"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Metadata struct {
		URL          string `json:"url"`
		LastFrameURL string `json:"last_frame_url"`
	} `json:"metadata"`
	ContentURL  string `json:"content_url"`
	DownloadURL string `json:"download_url"`
	Data        struct {
		ID         string          `json:"id"`
		TaskID     string          `json:"task_id"`
		Status     string          `json:"status"`
		Progress   string          `json:"progress"`
		ResultURL  string          `json:"result_url"`
		FailReason string          `json:"fail_reason"`
		Data       json.RawMessage `json:"data"`
	} `json:"data"`
}

type compatibleReference struct {
	Type string `json:"type"`
	Role string `json:"role"`
	URL  string `json:"url"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	if isNewAPICompatibleVideoBaseURL(a.baseURL) {
		return buildNewAPIVideoTaskURL(a.baseURL, ""), nil
	}
	return buildContentGenerationTaskURL(a.baseURL, ""), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 检测请求 metadata 中是否包含视频输入，返回视频折扣 OtherRatio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	if hasVideoInMetadata(req.Metadata) {
		if ratio, ok := GetVideoInputRatio(info.OriginModelName); ok {
			return map[string]float64{"video_input": ratio}
		}
	}
	return nil
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	if isNewAPICompatibleVideoBaseURL(a.baseURL) {
		req.Model = resolveCompatibleUpstreamModelName(info.UpstreamModelName, req.Model)
		normalizeCompatibleVideoRequest(&req)
		body, err := a.convertToCompatibleRequestPayload(&req)
		if err != nil {
			return nil, err
		}
		data, err := common.Marshal(body)
		if err != nil {
			return nil, err
		}
		return bytes.NewReader(data), nil
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	body.Model = ResolveModelName(body.Model)
	info.UpstreamModelName = body.Model
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamID := dResp.ID
	if upstreamID == "" {
		var compatibleResp compatibleTaskResponse
		if err := common.Unmarshal(responseBody, &compatibleResp); err == nil {
			upstreamID = firstNonEmptyString(
				compatibleResp.ID,
				compatibleResp.TaskID,
				compatibleResp.Data.ID,
				compatibleResp.Data.TaskID,
			)
		}
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return upstreamID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := buildContentGenerationTaskURL(baseUrl, taskID)
	if isNewAPICompatibleVideoBaseURL(baseUrl) {
		uri = buildNewAPIVideoTaskURL(baseUrl, taskID)
	}

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func isNewAPICompatibleVideoBaseURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if strings.Contains(host, "volces.com") {
		return false
	}
	return true
}

func buildNewAPIVideoTaskURL(baseURL string, taskID string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	trimmed += "/v1/videos"
	if taskID != "" {
		return trimmed + "/" + taskID
	}
	return trimmed
}

func resolveCompatibleUpstreamModelName(mappedModelName string, requestModelName string) string {
	modelName := firstNonEmptyString(mappedModelName, requestModelName)
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case "seedance-2.0", "seedance 2.0", "doubao-seedance-2-0-260128":
		return "Seedance 2.0"
	case "seedance-2.0-fast", "seedance 2.0 fast", "doubao-seedance-2-0-fast-260128":
		return "Seedance 2.0 Fast"
	default:
		return modelName
	}
}

func normalizeCompatibleVideoRequest(req *relaycommon.TaskSubmitReq) {
	if req == nil {
		return
	}
	if req.Size == "" {
		req.Size = firstNonEmptyString(req.Ratio, req.AspectRatio)
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	if req.Resolution != "" {
		req.Metadata["resolution"] = req.Resolution
	} else if _, ok := req.Metadata["resolution"]; !ok {
		req.Metadata["resolution"] = "720p"
	}
	if req.Size != "" {
		req.Metadata["aspectRatio"] = req.Size
	}
}

func buildContentGenerationTaskURL(baseURL string, taskID string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(trimmed, "/api/v3/contents/generations/tasks"):
		// Some admins paste the SDK base plus resource path directly.
	case strings.HasSuffix(trimmed, "/api/v3"):
		trimmed += "/contents/generations/tasks"
	default:
		trimmed += "/api/v3/contents/generations/tasks"
	}
	if taskID != "" {
		return trimmed + "/" + taskID
	}
	return trimmed
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	} else if req.Duration > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(req.Duration))
	}

	if req.Ratio != "" {
		r.Ratio = req.Ratio
	} else if req.AspectRatio != "" {
		r.Ratio = req.AspectRatio
	}
	if req.Resolution != "" {
		r.Resolution = req.Resolution
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) convertToCompatibleRequestPayload(req *relaycommon.TaskSubmitReq) (map[string]any, error) {
	data, err := common.Marshal(req)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := common.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	delete(body, "image")
	delete(body, "images")
	delete(body, "ratio")
	delete(body, "aspect_ratio")

	references, err := a.compatibleReferences(req)
	if err != nil {
		return nil, err
	}
	if len(references) > 0 {
		body["references"] = references
	}
	return body, nil
}

func (a *TaskAdaptor) compatibleReferences(req *relaycommon.TaskSubmitReq) ([]compatibleReference, error) {
	images := append([]string{}, req.Images...)
	if req.Image != "" && !lo.Contains(images, req.Image) {
		images = append(images, req.Image)
	}
	references := make([]compatibleReference, 0, len(images))
	for _, imageURL := range images {
		imageURL = strings.TrimSpace(imageURL)
		if imageURL == "" {
			continue
		}
		if strings.HasPrefix(imageURL, "data:") {
			uploadedURL, err := a.uploadCompatibleAsset(imageURL)
			if err != nil {
				return nil, err
			}
			imageURL = uploadedURL
		}
		references = append(references, compatibleReference{
			Type: "image",
			Role: "reference_image",
			URL:  imageURL,
		})
	}
	return references, nil
}

func (a *TaskAdaptor) uploadCompatibleAsset(dataURL string) (string, error) {
	mimeType, payload, ok := strings.Cut(strings.TrimSpace(dataURL), ",")
	if !ok || !strings.HasPrefix(mimeType, "data:") {
		return "", fmt.Errorf("invalid data url")
	}
	contentType := strings.TrimPrefix(mimeType, "data:")
	contentType = strings.TrimSuffix(contentType, ";base64")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fileBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decode compatible asset failed: %w", err)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	exts, _ := mime.ExtensionsByType(contentType)
	ext := ".bin"
	if len(exts) > 0 {
		ext = exts[0]
	}
	part, err := writer.CreateFormFile("file", "reference"+ext)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	uploadURL := strings.TrimRight(strings.TrimSpace(a.baseURL), "/") + "/v1/video-assets/upload"
	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client, err := service.GetHttpClientWithProxy("")
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload compatible asset failed: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		URL      string `json:"url"`
		FilePath string `json:"file_path"`
	}
	if err := common.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	assetURL := firstNonEmptyString(result.URL, result.FilePath)
	if assetURL == "" {
		return "", fmt.Errorf("upload compatible asset returned empty url")
	}
	return assetURL, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	if taskResult, ok := parseCompatibleTaskResult(respBody); ok {
		return taskResult, nil
	}

	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func parseCompatibleTaskResult(respBody []byte) (*relaycommon.TaskInfo, bool) {
	var res compatibleTaskResponse
	if err := common.Unmarshal(respBody, &res); err != nil {
		return nil, false
	}
	status := firstNonEmptyString(res.Data.Status, res.Status)
	resultURL := firstNonEmptyString(res.Metadata.URL, res.ContentURL, res.Data.ResultURL)
	if status == "" && len(res.Data.Data) == 0 && resultURL == "" {
		return nil, false
	}

	taskResult := relaycommon.TaskInfo{Code: 0}
	switch strings.ToUpper(status) {
	case "NOT_START", "SUBMITTED", "QUEUED":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = firstNonEmptyString(compatibleProgressString(res.Progress), res.Data.Progress, "10%")
	case "IN_PROGRESS", "PROCESSING", "RUNNING":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = firstNonEmptyString(compatibleProgressString(res.Progress), res.Data.Progress, "50%")
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		// BoboToken media_token links are short lived. Leave Url empty so the
		// task is stored with our stable proxy URL; the proxy refreshes upstream.
	case "FAILURE", "FAILED", "CANCELLED":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = firstNonEmptyString(res.Data.FailReason)
		if res.Error != nil {
			taskResult.Reason = firstNonEmptyString(taskResult.Reason, res.Error.Message)
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = firstNonEmptyString(res.Data.Progress, "30%")
	}

	if taskResult.Url == "" && len(res.Data.Data) > 0 {
		var nested responseTask
		if err := common.Unmarshal(res.Data.Data, &nested); err == nil {
			taskResult.Url = nested.Content.VideoURL
			if taskResult.TotalTokens == 0 {
				taskResult.CompletionTokens = nested.Usage.CompletionTokens
				taskResult.TotalTokens = nested.Usage.TotalTokens
			}
		}
	}

	return &taskResult, true
}

func compatibleProgressString(progress any) string {
	switch v := progress.(type) {
	case float64:
		return fmt.Sprintf("%d%%", int(v))
	case int:
		return fmt.Sprintf("%d%%", v)
	case string:
		if strings.TrimSpace(v) == "" {
			return ""
		}
		if strings.Contains(v, "%") {
			return v
		}
		return v + "%"
	default:
		return ""
	}
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	videoURL := dResp.Content.VideoURL
	if videoURL == "" {
		videoURL = originTask.GetResultURL()
	}
	openAIVideo.SetMetadata("url", videoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}
