package keyring

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	sdkkeyring "github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	evmhd "github.com/cosmos/evm/crypto/hd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEVMKeyring creates an in-memory keyring supporting both key algorithms.
func newEVMKeyring(t *testing.T) sdkkeyring.Keyring {
	t.Helper()
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	evmcryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	return sdkkeyring.NewInMemory(cdc, evmhd.EthSecp256k1Option())
}

func TestGetBech32Address(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	kr := sdkkeyring.NewInMemory(cdc)

	rec, _, err := kr.NewMnemonic("test", sdkkeyring.English, DefaultHDPath, DefaultBIP39Passphrase, hd.Secp256k1)
	if err != nil {
		t.Fatalf("new mnemonic: %v", err)
	}
	addr, err := rec.GetAddress()
	if err != nil {
		t.Fatalf("get address: %v", err)
	}

	got, err := GetBech32Address(kr, "test", "lumera")
	if err != nil {
		t.Fatalf("get bech32 address: %v", err)
	}
	if got == "" {
		t.Fatal("empty bech32 address")
	}
	bz, err := sdk.GetFromBech32(got, "lumera")
	if err != nil {
		t.Fatalf("decode bech32: %v", err)
	}
	if !bytes.Equal(bz, addr.Bytes()) {
		t.Fatalf("decoded address mismatch")
	}
}

func TestDefaultHDPath_IsCoinType60(t *testing.T) {
	assert.Equal(t, "m/44'/60'/0'/0/0", DefaultHDPath,
		"DefaultHDPath should use EVM coin type 60")
}

func TestCreateNewAccount_ProducesEthSecp256k1Key(t *testing.T) {
	kr := newEVMKeyring(t)

	_, rec, err := CreateNewAccount(kr, "test-evm")
	require.NoError(t, err)

	pubKey, err := rec.GetPubKey()
	require.NoError(t, err)

	_, isEthSecp := pubKey.(*ethsecp256k1.PubKey)
	assert.True(t, isEthSecp, "CreateNewAccount should produce eth_secp256k1 key")

	_, isLegacy := pubKey.(*secp256k1.PubKey)
	assert.False(t, isLegacy, "CreateNewAccount should NOT produce legacy secp256k1 key")
}

func TestRecoverAccountFromMnemonic_ProducesEthSecp256k1Key(t *testing.T) {
	kr := newEVMKeyring(t)

	mn, err := GenerateMnemonic()
	require.NoError(t, err)

	rec, err := RecoverAccountFromMnemonic(kr, "recovered", mn)
	require.NoError(t, err)

	pubKey, err := rec.GetPubKey()
	require.NoError(t, err)

	_, isEthSecp := pubKey.(*ethsecp256k1.PubKey)
	assert.True(t, isEthSecp, "recovered key should be eth_secp256k1")
}

func TestRecoverAccountFromMnemonic_Deterministic(t *testing.T) {
	mn, err := GenerateMnemonic()
	require.NoError(t, err)

	kr1 := newEVMKeyring(t)
	rec1, err := RecoverAccountFromMnemonic(kr1, "key1", mn)
	require.NoError(t, err)
	addr1, err := rec1.GetAddress()
	require.NoError(t, err)

	kr2 := newEVMKeyring(t)
	rec2, err := RecoverAccountFromMnemonic(kr2, "key2", mn)
	require.NoError(t, err)
	addr2, err := rec2.GetAddress()
	require.NoError(t, err)

	assert.Equal(t, addr1.String(), addr2.String(),
		"same mnemonic should produce same address")
}

func TestDerivePrivKeyFromMnemonic_ReturnsEthSecp256k1(t *testing.T) {
	mn, err := GenerateMnemonic()
	require.NoError(t, err)

	privKey, err := DerivePrivKeyFromMnemonic(mn, "")
	require.NoError(t, err)
	require.NotNil(t, privKey)

	assert.IsType(t, &ethsecp256k1.PrivKey{}, privKey)
	assert.Len(t, privKey.Key, 32, "private key should be 32 bytes")
}

