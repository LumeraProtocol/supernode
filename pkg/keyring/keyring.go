// Package keyring provides helpers around Cosmos-SDK key management.
package keyring

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	"github.com/cosmos/go-bip39"

	"github.com/LumeraProtocol/supernode/supernode/config"
)

const (
	DefaultBIP39Passphrase = ""
	DefaultHDPath          = "m/44'/118'/0'/0/0"
	AccountAddressPrefix   = "lumera"
	KeyringServiceName     = "supernode-keyring"
	defaultEntropySize     = 256
)

func InitSDKConfig() {
	cfg := types.GetConfig()
	cfg.SetBech32PrefixForAccount(AccountAddressPrefix, AccountAddressPrefix+"pub")
	cfg.SetBech32PrefixForValidator(AccountAddressPrefix+"valoper", AccountAddressPrefix+"valoperpub")
	cfg.SetBech32PrefixForConsensusNode(AccountAddressPrefix+"valcons", AccountAddressPrefix+"valconspub")
	cfg.Seal()
}

func InitKeyring(cfg config.KeyringConfig) (sdkkeyring.Keyring, error) {
	backend, err := normaliseBackend(cfg.Backend)
	if err != nil {
		return nil, err
	}
	// Use the directory as-is, it should already be resolved by the config
	dir := cfg.Dir

	reader, err := buildReaderAndPossiblySwapStdin(cfg, backend)
	if err != nil {
		return nil, err
	}

	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)

	return sdkkeyring.New(KeyringServiceName, backend, dir, reader, cdc)
}

// buildReaderAndPossiblySwapStdin returns the reader handed to Cosmos-SDK.
// For automation we replace os.Stdin with the read-end of an os.Pipe whose
// FD is *not* a TTY, so input.GetPassword() falls into its non-interactive
// code path.
func buildReaderAndPossiblySwapStdin(cfg config.KeyringConfig, backend string) (io.Reader, error) {
	if backend == "test" {
		return nil, nil
	}

	pass := selectPassphrase(cfg)
	if pass == "" {
		// Interactive: leave the real terminal in place.
		return os.Stdin, nil
	}

	// ---------- non-interactive ----------

	// Create a pipe (both ends are *os.File, so assignment is legal).
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	// Replace os.Stdin with the pipe's read end (non-TTY).
	os.Stdin = r

	// Continuously write the passphrase + newline from a goroutine.
	go func() {
		line := []byte(pass + "\n")
		for {
			if _, err := w.Write(line); err != nil {
				return // pipe closed on shutdown
			}
		}
	}()

	// The keyring prompt reads from the reader we pass to the SDK, so wrap
	// the same *os.File r in a bufio.Reader and return it.
	return bufio.NewReader(r), nil
}

func selectPassphrase(cfg config.KeyringConfig) string {
	switch {
	case cfg.PassPlain != "":
		return cfg.PassPlain
	case cfg.PassEnv != "":
		return os.Getenv(cfg.PassEnv)
	case cfg.PassFile != "":
		if b, err := os.ReadFile(cfg.PassFile); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func normaliseBackend(b string) (string, error) {
	switch strings.ToLower(b) {
	case "file":
		return "file", nil
	case "os":
		return "os", nil
	case "test":
		return "test", nil
	case "memory", "mem":
		return "memory", nil
	default:
		return "", fmt.Errorf("unsupported keyring backend: %s", b)
	}
}


func GenerateMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(defaultEntropySize)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

func CreateNewAccount(kr sdkkeyring.Keyring, name string) (string, *sdkkeyring.Record, error) {
	mn, err := GenerateMnemonic()
	if err != nil {
		return "", nil, err
	}
	info, err := kr.NewAccount(name, mn, DefaultBIP39Passphrase, DefaultHDPath, hd.Secp256k1)
	return mn, info, err
}

func RecoverAccountFromMnemonic(kr sdkkeyring.Keyring, name, mnemonic string) (*sdkkeyring.Record, error) {
	return kr.NewAccount(name, mnemonic, DefaultBIP39Passphrase, DefaultHDPath, hd.Secp256k1)
}

func DerivePrivKeyFromMnemonic(mnemonic, hdPath string) (*secp256k1.PrivKey, error) {
	if hdPath == "" {
		hdPath = DefaultHDPath
	}
	seed := bip39.NewSeed(mnemonic, DefaultBIP39Passphrase)
	master, ch := hd.ComputeMastersFromSeed(seed)
	derived, err := hd.DerivePrivateKeyForPath(master, ch, hdPath)
	if err != nil {
		return nil, err
	}
	return &secp256k1.PrivKey{Key: derived}, nil
}

func SignBytes(kr sdkkeyring.Keyring, name string, bz []byte) ([]byte, error) {
	rec, err := kr.Key(name)
	if err != nil {
		return nil, err
	}
	addr, err := rec.GetAddress()
	if err != nil {
		return nil, err
	}
	sig, _, err := kr.SignByAddress(addr, bz, signing.SignMode_SIGN_MODE_DIRECT)
	return sig, err
}
