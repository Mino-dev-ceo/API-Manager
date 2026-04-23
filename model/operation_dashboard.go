package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
)

type OperationMetric struct {
	TodayRequests int64 `json:"today_requests"`
	TodayTokens   int64 `json:"today_tokens"`
	TodayQuota    int64 `json:"today_quota"`
	ActiveUsers   int64 `json:"active_users"`
	TotalUsers    int64 `json:"total_users"`
}

type OperationChannelHealth struct {
	TotalChannels    int64                    `json:"total_channels"`
	EnabledChannels  int64                    `json:"enabled_channels"`
	DisabledChannels int64                    `json:"disabled_channels"`
	AvgResponseMs    float64                  `json:"avg_response_ms"`
	RecentErrors     int64                    `json:"recent_errors"`
	Channels         []OperationChannelStatus `json:"channels"`
}

type OperationChannelStatus struct {
	Id             int    `json:"id"`
	Name           string `json:"name"`
	Status         int    `json:"status"`
	ResponseTimeMs int    `json:"response_time_ms"`
	UsedQuota      int64  `json:"used_quota"`
	ErrorCount     int64  `json:"error_count"`
}

type OperationModelStat struct {
	ModelName string `json:"model_name"`
	Requests  int64  `json:"requests"`
	Tokens    int64  `json:"tokens"`
	Quota     int64  `json:"quota"`
	AvgMs     float64  `json:"avg_ms"`
}

type OperationRiskUser struct {
	UserId    int    `json:"user_id"`
	Username  string `json:"username"`
	Requests  int64  `json:"requests"`
	Tokens    int64  `json:"tokens"`
	Quota     int64  `json:"quota"`
	AvgMs     float64  `json:"avg_ms"`
	RiskLevel string `json:"risk_level"`
}

type OperationPaymentStat struct {
	TodayTopupAmount float64               `json:"today_topup_amount"`
	PendingOrders    int64                 `json:"pending_orders"`
	PaidOrders       int64                 `json:"paid_orders"`
	FailedOrders     int64                 `json:"failed_orders"`
	RecentOrders     []OperationTopupOrder `json:"recent_orders"`
}

type OperationTopupOrder struct {
	Id            int     `json:"id"`
	UserId        int     `json:"user_id"`
	Amount        int64   `json:"amount"`
	Money         float64 `json:"money"`
	TradeNo       string  `json:"trade_no"`
	PaymentMethod string  `json:"payment_method"`
	Status        string  `json:"status"`
	CreateTime    int64   `json:"create_time"`
	CompleteTime  int64   `json:"complete_time"`
}

type OperationTokenStat struct {
	TotalTokens    int64                 `json:"total_tokens"`
	ActiveTokens   int64                 `json:"active_tokens"`
	DisabledTokens int64                 `json:"disabled_tokens"`
	TopTokens      []OperationTokenUsage `json:"top_tokens"`
}

type OperationTokenUsage struct {
	Id           int    `json:"id"`
	UserId       int    `json:"user_id"`
	Name         string `json:"name"`
	Status       int    `json:"status"`
	UsedQuota    int    `json:"used_quota"`
	AccessedTime int64  `json:"accessed_time"`
}

