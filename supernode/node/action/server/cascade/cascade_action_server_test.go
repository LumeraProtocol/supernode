package cascade

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	pb "github.com/LumeraProtocol/supernode/v2/gen/supernode/action/cascade"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade"
	lumeramocks "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors/mocks"
	cascademocks "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/mocks"
	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestRegister_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	// Expect Register to be called with any input, respond via callback
	mockTask.EXPECT().Register(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.RegisterRequest, send func(*cascade.RegisterResponse) error) error {
			return send(&cascade.RegisterResponse{
				EventType: 1,
				Message:   "registration successful",
				TxHash:    "tx123",
			})
		},
	).Times(1)

	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockStream{
		ctx: context.Background(),
		request: []*pb.RegisterRequest{
			{RequestType: &pb.RegisterRequest_Chunk{Chunk: &pb.DataChunk{Data: []byte("abc123")}}},
			{RequestType: &pb.RegisterRequest_Metadata{
				Metadata: &pb.Metadata{TaskId: "t1", ActionId: "a1"},
			}},
		},
	}

	err := server.Register(stream)
	assert.NoError(t, err)
	assert.Len(t, stream.sent, 1)
	assert.Equal(t, "registration successful", stream.sent[0].Message)
	assert.Equal(t, "tx123", stream.sent[0].TxHash)
}

func TestRegister_Error_NoMetadata(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)
	server := NewCascadeActionServer(mockFactory)

	stream := &mockStream{
		ctx: context.Background(),
		request: []*pb.RegisterRequest{
			{RequestType: &pb.RegisterRequest_Chunk{Chunk: &pb.DataChunk{Data: []byte("abc123")}}},
		},
	}

	err := server.Register(stream)
	assert.EqualError(t, err, "no metadata received")
}

func TestRegister_Error_TaskFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	mockTask.EXPECT().Register(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("task failed")).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockStream{
		ctx: context.Background(),
		request: []*pb.RegisterRequest{
			{RequestType: &pb.RegisterRequest_Chunk{Chunk: &pb.DataChunk{Data: []byte("abc123")}}},
			{RequestType: &pb.RegisterRequest_Metadata{
				Metadata: &pb.Metadata{TaskId: "t1", ActionId: "a1"},
			}},
		},
	}

	err := server.Register(stream)
	assert.EqualError(t, err, "registration failed: task failed")
}

func TestDownload_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create temporary file for testing
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")
	testContent := []byte("test file content for download")
	require.NoError(t, os.WriteFile(testFilePath, testContent, 0644))

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	// Expect Download to be called with any input, respond via callback
	mockTask.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.DownloadRequest, send func(*cascade.DownloadResponse) error) error {
			// Send download event first
			err := send(&cascade.DownloadResponse{
				EventType:     1,
				Message:       "download started",
				FilePath:      testFilePath,
				DownloadedDir: tempDir,
			})
			if err != nil {
				return err
			}
			// Send completion event
			return send(&cascade.DownloadResponse{
				EventType: 2,
				Message:   "download completed",
			})
		},
	).Times(1)

	mockTask.EXPECT().CleanupDownload(gomock.Any(), tempDir).Return(nil).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId: "action123",
	}

	err := server.Download(req, stream)
	assert.NoError(t, err)
	assert.Len(t, stream.sent, 3) // 2 events + 1 chunk (actual file content)

	// Check first response is an event
	assert.NotNil(t, stream.sent[0].GetEvent())
	assert.Equal(t, "download started", stream.sent[0].GetEvent().Message)

	// Check second response is an event
	assert.NotNil(t, stream.sent[1].GetEvent())
	assert.Equal(t, "download completed", stream.sent[1].GetEvent().Message)

	// Check third response is a chunk with correct content
	assert.NotNil(t, stream.sent[2].GetChunk())
	assert.Equal(t, testContent, stream.sent[2].GetChunk().Data)
}

func TestDownload_Error_TaskFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	mockTask.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("download failed")).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId: "action123",
	}

	err := server.Download(req, stream)
	assert.EqualError(t, err, "download failed")
}

func TestDownload_Error_NoArtefactRetrieved(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	// Download succeeds but returns no file path
	mockTask.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.DownloadRequest, send func(*cascade.DownloadResponse) error) error {
			return send(&cascade.DownloadResponse{
				EventType: 1,
				Message:   "download completed but no file",
				FilePath:  "", // Empty file path
			})
		},
	).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId: "action123",
	}

	err := server.Download(req, stream)
	assert.EqualError(t, err, "no artefact to stream")
}

