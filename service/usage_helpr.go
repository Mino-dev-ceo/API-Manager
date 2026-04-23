package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

//func GetPromptTokens(textRequest dto.GeneralOpenAIRequest, relayMode int) (int, error) {
//	switch relayMode {
//	case constant.RelayModeChatCompletions:
//		return CountTokenMessages(textRequest.Messages, textRequest.Model)
//	case constant.RelayModeCompletions:
//		return CountTokenInput(textRequest.Prompt, textRequest.Model), nil
//	case constant.RelayModeModerations:
//		return CountTokenInput(textRequest.Input, textRequest.Model), nil
//	}
//	return 0, errors.New("unknown relay mode")
//}

func ResponseText2Usage(c *gin.Context, responseText string, modeName string, promptTokens int) *dto.Usage {
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := &dto.Usage{}
	usage.PromptTokens = promptTokens
	usage.CompletionTokens = EstimateTokenByModel(modeName, responseText)
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}

func nonNegativeUsageInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func NormalizeUsage(usage *dto.Usage) {
	if usage == nil {
		return
	}

	usage.PromptTokens = nonNegativeUsageInt(usage.PromptTokens)
	usage.CompletionTokens = nonNegativeUsageInt(usage.CompletionTokens)
	usage.TotalTokens = nonNegativeUsageInt(usage.TotalTokens)
	usage.InputTokens = nonNegativeUsageInt(usage.InputTokens)
	usage.OutputTokens = nonNegativeUsageInt(usage.OutputTokens)
	usage.PromptCacheHitTokens = nonNegativeUsageInt(usage.PromptCacheHitTokens)
	usage.ClaudeCacheCreation5mTokens = nonNegativeUsageInt(usage.ClaudeCacheCreation5mTokens)
	usage.ClaudeCacheCreation1hTokens = nonNegativeUsageInt(usage.ClaudeCacheCreation1hTokens)

	usage.PromptTokensDetails.CachedTokens = nonNegativeUsageInt(usage.PromptTokensDetails.CachedTokens)
	usage.PromptTokensDetails.CachedCreationTokens = nonNegativeUsageInt(usage.PromptTokensDetails.CachedCreationTokens)
	usage.PromptTokensDetails.TextTokens = nonNegativeUsageInt(usage.PromptTokensDetails.TextTokens)
	usage.PromptTokensDetails.AudioTokens = nonNegativeUsageInt(usage.PromptTokensDetails.AudioTokens)
	usage.PromptTokensDetails.ImageTokens = nonNegativeUsageInt(usage.PromptTokensDetails.ImageTokens)
	usage.CompletionTokenDetails.TextTokens = nonNegativeUsageInt(usage.CompletionTokenDetails.TextTokens)
	usage.CompletionTokenDetails.AudioTokens = nonNegativeUsageInt(usage.CompletionTokenDetails.AudioTokens)
	usage.CompletionTokenDetails.ReasoningTokens = nonNegativeUsageInt(usage.CompletionTokenDetails.ReasoningTokens)
	if usage.InputTokensDetails != nil {
		usage.InputTokensDetails.CachedTokens = nonNegativeUsageInt(usage.InputTokensDetails.CachedTokens)
		usage.InputTokensDetails.CachedCreationTokens = nonNegativeUsageInt(usage.InputTokensDetails.CachedCreationTokens)
		usage.InputTokensDetails.TextTokens = nonNegativeUsageInt(usage.InputTokensDetails.TextTokens)
		usage.InputTokensDetails.AudioTokens = nonNegativeUsageInt(usage.InputTokensDetails.AudioTokens)
		usage.InputTokensDetails.ImageTokens = nonNegativeUsageInt(usage.InputTokensDetails.ImageTokens)
	}

	if usage.PromptTokens == 0 && usage.InputTokens > 0 {
		usage.PromptTokens = usage.InputTokens
	}
	if usage.CompletionTokens == 0 && usage.OutputTokens > 0 {
		usage.CompletionTokens = usage.OutputTokens
	}
	if usage.InputTokens == 0 && usage.PromptTokens > 0 {
		usage.InputTokens = usage.PromptTokens
	}
	if usage.OutputTokens == 0 && usage.CompletionTokens > 0 {
		usage.OutputTokens = usage.CompletionTokens
	}

	calculatedTotal := usage.PromptTokens + usage.CompletionTokens
	if calculatedTotal == 0 && usage.TotalTokens > 0 {
		usage.PromptTokens = usage.TotalTokens
		usage.InputTokens = usage.TotalTokens
		calculatedTotal = usage.TotalTokens
	}
	if usage.TotalTokens == 0 || usage.TotalTokens < calculatedTotal {
		usage.TotalTokens = calculatedTotal
	}
}

func ValidUsage(usage *dto.Usage) bool {
	NormalizeUsage(usage)
	return usage != nil && usage.TotalTokens > 0
}
