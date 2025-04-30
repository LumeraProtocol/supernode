package conn

import (
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
	. "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

var (
	// ALTS record protocol names.
	ALTSRecordProtocols = make([]string, 0)
)

func RegisterALTSRecordProtocols() {
	altsRecordFuncs := map[string]ALTSRecordFunc{
		// ALTS handshaker protocols.
		RecordProtocolAESGCM: func(s Side, keyData []byte) (ALTSRecordCrypto, error) {
			return NewAES128GCM(s, keyData)
		},
		RecordProtocolAESGCMReKey: func(s Side, keyData []byte) (ALTSRecordCrypto, error) {
			return NewAES128GCMRekey(s, keyData)
		},
		RecordProtocolXChaCha20Poly1305ReKey: func(s Side, keyData []byte) (ALTSRecordCrypto, error) {
			return NewXChaCha20Poly1305ReKey(s, keyData)
		},
	}
	for protocol, f := range altsRecordFuncs {
		if err := RegisterProtocol(protocol, f); err != nil {
			panic(err)
		}
		ALTSRecordProtocols = append(ALTSRecordProtocols, protocol)
	}
}

func UnregisterALTSRecordProtocols() {
	ALTSRecordProtocols = make([]string, 0)
}

var (
	recMu sync.RWMutex
	facts = make(map[string]common.ALTSRecordFunc)
)

// registerFactory is called from init() blocks.
func registerFactory(proto string, f common.ALTSRecordFunc) {
	recMu.Lock()
	facts[proto] = f
	recMu.Unlock()
}

// lookupFactory is used by NewConn.
func lookupFactory(proto string) (common.ALTSRecordFunc, bool) {
	recMu.RLock()
	f, ok := facts[proto]
	recMu.RUnlock()
	return f, ok
}
