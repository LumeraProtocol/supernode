package cascade

import (
	"context"
	"io"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"google.golang.org/grpc/metadata"
)

// mockStream simulates pb.CascadeService_RegisterServer
type mockStream struct {
	ctx     context.Context
	request []*pb.RegisterRequest
	sent    []*pb.RegisterResponse
	pos     int
}

func (m *mockStream) Context() context.Context {
	return m.ctx
}

func (m *mockStream) Send(resp *pb.RegisterResponse) error {
	m.sent = append(m.sent, resp)
	return nil
}

func (m *mockStream) Recv() (*pb.RegisterRequest, error) {
	if m.pos >= len(m.request) {
		return nil, io.EOF
	}
	req := m.request[m.pos]
	m.pos++
	return req, nil
}

func (m *mockStream) SetHeader(md metadata.MD) error  { return nil }
func (m *mockStream) SendHeader(md metadata.MD) error { return nil }
func (m *mockStream) SetTrailer(md metadata.MD)       {}
func (m *mockStream) SendMsg(_ any) error             { return nil }
func (m *mockStream) RecvMsg(_ any) error             { return nil }
