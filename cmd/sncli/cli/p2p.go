package cli

import (
	"fmt"
	"io"
	"net"
	"time"
	"strconv"
	"encoding/binary"	
	"encoding/gob"
	"bytes"

	"github.com/LumeraProtocol/lumera/x/lumeraid/securekeyx"
	ltc "github.com/LumeraProtocol/supernode/v2/pkg/net/credentials"
	"google.golang.org/grpc/credentials"
	snUtils "github.com/LumeraProtocol/supernode/v2/pkg/utils"
	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
)

const (
	defaultMaxPayloadSize              = 2 // MB
)

// P2P keeps all state needed for secure P2P operations.
type P2P struct {
	cfgOpts     *CLIConfig
	clientTC    credentials.TransportCredentials
	sender      *kademlia.Node
	receiver    *kademlia.Node

	Timeout	 time.Duration
	Conn     net.Conn
	Listener net.Listener
}

// encode the message
func encode(message *kademlia.Message) ([]byte, error) {
	var buf bytes.Buffer

	encoder := gob.NewEncoder(&buf)
	// encode the message with gob library
	if err := encoder.Encode(message); err != nil {
		return nil, err
	}

	if snUtils.BytesIntToMB(buf.Len()) > defaultMaxPayloadSize {
		return nil, fmt.Errorf("payload too big")
	}

	var header [8]byte
	// prepare the header
	binary.PutUvarint(header[:], uint64(buf.Len()))

	var data []byte
	data = append(data, header[:]...)
	data = append(data, buf.Bytes()...)

	return data, nil
}

// decode the message
func decode(conn io.Reader) (*kademlia.Message, error) {
	// read the header
	header := make([]byte, 8)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}

	// parse the length of message
	length, err := binary.ReadUvarint(bytes.NewBuffer(header))
	if err != nil {
		return nil, fmt.Errorf("parse header length: %w", err)
	}

	if snUtils.BytesToMB(length) > defaultMaxPayloadSize {
		return nil, fmt.Errorf("payload too big")
	}

	// read the message body
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, err
	}

	// new a decoder
	decoder := gob.NewDecoder(bytes.NewBuffer(data))
	// decode the message structure
	message := &kademlia.Message{}
	if err = decoder.Decode(message); err != nil {
		return nil, err
	}

	return message, nil
}

func (p *P2P) Initialize(cli *CLI) error {
	if cli == nil || cli.CfgOpts == nil {
		return fmt.Errorf("P2P: CLI/config not initialized")
	}
	p.cfgOpts = cli.CfgOpts

	if p.cfgOpts.GetLocalIdentity() == "" {
		return fmt.Errorf("P2P: keyring.local_address is required")
	}
	if p.cfgOpts.GetRemoteIdentity() == "" {
		return fmt.Errorf("P2P: supernode.address is required")
	}
	if p.cfgOpts.Supernode.P2PEndpoint == "" {
		return fmt.Errorf("P2P: --p2p_endpoint (REMOTE host:port) is required")
	}
	host, portStr, err := net.SplitHostPort(p.cfgOpts.Supernode.P2PEndpoint)
	if err != nil {
		return fmt.Errorf("P2P: failed to parse --p2p_endpoint: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("P2P: invalid port number in --p2p_endpoint: %w", err)
	}

	p.clientTC, err = ltc.NewClientCreds(&ltc.ClientOptions{
		CommonOptions: ltc.CommonOptions{
			Keyring:       cli.kr,
			LocalIdentity: p.cfgOpts.GetLocalIdentity(),
			PeerType:      securekeyx.Simplenode,
			Validator:     NewSecureKeyExchangeValidator(cli.lumeraClient),
		},
	})
	if err != nil {
		return fmt.Errorf("P2P: failed to create client credentials: %w", err)
	}
	lumeraTC, ok := p.clientTC.(*ltc.LumeraTC)
	if !ok {
		return fmt.Errorf("invalid credentials type")
	}

	lumeraTC.SetRemoteIdentity(p.cfgOpts.GetRemoteIdentity())

	p.sender = &kademlia.Node{
		ID: []byte(p.cfgOpts.GetLocalIdentity()),
		IP: "localhost",
		Port: 4445,
	}
	p.sender.SetHashedID()

	p.receiver = &kademlia.Node{
		ID: []byte(p.cfgOpts.GetRemoteIdentity()),
		IP: host,
		Port: uint16(port),
	}
	p.receiver.SetHashedID()
	p.Timeout = 15 * time.Second

	return nil
}