func TestDerivePrivKeyFromMnemonic_MatchesKeyring(t *testing.T) {
	mn, err := GenerateMnemonic()
	require.NoError(t, err)

	// Derive via standalone function.
	privKey, err := DerivePrivKeyFromMnemonic(mn, "")
	require.NoError(t, err)
	derivedAddr := sdk.AccAddress(privKey.PubKey().Address())

	// Recover via keyring.
	kr := newEVMKeyring(t)
	rec, err := RecoverAccountFromMnemonic(kr, "test", mn)
	require.NoError(t, err)
	krAddr, err := rec.GetAddress()
	require.NoError(t, err)

	assert.Equal(t, krAddr.String(), derivedAddr.String(),
		"DerivePrivKeyFromMnemonic and RecoverAccountFromMnemonic should produce same address")
}

func TestLegacyAndEVMKeys_ProduceDifferentAddresses(t *testing.T) {
	kr := newEVMKeyring(t)
	mn, err := GenerateMnemonic()
	require.NoError(t, err)

	// Create legacy key (coin type 118).
	legacyRec, err := kr.NewAccount("legacy", mn, "", "m/44'/118'/0'/0/0", hd.Secp256k1)
	require.NoError(t, err)
	legacyAddr, err := legacyRec.GetAddress()
	require.NoError(t, err)

	// Create EVM key (coin type 60) with same mnemonic.
	evmRec, err := kr.NewAccount("evm", mn, "", "m/44'/60'/0'/0/0", evmhd.EthSecp256k1)
	require.NoError(t, err)
	evmAddr, err := evmRec.GetAddress()
	require.NoError(t, err)

	assert.NotEqual(t, legacyAddr.String(), evmAddr.String(),
		"same mnemonic with different coin types should produce different addresses")

	// Verify key types.
	legacyPub, _ := legacyRec.GetPubKey()
	_, isLegacy := legacyPub.(*secp256k1.PubKey)
	assert.True(t, isLegacy)

	evmPub, _ := evmRec.GetPubKey()
	_, isEVM := evmPub.(*ethsecp256k1.PubKey)
	assert.True(t, isEVM)
}

func TestSignBytes_WithEVMKey(t *testing.T) {
	kr := newEVMKeyring(t)
	_, _, err := CreateNewAccount(kr, "signer")
	require.NoError(t, err)

	msg := []byte("hello world")
	sig, err := SignBytes(kr, "signer", msg)
	require.NoError(t, err)
	assert.NotEmpty(t, sig, "signature should not be empty")
}

func TestGetAddress_WithEVMKey(t *testing.T) {
	kr := newEVMKeyring(t)
	_, _, err := CreateNewAccount(kr, "test")
	require.NoError(t, err)

	addr, err := GetAddress(kr, "test")
	require.NoError(t, err)
	assert.NotEmpty(t, addr.String())
}

func TestInitSDKConfig_IsIdempotent(t *testing.T) {
	InitSDKConfig()
	InitSDKConfig()

	cfg := sdk.GetConfig()
	assert.Equal(t, AccountAddressPrefix, cfg.GetBech32AccountAddrPrefix())
	assert.Equal(t, AccountAddressPrefix+"pub", cfg.GetBech32AccountPubPrefix())
	assert.Equal(t, AccountAddressPrefix+"valoper", cfg.GetBech32ValidatorAddrPrefix())
	assert.Equal(t, AccountAddressPrefix+"valoperpub", cfg.GetBech32ValidatorPubPrefix())
	assert.Equal(t, AccountAddressPrefix+"valcons", cfg.GetBech32ConsensusAddrPrefix())
	assert.Equal(t, AccountAddressPrefix+"valconspub", cfg.GetBech32ConsensusPubPrefix())
}

func TestInitSDKConfig_SealedConfigDoesNotPanic(t *testing.T) {
	if os.Getenv("SUPERNODE_TEST_SEALED_CONFIG") == "1" {
		sdk.GetConfig().Seal()
		InitSDKConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestInitSDKConfig_SealedConfigDoesNotPanic")
	cmd.Env = append(os.Environ(), "SUPERNODE_TEST_SEALED_CONFIG=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "subprocess should succeed without panic: %s", string(out))
}
