package lumera

import (
	"context"
	"fmt"
	"time"

	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	ristretto "github.com/dgraph-io/ristretto/v2"
	"golang.org/x/sync/singleflight"
)

const (
	// Expect up to ~100 live keys; use TinyLFU counters ~10x.
	cacheNumCounters = 1_000
	// With per-item cost of 1, cap total items to 100.
	cacheMaxCost     = 100
	cacheBufferItems = 64
	cacheItemCost    = 1
	cacheTTL         = time.Hour
)

func newStringCache[T any]() *ristretto.Cache[string, T] {
	c, _ := ristretto.NewCache(&ristretto.Config[string, T]{
		NumCounters: cacheNumCounters,
		MaxCost:     cacheMaxCost,
		BufferItems: cacheBufferItems,
	})
	return c
}

type SecureKeyExchangeValidator struct {
	lumeraClient   Client
	accountCache   *ristretto.Cache[string, *authtypes.QueryAccountInfoResponse]
	supernodeCache *ristretto.Cache[string, *sntypes.SuperNode]
	sf             singleflight.Group
}

func NewSecureKeyExchangeValidator(lumeraClient Client) *SecureKeyExchangeValidator {
	return &SecureKeyExchangeValidator{
		lumeraClient:   lumeraClient,
		accountCache:   newStringCache[*authtypes.QueryAccountInfoResponse](),
		supernodeCache: newStringCache[*sntypes.SuperNode](),
	}
}

func (v *SecureKeyExchangeValidator) AccountInfoByAddress(ctx context.Context, addr string) (*authtypes.QueryAccountInfoResponse, error) {
	if v.accountCache != nil {
		if val, ok := v.accountCache.Get(addr); ok && val != nil {
			return val, nil
		}
	}

	// Deduplicate concurrent fetches for the same address
	res, err, _ := v.sf.Do("acct:"+addr, func() (any, error) {
		// Double-check cache inside singleflight window (cheap and safe)
		if v.accountCache != nil {
			if val, ok := v.accountCache.Get(addr); ok && val != nil {
				return val, nil
			}
		}

		accountInfo, err := v.lumeraClient.Auth().AccountInfoByAddress(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to get account info: %w", err)
		}
		if accountInfo != nil && v.accountCache != nil {
			v.accountCache.SetWithTTL(addr, accountInfo, cacheItemCost, cacheTTL)
		}
		return accountInfo, nil
	})
	if err != nil {
		return nil, err
	}
	ai, _ := res.(*authtypes.QueryAccountInfoResponse)
	if ai == nil {
		return nil, fmt.Errorf("account info is nil")
	}
	return ai, nil
}

func (v *SecureKeyExchangeValidator) GetSupernodeBySupernodeAddress(ctx context.Context, address string) (*sntypes.SuperNode, error) {
	if v.supernodeCache != nil {
		if val, ok := v.supernodeCache.Get(address); ok && val != nil {
			return val, nil
		}
	}

	// Deduplicate concurrent fetches for the same supernode address
	res, err, _ := v.sf.Do("sn:"+address, func() (any, error) {
		// Double-check cache inside singleflight window
		if v.supernodeCache != nil {
			if val, ok := v.supernodeCache.Get(address); ok && val != nil {
				return val, nil
			}
		}

		supernodeInfo, err := v.lumeraClient.SuperNode().GetSupernodeBySupernodeAddress(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("failed to get supernode info: %w", err)
		}
		if supernodeInfo == nil {
			return nil, fmt.Errorf("supernode info is nil")
		}
		if v.supernodeCache != nil {
			v.supernodeCache.SetWithTTL(address, supernodeInfo, cacheItemCost, cacheTTL)
		}
		return supernodeInfo, nil
	})
	if err != nil {
		return nil, err
	}
	sn, _ := res.(*sntypes.SuperNode)
	if sn == nil {
		return nil, fmt.Errorf("supernode info is nil")
	}
	return sn, nil
}
