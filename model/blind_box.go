package model

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const blindBoxOptionKey = "MinoBlindBoxPackages"

type BlindBoxReward struct {
	Multiplier float64 `json:"multiplier"`
	Weight     int     `json:"weight"`
	Label      string  `json:"label"`
}

type BlindBoxPackage struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Price     int              `json:"price"`
	Enabled   bool             `json:"enabled"`
	SortOrder int              `json:"sort_order"`
	Rewards   []BlindBoxReward `json:"rewards"`
}

type BlindBoxConfig struct {
	Packages []BlindBoxPackage `json:"packages"`
}

type BlindBoxOpenResult struct {
	Package    BlindBoxPackage `json:"package"`
	Price      int             `json:"price"`
	Reward     int             `json:"reward"`
	Multiplier float64         `json:"multiplier"`
	Net        int             `json:"net"`
	Balance    int             `json:"balance"`
	Label      string          `json:"label"`
}

func defaultBlindBoxConfig() BlindBoxConfig {
	rewards := []BlindBoxReward{
		{Multiplier: 0.5, Weight: 26, Label: "保底返还"},
		{Multiplier: 0.8, Weight: 24, Label: "小额回收"},
		{Multiplier: 1, Weight: 25, Label: "原价返还"},
		{Multiplier: 1.5, Weight: 14, Label: "小赚一笔"},
		{Multiplier: 2, Weight: 8, Label: "双倍惊喜"},
		{Multiplier: 5, Weight: 3, Label: "欧皇大奖"},
	}
	return BlindBoxConfig{Packages: []BlindBoxPackage{
		{ID: "box-1", Name: "体验盲盒", Price: 1, Enabled: true, SortOrder: 1, Rewards: rewards},
		{ID: "box-10", Name: "进阶盲盒", Price: 10, Enabled: true, SortOrder: 2, Rewards: rewards},
		{ID: "box-100", Name: "旗舰盲盒", Price: 100, Enabled: true, SortOrder: 3, Rewards: rewards},
	}}
}

func GetBlindBoxConfig() (BlindBoxConfig, error) {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[blindBoxOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return defaultBlindBoxConfig(), nil
	}

	var config BlindBoxConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		common.SysLog("failed to parse blind box config, fallback to default: " + err.Error())
		return defaultBlindBoxConfig(), nil
	}
	return normalizeBlindBoxConfig(config)
}

func SaveBlindBoxConfig(config BlindBoxConfig) (BlindBoxConfig, error) {
	normalized, err := normalizeBlindBoxConfig(config)
	if err != nil {
		return normalized, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return normalized, err
	}
	if err := UpdateOption(blindBoxOptionKey, string(payload)); err != nil {
		return normalized, err
	}
	return normalized, nil
}

func ListEnabledBlindBoxPackages() ([]BlindBoxPackage, error) {
	config, err := GetBlindBoxConfig()
	if err != nil {
		return nil, err
	}
	packages := make([]BlindBoxPackage, 0, len(config.Packages))
	for _, item := range config.Packages {
		if item.Enabled {
			packages = append(packages, item)
		}
	}
	return packages, nil
}

func OpenBlindBox(userID int, packageID string) (*BlindBoxOpenResult, error) {
	if userID == 0 {
		return nil, errors.New("无效的用户")
	}
	config, err := GetBlindBoxConfig()
	if err != nil {
		return nil, err
	}
	var selected *BlindBoxPackage
	for i := range config.Packages {
		if config.Packages[i].ID == packageID && config.Packages[i].Enabled {
			selected = &config.Packages[i]
			break
		}
	}
	if selected == nil {
		return nil, errors.New("盲盒套餐不存在或已停用")
	}

	reward, err := pickBlindBoxReward(selected.Rewards)
	if err != nil {
		return nil, err
	}
	priceQuota := cnyAmountToQuota(selected.Price)
	rewardQuota := int(math.Round(float64(priceQuota) * reward.Multiplier))
	if rewardQuota < 0 {
		rewardQuota = 0
	}
	delta := rewardQuota - priceQuota
	balance := 0

	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "quota").First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if user.Quota < priceQuota {
			return errors.New("钱包余额不足，无法开启盲盒")
		}
		balance = user.Quota + delta
		return tx.Model(&User{}).Where("id = ?", userID).Update("quota", balance).Error
	})
	if err != nil {
		return nil, err
	}
	if delta >= 0 {
		if err := cacheIncrUserQuota(userID, int64(delta)); err != nil {
			common.SysLog("failed to increase user quota cache after blind box: " + err.Error())
		}
	} else if err := cacheDecrUserQuota(userID, int64(-delta)); err != nil {
		common.SysLog("failed to decrease user quota cache after blind box: " + err.Error())
	}

	RecordLog(userID, LogTypeTopup, fmt.Sprintf("开启%s，消耗 %s，获得 %s", selected.Name, logger.LogQuota(priceQuota), logger.LogQuota(rewardQuota)))
	return &BlindBoxOpenResult{
		Package:    *selected,
		Price:      priceQuota,
		Reward:     rewardQuota,
		Multiplier: reward.Multiplier,
		Net:        delta,
		Balance:    balance,
		Label:      reward.Label,
	}, nil
}

