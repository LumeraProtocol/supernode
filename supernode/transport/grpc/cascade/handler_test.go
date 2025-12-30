package cascade

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	tasks "github.com/LumeraProtocol/supernode/v2/pkg/task"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeRegisterStream struct {
	ctx  context.Context
	reqs []*pb.RegisterRequest
	i    int
}

func (s *fakeRegisterStream) Send(*pb.RegisterResponse) error { return nil }

func (s *fakeRegisterStream) Recv() (*pb.RegisterRequest, error) {
	if s.i >= len(s.reqs) {
		return nil, io.EOF
	}
	r := s.reqs[s.i]
	s.i++
	return r, nil
}

func (s *fakeRegisterStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeRegisterStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeRegisterStream) SetTrailer(metadata.MD)       {}
func (s *fakeRegisterStream) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
func (s *fakeRegisterStream) SendMsg(interface{}) error { return nil }
func (s *fakeRegisterStream) RecvMsg(interface{}) error { return nil }

func TestRegister_CleansTempDirOnHandlerError(t *testing.T) {
	tmpRoot := t.TempDir()

	prevTmpDir, hadPrevTmpDir := os.LookupEnv("TMPDIR")
	t.Cleanup(func() {
		if hadPrevTmpDir {
			_ = os.Setenv("TMPDIR", prevTmpDir)
		} else {
			_ = os.Unsetenv("TMPDIR")
		}
	})
	if err := os.Setenv("TMPDIR", tmpRoot); err != nil {
		t.Fatalf("set TMPDIR: %v", err)
	}

	server := &ActionServer{}
	err := server.Register(&fakeRegisterStream{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	entries, rerr := os.ReadDir(tmpRoot)
	if rerr != nil {
		t.Fatalf("read tmpRoot: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected TMPDIR to be empty, found %d entries", len(entries))
	}
}

func TestRegister_RejectsDuplicateActionID(t *testing.T) {
	tmpRoot := t.TempDir()

	prevTmpDir, hadPrevTmpDir := os.LookupEnv("TMPDIR")
	t.Cleanup(func() {
		if hadPrevTmpDir {
			_ = os.Setenv("TMPDIR", prevTmpDir)
		} else {
			_ = os.Unsetenv("TMPDIR")
		}
	})
	if err := os.Setenv("TMPDIR", tmpRoot); err != nil {
		t.Fatalf("set TMPDIR: %v", err)
	}

	tr := tasks.New()
	actionID := "action-1"
	tr.Start(serviceCascadeUpload, actionID)

	server := &ActionServer{tracker: tr, uploadTimeout: time.Second}
	stream := &fakeRegisterStream{
		reqs: []*pb.RegisterRequest{
			{RequestType: &pb.RegisterRequest_Metadata{Metadata: &pb.Metadata{TaskId: "task-1", ActionId: actionID}}},
		},
	}

	err := server.Register(stream)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
}
