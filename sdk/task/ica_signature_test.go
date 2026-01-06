package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/testutil"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/LumeraProtocol/supernode/v2/sdk/config"
	"github.com/LumeraProtocol/supernode/v2/sdk/log"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	icatypes "github.com/cosmos/ibc-go/v10/modules/apps/27-interchain-accounts/types"
	"github.com/stretchr/testify/require"
)

type fakeLumeraClient struct {
	accountByAddress      func(ctx context.Context, addr string) (sdk.AccountI, error)
	queryTxsByEvents      func(ctx context.Context, query string, page, limit uint64) (*sdktx.GetTxsEventResponse, error)
	decodeCascadeMetadata func(ctx context.Context, action lumera.Action) (actiontypes.CascadeMetadata, error)
	verifySignature       func(ctx context.Context, accountAddr string, data []byte, signature []byte) error
}

func (f *fakeLumeraClient) AccountInfoByAddress(context.Context, string) (*authtypes.QueryAccountInfoResponse, error) {
	return nil, nil
}

func (f *fakeLumeraClient) GetAction(context.Context, string) (lumera.Action, error) {
	return lumera.Action{}, nil
}

func (f *fakeLumeraClient) GetSupernodes(context.Context, int64) ([]lumera.Supernode, error) {
	return nil, nil
}

func (f *fakeLumeraClient) GetSupernodeBySupernodeAddress(context.Context, string) (*sntypes.SuperNode, error) {
	return nil, nil
}

func (f *fakeLumeraClient) GetSupernodeWithLatestAddress(context.Context, string) (*lumera.SuperNodeInfo, error) {
	return nil, nil
}

func (f *fakeLumeraClient) DecodeCascadeMetadata(ctx context.Context, action lumera.Action) (actiontypes.CascadeMetadata, error) {
	if f.decodeCascadeMetadata == nil {
		return actiontypes.CascadeMetadata{}, fmt.Errorf("decode not configured")
	}
	return f.decodeCascadeMetadata(ctx, action)
}

func (f *fakeLumeraClient) VerifySignature(ctx context.Context, accountAddr string, data []byte, signature []byte) error {
	if f.verifySignature == nil {
		return fmt.Errorf("verify not configured")
	}
	return f.verifySignature(ctx, accountAddr, data, signature)
}

func (f *fakeLumeraClient) AccountByAddress(ctx context.Context, addr string) (sdk.AccountI, error) {
	if f.accountByAddress == nil {
		return nil, fmt.Errorf("accountByAddress not configured")
	}
	return f.accountByAddress(ctx, addr)
}

func (f *fakeLumeraClient) QueryTxsByEvents(ctx context.Context, query string, page, limit uint64) (*sdktx.GetTxsEventResponse, error) {
	if f.queryTxsByEvents == nil {
		return nil, fmt.Errorf("queryTxsByEvents not configured")
	}
	return f.queryTxsByEvents(ctx, query, page, limit)
}

func (f *fakeLumeraClient) GetBalance(context.Context, string, string) (*banktypes.QueryBalanceResponse, error) {
	return nil, nil
}

func (f *fakeLumeraClient) GetActionParams(context.Context) (*actiontypes.QueryParamsResponse, error) {
	return nil, nil
}

func (f *fakeLumeraClient) GetActionFee(context.Context, string) (*actiontypes.QueryGetActionFeeResponse, error) {
	return nil, nil
}

func TestGetICAOwnerAndAppPubkeyFromKeyring(t *testing.T) {
	kr := testutil.CreateTestKeyring()
	accounts := testutil.SetupTestAccounts(t, kr, []string{"owner"})
	rec, err := kr.Key("owner")
	require.NoError(t, err)
	addr, err := rec.GetAddress()
	require.NoError(t, err)
	pubKey, err := rec.GetPubKey()
	require.NoError(t, err)

	cfg := config.NewConfig(config.AccountConfig{
		KeyName:         "owner",
		Keyring:         kr,
		ICAOwnerKeyName: "owner",
		ICAOwnerHRP:     "lumera",
	}, config.LumeraConfig{})

	m := &ManagerImpl{
		config:  cfg,
		keyring: kr,
		logger:  log.NewNoopLogger(),
	}

	owner, appPubkey, err := m.getICAOwnerAndAppPubkey(context.Background(), lumera.Action{Creator: accounts[0].Address})
	require.NoError(t, err)

	expectedOwner, err := sdk.Bech32ifyAddressBytes("lumera", addr.Bytes())
	require.NoError(t, err)
	require.Equal(t, expectedOwner, owner)
	require.Equal(t, pubKey.Bytes(), appPubkey)
}

