package verifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/supernode/config"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
)

// ConfigVerifier implements ConfigVerifierService
type ConfigVerifier struct {
	config       *config.Config
	lumeraClient lumera.Client
	keyring      keyring.Keyring
}

// NewConfigVerifier creates a new config verifier service
func NewConfigVerifier(cfg *config.Config, client lumera.Client, kr keyring.Keyring) ConfigVerifierService {
	return &ConfigVerifier{
		config:       cfg,
		lumeraClient: client,
		keyring:      kr,
	}
}

// VerifyConfig performs comprehensive config validation against chain
func (cv *ConfigVerifier) VerifyConfig(ctx context.Context) (*VerificationResult, error) {
	result := &VerificationResult{
		Valid:    true,
		Errors:   []ConfigError{},
		Warnings: []ConfigError{},
	}

	logtrace.Debug(ctx, "Starting config verification", logtrace.Fields{
		"identity": cv.config.SupernodeConfig.Identity,
		"key_name": cv.config.SupernodeConfig.KeyName,
		"p2p_port": cv.config.P2PConfig.Port,
	})

	// Check 1: Verify keyring contains the key
	if err := cv.checkKeyExists(result); err != nil {
		return result, err
	}

	// Check 2: Verify key resolves to correct identity
	if err := cv.checkIdentityMatches(result); err != nil {
		return result, err
	}

	// If keyring checks failed, don't proceed with chain queries
	if !result.IsValid() {
		return result, nil
	}

	// Check 3: Query chain for supernode registration
	supernode, err := cv.checkSupernodeExists(ctx, result)
	if err != nil {
		return result, err
	}

	// If supernode doesn't exist, don't proceed with field comparisons
	if supernode == nil {
		return result, nil
	}

	// Check 4: Verify P2P port matches
	cv.checkP2PPortMatches(result, supernode)

	// Check 5: Verify supernode state is active
	cv.checkSupernodeState(result, supernode)

	// Check 6: Check supernode port alignment with on-chain registration
	cv.checkSupernodePortAlignment(result, supernode)

	// Check 7: Check host alignment with on-chain registration (warning only - may differ due to load balancer)
	cv.checkHostAlignment(result, supernode)

	logtrace.Info(ctx, "Config verification completed", logtrace.Fields{
		"valid":    result.IsValid(),
		"errors":   len(result.Errors),
		"warnings": len(result.Warnings),
	})

	return result, nil
}

// checkKeyExists verifies the configured key exists in keyring
func (cv *ConfigVerifier) checkKeyExists(result *VerificationResult) error {
	_, err := cv.keyring.Key(cv.config.SupernodeConfig.KeyName)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{
			Field:   "key_name",
			Actual:  cv.config.SupernodeConfig.KeyName,
			Message: fmt.Sprintf("Key '%s' not found in keyring", cv.config.SupernodeConfig.KeyName),
		})
	}
	return nil
}

// checkIdentityMatches verifies key resolves to configured identity
func (cv *ConfigVerifier) checkIdentityMatches(result *VerificationResult) error {
	keyInfo, err := cv.keyring.Key(cv.config.SupernodeConfig.KeyName)
	if err != nil {
		// Already handled in checkKeyExists
		return nil
	}

	pubKey, err := keyInfo.GetPubKey()
	if err != nil {
		return fmt.Errorf("failed to get public key for key '%s': %w", cv.config.SupernodeConfig.KeyName, err)
	}

	addr := sdk.AccAddress(pubKey.Address())
	if addr.String() != cv.config.SupernodeConfig.Identity {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{
			Field:    "identity",
			Expected: addr.String(),
			Actual:   cv.config.SupernodeConfig.Identity,
			Message:  fmt.Sprintf("Key '%s' resolves to %s but config identity is %s", cv.config.SupernodeConfig.KeyName, addr.String(), cv.config.SupernodeConfig.Identity),
		})
	}
	return nil
}