func cnyAmountToQuota(amount int) int {
	exchangeRate := operation_setting.USDExchangeRate
	if exchangeRate <= 0 {
		exchangeRate = 7.3
	}
	quota := int(math.Round(float64(amount) / exchangeRate * common.QuotaPerUnit))
	if quota < 1 {
		return 1
	}
	return quota
}

func normalizeBlindBoxConfig(config BlindBoxConfig) (BlindBoxConfig, error) {
	if len(config.Packages) == 0 {
		config = defaultBlindBoxConfig()
	}
	normalized := BlindBoxConfig{Packages: make([]BlindBoxPackage, 0, len(config.Packages))}
	seen := map[string]bool{}
	for index, item := range config.Packages {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("box-%d", index+1)
		}
		if seen[id] {
			return normalized, fmt.Errorf("盲盒套餐 ID 重复：%s", id)
		}
		seen[id] = true
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = fmt.Sprintf("盲盒 %d", index+1)
		}
		if item.Price <= 0 {
			return normalized, fmt.Errorf("%s 的价格必须大于 0", name)
		}
		rewards, err := normalizeBlindBoxRewards(item.Rewards)
		if err != nil {
			return normalized, fmt.Errorf("%s：%w", name, err)
		}
		sortOrder := item.SortOrder
		if sortOrder == 0 {
			sortOrder = index + 1
		}
		normalized.Packages = append(normalized.Packages, BlindBoxPackage{
			ID:        id,
			Name:      name,
			Price:     item.Price,
			Enabled:   item.Enabled,
			SortOrder: sortOrder,
			Rewards:   rewards,
		})
	}
	sort.SliceStable(normalized.Packages, func(i, j int) bool {
		return normalized.Packages[i].SortOrder < normalized.Packages[j].SortOrder
	})
	return normalized, nil
}

func normalizeBlindBoxRewards(rewards []BlindBoxReward) ([]BlindBoxReward, error) {
	if len(rewards) == 0 {
		rewards = defaultBlindBoxConfig().Packages[0].Rewards
	}
	normalized := make([]BlindBoxReward, 0, len(rewards))
	totalWeight := 0
	for _, item := range rewards {
		if item.Multiplier < 0.5 || item.Multiplier > 5 {
			return nil, errors.New("中奖倍率必须在 0.5 到 5 之间")
		}
		if item.Weight <= 0 {
			return nil, errors.New("中奖权重必须大于 0")
		}
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = fmt.Sprintf("%.2fx", item.Multiplier)
		}
		totalWeight += item.Weight
		normalized = append(normalized, BlindBoxReward{
			Multiplier: item.Multiplier,
			Weight:     item.Weight,
			Label:      label,
		})
	}
	if totalWeight <= 0 {
		return nil, errors.New("中奖权重总和必须大于 0")
	}
	return normalized, nil
}

func pickBlindBoxReward(rewards []BlindBoxReward) (BlindBoxReward, error) {
	total := 0
	for _, reward := range rewards {
		total += reward.Weight
	}
	if total <= 0 {
		return BlindBoxReward{}, errors.New("盲盒概率未配置")
	}
	random, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
	if err != nil {
		return BlindBoxReward{}, err
	}
	needle := int(random.Int64()) + 1
	current := 0
	for _, reward := range rewards {
		current += reward.Weight
		if needle <= current {
			return reward, nil
		}
	}
	return rewards[len(rewards)-1], nil
}