func TestGetICAOwnerAndAppPubkeyFromChain(t *testing.T) {
	creator := "creator"
	owner := "owner-addr"
	appPubkey := []byte{1, 2, 3}

	base := authtypes.NewBaseAccountWithAddress(sdk.AccAddress([]byte("creator-address")))
	ica := icatypes.NewInterchainAccount(base, owner)

	req := &actiontypes.MsgRequestAction{
		Creator:   creator,
		AppPubkey: appPubkey,
	}
	any, err := codectypes.NewAnyWithValue(req)
	require.NoError(t, err)
	tx := &sdktx.Tx{Body: &sdktx.TxBody{Messages: []*codectypes.Any{any}}}
	resp := &sdktx.GetTxsEventResponse{Txs: []*sdktx.Tx{tx}}

	fake := &fakeLumeraClient{
		accountByAddress: func(ctx context.Context, addr string) (sdk.AccountI, error) {
			return ica, nil
		},
		queryTxsByEvents: func(ctx context.Context, query string, page, limit uint64) (*sdktx.GetTxsEventResponse, error) {
			return resp, nil
		},
	}

	cfg := config.NewConfig(config.AccountConfig{}, config.LumeraConfig{})
	m := &ManagerImpl{
		lumeraClient: fake,
		config:       cfg,
		logger:       log.NewNoopLogger(),
	}

	gotOwner, gotPubkey, err := m.getICAOwnerAndAppPubkey(context.Background(), lumera.Action{Creator: creator, ID: "action-1"})
	require.NoError(t, err)
	require.Equal(t, owner, gotOwner)
	require.Equal(t, appPubkey, gotPubkey)
}

func TestValidateICASignatureFromKeyring(t *testing.T) {
	kr := testutil.CreateTestKeyring()
	testutil.SetupTestAccounts(t, kr, []string{"owner"})

	cfg := config.NewConfig(config.AccountConfig{
		KeyName:         "owner",
		Keyring:         kr,
		ICAOwnerKeyName: "owner",
		ICAOwnerHRP:     "lumera",
	}, config.LumeraConfig{})

	m := &ManagerImpl{
		config:  cfg,
		keyring: kr,
		logger:  log.NewNoopLogger(),
	}

	dataHash := "hash-b64"
	sig, _, err := kr.Sign("owner", []byte(dataHash), signing.SignMode_SIGN_MODE_DIRECT)
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	err = m.validateICASignature(context.Background(), lumera.Action{Creator: "creator"}, dataHash, sigB64)
	require.NoError(t, err)
}

func TestValidateSignatureFallsBackToICA(t *testing.T) {
	creator := "creator"
	dataHash := "payload-b64"
	key := secp256k1.GenPrivKey()
	sig, err := key.Sign([]byte(dataHash))
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	appPubkey := key.PubKey().Bytes()
	req := &actiontypes.MsgRequestAction{
		Creator:   creator,
		AppPubkey: appPubkey,
	}
	any, err := codectypes.NewAnyWithValue(req)
	require.NoError(t, err)
	tx := &sdktx.Tx{Body: &sdktx.TxBody{Messages: []*codectypes.Any{any}}}
	resp := &sdktx.GetTxsEventResponse{Txs: []*sdktx.Tx{tx}}

	base := authtypes.NewBaseAccountWithAddress(sdk.AccAddress([]byte("creator-address")))
	ica := icatypes.NewInterchainAccount(base, "owner")

	fake := &fakeLumeraClient{
		accountByAddress: func(ctx context.Context, addr string) (sdk.AccountI, error) {
			return ica, nil
		},
		queryTxsByEvents: func(ctx context.Context, query string, page, limit uint64) (*sdktx.GetTxsEventResponse, error) {
			return resp, nil
		},
		decodeCascadeMetadata: func(ctx context.Context, action lumera.Action) (actiontypes.CascadeMetadata, error) {
			return actiontypes.CascadeMetadata{DataHash: dataHash}, nil
		},
		verifySignature: func(ctx context.Context, accountAddr string, data []byte, signature []byte) error {
			return fmt.Errorf("on-chain verify failed")
		},
	}

	cfg := config.NewConfig(config.AccountConfig{}, config.LumeraConfig{})
	m := &ManagerImpl{
		lumeraClient: fake,
		config:       cfg,
		logger:       log.NewNoopLogger(),
	}

	err = m.validateSignature(context.Background(), lumera.Action{Creator: creator, ID: "action-1"}, sigB64)
	require.NoError(t, err)
}
