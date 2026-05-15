package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const ImageHistoryLimit = 5

type ImageHistory struct {
	ID        int64  `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt int64  `json:"created_at" gorm:"index;index:idx_image_history_user_created,priority:2"`
	UpdatedAt int64  `json:"updated_at"`
	UserId    int    `json:"user_id" gorm:"index:idx_image_history_user_created,priority:1;uniqueIndex:idx_image_history_user_object,priority:1"`
	TaskID    string `json:"task_id" gorm:"type:varchar(191);index"`
	ObjectKey string `json:"object_key" gorm:"type:varchar(255);not null;uniqueIndex:idx_image_history_user_object,priority:2"`
	Prompt    string `json:"prompt" gorm:"type:text"`
	Model     string `json:"model" gorm:"type:varchar(191)"`
	Size      string `json:"size" gorm:"type:varchar(64)"`
	Quality   string `json:"quality" gorm:"type:varchar(64)"`
	Source    string `json:"source" gorm:"type:varchar(32)"`
}

func ListUserImageHistory(userId int, limit int) ([]*ImageHistory, error) {
	if limit <= 0 || limit > ImageHistoryLimit {
		limit = ImageHistoryLimit
	}
	var items []*ImageHistory
	err := DB.Where("user_id = ?", userId).
		Order("created_at desc, id desc").
		Limit(limit).
		Find(&items).Error
	return items, err
}

func GetUserImageHistoryByObjectKey(userId int, objectKey string) (*ImageHistory, bool, error) {
	var item ImageHistory
	err := DB.Where("user_id = ? AND object_key = ?", userId, objectKey).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

func AddImageHistoryAndTrim(item *ImageHistory, limit int) ([]string, error) {
	if limit <= 0 {
		limit = ImageHistoryLimit
	}
	now := time.Now().Unix()
	if item.CreatedAt == 0 {
		item.CreatedAt = now
	}
	item.UpdatedAt = now

	var deletedKeys []string
	err := DB.Transaction(func(tx *gorm.DB) error {
		attrs := *item
		if err := tx.Where("user_id = ? AND object_key = ?", item.UserId, item.ObjectKey).
			Attrs(ImageHistory{
				CreatedAt: attrs.CreatedAt,
				UserId:    attrs.UserId,
				ObjectKey: attrs.ObjectKey,
				TaskID:    attrs.TaskID,
				Prompt:    attrs.Prompt,
				Model:     attrs.Model,
				Size:      attrs.Size,
				Quality:   attrs.Quality,
				Source:    attrs.Source,
			}).
			Assign(ImageHistory{
				UpdatedAt: attrs.UpdatedAt,
				TaskID:    attrs.TaskID,
				Prompt:    attrs.Prompt,
				Model:     attrs.Model,
				Size:      attrs.Size,
				Quality:   attrs.Quality,
				Source:    attrs.Source,
			}).
			FirstOrCreate(item).Error; err != nil {
			return err
		}

		var stale []*ImageHistory
		if err := tx.Where("user_id = ?", item.UserId).
			Order("created_at desc, id desc").
			Limit(1000).
			Offset(limit).
			Find(&stale).Error; err != nil {
			return err
		}
		if len(stale) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(stale))
		deletedKeys = make([]string, 0, len(stale))
		for _, row := range stale {
			ids = append(ids, row.ID)
			if row.ObjectKey != "" {
				deletedKeys = append(deletedKeys, row.ObjectKey)
			}
		}
		return tx.Where("id IN ?", ids).Delete(&ImageHistory{}).Error
	})
	return deletedKeys, err
}
