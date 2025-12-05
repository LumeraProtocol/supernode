package verifier

import (
	"context"
	"fmt"
	"net"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
	snmodule "github.com/LumeraProtocol/supernode/v2/pkg/lumera/modules/supernode"
	"github.com/LumeraProtocol/supernode/v2/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type ConfigVerifier struct {
	config       *config.Config
	lumeraClient lumera.Client
	keyring      keyring.Keyring
}

func NewConfigVerifier(cfg *config.Config, client lumera.Client, kr keyring.Keyring) ConfigVerifierService {
	return &ConfigVerifier{config: cfg, lumeraClient: client, keyring: kr}
}

func (cv *ConfigVerifier) VerifyConfig(ctx context.Context) (*VerificationResult, error) {
	result := &VerificationResult{Valid: true, Errors: []ConfigError{}, Warnings: []ConfigError{}}
	logtrace.Debug(ctx, "Starting config verification", logtrace.Fields{"identity": cv.config.SupernodeConfig.Identity, "key_name": cv.config.SupernodeConfig.KeyName, "p2p_port": cv.config.P2PConfig.Port})
	if err := cv.checkKeyExists(result); err != nil {
		return result, err
	}
	if err := cv.checkIdentityMatches(result); err != nil {
		return result, err
	}
	if !result.IsValid() {
		return result, nil
	}
	supernodeInfo, err := cv.checkSupernodeExists(ctx, result)
	if err != nil {
		return result, err
	}
	if supernodeInfo == nil {
		return result, nil
	}
	cv.checkSupernodeState(result, supernodeInfo)
	cv.checkPortsAvailable(result)
	logtrace.Debug(ctx, "Config verification completed", logtrace.Fields{"valid": result.IsValid(), "errors": len(result.Errors), "warnings": len(result.Warnings)})
	return result, nil
}

func (cv *ConfigVerifier) checkKeyExists(result *VerificationResult) error {
	_, err := cv.keyring.Key(cv.config.SupernodeConfig.KeyName)
	if err == nil {
		return nil
	}

	result.Valid = false

	// Provide a more actionable error message that includes keyring backend and
	// directory, and preserve the original error for debugging.
	msg := fmt.Sprintf(
		"failed to load key %q from keyring (backend=%s, dir=%s): %v. "+
			"Ensure the key exists and that your keyring passphrase configuration is correct.",
		cv.config.SupernodeConfig.KeyName,
		cv.config.KeyringConfig.Backend,
		cv.config.GetKeyringDir(),
		err,
	)

	result.Errors = append(result.Errors, ConfigError{
		Field:   "key_name",
		Actual:  cv.config.SupernodeConfig.KeyName,
		Message: msg,
	})
	return nil
}

func (cv *ConfigVerifier) checkIdentityMatches(result *VerificationResult) error {
	keyInfo, err := cv.keyring.Key(cv.config.SupernodeConfig.KeyName)
	if err != nil {
		return nil
	}
	pubKey, err := keyInfo.GetPubKey()
	if err != nil {
		return fmt.Errorf("failed to get public key for key '%s': %w", cv.config.SupernodeConfig.KeyName, err)
	}
	addr := sdk.AccAddress(pubKey.Address())
	if addr.String() != cv.config.SupernodeConfig.Identity {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{Field: "identity", Expected: addr.String(), Actual: cv.config.SupernodeConfig.Identity, Message: fmt.Sprintf("Key '%s' resolves to %s but config identity is %s", cv.config.SupernodeConfig.KeyName, addr.String(), cv.config.SupernodeConfig.Identity)})
	}
	return nil
}

func (cv *ConfigVerifier) checkSupernodeExists(ctx context.Context, result *VerificationResult) (*snmodule.SuperNodeInfo, error) {
	sn, err := cv.lumeraClient.SuperNode().GetSupernodeWithLatestAddress(ctx, cv.config.SupernodeConfig.Identity)
	if err != nil {
		result.Valid = false

		msg := fmt.Sprintf(
			"failed to fetch supernode registration from chain (identity=%s, grpc=%s): %v",
			cv.config.SupernodeConfig.Identity,
			cv.config.LumeraClientConfig.GRPCAddr,
			err,
		)

		result.Errors = append(result.Errors, ConfigError{
			Field:   "registration",
			Actual:  "error",
			Message: msg,
		})
		return nil, err
	}
	if sn == nil {
		result.Valid = false
		msg := fmt.Sprintf(
			"supernode identity %s is not registered on chain (grpc=%s)",
			cv.config.SupernodeConfig.Identity,
			cv.config.LumeraClientConfig.GRPCAddr,
		)
		result.Errors = append(result.Errors, ConfigError{
			Field:   "registration",
			Actual:  "not_found",
			Message: msg,
		})
		return nil, nil
	}
	return sn, nil
}

func (cv *ConfigVerifier) checkSupernodeState(result *VerificationResult, supernodeInfo *snmodule.SuperNodeInfo) {
	if supernodeInfo.CurrentState != "" && supernodeInfo.CurrentState != "SUPERNODE_STATE_ACTIVE" {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{Field: "state", Expected: "SUPERNODE_STATE_ACTIVE", Actual: supernodeInfo.CurrentState, Message: fmt.Sprintf("Supernode state is %s (expected ACTIVE)", supernodeInfo.CurrentState)})
	}
}

func (cv *ConfigVerifier) checkPortsAvailable(result *VerificationResult) {
	if !cv.isPortAvailable(cv.config.SupernodeConfig.Host, int(cv.config.SupernodeConfig.Port)) {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{Field: "supernode_port", Actual: fmt.Sprintf("%d", cv.config.SupernodeConfig.Port), Message: fmt.Sprintf("Port %d is already in use. Please stop the conflicting service or choose a different port", cv.config.SupernodeConfig.Port)})
	}
	if !cv.isPortAvailable(cv.config.SupernodeConfig.Host, int(cv.config.P2PConfig.Port)) {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{Field: "p2p_port", Actual: fmt.Sprintf("%d", cv.config.P2PConfig.Port), Message: fmt.Sprintf("Port %d is already in use. Please stop the conflicting service or choose a different port", cv.config.P2PConfig.Port)})
	}
	gatewayPort := int(cv.config.SupernodeConfig.GatewayPort)
	if gatewayPort == 0 {
		gatewayPort = 8002
	}
	if !cv.isPortAvailable(cv.config.SupernodeConfig.Host, gatewayPort) {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{Field: "gateway_port", Actual: fmt.Sprintf("%d", gatewayPort), Message: fmt.Sprintf("Port %d is already in use. Please stop the conflicting service or choose a different port", gatewayPort)})
	}
}

func (cv *ConfigVerifier) isPortAvailable(host string, port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
