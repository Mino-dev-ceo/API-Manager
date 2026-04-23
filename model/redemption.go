package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Redemption struct {
	Id           int            `json:"id"`
	UserId       int            `json:"user_id"`
	Key          string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status       int            `json:"status" gorm:"default:1"`
	Name         string         `json:"name" gorm:"index"`
	Quota        int            `json:"quota" gorm:"default:100"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime int64          `json:"redeemed_time" gorm:"bigint"`
	Count        int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId   int            `json:"used_user_id"`
	DeletedAt    gorm.DeletedAt `gorm:"index"`
	ExpiredTime  int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

func GetAllRedemptions(startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	// 开始事务
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 获取总数
	err = tx.Model(&Redemption{}).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 获取分页数据
	err = tx.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetUserRedemptions(userId int, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&Redemption{}).Where("user_id = ?", userId)
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return redemptions, total, nil
}

func SearchRedemptions(keyword string, startIdx int, num int) (redemptions []*Redemption, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Build query based on keyword type
	query := tx.Model(&Redemption{})

	// Only try to convert to ID if the string represents a valid integer
	if id, err := strconv.Atoi(keyword); err == nil {
		query = query.Where("id = ? OR name LIKE ?", id, keyword+"%")
	} else {
		query = query.Where("name LIKE ?", keyword+"%")
	}

	// Get total count
	err = query.Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated data
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&redemptions).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return redemptions, total, nil
}

func GetRedemptionById(id int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	var err error = nil
	err = DB.First(&redemption, "id = ?", id).Error
	return &redemption, err
}

func CreateUserRedemptions(userId int, name string, quota int, count int, expiredTime int64) (keys []string, err error) {
	if userId == 0 {
		return nil, errors.New("无效的 user id")
	}
	if quota <= 0 {
		return nil, errors.New("兑换额度必须大于 0")
	}
	if count <= 0 {
		return nil, errors.New("兑换码个数必须大于 0")
	}
	if count > 100 {
		return nil, errors.New("一次兑换码批量生成的个数不能大于 100")
	}
	if quota > math.MaxInt/count {
		return nil, errors.New("兑换额度过大")
	}

	totalQuota := quota * count
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "quota").First(&user, "id = ?", userId).Error; err != nil {
			return err
		}
		if user.Quota < totalQuota {
			return errors.New("可用余额不足，无法生成兑换码")
		}
		if err := tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota - ?", totalQuota)).Error; err != nil {
			return err
		}

		keys = make([]string, 0, count)
		for i := 0; i < count; i++ {
			key := common.GetUUID()
			record := Redemption{
				UserId:      userId,
				Name:        name,
				Key:         key,
				Status:      common.RedemptionCodeStatusEnabled,
				CreatedTime: common.GetTimestamp(),
				Quota:       quota,
				ExpiredTime: expiredTime,
			}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := cacheDecrUserQuota(userId, int64(totalQuota)); err != nil {
		common.SysLog("failed to decrease user quota cache after creating redemptions: " + err.Error())
	}

	RecordLog(userId, LogTypeManage, fmt.Sprintf("生成兑换码 %d 个，总额度 %s", count, logger.LogQuota(totalQuota)))
	return keys, nil
}

func Redeem(key string, userId int) (quota int, err error) {
	if key == "" {
		return 0, errors.New("未提供兑换码")
	}
	if userId == 0 {
		return 0, errors.New("无效的 user id")
	}
	redemption := &Redemption{}

	keyCol := "`key`"
	if common.UsingPostgreSQL {
		keyCol = `"key"`
	}
	common.RandomSleep()
	err = DB.Transaction(func(tx *gorm.DB) error {
		err := tx.Set("gorm:query_option", "FOR UPDATE").Where(keyCol+" = ?", key).First(redemption).Error
		if err != nil {
			return errors.New("无效的兑换码")
		}
		if redemption.Status != common.RedemptionCodeStatusEnabled {
			return errors.New("该兑换码已被使用")
		}
		if redemption.ExpiredTime != 0 && redemption.ExpiredTime < common.GetTimestamp() {
			return errors.New("该兑换码已过期")
		}
		err = tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error
		if err != nil {
			return err
		}
		redemption.RedeemedTime = common.GetTimestamp()
		redemption.Status = common.RedemptionCodeStatusUsed
		redemption.UsedUserId = userId
		err = tx.Save(redemption).Error
		return err
	})
	if err != nil {
		common.SysError("redemption failed: " + err.Error())
		return 0, ErrRedeemFailed
	}
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("通过兑换码充值 %s，兑换码ID %d", logger.LogQuota(redemption.Quota), redemption.Id))
	return redemption.Quota, nil
}

func (redemption *Redemption) Insert() error {
	var err error
	err = DB.Create(redemption).Error
	return err
}

func (redemption *Redemption) SelectUpdate() error {
	// This can update zero values
	return DB.Model(redemption).Select("redeemed_time", "status").Updates(redemption).Error
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (redemption *Redemption) Update() error {
	var err error
	err = DB.Model(redemption).Select("name", "status", "quota", "redeemed_time", "expired_time").Updates(redemption).Error
	return err
}

func (redemption *Redemption) Delete() error {
	var err error
	err = DB.Delete(redemption).Error
	return err
}

func DeleteRedemptionById(id int) (err error) {
	if id == 0 {
		return errors.New("id 为空！")
	}
	redemption := Redemption{Id: id}
	err = DB.Where(redemption).First(&redemption).Error
	if err != nil {
		return err
	}
	return redemption.Delete()
}

func RevokeUserRedemptionById(id int, userId int) (*Redemption, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	if userId == 0 {
		return nil, errors.New("无效的 user id")
	}

	redemption := &Redemption{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(redemption, "id = ? AND user_id = ?", id, userId).Error; err != nil {
			return err
		}
		if redemption.Status == common.RedemptionCodeStatusUsed || redemption.UsedUserId != 0 {
			return errors.New("该兑换码已被使用，无法撤销")
		}
		if redemption.Status == common.RedemptionCodeStatusDisabled {
			return errors.New("该兑换码已撤销")
		}

		redemption.Status = common.RedemptionCodeStatusDisabled
		if err := tx.Model(redemption).Select("status").Updates(redemption).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", userId).Update("quota", gorm.Expr("quota + ?", redemption.Quota)).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := cacheIncrUserQuota(userId, int64(redemption.Quota)); err != nil {
		common.SysLog("failed to increase user quota cache after revoking redemption: " + err.Error())
	}

	RecordLog(userId, LogTypeManage, fmt.Sprintf("撤销兑换码 %d，返还额度 %s", redemption.Id, logger.LogQuota(redemption.Quota)))
	return redemption, nil
}

func DeleteInvalidRedemptions() (int64, error) {
	now := common.GetTimestamp()
	result := DB.Where("status IN ? OR (status = ? AND expired_time != 0 AND expired_time < ?)", []int{common.RedemptionCodeStatusUsed, common.RedemptionCodeStatusDisabled}, common.RedemptionCodeStatusEnabled, now).Delete(&Redemption{})
	return result.RowsAffected, result.Error
}
