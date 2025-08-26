package cli

import (
	"fmt"
	"time"
	"net"
	"context"
	"strconv"

	"github.com/LumeraProtocol/supernode/v2/p2p/kademlia"
)

const (
	defaultConnDeadline                = 10 * time.Minute
)

// Ping dials and sends a Ping message to the remote P2P endpoint
func (p *P2P) Ping(timeout time.Duration) error {
	if p.clientTC == nil {
		return fmt.Errorf("P2P: client credentials not initialized")
	}
	if timeout <= 0 {
		timeout = p.Timeout
	}

	clientCtx, clientCancel := context.WithTimeout(context.Background(), timeout)
	defer clientCancel()

	// dial the remote address with tcp
	remoteAddress := p.receiver.IP + ":" + strconv.Itoa(int(p.receiver.Port))
	var d net.Dialer
	rawConn, err := d.DialContext(clientCtx, "tcp", remoteAddress)
	if err != nil {
		return fmt.Errorf("P2P: failed to dial remote address %s: %w", remoteAddress, err)
	}
	defer rawConn.Close()

	// set the deadline for read and write
	rawConn.SetDeadline(time.Now().UTC().Add(defaultConnDeadline))

	secureConn, _, err := p.clientTC.ClientHandshake(clientCtx, "", rawConn)
	if err != nil {
		return fmt.Errorf("P2P: failed to perform client handshake: %w", err)
	}
	defer secureConn.Close()

	request := &kademlia.Message{
		Sender:    p.sender,
		Receiver:  p.receiver,
		MessageType:     kademlia.Ping,
	}
	// encode and send the request message
	data, err := encode(request)
	if err != nil {
		return fmt.Errorf("P2P: failed to encode request message: %w", err)
	}
	if _, err := secureConn.Write(data); err != nil {
		return fmt.Errorf("P2P: failed to write to secure connection: %w", err)
	}

	// receive and decode the response message
	response, err := decode(secureConn)
	if err != nil {
		return fmt.Errorf("conn read: %w", err)
	}
	if response.MessageType != kademlia.Ping {
		return fmt.Errorf("P2P: unexpected response message type: %v", response.MessageType)
	}
	fmt.Printf("P2P is alive (%s)\n", remoteAddress)

	return nil
}