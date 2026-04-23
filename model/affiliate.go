package model

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const AffiliateCommissionStatusSettled = "settled"

type AffiliateCommission struct {
	Id                int     `json:"id"`
	InviterId         int     `json:"inviter_id" gorm:"index"`
	InviteeId         int     `json:"invitee_id" gorm:"index"`
	TopUpId           int     `json:"topup_id" gorm:"index"`
	TradeNo           string  `json:"trade_no" gorm:"uniqueIndex;type:varchar(255)"`
	BaseQuota         int     `json:"base_quota"`
	CommissionQuota   int     `json:"commission_quota"`
	CommissionPercent float64 `json:"commission_percent"`
	Status            string  `json:"status" gorm:"type:varchar(32);index"`
	CreateTime        int64   `json:"create_time"`
}

type AffiliateChild struct {
	UserId          int     `json:"user_id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	Group           string  `json:"group"`
	Status          int     `json:"status"`
	TopupCount      int64   `json:"topup_count"`
	TopupQuota      int     `json:"topup_quota"`
	TopupMoney      float64 `json:"topup_money"`
	CommissionQuota int     `json:"commission_quota"`
}

type AffiliateOverview struct {
	Link              string                `json:"link"`
	Code              string                `json:"code"`
	InvitedCount      int                   `json:"invited_count"`
	PendingQuota      int                   `json:"pending_quota"`
	HistoryQuota      int                   `json:"history_quota"`
	TransferredQuota  int                   `json:"transferred_quota"`
	InviterId         int                   `json:"inviter_id"`
	CommissionEnabled bool                  `json:"commission_enabled"`
	CommissionPercent float64               `json:"commission_percent"`
	Children          []AffiliateChild      `json:"children"`
	Records           []AffiliateCommission `json:"records"`
}

func (AffiliateCommission) TableName() string {
	return "affiliate_commissions"
}

func GrantAffiliateCommission(tx *gorm.DB, topUp *TopUp, quotaToAdd int) error {
	if tx == nil || topUp == nil || quotaToAdd <= 0 {
		return nil
	}
	if !common.AffiliateCommissionEnabled || common.AffiliateCommissionPercent <= 0 {
		return nil
	}

	var invitee User
	if err := tx.Select("id", "inviter_id").Where("id = ?", topUp.UserId).First(&invitee).Error; err != nil {
		return err
	}
	if invitee.InviterId <= 0 || invitee.InviterId == invitee.Id {
		return nil
	}

	commission := int(math.Round(decimal.NewFromInt(int64(quotaToAdd)).
		Mul(decimal.NewFromFloat(common.AffiliateCommissionPercent)).
		Div(decimal.NewFromInt(100)).
		InexactFloat64()))
	if commission <= 0 {
		return nil
	}

	var inviter User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "aff_quota", "aff_history").Where("id = ?", invitee.InviterId).First(&inviter).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	record := AffiliateCommission{
		InviterId:         inviter.Id,
		InviteeId:         invitee.Id,
		TopUpId:           topUp.Id,
		TradeNo:           topUp.TradeNo,
		BaseQuota:         quotaToAdd,
		CommissionQuota:   commission,
		CommissionPercent: common.AffiliateCommissionPercent,
		Status:            AffiliateCommissionStatusSettled,
		CreateTime:        common.GetTimestamp(),
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}

	return tx.Model(&User{}).Where("id = ?", inviter.Id).Updates(map[string]interface{}{
		"aff_quota":   gorm.Expr("aff_quota + ?", commission),
		"aff_history": gorm.Expr("aff_history + ?", commission),
	}).Error
}

func GetAffiliateOverview(userId int, origin string) (*AffiliateOverview, error) {
	var current User
	if err := DB.Where("id = ?", userId).First(&current).Error; err != nil {
		return nil, err
	}
	if current.AffCode == "" {
		current.AffCode = common.GetRandomString(4)
		if err := DB.Model(&current).Update("aff_code", current.AffCode).Error; err != nil {
			return nil, err
		}
	}

	var children []User
	if err := DB.Select("id", "username", "email", commonGroupCol, "status").Where("inviter_id = ?", current.Id).Order("id desc").Limit(200).Find(&children).Error; err != nil {
		return nil, err
	}

	items := make([]AffiliateChild, 0, len(children))
	for _, child := range children {
		item := AffiliateChild{
			UserId:   child.Id,
			Username: child.Username,
			Email:    child.Email,
			Group:    child.Group,
			Status:   child.Status,
		}
		_ = DB.Model(&TopUp{}).
			Where("user_id = ? AND status = ?", child.Id, common.TopUpStatusSuccess).
			Select("COUNT(*)").
			Scan(&item.TopupCount).Error
		var topup struct {
			Amount int64
			Money  float64
		}
		_ = DB.Model(&TopUp{}).
			Where("user_id = ? AND status = ?", child.Id, common.TopUpStatusSuccess).
			Select("COALESCE(SUM(amount), 0) AS amount, COALESCE(SUM(money), 0) AS money").
			Scan(&topup).Error
		item.TopupQuota = int(decimal.NewFromInt(topup.Amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
		item.TopupMoney = topup.Money
		_ = DB.Model(&AffiliateCommission{}).
			Where("inviter_id = ? AND invitee_id = ?", current.Id, child.Id).
			Select("COALESCE(SUM(commission_quota), 0)").
			Scan(&item.CommissionQuota).Error
		items = append(items, item)
	}

	var records []AffiliateCommission
	if err := DB.Where("inviter_id = ?", current.Id).Order("id desc").Limit(100).Find(&records).Error; err != nil {
		return nil, err
	}

	link := ""
	origin = strings.TrimRight(origin, "/")
	if origin != "" && current.AffCode != "" {
		link = origin + "/register?aff=" + current.AffCode
	}

	return &AffiliateOverview{
		Link:              link,
		Code:              current.AffCode,
		InvitedCount:      current.AffCount,
		PendingQuota:      current.AffQuota,
		HistoryQuota:      current.AffHistoryQuota,
		TransferredQuota:  maxInt(0, current.AffHistoryQuota-current.AffQuota),
		InviterId:         current.InviterId,
		CommissionEnabled: common.AffiliateCommissionEnabled,
		CommissionPercent: common.AffiliateCommissionPercent,
		Children:          items,
		Records:           records,
	}, nil
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