// checkSupernodeExists queries chain for supernode registration
func (cv *ConfigVerifier) checkSupernodeExists(ctx context.Context, result *VerificationResult) (*sntypes.SuperNode, error) {
	sn, err := cv.lumeraClient.SuperNode().GetSupernodeBySupernodeAddress(ctx, cv.config.SupernodeConfig.Identity)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{
			Field:   "registration",
			Actual:  "not_registered",
			Message: fmt.Sprintf("Supernode not registered on chain for address %s", cv.config.SupernodeConfig.Identity),
		})
		return nil, nil
	}
	return sn, nil
}

// checkP2PPortMatches compares config P2P port with chain
func (cv *ConfigVerifier) checkP2PPortMatches(result *VerificationResult, supernode *sntypes.SuperNode) {
	configPort := fmt.Sprintf("%d", cv.config.P2PConfig.Port)
	chainPort := supernode.P2PPort
	
	if chainPort != "" && chainPort != configPort {
		result.Valid = false
		result.Errors = append(result.Errors, ConfigError{
			Field:    "p2p_port",
			Expected: chainPort,
			Actual:   configPort,
			Message:  fmt.Sprintf("P2P port mismatch: config=%s, chain=%s", configPort, chainPort),
		})
	}
}

// checkSupernodeState verifies supernode is in active state
func (cv *ConfigVerifier) checkSupernodeState(result *VerificationResult, supernode *sntypes.SuperNode) {
	if len(supernode.States) > 0 {
		lastState := supernode.States[len(supernode.States)-1]
		if lastState.State.String() != "SUPERNODE_STATE_ACTIVE" {
			result.Valid = false
			result.Errors = append(result.Errors, ConfigError{
				Field:    "state",
				Expected: "SUPERNODE_STATE_ACTIVE",
				Actual:   lastState.State.String(),
				Message:  fmt.Sprintf("Supernode state is %s (expected ACTIVE)", lastState.State.String()),
			})
		}
	}
}

// checkSupernodePortAlignment compares supernode port with on-chain registered port (error if mismatch)
func (cv *ConfigVerifier) checkSupernodePortAlignment(result *VerificationResult, supernode *sntypes.SuperNode) {
	if len(supernode.PrevIpAddresses) > 0 {
		chainAddress := supernode.PrevIpAddresses[len(supernode.PrevIpAddresses)-1].Address
		
		// Extract port from chain address
		var chainPort string
		if idx := strings.LastIndex(chainAddress, ":"); idx != -1 {
			chainPort = chainAddress[idx+1:]
		}
		
		configPort := fmt.Sprintf("%d", cv.config.SupernodeConfig.Port)
		if chainPort != "" && chainPort != configPort {
			result.Valid = false
			result.Errors = append(result.Errors, ConfigError{
				Field:    "supernode_port",
				Expected: chainPort,
				Actual:   configPort,
				Message:  fmt.Sprintf("Supernode port mismatch: config=%s, chain=%s", configPort, chainPort),
			})
		}
	}
}

// checkHostAlignment compares host with on-chain registered host (warning only - may differ due to load balancer)
func (cv *ConfigVerifier) checkHostAlignment(result *VerificationResult, supernode *sntypes.SuperNode) {
	if len(supernode.PrevIpAddresses) > 0 {
		chainAddress := supernode.PrevIpAddresses[len(supernode.PrevIpAddresses)-1].Address
		
		// Extract host from chain address
		chainHost := chainAddress
		if idx := strings.LastIndex(chainAddress, ":"); idx != -1 {
			chainHost = chainAddress[:idx]
		}
		
		if chainHost != cv.config.SupernodeConfig.Host {
			result.Warnings = append(result.Warnings, ConfigError{
				Field:    "host",
				Expected: cv.config.SupernodeConfig.Host,
				Actual:   chainHost,
				Message:  fmt.Sprintf("Host mismatch: config=%s, chain=%s", cv.config.SupernodeConfig.Host, chainHost),
			})
		}
	}
}