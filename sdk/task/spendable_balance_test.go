package task

import (
	"context"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

func TestHasMinimumSpendableBalance_AllowsVestingAccountWithUnlockedFunds(t *testing.T) {
	fake := &fakeLumeraClient{
		getSpendableBalance: func(context.Context, string, string) (*banktypes.QuerySpendableBalanceByDenomResponse, error) {
			return &banktypes.QuerySpendableBalanceByDenomResponse{
				Balance: &types.Coin{Denom: "ulume", Amount: sdkmath.NewInt(1_500_000)},
			}, nil
		},
	}

	ok, reason := hasMinimumSpendableBalance(context.Background(), fake, "lumera1vestingwithspendable", "ulume", sdkmath.NewInt(minEligibleBalanceULUME))
	require.True(t, ok)
	require.Empty(t, reason)
}

func TestHasMinimumSpendableBalance_RejectsVestingAccountWithoutUnlockedFunds(t *testing.T) {
	fake := &fakeLumeraClient{
		getSpendableBalance: func(context.Context, string, string) (*banktypes.QuerySpendableBalanceByDenomResponse, error) {
			return &banktypes.QuerySpendableBalanceByDenomResponse{
				Balance: &types.Coin{Denom: "ulume", Amount: sdkmath.ZeroInt()},
			}, nil
		},
	}

	ok, reason := hasMinimumSpendableBalance(context.Background(), fake, "lumera1vestinglockedonly", "ulume", sdkmath.NewInt(minEligibleBalanceULUME))
	require.False(t, ok)
	require.Contains(t, reason, "insufficient spendable balance")
}
