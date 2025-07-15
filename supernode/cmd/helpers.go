package cmd

import (
	"fmt"
	"strings"

	"github.com/LumeraProtocol/supernode/pkg/keyring"
	"github.com/LumeraProtocol/supernode/supernode/config"
	cKeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// initKeyringFromConfig initializes keyring using app configuration
func initKeyringFromConfig(config *config.Config) (cKeyring.Keyring, error) {
	return keyring.InitKeyring(config.KeyringConfig.Backend, config.GetKeyringDir())
}

// getAddressFromKeyName extracts address from keyring using key name
func getAddressFromKeyName(kr cKeyring.Keyring, keyName string) (sdk.AccAddress, error) {
	keyInfo, err := kr.Key(keyName)
	if err != nil {
		return nil, fmt.Errorf("key not found: %w", err)
	}

	address, err := keyInfo.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from key: %w", err)
	}

	return address, nil
}

// processAndValidateMnemonic processes and validates a mnemonic phrase
func processAndValidateMnemonic(mnemonic string) (string, error) {
	// Normalize whitespace (replace multiple spaces with single space)
	processed := strings.TrimSpace(mnemonic)
	processed = strings.Join(strings.Fields(processed), " ")

	// Validate BIP39 mnemonic word count
	wordCount := len(strings.Fields(processed))
	if !isValidBIP39WordCount(wordCount) {
		return "", fmt.Errorf("invalid mnemonic word count: %d. Valid BIP39 mnemonic lengths are 12, 15, 18, 21, or 24 words", wordCount)
	}

	return processed, nil
}

// isValidBIP39WordCount checks if the word count is valid for BIP39 mnemonics
func isValidBIP39WordCount(wordCount int) bool {
	validCounts := []int{12, 15, 18, 21, 24}
	for _, count := range validCounts {
		if wordCount == count {
			return true
		}
	}
	return false
}
