package openai

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func maybePersistOpenAIImageResponse(c *gin.Context, info *relaycommon.RelayInfo, responseBody []byte) []byte {
	if c == nil || info == nil || len(responseBody) == 0 {
		return responseBody
	}
	if info.RelayMode != relayconstant.RelayModeImagesGenerations && info.RelayMode != relayconstant.RelayModeImagesEdits {
		return responseBody
	}

	var imageResp dto.ImageResponse
	if err := common.Unmarshal(responseBody, &imageResp); err != nil || len(imageResp.Data) == 0 {
		return responseBody
	}

	opts := imageResponsePersistOptions(info)
	stored, err := service.PersistImageResponse(c.Request.Context(), &imageResp, opts)
	if err != nil {
		if errors.Is(err, service.ErrImageStorageNotConfigured) {
			logger.LogWarn(c, fmt.Sprintf("skip persisting sync image response because storage is not configured request_id=%s", info.RequestId))
			return responseBody
		}
		logger.LogError(c, fmt.Sprintf("persist sync image response failed request_id=%s: %v", info.RequestId, err))
		return responseBody
	}
	if stored == 0 {
		return responseBody
	}

	nextBody, err := common.Marshal(imageResp)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("marshal persisted sync image response failed request_id=%s: %v", info.RequestId, err))
		return responseBody
	}
	logger.LogInfo(c, fmt.Sprintf("persisted sync image response request_id=%s user_id=%d stored=%d", info.RequestId, info.UserId, stored))
	return nextBody
}

func imageResponsePersistOptions(info *relaycommon.RelayInfo) service.ImageResponsePersistOptions {
	opts := service.ImageResponsePersistOptions{
		UserId:          info.UserId,
		RequestId:       info.RequestId,
		Model:           info.OriginModelName,
		Source:          "sync",
		StoreHistory:    common.GetEnvOrDefaultBool("IMAGE_SYNC_STORE_HISTORY", false),
		StoreRemoteURLs: common.GetEnvOrDefaultBool("IMAGE_SYNC_STORE_REMOTE_URLS", false),
	}

	switch req := info.Request.(type) {
	case *dto.ImageRequest:
		applyImageRequestPersistOptions(&opts, req)
	}
	return opts
}

func applyImageRequestPersistOptions(opts *service.ImageResponsePersistOptions, req *dto.ImageRequest) {
	if opts == nil || req == nil {
		return
	}
	opts.Prompt = req.Prompt
	opts.Size = req.Size
	opts.Quality = req.Quality
	if opts.Model == "" {
		opts.Model = req.Model
	}
}
