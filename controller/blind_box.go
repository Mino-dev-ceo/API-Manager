package controller

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func ListBlindBoxPackages(c *gin.Context) {
	packages, err := model.ListEnabledBlindBoxPackages()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"packages": packages})
}

func OpenBlindBox(c *gin.Context) {
	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PackageID == "" {
		common.ApiError(c, errors.New("请选择盲盒套餐"))
		return
	}
	result, err := model.OpenBlindBox(c.GetInt("id"), req.PackageID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func GetBlindBoxSettings(c *gin.Context) {
	config, err := model.GetBlindBoxConfig()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, config)
}

func SaveBlindBoxSettings(c *gin.Context) {
	var config model.BlindBoxConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		common.ApiError(c, err)
		return
	}
	saved, err := model.SaveBlindBoxConfig(config)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, saved)
}