func TestDownload_Error_FileReadFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	// Mock download that returns a non-existent file path
	mockTask.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.DownloadRequest, send func(*cascade.DownloadResponse) error) error {
			return send(&cascade.DownloadResponse{
				EventType:     1,
				Message:       "download completed",
				FilePath:      "/non/existent/file.txt", // Non-existent file
				DownloadedDir: "/tmp/test",
			})
		},
	).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId: "action123",
	}

	err := server.Download(req, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory")
}

func TestDownload_WithoutSignature_SkipsVerification(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create temporary file for testing
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")
	testContent := []byte("test file content")
	require.NoError(t, os.WriteFile(testFilePath, testContent, 0644))

	mockTask := cascademocks.NewMockCascadeTask(ctrl)
	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)

	// Expect Download to be called - signature verification should be skipped
	mockTask.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.DownloadRequest, send func(*cascade.DownloadResponse) error) error {
			return send(&cascade.DownloadResponse{
				EventType:     1,
				Message:       "download completed",
				FilePath:      testFilePath,
				DownloadedDir: tempDir,
			})
		},
	).Times(1)

	mockTask.EXPECT().CleanupDownload(gomock.Any(), tempDir).Return(nil).Times(1)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(mockTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	// Test with empty signature (should skip verification and proceed to download)
	req := &pb.DownloadRequest{
		ActionId:  "action123",
		Signature: "", // Empty signature should skip verification
	}

	err := server.Download(req, stream)
	assert.NoError(t, err)
	assert.Len(t, stream.sent, 2) // 1 event + 1 chunk

	// Verify we got the download event and file content
	assert.NotNil(t, stream.sent[0].GetEvent())
	assert.Equal(t, "download completed", stream.sent[0].GetEvent().Message)
	assert.NotNil(t, stream.sent[1].GetChunk())
	assert.Equal(t, testContent, stream.sent[1].GetChunk().Data)
}

func TestDownload_WithSignature_PublicAction_ValidSignature_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create temporary file for testing
	tempDir := t.TempDir()
	testFilePath := filepath.Join(tempDir, "test_file.txt")
	testContent := []byte("test file content for public action")
	require.NoError(t, os.WriteFile(testFilePath, testContent, 0644))

	// Setup test data
	actionID := "action123"
	creatorAddress := "creator123"
	requesterAddress := "requester456"
	validSignature := base64.StdEncoding.EncodeToString([]byte("valid_signature_bytes"))

	// Create public cascade metadata
	cascadeMetadata := &actiontypes.CascadeMetadata{
		Public: true,
	}
	metadataBytes, err := proto.Marshal(cascadeMetadata)
	require.NoError(t, err)

	// Create a real CascadeRegistrationTask with mocked LumeraClient
	mockLumeraClient := lumeramocks.NewMockLumeraClient(ctrl)

	// Mock GetAction to return action details
	mockLumeraClient.EXPECT().GetAction(gomock.Any(), actionID).Return(&actiontypes.QueryGetActionResponse{
		Action: &actiontypes.Action{
			ActionID: actionID,
			Creator:  creatorAddress,
			Metadata: metadataBytes,
		},
	}, nil).Times(1)

	// Mock Verify to simulate successful signature verification
	expectedSignatureData := actionID + "." + creatorAddress
	mockLumeraClient.EXPECT().Verify(gomock.Any(), requesterAddress, []byte(expectedSignatureData), []byte("valid_signature_bytes")).Return(nil).Times(1)

	// Create real cascade service and task with mocked client
	cascadeService := &cascade.CascadeService{
		LumeraClient: mockLumeraClient,
	}

	cascadeTask := &cascade.CascadeRegistrationTask{
		CascadeService: cascadeService,
	}

	// Mock the Download method after verification passes
	mockTaskForDownload := cascademocks.NewMockCascadeTask(ctrl)
	mockTaskForDownload.EXPECT().Download(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req *cascade.DownloadRequest, send func(*cascade.DownloadResponse) error) error {
			return send(&cascade.DownloadResponse{
				EventType:     1,
				Message:       "download completed",
				FilePath:      testFilePath,
				DownloadedDir: tempDir,
			})
		},
	).Times(1)
	mockTaskForDownload.EXPECT().CleanupDownload(gomock.Any(), tempDir).Return(nil).Times(1)

	// Use test factory that returns verification task first, then download task
	factoryCallCount := 0
	mockFactory := &testCascadeServiceFactory{
		verificationTask: cascadeTask,
		downloadTask:     mockTaskForDownload,
		callCount:        &factoryCallCount,
	}

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId:         actionID,
		Signature:        validSignature,
		RequestorAddress: requesterAddress,
	}

	err = server.Download(req, stream)
	assert.NoError(t, err)
	assert.Len(t, stream.sent, 2) // 1 event + 1 chunk

	// Verify we got the download event and file content
	assert.NotNil(t, stream.sent[0].GetEvent())
	assert.Equal(t, "download completed", stream.sent[0].GetEvent().Message)
	assert.NotNil(t, stream.sent[1].GetChunk())
	assert.Equal(t, testContent, stream.sent[1].GetChunk().Data)
}

