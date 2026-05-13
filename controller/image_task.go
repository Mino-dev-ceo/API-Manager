package controller

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func CreateImageGenerationTask(c *gin.Context) {
	createImageTask(c, relayconstant.RelayModeImagesGenerations, constant.TaskActionImageGeneration, "/v1/images/generations")
}

func CreateImageEditTask(c *gin.Context) {
	createImageTask(c, relayconstant.RelayModeImagesEdits, constant.TaskActionImageEdit, "/v1/images/edits")
}

func CreateUserImageGenerationTask(c *gin.Context) {
	createUserImageTask(c, relayconstant.RelayModeImagesGenerations, constant.TaskActionImageGeneration, "/v1/images/generations")
}

func CreateUserImageEditTask(c *gin.Context) {
	createUserImageTask(c, relayconstant.RelayModeImagesEdits, constant.TaskActionImageEdit, "/v1/images/edits")
}

func createUserImageTask(c *gin.Context, relayMode int, action string, requestPath string) {
	if err := setupUserImageTaskToken(c); err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "access_denied",
			},
		})
		return
	}
	createImageTask(c, relayMode, action, requestPath)
}

func setupUserImageTaskToken(c *gin.Context) error {
	tokenID, err := imageTaskTokenID(c)
	if err != nil {
		return err
	}
	userID := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenID, userID)
	if err != nil {
		return fmt.Errorf("API Key 不存在或不属于当前用户")
	}
	token, err = model.ValidateUserToken(token.Key)
	if err != nil {
		return fmt.Errorf("API Key 不可用，请检查状态、余额或过期时间")
	}
	if token.UserId != userID {
		return fmt.Errorf("API Key 不属于当前用户")
	}
	if err := ensureImageTaskTokenIPAllowed(c, token); err != nil {
		return err
	}
	if err := setupImageTaskUserGroup(c, token); err != nil {
		return err
	}
	return middleware.SetupContextForToken(c, token)
}

func imageTaskTokenID(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.GetHeader("X-Mino-Token-Id"))
	if raw == "" {
		return 0, fmt.Errorf("缺少 X-Mino-Token-Id 请求头")
	}
	tokenID, err := strconv.Atoi(raw)
	if err != nil || tokenID <= 0 {
		return 0, fmt.Errorf("X-Mino-Token-Id 无效")
	}
	return tokenID, nil
}

func ensureImageTaskTokenIPAllowed(c *gin.Context, token *model.Token) error {
	allowIPs := token.GetIpLimits()
	if len(allowIPs) == 0 {
		return nil
	}
	ip := net.ParseIP(c.ClientIP())
	if ip == nil {
		return fmt.Errorf("无法解析客户端 IP 地址")
	}
	if !common.IsIpInCIDRList(ip, allowIPs) {
		return fmt.Errorf("您的 IP 不在令牌允许访问的列表中")
	}
	return nil
}

func setupImageTaskUserGroup(c *gin.Context, token *model.Token) error {
	userCache, err := model.GetUserCache(token.UserId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CreateUserImageTask GetUserCache error for user %d: %v", token.UserId, err))
		return errors.New(common.TranslateMessage(c, i18n.MsgDatabaseError))
	}
	if userCache.Status != common.UserStatusEnabled {
		return errors.New(common.TranslateMessage(c, i18n.MsgAuthUserBanned))
	}
	userCache.WriteContext(c)

	usingGroup := userCache.Group
	if token.Group != "" {
		if _, ok := service.GetUserUsableGroups(userCache.Group)[token.Group]; !ok {
			return fmt.Errorf("无权访问 %s 分组", token.Group)
		}
		if !ratio_setting.ContainsGroupRatio(token.Group) && token.Group != "auto" {
			return fmt.Errorf("分组 %s 已被弃用", token.Group)
		}
		usingGroup = token.Group
	}
	common.SetContextKey(c, constant.ContextKeyUsingGroup, usingGroup)
	return nil
}

