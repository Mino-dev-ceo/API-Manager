package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	cpaAsyncRelayModeAuto  = "auto"
	cpaAsyncRelayModeForce = "force"
	cpaAsyncRelayModeOff   = "off"
)

type cpaAsyncTaskCreateResponse struct {
	ID     string             `json:"id"`
	TaskID string             `json:"task_id"`
	Status string             `json:"status"`
	Error  *cpaAsyncTaskError `json:"error,omitempty"`
}

type cpaAsyncTaskError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Status  int    `json:"status,omitempty"`
}

func shouldUseCPAAsyncImageRelay(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if c == nil || info == nil {
		return false
	}
	if strings.TrimSpace(c.GetHeader("X-Mino-Async-Image-Task")) == "" {
		return false
	}
	if info.RelayMode != relayconstant.RelayModeImagesGenerations && info.RelayMode != relayconstant.RelayModeImagesEdits {
		return false
	}
	if cpaAsyncHeaderBool(c.GetHeader("X-Mino-Async-Image-Upscale")) {
		return true
	}

	mode := strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("IMAGE_CPA_ASYNC_RELAY", cpaAsyncRelayModeAuto)))
	switch mode {
	case "0", "false", "disabled", "disable", cpaAsyncRelayModeOff:
		return false
	case "1", "true", "enabled", "enable", "on", cpaAsyncRelayModeForce:
		return true
	default:
		return looksLikeCPAAsyncUpstream(
			info.ChannelBaseUrl,
			common.GetContextKeyString(c, constant.ContextKeyChannelName),
		)
	}
}

func cpaAsyncHeaderBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on", "enabled", "enable":
		return true
	default:
		return false
	}
}

func looksLikeCPAAsyncUpstream(values ...string) bool {
	for _, value := range values {
		text := strings.ToLower(strings.TrimSpace(value))
		if text == "" {
			continue
		}
		for _, marker := range []string{"cli-proxy", "cliproxy", "cpa", "codex"} {
			if strings.Contains(text, marker) {
				return true
			}
		}
	}
	return false
}

func (a *Adaptor) doCPAAsyncImageRequest(c *gin.Context, info *relaycommon.RelayInfo, body []byte) (*http.Response, bool, error) {
	syncURL, err := a.GetRequestURL(info)
	if err != nil {
		return nil, false, fmt.Errorf("get cpa image request url failed: %w", err)
	}
	createURL, ok, err := cpaAsyncImageCreateURL(syncURL)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, true, fmt.Errorf("request url %s is not a cpa image endpoint", syncURL)
	}

	client, err := cpaAsyncHTTPClient(info)
	if err != nil {
		return nil, false, err
	}

	createReq, err := a.newCPAAsyncImageHTTPRequest(c, info, http.MethodPost, createURL, body)
	if err != nil {
		return nil, false, err
	}
	createResp, err := client.Do(createReq)
	if err != nil {
		return nil, false, fmt.Errorf("submit cpa async image task failed: %w", err)
	}
	createBody, statusCode, err := readCPAAsyncHTTPResponse(createResp)
	if err != nil {
		return nil, false, err
	}
	if isCPAAsyncUnsupportedStatus(statusCode) {
		return nil, true, fmt.Errorf("cpa async endpoint unsupported, status=%d", statusCode)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, false, fmt.Errorf("submit cpa async image task returned HTTP %d: %s", statusCode, compactCPAAsyncBody(createBody))
	}

	taskID, err := parseCPAAsyncTaskID(createBody)
	if err != nil {
		return nil, true, err
	}
	taskURL, err := cpaAsyncImageTaskURL(createURL, taskID)
	if err != nil {
		return nil, false, err
	}

	finalBody, err := a.pollCPAAsyncImageTask(c, info, client, taskURL)
	if err != nil {
		return nil, false, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(finalBody)),
		Request:    createReq,
	}, false, nil
}

