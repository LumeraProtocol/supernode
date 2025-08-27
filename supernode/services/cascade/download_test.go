package cascade_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	p2pmocks "github.com/LumeraProtocol/supernode/v2/p2p/mocks"
	codecpkg "github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
	cascadeadaptormocks "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors/mocks"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common/base"
	"github.com/cosmos/gogoproto/proto"
	"lukechampine.com/blake3"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestCascadeRegistrationTask_Download(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Create test file to simulate restored file
	tmpFile, err := os.CreateTemp("", "cascade-test-download")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	testData := []byte("test download data")
	_, err = tmpFile.Write(testData)
	assert.NoError(t, err)
	err = tmpFile.Close()
	assert.NoError(t, err)

	// Calculate hash for verification
	hash := blake3.Sum256(testData)
	hashBytes := hash[:]
	b64Hash := base64.StdEncoding.EncodeToString(hashBytes)

	tests := []struct {
		name           string
		setupMocks     func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService)
		expectedError  string
		expectedEvents int
	}{
		{
			name: "happy path",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				// 1. Get action details
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStateDone,
							Metadata: encodedDownloadCascadeMetadata(b64Hash, t),
						},
					}, nil)

				// 2. P2P retrieve index file
				indexData := createMockIndexFile(t)
				p2pClient.On("Retrieve", context.Background(), "index_id_1").
					Return(indexData, nil)

				// 3. P2P retrieve layout file
				layoutData := createMockLayoutFile(t)
				p2pClient.On("Retrieve", context.Background(), "layout_id_1").
					Return(layoutData, nil)

				// 4. P2P batch retrieve symbols
				mockSymbols := map[string][]byte{
					"symbol1": []byte("symbol_data_1"),
					"symbol2": []byte("symbol_data_2"),
				}
				p2pClient.On("BatchRetrieve", context.Background(), []string{"symbol1", "symbol2", "symbol3"}, 3, "action123").
					Return(mockSymbols, nil)

				// 5. Codec decode symbols
				codec.EXPECT().
					Decode(gomock.Any(), gomock.Any()).
					Return(adaptors.DecodeResponse{
						FilePath:     tmpFile.Name(),
						DecodeTmpDir: "/tmp/decode",
					}, nil)
			},
			expectedError:  "",
			expectedEvents: 4, // action retrieved, validated, metadata decoded, artifacts downloaded
		},
		{
			name: "action not found",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(nil, assert.AnError)
			},
			expectedError:  "failed to get action",
			expectedEvents: 0,
		},
		{
			name: "action not in done state",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStatePending, // Not done
							Metadata: encodedDownloadCascadeMetadata(b64Hash, t),
						},
					}, nil)
			},
			expectedError:  "action is not in a valid state",
			expectedEvents: 1, // only action retrieved event
		},
		{
			name: "no artifacts found",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStateDone,
							Metadata: encodedDownloadCascadeMetadata(b64Hash, t),
						},
					}, nil)

				// P2P retrieve fails for all index files
				p2pClient.On("Retrieve", context.Background(), "index_id_1").
					Return(nil, assert.AnError)
				p2pClient.On("Retrieve", context.Background(), "index_id_2").
					Return(nil, assert.AnError)
			},
			expectedError:  "no symbols found in RQ metadata",
			expectedEvents: 3, // action retrieved, validated, metadata decoded
		},
		{
			name: "symbol retrieval fails",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStateDone,
							Metadata: encodedDownloadCascadeMetadata(b64Hash, t),
						},
					}, nil)

				// P2P retrieve index file succeeds
				indexData := createMockIndexFile(t)
				p2pClient.On("Retrieve", context.Background(), "index_id_1").
					Return(indexData, nil)

				// P2P retrieve layout file succeeds
				layoutData := createMockLayoutFile(t)
				p2pClient.On("Retrieve", context.Background(), "layout_id_1").
					Return(layoutData, nil)

				// P2P batch retrieve fails
				p2pClient.On("BatchRetrieve", context.Background(), []string{"symbol1", "symbol2", "symbol3"}, 3, "action123").
					Return(nil, assert.AnError)
			},
			expectedError:  "failed to retrieve symbols",
			expectedEvents: 3, // action retrieved, validated, metadata decoded
		},
		{
			name: "decode fails",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStateDone,
							Metadata: encodedDownloadCascadeMetadata(b64Hash, t),
						},
					}, nil)

				// P2P retrieve index file
				indexData := createMockIndexFile(t)
				p2pClient.On("Retrieve", context.Background(), "index_id_1").
					Return(indexData, nil)

				// P2P retrieve layout file
				layoutData := createMockLayoutFile(t)
				p2pClient.On("Retrieve", context.Background(), "layout_id_1").
					Return(layoutData, nil)

				// P2P batch retrieve symbols
				mockSymbols := map[string][]byte{
					"symbol1": []byte("symbol_data_1"),
				}
				p2pClient.On("BatchRetrieve", context.Background(), []string{"symbol1", "symbol2", "symbol3"}, 3, "action123").
					Return(mockSymbols, nil)

				// Codec decode fails
				codec.EXPECT().
					Decode(gomock.Any(), gomock.Any()).
					Return(adaptors.DecodeResponse{}, assert.AnError)
			},
			expectedError:  "decode symbols using RaptorQ",
			expectedEvents: 3, // action retrieved, validated, metadata decoded
		},
		{
			name: "hash verification fails",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2pClient *p2pmocks.Client, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID: "action123",
							Creator:  "creator1",
							State:    actiontypes.ActionStateDone,
							Metadata: encodedDownloadCascadeMetadata("wrong_hash", t), // Wrong hash
						},
					}, nil)

				// P2P retrieve index file
				indexData := createMockIndexFile(t)
				p2pClient.On("Retrieve", context.Background(), "index_id_1").
					Return(indexData, nil)

				// P2P retrieve layout file
				layoutData := createMockLayoutFile(t)
				p2pClient.On("Retrieve", context.Background(), "layout_id_1").
					Return(layoutData, nil)

				// P2P batch retrieve symbols
				mockSymbols := map[string][]byte{
					"symbol1": []byte("symbol_data_1"),
				}
				p2pClient.On("BatchRetrieve", context.Background(), []string{"symbol1", "symbol2", "symbol3"}, 3, "action123").
					Return(mockSymbols, nil)

				// Codec decode returns the test file
				codec.EXPECT().
					Decode(gomock.Any(), gomock.Any()).
					Return(adaptors.DecodeResponse{
						FilePath:     tmpFile.Name(),
						DecodeTmpDir: "/tmp/decode",
					}, nil)
			},
			expectedError:  "data hash doesn't match",
			expectedEvents: 3, // action retrieved, validated, metadata decoded
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLumera := cascadeadaptormocks.NewMockLumeraClient(ctrl)
			mockCodec := cascadeadaptormocks.NewMockCodecService(ctrl)
			mockP2PClient := p2pmocks.NewClient(t)
			mockP2P := cascadeadaptormocks.NewMockP2PService(ctrl)

			tt.setupMocks(mockLumera, mockCodec, mockP2PClient, mockP2P)

			config := &cascade.Config{Config: common.Config{
				SupernodeAccountAddress: "lumera1abcxyz",
			}}

			service := cascade.NewCascadeService(
				config,
				nil, nil, nil, nil,
			)

			service.LumeraClient = mockLumera
			service.P2P = mockP2P
			service.RQ = mockCodec
			// Set the P2PClient on the base SuperNodeService
			service.SuperNodeService = &base.SuperNodeService{
				P2PClient: mockP2PClient,
			}

			task := cascade.NewCascadeRegistrationTask(service)

			req := &cascade.DownloadRequest{
				ActionID: "action123",
			}

			var events []cascade.DownloadResponse
			err := task.Download(context.Background(), req, func(resp *cascade.DownloadResponse) error {
				events = append(events, *resp)
				return nil
			})

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Len(t, events, tt.expectedEvents)

				// Verify event types for successful case
				if tt.name == "happy path" {
					assert.Equal(t, cascade.SupernodeEventTypeActionRetrieved, events[0].EventType)
					assert.Equal(t, cascade.SupernodeEventTypeActionFinalized, events[1].EventType)
					assert.Equal(t, cascade.SupernodeEventTypeMetadataDecoded, events[2].EventType)
					assert.Equal(t, cascade.SupernodeEventTypeArtefactsDownloaded, events[3].EventType)
					assert.NotEmpty(t, events[3].FilePath)
					assert.NotEmpty(t, events[3].DownloadedDir)
				}
			}

			assert.Len(t, events, tt.expectedEvents)
		})
	}
}

