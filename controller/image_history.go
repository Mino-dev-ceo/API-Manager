package controller

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetUserImageHistory(c *gin.Context) {
	items, err := imageHistoryResponseItems(c, c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"items": items,
		"limit": model.ImageHistoryLimit,
	})
}

func GetUserImageContent(c *gin.Context) {
	objectKey := strings.TrimSpace(c.Query("key"))
	if objectKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "image key is required"}})
		return
	}
	if _, ok, err := model.GetUserImageHistoryByObjectKey(c.GetInt("id"), objectKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	} else if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "image not found"}})
		return
	}
	data, contentType, err := service.ReadImageObject(c.Request.Context(), objectKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, data)
}

func imageContentProxyURL(objectKey string) string {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return ""
	}
	return "/api/user/image-content?key=" + url.QueryEscape(objectKey)
}