func (a *Adaptor) pollCPAAsyncImageTask(c *gin.Context, info *relaycommon.RelayInfo, client *http.Client, taskURL string) ([]byte, error) {
	timeoutSeconds := common.GetEnvOrDefault("IMAGE_CPA_ASYNC_POLL_TIMEOUT_SECONDS", common.GetEnvOrDefault("IMAGE_TASK_TIMEOUT_SECONDS", 1800))
	if timeoutSeconds <= 0 {
		timeoutSeconds = 1800
	}
	intervalMillis := common.GetEnvOrDefault("IMAGE_CPA_ASYNC_POLL_INTERVAL_MS", 2000)
	if intervalMillis <= 0 {
		intervalMillis = 2000
	}
	deadline := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Duration(intervalMillis) * time.Millisecond)
	defer ticker.Stop()

	for {
		finalBody, done, err := a.pollCPAAsyncImageTaskOnce(c, info, client, taskURL)
		if done || err != nil {
			return finalBody, err
		}

		select {
		case <-c.Request.Context().Done():
			return nil, c.Request.Context().Err()
		case <-deadline.C:
			return nil, fmt.Errorf("cpa async image task polling timed out after %ds", timeoutSeconds)
		case <-ticker.C:
		}
	}
}

func (a *Adaptor) pollCPAAsyncImageTaskOnce(c *gin.Context, info *relaycommon.RelayInfo, client *http.Client, taskURL string) ([]byte, bool, error) {
	req, err := a.newCPAAsyncImageHTTPRequest(c, info, http.MethodGet, taskURL, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("poll cpa async image task failed: %w", err)
	}
	body, statusCode, err := readCPAAsyncHTTPResponse(resp)
	if err != nil {
		return nil, false, err
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, false, fmt.Errorf("poll cpa async image task returned HTTP %d: %s", statusCode, compactCPAAsyncBody(body))
	}

	status := strings.ToLower(strings.TrimSpace(cpaAsyncJSONField(body, "status")))
	switch status {
	case "succeeded", "success", "completed", "complete":
		if message, failed := cpaAsyncUpscaleFailureMessage(body); failed {
			return nil, true, fmt.Errorf("cpa async image upscale failed: %s", message)
		}
		if cpaAsyncUpscaleStillPending(body) {
			return nil, false, nil
		}
		finalBody, err := cpaAsyncExtractImageResponse(body)
		return finalBody, true, err
	case "failed", "failure", "error":
		return nil, true, fmt.Errorf("cpa async image task failed: %s", cpaAsyncTaskErrorMessage(body))
	default:
		return nil, false, nil
	}
}

func (a *Adaptor) newCPAAsyncImageHTTPRequest(c *gin.Context, info *relaycommon.RelayInfo, method string, rawURL string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, rawURL, reader)
	if err != nil {
		return nil, fmt.Errorf("build cpa async image request failed: %w", err)
	}
	headers := req.Header
	if err := a.SetupRequestHeader(c, &headers, info); err != nil {
		return nil, fmt.Errorf("setup cpa async image request header failed: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	headerOverride, err := channel.ResolveHeaderOverride(info, c)
	if err != nil {
		return nil, err
	}
	applyCPAAsyncHeaderOverride(req, headerOverride)
	return req, nil
}

func cpaAsyncHTTPClient(info *relaycommon.RelayInfo) (*http.Client, error) {
	if info != nil && info.ChannelSetting.Proxy != "" {
		client, err := service.NewProxyHttpClient(info.ChannelSetting.Proxy)
		if err != nil {
			return nil, fmt.Errorf("new proxy http client failed: %w", err)
		}
		return client, nil
	}
	return service.GetHttpClient(), nil
}

func cpaAsyncImageCreateURL(rawURL string) (string, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false, fmt.Errorf("parse cpa image request url failed: %w", err)
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/images/generations"):
		u.Path = path + "/async"
		return u.String(), true, nil
	case strings.HasSuffix(path, "/images/edits"):
		u.Path = path + "/async"
		return u.String(), true, nil
	default:
		return "", false, nil
	}
}