func TestCascadeRegistrationTask_CleanupDownload(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	config := &cascade.Config{Config: common.Config{
		SupernodeAccountAddress: "lumera1abcxyz",
	}}

	service := cascade.NewCascadeService(
		config,
		nil, nil, nil, nil,
	)

	task := cascade.NewCascadeRegistrationTask(service)

	tests := []struct {
		name        string
		actionID    string
		setupDir    bool
		expectError bool
	}{
		{
			name:        "successful cleanup",
			actionID:    "test_action_123",
			setupDir:    true,
			expectError: false,
		},
		{
			name:        "empty action ID",
			actionID:    "",
			setupDir:    false,
			expectError: true,
		},
		{
			name:        "non-existent directory",
			actionID:    "non_existent",
			setupDir:    false,
			expectError: false, // RemoveAll doesn't error for non-existent paths
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup directory if needed
			if tt.setupDir {
				err := os.MkdirAll(tt.actionID, 0755)
				assert.NoError(t, err)

				// Create a file in the directory
				testFile := tt.actionID + "/test.txt"
				err = os.WriteFile(testFile, []byte("test"), 0644)
				assert.NoError(t, err)
			}

			err := task.CleanupDownload(context.Background(), tt.actionID)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Verify directory is removed if it was created
				if tt.setupDir {
					_, err := os.Stat(tt.actionID)
					assert.True(t, os.IsNotExist(err))
				}
			}
		})
	}
}