func createImageTask(c *gin.Context, relayMode int, action string, requestPath string) {
	if !service.ImageTasksEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": "image async tasks are not enabled",
				"type":    "service_unavailable",
			},
		})
		return
	}

	imageReq, err := helper.GetAndValidOpenAIImageRequest(c, relayMode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	taskID := model.GenerateTaskID()
	now := time.Now().Unix()
	task := &model.Task{
		CreatedAt:  now,
		UpdatedAt:  now,
		TaskID:     taskID,
		Platform:   constant.TaskPlatformOpenAIImage,
		UserId:     c.GetInt("id"),
		Group:      common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		ChannelId:  common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		Action:     action,
		Status:     model.TaskStatusQueued,
		SubmitTime: now,
		Progress:   "0%",
		Properties: model.Properties{
			Input:           imageReq.Prompt,
			OriginModelName: imageReq.Model,
			ImageSize:       imageReq.Size,
			ImageQuality:    imageReq.Quality,
		},
		PrivateData: model.TaskPrivateData{
			RelayTokenKey: c.GetString("token_key"),
			RequestBody:   base64.StdEncoding.EncodeToString(body),
			RequestType:   c.GetHeader("Content-Type"),
			RequestPath:   requestPath,
			RequestMethod: http.MethodPost,
			TokenId:       c.GetInt("token_id"),
		},
	}
	task.SetData(gin.H{
		"status": "queued",
		"model":  imageReq.Model,
	})
	if err := task.Insert(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "server_error",
			},
		})
		return
	}

	c.JSON(http.StatusAccepted, imageTaskResponse(c, task))
}

func GetImageTask(c *gin.Context) {
	taskID := c.Param("task_id")
	task, exists, err := model.GetByTaskId(c.GetInt("id"), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "server_error",
			},
		})
		return
	}
	if !exists || task.Platform != constant.TaskPlatformOpenAIImage {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "image task not found",
				"type":    "not_found_error",
			},
		})
		return
	}
	c.JSON(http.StatusOK, imageTaskResponse(c, task))
}

func GetImageHistory(c *gin.Context) {
	items, err := imageHistoryResponseItems(c, c.GetInt("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "server_error",
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"limit": model.ImageHistoryLimit,
	})
}

func imageHistoryResponseItems(c *gin.Context, userId int) ([]gin.H, error) {
	rows, err := model.ListUserImageHistory(userId, model.ImageHistoryLimit)
	if err != nil {
		return nil, err
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		url, err := service.ImageObjectURL(c.Request.Context(), row.ObjectKey)
		if err != nil {
			return nil, err
		}
		items = append(items, gin.H{
			"id":         row.ID,
			"task_id":    row.TaskID,
			"object_key": row.ObjectKey,
			"proxy_url":  imageContentProxyURL(row.ObjectKey),
			"src":        url,
			"url":        url,
			"source":     "url",
			"prompt":     row.Prompt,
			"model":      row.Model,
			"size":       row.Size,
			"quality":    row.Quality,
			"created_at": row.CreatedAt,
		})
	}
	return items, nil
}

func imageTaskResponse(c *gin.Context, task *model.Task) gin.H {
	status := imageTaskPublicStatus(task.Status)
	resp := gin.H{
		"id":           task.TaskID,
		"object":       "image.task",
		"status":       status,
		"progress":     task.Progress,
		"created_at":   task.SubmitTime,
		"started_at":   task.StartTime,
		"completed_at": task.FinishTime,
		"model":        task.Properties.OriginModelName,
	}
	if task.FailReason != "" {
		resp["error"] = gin.H{"message": task.FailReason}
	}
	if task.Status == model.TaskStatusSuccess {
		imageResp, err := imageTaskResult(c, task)
		if err == nil {
			resp["response"] = imageResp
			resp["data"] = imageResp.Data
			resp["created"] = imageResp.Created
		} else if !errors.Is(err, errImageTaskNoResult) {
			resp["error"] = gin.H{"message": err.Error()}
		}
	}
	return resp
}

var errImageTaskNoResult = errors.New("image task has no result")

func imageTaskResult(c *gin.Context, task *model.Task) (*dto.ImageResponse, error) {
	if len(task.Data) == 0 {
		return nil, errImageTaskNoResult
	}
	var imageResp dto.ImageResponse
	if err := task.GetData(&imageResp); err != nil {
		return nil, err
	}
	for i := range imageResp.Data {
		if imageResp.Data[i].ObjectKey == "" {
			continue
		}
		url, err := service.ImageObjectURL(c.Request.Context(), imageResp.Data[i].ObjectKey)
		if err != nil {
			return nil, err
		}
		imageResp.Data[i].Url = url
		imageResp.Data[i].ProxyUrl = imageContentProxyURL(imageResp.Data[i].ObjectKey)
	}
	return &imageResp, nil
}

func imageTaskPublicStatus(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "succeeded"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusInProgress:
		return "running"
	default:
		return "queued"
	}
}