func cpaAsyncImageTaskURL(createURL string, taskID string) (string, error) {
	u, err := url.Parse(createURL)
	if err != nil {
		return "", fmt.Errorf("parse cpa async image task url failed: %w", err)
	}
	path := strings.TrimRight(u.Path, "/")
	var basePath string
	switch {
	case strings.HasSuffix(path, "/images/generations/async"):
		basePath = strings.TrimSuffix(path, "/images/generations/async")
	case strings.HasSuffix(path, "/images/edits/async"):
		basePath = strings.TrimSuffix(path, "/images/edits/async")
	default:
		return "", fmt.Errorf("invalid cpa async image create url: %s", createURL)
	}
	u.Path = basePath + "/images/tasks/" + taskID
	u.RawPath = basePath + "/images/tasks/" + url.PathEscape(taskID)
	u.RawQuery = ""
	return u.String(), nil
}

func isCPAAsyncUnsupportedStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

func readCPAAsyncHTTPResponse(resp *http.Response) ([]byte, int, error) {
	if resp == nil {
		return nil, 0, fmt.Errorf("cpa async image response is nil")
	}
	defer service.CloseResponseBodyGracefully(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read cpa async image response failed: %w", err)
	}
	return body, resp.StatusCode, nil
}

func parseCPAAsyncTaskID(body []byte) (string, error) {
	var payload cpaAsyncTaskCreateResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("parse cpa async image task response failed: %w", err)
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(payload.ID)
	}
	if taskID == "" {
		return "", fmt.Errorf("cpa async image task response missing task id")
	}
	return taskID, nil
}

func cpaAsyncExtractImageResponse(body []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse cpa async image task result failed: %w", err)
	}
	finalData, hasFinalData := cpaAsyncFinalURLData(raw)
	if hasFinalData {
		return cpaAsyncBuildImageResponse(raw, finalData)
	}
	if response, ok := raw["response"]; ok && len(bytes.TrimSpace(response)) > 0 && !bytes.Equal(bytes.TrimSpace(response), []byte("null")) {
		return response, nil
	}
	data, ok := raw["data"]
	if !ok || len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, fmt.Errorf("cpa async image task result missing image data")
	}
	return cpaAsyncBuildImageResponse(raw, data)
}

func cpaAsyncBuildImageResponse(raw map[string]json.RawMessage, data json.RawMessage) ([]byte, error) {
	out := map[string]json.RawMessage{
		"data": data,
	}
	if created, ok := raw["created"]; ok && len(bytes.TrimSpace(created)) > 0 {
		out["created"] = created
	} else {
		createdBytes, _ := json.Marshal(time.Now().Unix())
		out["created"] = createdBytes
	}
	if usage, ok := raw["usage"]; ok && len(bytes.TrimSpace(usage)) > 0 {
		out["usage"] = usage
	}
	finalBody, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("build cpa async image final response failed: %w", err)
	}
	return finalBody, nil
}

func cpaAsyncFinalURLData(raw map[string]json.RawMessage) (json.RawMessage, bool) {
	for _, field := range []string{"final_url", "result_image_url"} {
		value, ok := raw[field]
		if !ok {
			continue
		}
		var finalURL string
		if err := json.Unmarshal(value, &finalURL); err == nil && strings.TrimSpace(finalURL) != "" {
			data, err := json.Marshal([]map[string]string{{"url": strings.TrimSpace(finalURL)}})
			return data, err == nil
		}
	}
	jobs := make([]cpaAsyncUpscaleJob, 0, 1)
	if value, ok := raw["upscale_job"]; ok && len(bytes.TrimSpace(value)) > 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var job cpaAsyncUpscaleJob
		if err := json.Unmarshal(value, &job); err == nil {
			jobs = append(jobs, job)
		}
	}
	if value, ok := raw["upscale_jobs"]; ok && len(bytes.TrimSpace(value)) > 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var list []cpaAsyncUpscaleJob
		if err := json.Unmarshal(value, &list); err == nil {
			jobs = append(jobs, list...)
		}
	}
	if len(jobs) == 0 {
		return nil, false
	}
	data := make([]map[string]string, 0, len(jobs))
	for _, job := range jobs {
		if !cpaAsyncUpscaleJobSucceeded(job) {
			return nil, false
		}
		url := strings.TrimSpace(job.ResultImageURL)
		if url == "" {
			return nil, false
		}
		data = append(data, map[string]string{"url": url})
	}
	finalData, err := json.Marshal(data)
	return finalData, err == nil
}

