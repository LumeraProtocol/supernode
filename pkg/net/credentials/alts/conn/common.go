package conn

import (
	"encoding/binary"
	"errors"
	"fmt"

	altscommon "github.com/LumeraProtocol/supernode/pkg/net/credentials/alts/common"
)

const (
	// GcmTagSize is the GCM tag size is the difference in length between
	// plaintext and ciphertext. From crypto/cipher/gcm.go in Go crypto
	// library.
	GcmTagSize = 16
)

// ErrAuth occurs on authentication failure.
var ErrAuth = errors.New("message authentication failed")

// SliceForAppend takes a slice and a requested number of bytes. It returns a
// slice with the contents of the given slice followed by that many bytes and a
// second slice that aliases into it and contains only the extra bytes. If the
// original slice has sufficient capacity then no allocation is performed.
func SliceForAppend(in []byte, n int) (head, tail []byte) {
	if total := len(in) + n; cap(in) >= total {
		head = in[:total]
	} else {
		head = make([]byte, total)
		copy(head, in)
	}
	tail = head[len(in):]
	return head, tail
}

// ParseFramedMsg parse the provided buffer and returns a frame of the format
// msgLength+msg and any remaining bytes in that buffer.
func ParseFramedMsg(b []byte, maxLen uint32) ([]byte, []byte, error) {
	// If the size field is not complete, return the provided buffer as
	// remaining buffer.
	if len(b) < altscommon.MsgLenFieldSize {
		return nil, b, nil
	}
	msgLenField := b[:altscommon.MsgLenFieldSize]
	length := binary.LittleEndian.Uint32(msgLenField)
	if length > maxLen {
		return nil, nil, fmt.Errorf("received the frame length %d larger than the limit %d", length, maxLen)
	}
	if len(b) < int(length)+4 { // account for the first 4 msg length bytes.
		// Frame is not complete yet.
		return nil, b, nil
	}
	return b[:altscommon.MsgLenFieldSize+length], b[altscommon.MsgLenFieldSize+length:], nil
}
