package cascade

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
)

func (task *CascadeRegistrationTask) buildSignatureVerifier(ctx context.Context, actionID, creator string, appPubkey []byte) cascadekit.Verifier {
	baseFields := logtrace.Fields{
		logtrace.FieldActionID: actionID,
		logtrace.FieldCreator:  creator,
	}
	if len(appPubkey) == 0 {
		logtrace.Debug(ctx, "ICA app_pubkey not present; using on-chain verify", baseFields)
		return func(data, sig []byte) error {
			return task.LumeraClient.Verify(ctx, creator, data, sig)
		}
	}
	logtrace.Info(ctx, "ICA app_pubkey loaded for signature verification", logtrace.WithFields(baseFields, logtrace.Fields{
		"pubkey_len": len(appPubkey),
	}))

	return func(data, sig []byte) error {
		err := verifyWithAppPubkey(appPubkey, data, sig)
		if err == nil {
			return nil
		}
		logtrace.Debug(ctx, "ICA app_pubkey verification failed; falling back to on-chain verify", logtrace.WithFields(baseFields, logtrace.Fields{
			logtrace.FieldError: err.Error(),
		}))
		return task.LumeraClient.Verify(ctx, creator, data, sig)
	}
}

func verifyWithAppPubkey(appPubkey, data, sig []byte) error {
	if len(appPubkey) == 0 {
		return fmt.Errorf("app pubkey is empty")
	}
	pubKey := secp256k1.PubKey{Key: appPubkey}
	if !pubKey.VerifySignature(data, sig) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
