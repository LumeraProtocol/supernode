package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	lumeracodec "github.com/LumeraProtocol/supernode/v2/pkg/lumera/codec"
	"github.com/LumeraProtocol/supernode/v2/sdk/adapters/lumera"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	icatypes "github.com/cosmos/ibc-go/v10/modules/apps/27-interchain-accounts/types"
)

var errNotICA = errors.New("creator is not an interchain account")

func (m *ManagerImpl) validateICASignature(ctx context.Context, action lumera.Action, dataHashB64 string, signature string) error {
	ownerAddr, appPubkey, err := m.getICAOwnerAndAppPubkey(ctx, action)
	if err != nil {
		return err
	}
	if ownerAddr == "" {
		return fmt.Errorf("ica owner address is empty")
	}
	if len(appPubkey) == 0 {
		return fmt.Errorf("ica app pubkey is empty")
	}

	pubKey := secp256k1.PubKey{Key: appPubkey}
	verify := func(data, sig []byte) error {
		if !pubKey.VerifySignature(data, sig) {
			return fmt.Errorf("invalid signature")
		}
		return nil
	}

	ownerErr := cascadekit.VerifyStringRawOrADR36(dataHashB64, signature, ownerAddr, verify)
	if ownerErr == nil {
		m.logger.Info(ctx, "ICA signature verified using app pubkey", "creator", action.Creator, "owner", ownerAddr)
		return nil
	}

	var creatorErr error
	if action.Creator != "" && action.Creator != ownerAddr {
		creatorErr = cascadekit.VerifyStringRawOrADR36(dataHashB64, signature, action.Creator, verify)
		if creatorErr == nil {
			m.logger.Info(ctx, "ICA signature verified using app pubkey (creator address)", "creator", action.Creator, "owner", ownerAddr)
			return nil
		}
	}

	if creatorErr != nil {
		return fmt.Errorf("ica signature verification failed (owner: %v; creator: %v)", ownerErr, creatorErr)
	}
	return fmt.Errorf("ica signature verification failed: %w", ownerErr)
}

func (m *ManagerImpl) getICAOwnerAndAppPubkey(ctx context.Context, action lumera.Action) (string, []byte, error) {
	ownerHRP := strings.TrimSpace(m.config.Account.ICAOwnerHRP)
	if ownerHRP != "" {
		return m.getICAOwnerAndAppPubkeyFromKeyring(ctx, ownerHRP)
	}
	return m.getICAOwnerAndAppPubkeyFromChain(ctx, action)
}

func (m *ManagerImpl) getICAOwnerAndAppPubkeyFromKeyring(ctx context.Context, ownerHRP string) (string, []byte, error) {
	if m.keyring == nil {
		return "", nil, fmt.Errorf("keyring is nil")
	}
	keyName := strings.TrimSpace(m.config.Account.ICAOwnerKeyName)
	if keyName == "" {
		keyName = m.config.Account.KeyName
	}
	if keyName == "" {
		return "", nil, fmt.Errorf("owner key name is empty")
	}
	rec, err := m.keyring.Key(keyName)
	if err != nil {
		return "", nil, fmt.Errorf("load key %s: %w", keyName, err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return "", nil, fmt.Errorf("get key address: %w", err)
	}
	ownerAddr, err := sdk.Bech32ifyAddressBytes(ownerHRP, addr.Bytes())
	if err != nil {
		return "", nil, fmt.Errorf("bech32ify owner address: %w", err)
	}
	pubKey, err := rec.GetPubKey()
	if err != nil {
		return "", nil, fmt.Errorf("get key pubkey: %w", err)
	}
	appPubkey := pubKey.Bytes()
	if len(appPubkey) == 0 {
		return "", nil, fmt.Errorf("owner key pubkey is empty")
	}
	m.logger.Info(ctx, "ICA owner derived from keyring", "key_name", keyName, "owner", ownerAddr)
	return ownerAddr, appPubkey, nil
}

func (m *ManagerImpl) getICAOwnerAndAppPubkeyFromChain(ctx context.Context, action lumera.Action) (string, []byte, error) {
	if action.Creator == "" {
		return "", nil, fmt.Errorf("action creator is empty")
	}
	account, err := m.lumeraClient.AccountByAddress(ctx, action.Creator)
	if err != nil {
		return "", nil, fmt.Errorf("query account: %w", err)
	}
	icaAccount, ok := account.(*icatypes.InterchainAccount)
	if !ok {
		return "", nil, errNotICA
	}
	if icaAccount.AccountOwner == "" {
		return "", nil, fmt.Errorf("ica owner not set for %s", action.Creator)
	}

	appPubkey, err := findRequestActionAppPubkey(ctx, m.lumeraClient, action.ID, action.Creator)
	if err != nil {
		return "", nil, err
	}

	return icaAccount.AccountOwner, appPubkey, nil
}

func findRequestActionAppPubkey(ctx context.Context, client lumera.Client, actionID string, creator string) ([]byte, error) {
	if actionID == "" {
		return nil, fmt.Errorf("action id is empty")
	}
	msgType := sdk.MsgTypeURL(&actiontypes.MsgRequestAction{})

	queries := []string{
		fmt.Sprintf("%s.%s='%s' AND message.action='%s'", actiontypes.EventTypeActionRegistered, actiontypes.AttributeKeyActionID, actionID, msgType),
		fmt.Sprintf("%s.%s='%s'", actiontypes.EventTypeActionRegistered, actiontypes.AttributeKeyActionID, actionID),
	}

	var lastErr error
	for _, query := range queries {
		resp, err := client.QueryTxsByEvents(ctx, query, 1, 1)
		if err != nil {
			lastErr = fmt.Errorf("tx search failed for %q: %w", query, err)
			continue
		}
		if resp == nil || len(resp.Txs) == 0 {
			lastErr = fmt.Errorf("no txs found for %q", query)
			continue
		}
		appPubkey, err := extractAppPubkeyFromTxs(resp.Txs, creator)
		if err != nil {
			lastErr = err
			continue
		}
		return appPubkey, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("request action tx not found")
	}
	return nil, lastErr
}

func extractAppPubkeyFromTxs(txs []*sdktx.Tx, creator string) ([]byte, error) {
	encCfg := lumeracodec.GetEncodingConfig()
	for _, tx := range txs {
		if tx == nil || tx.Body == nil {
			continue
		}
		for _, anyMsg := range tx.Body.Messages {
			var msg sdk.Msg
			if err := encCfg.InterfaceRegistry.UnpackAny(anyMsg, &msg); err != nil {
				continue
			}
			req, ok := msg.(*actiontypes.MsgRequestAction)
			if !ok {
				continue
			}
			if creator != "" && req.Creator != creator {
				continue
			}
			if len(req.AppPubkey) == 0 {
				return nil, fmt.Errorf("request action app pubkey is empty")
			}
			return req.AppPubkey, nil
		}
	}
	return nil, fmt.Errorf("request action message not found in txs")
}
