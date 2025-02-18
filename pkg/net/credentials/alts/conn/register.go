package conn

import (
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