func cpaAsyncUpscaleJobSucceeded(job cpaAsyncUpscaleJob) bool {
	status := cpaAsyncNormalizeStatus(job.Status)
	if status == "succeeded" {
		return true
	}
	return status == "" && strings.TrimSpace(job.ResultImageURL) != ""
}

type cpaAsyncUpscalePayload struct {
	Upscale     bool                 `json:"upscale"`
	UpscaleJob  *cpaAsyncUpscaleJob  `json:"upscale_job"`
	UpscaleJobs []cpaAsyncUpscaleJob `json:"upscale_jobs"`
}

type cpaAsyncUpscaleJob struct {
	Status         string          `json:"status"`
	ResultImageURL string          `json:"result_image_url"`
	ErrorMessage   string          `json:"error_message"`
	Error          json.RawMessage `json:"error"`
}

func cpaAsyncUpscaleStillPending(body []byte) bool {
	payload, ok := cpaAsyncParseUpscalePayload(body)
	if !ok || !cpaAsyncHasUpscale(payload) {
		return false
	}
	jobs := cpaAsyncCollectUpscaleJobs(payload)
	if len(jobs) == 0 {
		return true
	}
	for _, job := range jobs {
		if cpaAsyncUpscaleJobSucceeded(job) {
			continue
		}
		if cpaAsyncNormalizeStatus(job.Status) == "failed" {
			return false
		}
		return true
	}
	return false
}

func cpaAsyncUpscaleFailureMessage(body []byte) (string, bool) {
	payload, ok := cpaAsyncParseUpscalePayload(body)
	if !ok || !cpaAsyncHasUpscale(payload) {
		return "", false
	}
	for _, job := range cpaAsyncCollectUpscaleJobs(payload) {
		if cpaAsyncNormalizeStatus(job.Status) != "failed" {
			continue
		}
		message := strings.TrimSpace(job.ErrorMessage)
		if message == "" {
			message = cpaAsyncUpscaleErrorText(job.Error)
		}
		if message == "" {
			message = "upscale job failed"
		}
		return message, true
	}
	return "", false
}

func cpaAsyncParseUpscalePayload(body []byte) (cpaAsyncUpscalePayload, bool) {
	var payload cpaAsyncUpscalePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return payload, false
	}
	return payload, true
}

func cpaAsyncHasUpscale(payload cpaAsyncUpscalePayload) bool {
	return payload.Upscale || payload.UpscaleJob != nil || len(payload.UpscaleJobs) > 0
}

func cpaAsyncCollectUpscaleJobs(payload cpaAsyncUpscalePayload) []cpaAsyncUpscaleJob {
	jobs := make([]cpaAsyncUpscaleJob, 0, len(payload.UpscaleJobs)+1)
	if payload.UpscaleJob != nil {
		jobs = append(jobs, *payload.UpscaleJob)
	}
	jobs = append(jobs, payload.UpscaleJobs...)
	return jobs
}

func cpaAsyncNormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "success", "completed", "complete":
		return "succeeded"
	case "failed", "failure", "error":
		return "failed"
	default:
		return ""
	}
}

func cpaAsyncUpscaleErrorText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		return strings.TrimSpace(payload.Message)
	}
	return ""
}

func cpaAsyncJSONField(body []byte, field string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return ""
	}
	value, ok := raw[field]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text
	}
	return ""
}

func cpaAsyncTaskErrorMessage(body []byte) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return compactCPAAsyncBody(body)
	}
	if errRaw, ok := raw["error"]; ok {
		var taskErr cpaAsyncTaskError
		if err := json.Unmarshal(errRaw, &taskErr); err == nil && strings.TrimSpace(taskErr.Message) != "" {
			return taskErr.Message
		}
	}
	return compactCPAAsyncBody(body)
}

func compactCPAAsyncBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 1000 {
		return text[:1000] + "..."
	}
	return text
}

func applyCPAAsyncHeaderOverride(req *http.Request, headerOverride map[string]string) {
	if req == nil {
		return
	}
	for key, value := range headerOverride {
		req.Header.Set(key, value)
		if strings.EqualFold(key, "Host") {
			req.Host = value
		}
	}
}