// Test helper factory that can return different tasks for verification vs download
type testCascadeServiceFactory struct {
	verificationTask cascade.CascadeTask
	downloadTask     cascade.CascadeTask
	callCount        *int
}

func (f *testCascadeServiceFactory) NewCascadeRegistrationTask() cascade.CascadeTask {
	*f.callCount++
	if *f.callCount == 1 {
		return f.verificationTask
	}
	return f.downloadTask
}

func TestDownload_WithSignature_PrivateAction_NonCreatorAccess_Fails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Setup test data - requester is NOT the creator
	actionID := "action123"
	creatorAddress := "creator123"
	requesterAddress := "requester456" // Different from creator
	validSignature := base64.StdEncoding.EncodeToString([]byte("valid_signature_bytes"))

	// Create private cascade metadata
	cascadeMetadata := &actiontypes.CascadeMetadata{
		Public: false, // Private action
	}
	metadataBytes, err := proto.Marshal(cascadeMetadata)
	require.NoError(t, err)

	// Create a real CascadeRegistrationTask with mocked LumeraClient
	mockLumeraClient := lumeramocks.NewMockLumeraClient(ctrl)

	// Mock GetAction to return action details
	mockLumeraClient.EXPECT().GetAction(gomock.Any(), actionID).Return(&actiontypes.QueryGetActionResponse{
		Action: &actiontypes.Action{
			ActionID: actionID,
			Creator:  creatorAddress,
			Metadata: metadataBytes,
		},
	}, nil).Times(1)

	// Mock Verify to simulate successful signature verification (signature is valid, but access should be denied)
	expectedSignatureData := actionID + "." + creatorAddress
	mockLumeraClient.EXPECT().Verify(gomock.Any(), requesterAddress, []byte(expectedSignatureData), []byte("valid_signature_bytes")).Return(nil).Times(1)

	// Create real cascade service and task with mocked client
	cascadeService := &cascade.CascadeService{
		LumeraClient: mockLumeraClient,
	}

	cascadeTask := &cascade.CascadeRegistrationTask{
		CascadeService: cascadeService,
	}

	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(cascadeTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId:         actionID,
		Signature:        validSignature,
		RequestorAddress: requesterAddress,
	}

	err = server.Download(req, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access denied: private cascade can only be downloaded by creator")
}

func TestDownload_WithSignature_InvalidSignature_Fails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Setup test data
	actionID := "action123"
	creatorAddress := "creator123"
	requesterAddress := "requester456"
	invalidSignature := base64.StdEncoding.EncodeToString([]byte("invalid_signature_bytes"))

	// Create public cascade metadata
	cascadeMetadata := &actiontypes.CascadeMetadata{
		Public: true,
	}
	metadataBytes, err := proto.Marshal(cascadeMetadata)
	require.NoError(t, err)

	// Create a real CascadeRegistrationTask with mocked LumeraClient
	mockLumeraClient := lumeramocks.NewMockLumeraClient(ctrl)

	// Mock GetAction to return action details
	mockLumeraClient.EXPECT().GetAction(gomock.Any(), actionID).Return(&actiontypes.QueryGetActionResponse{
		Action: &actiontypes.Action{
			ActionID: actionID,
			Creator:  creatorAddress,
			Metadata: metadataBytes,
		},
	}, nil).Times(1)

	// Mock Verify to simulate failed signature verification
	expectedSignatureData := actionID + "." + creatorAddress
	mockLumeraClient.EXPECT().Verify(gomock.Any(), requesterAddress, []byte(expectedSignatureData), []byte("invalid_signature_bytes")).Return(errors.New("signature verification failed")).Times(1)

	// Create real cascade service and task with mocked client
	cascadeService := &cascade.CascadeService{
		LumeraClient: mockLumeraClient,
	}

	cascadeTask := &cascade.CascadeRegistrationTask{
		CascadeService: cascadeService,
	}

	mockFactory := cascademocks.NewMockCascadeServiceFactory(ctrl)
	mockFactory.EXPECT().NewCascadeRegistrationTask().Return(cascadeTask).Times(1)

	server := NewCascadeActionServer(mockFactory)

	stream := &mockDownloadStream{
		ctx: context.Background(),
	}

	req := &pb.DownloadRequest{
		ActionId:         actionID,
		Signature:        invalidSignature,
		RequestorAddress: requesterAddress,
	}

	err = server.Download(req, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}
