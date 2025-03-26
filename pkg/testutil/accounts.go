package testutil

import (
	"crypto/ecdh"
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/go-bip39"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
)

// setupTestKeyExchange creates a key exchange instance for testing
func SetupTestKeyExchange(t *testing.T, kb keyring.Keyring, addr string, peerType securekeyx.PeerType) *securekeyx.SecureKeyExchange {
	ke, err := securekeyx.NewSecureKeyExchange(kb, addr, peerType, ecdh.P256())
	require.NoError(t, err)
	return ke
}

func generateMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(256) // 128 bits for a 12-word mnemonic
	if err != nil {
		return "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", err
	}
	return mnemonic, nil
}

func CreateTestKeyring() keyring.Keyring {
	// Create a codec using the modern protobuf-based codec
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	protoCodec := codec.NewProtoCodec(interfaceRegistry)
	// Register public and private key implementations
	cryptocodec.RegisterInterfaces(interfaceRegistry)

	// Create an in-memory keyring
	kr := keyring.NewInMemory(protoCodec)

	return kr
}

func addTestAccountToKeyring(kr keyring.Keyring, accountName string) error {
	mnemonic, err := generateMnemonic()
	if err != nil {
		return err
	}
	algoList, _ := kr.SupportedAlgorithms()
	signingAlgo, err := keyring.NewSigningAlgoFromString("secp256k1", algoList)
	if err != nil {
		return err
	}
	hdPath := hd.CreateHDPath(118, 0, 0).String() // "118" is Cosmos coin type

	_, err = kr.NewAccount(accountName, mnemonic, "", hdPath, signingAlgo)
	if err != nil {
		return err
	}

	return nil
}

// setupTestAccounts creates test accounts in keyring
func SetupTestAccounts(t *testing.T, kr keyring.Keyring, accountNames []string) []string {
	var addresses []string

	for _, accountName := range accountNames {
		err := addTestAccountToKeyring(kr, accountName)
		require.NoError(t, err)

		keyInfo, err := kr.Key(accountName)
		require.NoError(t, err)

		address, err := keyInfo.GetAddress()
		require.NoError(t, err, "failed to get address for account %s", accountName)

		addresses = append(addresses, address.String())
	}

	return addresses
}