type OperationRealtimeLog struct {
	Id               int    `json:"id"`
	UserId           int    `json:"user_id"`
	Username         string `json:"username"`
	ModelName        string `json:"model_name"`
	TokenName        string `json:"token_name"`
	ChannelId        int    `json:"channel_id"`
	Quota            int    `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	UseTime          int    `json:"use_time"`
	Type             int    `json:"type"`
	Content          string `json:"content"`
	CreatedAt        int64  `json:"created_at"`
}

type OperationSystemStat struct {
	DatabaseOk bool   `json:"database_ok"`
	Uptime     int64  `json:"uptime"`
	Version    string `json:"version"`
}

type OperationDashboard struct {
	Overview     OperationMetric        `json:"overview"`
	Channel      OperationChannelHealth `json:"channel"`
	Models       []OperationModelStat   `json:"models"`
	RiskUsers    []OperationRiskUser    `json:"risk_users"`
	Payment      OperationPaymentStat   `json:"payment"`
	Tokens       OperationTokenStat     `json:"tokens"`
	RealtimeLogs []OperationRealtimeLog `json:"realtime_logs"`
	System       OperationSystemStat    `json:"system"`
}

func GetOperationDashboard() (*OperationDashboard, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()

	dashboard := &OperationDashboard{
		System: OperationSystemStat{
			DatabaseOk: true,
			Uptime:     common.GetTimestamp() - common.StartTime,
			Version:    common.Version,
		},
	}

	if err := fillOperationOverview(dashboard, todayStart); err != nil {
		return nil, err
	}
	if err := fillOperationChannels(dashboard, todayStart); err != nil {
		return nil, err
	}
	if err := fillOperationModels(dashboard, todayStart); err != nil {
		return nil, err
	}
	if err := fillOperationRiskUsers(dashboard, todayStart); err != nil {
		return nil, err
	}
	if err := fillOperationPayments(dashboard, todayStart); err != nil {
		return nil, err
	}
	if err := fillOperationTokens(dashboard); err != nil {
		return nil, err
	}
	if err := fillOperationRealtimeLogs(dashboard); err != nil {
		return nil, err
	}

	return dashboard, nil
}

func fillOperationOverview(dashboard *OperationDashboard, todayStart int64) error {
	if err := LOG_DB.Model(&Log{}).Where("type = ? AND created_at >= ?", LogTypeConsume, todayStart).Count(&dashboard.Overview.TodayRequests).Error; err != nil {
		return err
	}
	if err := LOG_DB.Model(&Log{}).Where("type = ? AND created_at >= ?", LogTypeConsume, todayStart).
		Select("COALESCE(SUM(prompt_tokens + completion_tokens), 0)").Scan(&dashboard.Overview.TodayTokens).Error; err != nil {
		return err
	}
	if err := LOG_DB.Model(&Log{}).Where("type = ? AND created_at >= ?", LogTypeConsume, todayStart).
		Select("COALESCE(SUM(quota), 0)").Scan(&dashboard.Overview.TodayQuota).Error; err != nil {
		return err
	}
	if err := LOG_DB.Model(&Log{}).Where("type = ? AND created_at >= ?", LogTypeConsume, todayStart).
		Distinct("user_id").Count(&dashboard.Overview.ActiveUsers).Error; err != nil {
		return err
	}
	return DB.Model(&User{}).Count(&dashboard.Overview.TotalUsers).Error
}

func fillOperationChannels(dashboard *OperationDashboard, todayStart int64) error {
	var channels []Channel
	if err := DB.Model(&Channel{}).Order("status asc, response_time desc").Limit(8).Find(&channels).Error; err != nil {
		return err
	}
	if err := DB.Model(&Channel{}).Count(&dashboard.Channel.TotalChannels).Error; err != nil {
		return err
	}
	if err := DB.Model(&Channel{}).Where("status = ?", common.ChannelStatusEnabled).Count(&dashboard.Channel.EnabledChannels).Error; err != nil {
		return err
	}
	if err := DB.Model(&Channel{}).Where("status <> ?", common.ChannelStatusEnabled).Count(&dashboard.Channel.DisabledChannels).Error; err != nil {
		return err
	}
	if err := DB.Model(&Channel{}).Select("COALESCE(AVG(response_time), 0)").Scan(&dashboard.Channel.AvgResponseMs).Error; err != nil {
		return err
	}
	if err := LOG_DB.Model(&Log{}).Where("type = ? AND created_at >= ?", LogTypeError, todayStart).Count(&dashboard.Channel.RecentErrors).Error; err != nil {
		return err
	}

	type channelError struct {
		ChannelId int
		Errors    int64
	}
	var channelErrors []channelError
	if err := LOG_DB.Model(&Log{}).Select("channel_id, COUNT(*) AS errors").
		Where("type = ? AND created_at >= ?", LogTypeError, todayStart).
		Group("channel_id").Scan(&channelErrors).Error; err != nil {
		return err
	}
	errorMap := make(map[int]int64, len(channelErrors))
	for _, row := range channelErrors {
		errorMap[row.ChannelId] = row.Errors
	}

	dashboard.Channel.Channels = make([]OperationChannelStatus, 0, len(channels))
	for _, channel := range channels {
		dashboard.Channel.Channels = append(dashboard.Channel.Channels, OperationChannelStatus{
			Id:             channel.Id,
			Name:           channel.Name,
			Status:         channel.Status,
			ResponseTimeMs: channel.ResponseTime,
			UsedQuota:      channel.UsedQuota,
			ErrorCount:     errorMap[channel.Id],
		})
	}
	return nil
}

func fillOperationModels(dashboard *OperationDashboard, todayStart int64) error {
	if err := LOG_DB.Model(&Log{}).
		Select("model_name, COUNT(*) AS requests, COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(SUM(quota), 0) AS quota, COALESCE(AVG(use_time), 0) AS avg_ms").
		Where("type = ? AND created_at >= ? AND model_name <> ''", LogTypeConsume, todayStart).
		Group("model_name").
		Order("quota DESC").
		Limit(8).
		Scan(&dashboard.Models).Error; err != nil {
		return err
	}
	for index := range dashboard.Models {
		dashboard.Models[index].AvgMs = dashboard.Models[index].AvgMs * 1000
	}
	return nil
}

func fillOperationRiskUsers(dashboard *OperationDashboard, todayStart int64) error {
	if err := LOG_DB.Model(&Log{}).
		Select("user_id, username, COUNT(*) AS requests, COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS tokens, COALESCE(SUM(quota), 0) AS quota, COALESCE(AVG(use_time), 0) AS avg_ms").
		Where("type = ? AND created_at >= ?", LogTypeConsume, todayStart).
		Group("user_id, username").
		Order("quota DESC").
		Limit(8).
		Scan(&dashboard.RiskUsers).Error; err != nil {
		return err
	}
	for index := range dashboard.RiskUsers {
		row := &dashboard.RiskUsers[index]
		row.AvgMs = row.AvgMs * 1000
		row.RiskLevel = "正常"
		if row.Requests >= 1000 || row.Quota >= 1000000 {
			row.RiskLevel = "高消耗"
		} else if row.Requests >= 200 || row.AvgMs >= 15000 {
			row.RiskLevel = "需关注"
		}
	}
	return nil
}

func fillOperationPayments(dashboard *OperationDashboard, todayStart int64) error {
	if err := DB.Model(&TopUp{}).Where("status = ? AND complete_time >= ?", common.TopUpStatusSuccess, todayStart).
		Select("COALESCE(SUM(money), 0)").Scan(&dashboard.Payment.TodayTopupAmount).Error; err != nil {
		return err
	}
	if err := DB.Model(&TopUp{}).Where("status = ?", common.TopUpStatusPending).Count(&dashboard.Payment.PendingOrders).Error; err != nil {
		return err
	}
	if err := DB.Model(&TopUp{}).Where("status = ?", common.TopUpStatusSuccess).Count(&dashboard.Payment.PaidOrders).Error; err != nil {
		return err
	}
	if err := DB.Model(&TopUp{}).Where("status IN ?", []string{common.TopUpStatusFailed, common.TopUpStatusExpired}).Count(&dashboard.Payment.FailedOrders).Error; err != nil {
		return err
	}

	var topups []TopUp
	if err := DB.Model(&TopUp{}).Order("id desc").Limit(8).Find(&topups).Error; err != nil {
		return err
	}
	dashboard.Payment.RecentOrders = make([]OperationTopupOrder, 0, len(topups))
	for _, topup := range topups {
		dashboard.Payment.RecentOrders = append(dashboard.Payment.RecentOrders, OperationTopupOrder{
			Id:            topup.Id,
			UserId:        topup.UserId,
			Amount:        topup.Amount,
			Money:         topup.Money,
			TradeNo:       topup.TradeNo,
			PaymentMethod: topup.PaymentMethod,
			Status:        topup.Status,
			CreateTime:    topup.CreateTime,
			CompleteTime:  topup.CompleteTime,
		})
	}
	return nil
}

func fillOperationTokens(dashboard *OperationDashboard) error {
	if err := DB.Model(&Token{}).Count(&dashboard.Tokens.TotalTokens).Error; err != nil {
		return err
	}
	if err := DB.Model(&Token{}).Where("status = ?", common.TokenStatusEnabled).Count(&dashboard.Tokens.ActiveTokens).Error; err != nil {
		return err
	}
	if err := DB.Model(&Token{}).Where("status <> ?", common.TokenStatusEnabled).Count(&dashboard.Tokens.DisabledTokens).Error; err != nil {
		return err
	}
	var tokens []Token
	if err := DB.Model(&Token{}).Order("used_quota desc, accessed_time desc").Limit(8).Find(&tokens).Error; err != nil {
		return err
	}
	dashboard.Tokens.TopTokens = make([]OperationTokenUsage, 0, len(tokens))
	for _, token := range tokens {
		dashboard.Tokens.TopTokens = append(dashboard.Tokens.TopTokens, OperationTokenUsage{
			Id:           token.Id,
			UserId:       token.UserId,
			Name:         token.Name,
			Status:       token.Status,
			UsedQuota:    token.UsedQuota,
			AccessedTime: token.AccessedTime,
		})
	}
	return nil
}

func fillOperationRealtimeLogs(dashboard *OperationDashboard) error {
	var logs []Log
	if err := LOG_DB.Model(&Log{}).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError}).
		Order("id desc").
		Limit(20).
		Find(&logs).Error; err != nil {
		return err
	}
	dashboard.RealtimeLogs = make([]OperationRealtimeLog, 0, len(logs))
	for _, log := range logs {
		dashboard.RealtimeLogs = append(dashboard.RealtimeLogs, OperationRealtimeLog{
			Id:               log.Id,
			UserId:           log.UserId,
			Username:         log.Username,
			ModelName:        log.ModelName,
			TokenName:        log.TokenName,
			ChannelId:        log.ChannelId,
			Quota:            log.Quota,
			PromptTokens:     log.PromptTokens,
			CompletionTokens: log.CompletionTokens,
			UseTime:          log.UseTime,
			Type:             log.Type,
			Content:          log.Content,
			CreatedAt:        log.CreatedAt,
		})
	}
	return nil
}
