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

	mode := strings.ToLower(strings.TrimSpace(common.GetEnvOrDefaultString("IMAGE_CPA_ASYNC_RELAY", cpaAsyncRelayModeAuto)))
	switch mode {
	case "0", "false", "disabled", "disable", cpaAsyncRelayModeOff:
		return false
	case "1", "true", "enabled", "enable", "on", cpaAsyncRelayModeForce:
		return true
	default:
		return looksLikeCPAAsyncUpstream(info.ChannelBaseUrl)
	}
}

func looksLikeCPAAsyncUpstream(baseURL string) bool {
	host := strings.ToLower(strings.TrimSpace(baseURL))
	if host == "" {
		return false
	}
	for _, marker := range []string{"cli-proxy", "cliproxy", "cpa", "codex"} {
		if strings.Contains(host, marker) {
			return true
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
	if response, ok := raw["response"]; ok && len(bytes.TrimSpace(response)) > 0 && !bytes.Equal(bytes.TrimSpace(response), []byte("null")) {
		return response, nil
	}
	data, ok := raw["data"]
	if !ok || len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, fmt.Errorf("cpa async image task result missing image data")
	}

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
