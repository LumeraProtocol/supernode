package cascade

import (
	"context"
	"encoding/base64"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/require"
)

type fakeCascadeLumeraClient struct {
	verifyFn    func(ctx context.Context, address string, msg []byte, sig []byte) error
	getActionFn func(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error)
}

func (f *fakeCascadeLumeraClient) GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error) {
	if f.getActionFn == nil {
		return nil, nil
	}
	return f.getActionFn(ctx, actionID)
}

func (f *fakeCascadeLumeraClient) GetTopSupernodes(ctx context.Context, blockHeight uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return nil, nil
}

func (f *fakeCascadeLumeraClient) Verify(ctx context.Context, address string, msg []byte, sig []byte) error {
	if f.verifyFn == nil {
		return nil
	}
	return f.verifyFn(ctx, address, msg, sig)
}

func (f *fakeCascadeLumeraClient) GetActionFee(ctx context.Context, dataSizeKB string) (*actiontypes.QueryGetActionFeeResponse, error) {
	return nil, nil
}

func (f *fakeCascadeLumeraClient) SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error) {
	return nil, nil
}

func (f *fakeCascadeLumeraClient) FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error) {
	return nil, nil
}

func TestVerifyWithAppPubkey(t *testing.T) {
	key := secp256k1.GenPrivKey()
	data := []byte("payload")
	sig, err := key.Sign(data)
	require.NoError(t, err)

	err = verifyWithAppPubkey(key.PubKey().Bytes(), data, sig)
	require.NoError(t, err)

	err = verifyWithAppPubkey(nil, data, sig)
	require.Error(t, err)
}

func TestBuildSignatureVerifierFallback(t *testing.T) {
	keyApp := secp256k1.GenPrivKey()
	keySigner := secp256k1.GenPrivKey()
	data := []byte("payload")
	sig, err := keySigner.Sign(data)
	require.NoError(t, err)
	expectedSig := sig

	var verifyCalled bool
	fake := &fakeCascadeLumeraClient{
		verifyFn: func(ctx context.Context, address string, msg []byte, sig []byte) error {
			verifyCalled = true
			require.Equal(t, data, msg)
			require.Equal(t, expectedSig, sig)
			return nil
		},
	}

	service := &CascadeService{LumeraClient: fake}
	task := &CascadeRegistrationTask{CascadeService: service}

	verify := task.buildSignatureVerifier(context.Background(), "action-1", "creator", keyApp.PubKey().Bytes())
	require.NoError(t, verify(data, sig))
	require.True(t, verifyCalled)
}

func TestVerifyDownloadSignatureWithAppPubkey(t *testing.T) {
	key := secp256k1.GenPrivKey()
	actionID := "action-1"
	signature, err := key.Sign([]byte(actionID))
	require.NoError(t, err)
	sigB64 := base64.StdEncoding.EncodeToString(signature)

	fake := &fakeCascadeLumeraClient{
		getActionFn: func(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error) {
			return &actiontypes.QueryGetActionResponse{
				Action: &actiontypes.Action{
					ActionID:  actionID,
					Creator:   "creator",
					AppPubkey: key.PubKey().Bytes(),
				},
			}, nil
		},
		verifyFn: func(ctx context.Context, address string, msg []byte, sig []byte) error {
			return nil
		},
	}

	service := &CascadeService{LumeraClient: fake}
	task := &CascadeRegistrationTask{CascadeService: service}

	err = task.VerifyDownloadSignature(context.Background(), actionID, sigB64)
	require.NoError(t, err)
}
