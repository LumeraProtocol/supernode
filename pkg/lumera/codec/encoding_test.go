package codec

import (
	"testing"
	"time"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	"github.com/stretchr/testify/require"
)

func TestEncodingConfigUnpacksDelayedVestingAccount(t *testing.T) {
	cfg := GetEncodingConfig()

	addr := sdk.AccAddress([]byte("delayed-vesting-addr")).String()
	base := authtypes.NewBaseAccountWithAddress(sdk.MustAccAddressFromBech32(addr))
	coins := sdk.NewCoins(sdk.NewInt64Coin("ulume", 1))
	bva, err := vestingtypes.NewBaseVestingAccount(base, coins, time.Now().Unix())
	require.NoError(t, err)
	delayed := vestingtypes.NewDelayedVestingAccountRaw(bva)

	anyAcc, err := codectypes.NewAnyWithValue(delayed)
	require.NoError(t, err)

	var unpacked sdk.AccountI
	err = cfg.InterfaceRegistry.UnpackAny(anyAcc, &unpacked)
	require.NoError(t, err)

	_, ok := unpacked.(*vestingtypes.DelayedVestingAccount)
	require.True(t, ok)
}