// Helper functions

func encodedDownloadCascadeMetadata(hash string, t *testing.T) []byte {
	t.Helper()

	metadata := &actiontypes.CascadeMetadata{
		DataHash: hash,
		FileName: "download_test.txt",
		RqIdsIc:  2,
		RqIdsMax: 4,
		RqIdsIds: []string{"index_id_1", "index_id_2"},
		Public:   true,
	}

	bytes, err := proto.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal CascadeMetadata: %v", err)
	}

	return bytes
}

func createMockIndexFile(t *testing.T) []byte {
	t.Helper()

	// Create index file structure matching the IndexFile type
	indexFile := map[string]any{
		"version":    1,
		"layout_ids": []string{"layout_id_1", "layout_id_2"},
	}
	indexFileJSON, err := json.Marshal(indexFile)
	assert.NoError(t, err)

	indexFileB64 := base64.StdEncoding.EncodeToString(indexFileJSON)

	// Create the format that parseIndexFile expects
	// Format: base64IndexFile.signature.counter
	data := indexFileB64 + ".signature.1"

	// Return uncompressed data - the real parseIndexFile would decompress first
	// For testing, we simulate what would be decompressed
	return []byte(data)
}

func createMockLayoutFile(t *testing.T) []byte {
	t.Helper()

	// Create mock layout structure matching codec.Layout
	layout := codecpkg.Layout{
		Blocks: []codecpkg.Block{
			{
				BlockID: 1,
				Hash:    "block_hash_1",
				Symbols: []string{"symbol1", "symbol2", "symbol3"},
			},
		},
	}

	// Marshal the layout as JSON then base64 encode
	layoutJSON, err := json.Marshal(layout)
	assert.NoError(t, err)

	layoutB64 := base64.StdEncoding.EncodeToString(layoutJSON)

	// Create the format that parseRQMetadataFile expects
	// Format: base64Layout.signature.counter
	data := layoutB64 + ".signature.1"

	// Return uncompressed data - the real parseRQMetadataFile would decompress first
	return []byte(data)
}
