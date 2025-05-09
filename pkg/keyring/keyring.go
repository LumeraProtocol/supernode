package keyring

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/go-bip39"
)

const (
	// Default BIP39 passphrase
	DefaultBIP39Passphrase = ""

	// Default HD path for Cosmos accounts
	DefaultHDPath = "m/44'/118'/0'/0/0" // Cosmos HD path

	// Lumera address prefixes
	AccountAddressPrefix = "lumera"
	Name                 = "lumera"

	// Default keyring name
	KeyringServiceName = "supernode-keyring"

	defaultEntropySize = 256 // Default entropy size for mnemonic generation
)

// InitSDKConfig initializes the SDK configuration with Lumera-specific settings
func InitSDKConfig() {
	// Set prefixes
	accountPubKeyPrefix := AccountAddressPrefix + "pub"
	validatorAddressPrefix := AccountAddressPrefix + "valoper"
	validatorPubKeyPrefix := AccountAddressPrefix + "valoperpub"
	consNodeAddressPrefix := AccountAddressPrefix + "valcons"
	consNodePubKeyPrefix := AccountAddressPrefix + "valconspub"

	// Set and seal config
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(AccountAddressPrefix, accountPubKeyPrefix)
	config.SetBech32PrefixForValidator(validatorAddressPrefix, validatorPubKeyPrefix)
	config.SetBech32PrefixForConsensusNode(consNodeAddressPrefix, consNodePubKeyPrefix)
	config.Seal()
}

// InitKeyring initializes the keyring
func InitKeyring(backend, dir string) (keyring.Keyring, error) {
	// Determine keyring backend type
	var backendType string
	switch backend {
	case "file":
		backendType = "file"
	case "os":
		backendType = "os"
	case "test":
		backendType = "test"
	case "memory", "mem":
		backendType = "memory"
	default:
		return nil, fmt.Errorf("unsupported keyring backend: %s", backend)
	}

	// Create interface registry and codec
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)

	// Create keyring
	kr, err := keyring.New(
		KeyringServiceName,
		backendType,
		dir,
		nil, //Using nil for stdin to avoid interactive prompts when using test backend
		cdc,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize keyring: %w", err)
	}

	return kr, nil
}

// GenerateMnemonic generates a new BIP39 mnemonic
func GenerateMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(defaultEntropySize)
	if err != nil {
		return "", fmt.Errorf("failed to generate entropy: %w", err)
	}

	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("failed to generate mnemonic: %w", err)
	}

	return mnemonic, nil
}

// CreateNewAccount creates a new account in the keyring
func CreateNewAccount(kr keyring.Keyring, name string, entropySize int) (string, *keyring.Record, error) {
	// Generate a new mnemonic
	mnemonic, err := GenerateMnemonic()
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate mnemonic: %w", err)
	}

	// Create a new account with the generated mnemonic
	info, err := kr.NewAccount(
		name,
		mnemonic,
		DefaultBIP39Passphrase,
		DefaultHDPath,
		hd.Secp256k1,
	)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create new account: %w", err)
	}

	return mnemonic, info, nil
}

// RecoverAccountFromMnemonic recovers an account from a mnemonic
func RecoverAccountFromMnemonic(kr keyring.Keyring, name, mnemonic string) (*keyring.Record, error) {
	// Import account from mnemonic
	info, err := kr.NewAccount(
		name,
		mnemonic,
		DefaultBIP39Passphrase,
		DefaultHDPath,
		hd.Secp256k1,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to recover account from mnemonic: %w", err)
	}

	return info, nil
}

// DerivePrivKeyFromMnemonic derives a private key directly from a mnemonic
func DerivePrivKeyFromMnemonic(mnemonic, hdPath string) (*secp256k1.PrivKey, error) {
	if hdPath == "" {
		hdPath = DefaultHDPath
	}

	seed := bip39.NewSeed(mnemonic, DefaultBIP39Passphrase)
	master, ch := hd.ComputeMastersFromSeed(seed)

	derivedKey, err := hd.DerivePrivateKeyForPath(master, ch, hdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to derive private key: %w", err)
	}

	return &secp256k1.PrivKey{Key: derivedKey}, nil
}

// SignBytes signs a byte array using a key from the keyring
func SignBytes(kr keyring.Keyring, name string, bytes []byte) ([]byte, error) {
	// Get the key from the keyring
	record, err := kr.Key(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get key '%s' from keyring: %w", name, err)
	}

	// Get the address from the record
	addr, err := record.GetAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get address from record: %w", err)
	}
	// Sign the bytes
	signature, _, err := kr.SignByAddress(addr, bytes, signing.SignMode_SIGN_MODE_DIRECT)
	if err != nil {
		return nil, fmt.Errorf("failed to sign bytes: %w", err)
	}

	return signature, nil
}
