package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertRedemptionUser(t *testing.T, id int, quota int, usedQuota int) {
	t.Helper()
	user := &User{
		Id:        id,
		Username:  fmt.Sprintf("redemption_user_%d", id),
		Status:    common.UserStatusEnabled,
		Quota:     quota,
		UsedQuota: usedQuota,
		AffCode:   fmt.Sprintf("redemption_aff_%d", id),
	}
	require.NoError(t, DB.Create(user).Error)
}

func getRedemptionUserQuota(t *testing.T, userID int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").Where("id = ?", userID).First(&user).Error)
	return user.Quota
}

func countRedemptionsForUser(t *testing.T, userID int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&Redemption{}).Where("user_id = ?", userID).Count(&count).Error)
	return count
}

func TestCreateUserRedemptionsDeductsCurrentQuota(t *testing.T) {
	truncateTables(t)
	insertRedemptionUser(t, 501, 1000, 900)

	keys, err := CreateUserRedemptions(501, "团队分发", 100, 2, 0)
	require.NoError(t, err)
	assert.Len(t, keys, 2)
	assert.Equal(t, 800, getRedemptionUserQuota(t, 501))
	assert.Equal(t, int64(2), countRedemptionsForUser(t, 501))
}

func TestCreateUserRedemptionsRejectsInsufficientQuota(t *testing.T) {
	truncateTables(t)
	insertRedemptionUser(t, 502, 100, 0)

	keys, err := CreateUserRedemptions(502, "团队分发", 100, 2, 0)
	require.Error(t, err)
	assert.Nil(t, keys)
	assert.Equal(t, 100, getRedemptionUserQuota(t, 502))
	assert.Equal(t, int64(0), countRedemptionsForUser(t, 502))
}

func TestRevokeUserRedemptionRefundsUnusedQuota(t *testing.T) {
	truncateTables(t)
	insertRedemptionUser(t, 503, 1000, 0)

	keys, err := CreateUserRedemptions(503, "团队分发", 80, 1, 0)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, 920, getRedemptionUserQuota(t, 503))

	var redemption Redemption
	require.NoError(t, DB.Where("`key` = ?", keys[0]).First(&redemption).Error)

	revoked, err := RevokeUserRedemptionById(redemption.Id, 503)
	require.NoError(t, err)
	assert.Equal(t, common.RedemptionCodeStatusDisabled, revoked.Status)
	assert.Equal(t, 1000, getRedemptionUserQuota(t, 503))

	_, err = RevokeUserRedemptionById(redemption.Id, 503)
	require.Error(t, err)
	assert.Equal(t, 1000, getRedemptionUserQuota(t, 503))
}

func TestRevokeUserRedemptionRejectsOtherOwner(t *testing.T) {
	truncateTables(t)
	insertRedemptionUser(t, 504, 1000, 0)
	insertRedemptionUser(t, 505, 1000, 0)

	keys, err := CreateUserRedemptions(504, "团队分发", 80, 1, 0)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	var redemption Redemption
	require.NoError(t, DB.Where("`key` = ?", keys[0]).First(&redemption).Error)

	_, err = RevokeUserRedemptionById(redemption.Id, 505)
	require.Error(t, err)
	assert.Equal(t, 920, getRedemptionUserQuota(t, 504))
	assert.Equal(t, 1000, getRedemptionUserQuota(t, 505))
}
