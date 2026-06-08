package kademlia

import (
	"context"
	"testing"
)

// TestHandlePing_NilMessage ensures the request path rejects a nil message
// without dereferencing it. Regression for the kademlia handlePing nil-sender
// panic that crashed lumera-devnet-1 val4's supernode process (RCA: a peer
// sending a Ping with nil Sender caused gob.Encode to walk a nil *Node and
// SIGSEGV, killing the entire process because no upstream recover() existed).
func TestHandlePing_NilMessage(t *testing.T) {
	s := &Network{}
	res, err := s.handlePing(context.Background(), nil)
	if err == nil {
		t.Fatalf("nil message must return error; got res=%v err=nil", res)
	}
	if res != nil {
		t.Fatalf("nil message must return nil response; got %v", res)
	}
}

// TestHandlePing_NilSender ensures the request path rejects a Message with no
// Sender, before invoking dht.newMessage (which would deref the nil pointer).
func TestHandlePing_NilSender(t *testing.T) {
	s := &Network{}
	res, err := s.handlePing(context.Background(), &Message{MessageType: Ping})
	if err == nil {
		t.Fatalf("nil sender must return error; got res=%v err=nil", res)
	}
	if res != nil {
		t.Fatalf("nil sender must return nil response; got %v", res)
	}
}

// TestHandlePing_EmptySenderID ensures a Sender with empty ID is rejected.
// gob would happily encode an empty []byte but the encoded Receiver would be
// a self-reference with no identity — useless and a sanitisation gap.
func TestHandlePing_EmptySenderID(t *testing.T) {
	s := &Network{}
	cases := []struct {
		name string
		id   []byte
	}{
		{"nil-id", nil},
		{"empty-slice-id", []byte{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &Message{MessageType: Ping, Sender: &Node{ID: tc.id, IP: "1.2.3.4", Port: 4445}}
			res, err := s.handlePing(context.Background(), msg)
			if err == nil {
				t.Fatalf("empty sender ID must return error; got res=%v err=nil", res)
			}
			if res != nil {
				t.Fatalf("empty sender ID must return nil response; got %v", res)
			}
		})
	}
}

// TestHandlePing_PanicRecovered constructs a Message that — once it reaches
// dht.newMessage — would panic because the *Network has no DHT wired up.
// The deferred recover() in handlePing must turn that panic into an error
// return, NOT crash the test (which would crash the supernode in production).
//
// We satisfy the nil-guard with a valid Sender so the panic comes from the
// dht.newMessage call (nil *DHT receiver). This is the defence-in-depth
// layer of the fix: even if a future code path reintroduces a nil deref,
// the request handler must not kill the process.
func TestHandlePing_PanicRecovered(t *testing.T) {
	s := &Network{} // s.dht == nil → newMessage(...) will panic on receiver deref
	msg := &Message{
		MessageType: Ping,
		Sender:      &Node{ID: []byte("peer-id-32-bytes-or-whatever-fits"), IP: "1.2.3.4", Port: 4445},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handlePing leaked a panic to caller: %v", r)
		}
	}()
	res, err := s.handlePing(context.Background(), msg)
	if err == nil {
		t.Fatalf("expected error from recovered panic; got res=%v err=nil", res)
	}
}
