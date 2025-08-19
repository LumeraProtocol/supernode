package cascade_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	sdkmath "cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	codecpkg "github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
	cascadeadaptormocks "github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors/mocks"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/common"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
	"lukechampine.com/blake3"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func TestCascadeRegistrationTask_Register(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Setup input file
	tmpFile, err := os.CreateTemp("", "cascade-test-input")
	assert.NoError(t, err)

	_, _ = tmpFile.WriteString("mock data")

	err = tmpFile.Close() // ✅ ensure it's flushed to disk
	assert.NoError(t, err)

	rawHash, b64Hash := blake3HashRawAndBase64(t, tmpFile.Name())

	tests := []struct {
		name           string
		setupMocks     func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2p *cascadeadaptormocks.MockP2PService)
		expectedError  string
		expectedEvents int
	}{
		{
			name: "happy path",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2p *cascadeadaptormocks.MockP2PService) {

				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID:    "action123",
							Creator:     "creator1",
							BlockHeight: 100,
							Metadata:    encodedCascadeMetadata(b64Hash, t),
							Price: &sdk.Coin{
								Denom:  "ulume",
								Amount: sdkmath.NewInt(1000),
							},
						},
					}, nil)

				// 2. Top SNs
				lc.EXPECT().
					GetTopSupernodes(gomock.Any(), uint64(100)).
					Return(&sntypes.QueryGetTopSuperNodesForBlockResponse{
						Supernodes: []*sntypes.SuperNode{
							{
								SupernodeAccount: "lumera1abcxyz", // must match task.config.SupernodeAccountAddress
							},
						},
					}, nil)

				// 3. Signature verification - layout signature on layout file
				// Expect two verification calls: creator signature and layout signature
				lc.EXPECT().
					Verify(gomock.Any(), "creator1", gomock.Any(), gomock.Any()).
					Return(nil).
					Times(2)

				// 4. Finalize
				lc.EXPECT().
					FinalizeAction(gomock.Any(), "action123", gomock.Any()).
					Return(&sdktx.BroadcastTxResponse{TxResponse: &sdk.TxResponse{TxHash: "tx123"}}, nil)

				// 5. Params (if used in fee check)
				lc.EXPECT().GetActionFee(gomock.Any(), "10").Return(&actiontypes.QueryGetActionFeeResponse{Amount: "1000"}, nil)

				// 6. Encode input
				codec.EXPECT().
					EncodeInput(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
					Return(adaptors.EncodeResult{
						SymbolsDir: "/tmp",
						Metadata:   codecpkg.Layout{Blocks: []codecpkg.Block{{BlockID: 1, Hash: "abc"}}},
					}, nil)

				// 7. Store artefacts
				p2p.EXPECT().
					StoreArtefacts(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil)
			},
			expectedError:  "",
			expectedEvents: 11,
		},
		{
			name: "get-action fails",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, _ *cascadeadaptormocks.MockCodecService, _ *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(nil, assert.AnError)
			},
			expectedError:  "assert.AnError general error",
			expectedEvents: 0,
		},
		{
			name: "invalid data hash mismatch",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID:    "action123",
							Creator:     "creator1",
							BlockHeight: 100,
							Metadata:    encodedCascadeMetadata("some-other-hash", t), // ⛔ incorrect hash
							Price: &sdk.Coin{
								Denom:  "ulume",
								Amount: sdkmath.NewInt(1000),
							},
						},
					}, nil)

				lc.EXPECT().
					GetTopSupernodes(gomock.Any(), uint64(100)).
					Return(&sntypes.QueryGetTopSuperNodesForBlockResponse{
						Supernodes: []*sntypes.SuperNode{
							{SupernodeAccount: "lumera1abcxyz"},
						},
					}, nil)

				lc.EXPECT().GetActionFee(gomock.Any(), "10").Return(&actiontypes.QueryGetActionFeeResponse{Amount: "1000"}, nil)
			},
			expectedError:  "data hash doesn't match",
			expectedEvents: 5, // up to metadata decoded
		},
		{
			name: "fee too low",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID:    "action123",
							Creator:     "creator1",
							BlockHeight: 100,
							Metadata:    encodedCascadeMetadata(b64Hash, t),
							Price: &sdk.Coin{
								Denom:  "ulume",
								Amount: sdkmath.NewInt(50),
							},
						},
					}, nil)

				lc.EXPECT().GetActionFee(gomock.Any(), "10").Return(&actiontypes.QueryGetActionFeeResponse{Amount: "100"}, nil)

			},
			expectedError:  "action fee is too low",
			expectedEvents: 2, // until fee check
		},
		{
			name: "supernode not in top list",
			setupMocks: func(lc *cascadeadaptormocks.MockLumeraClient, codec *cascadeadaptormocks.MockCodecService, p2p *cascadeadaptormocks.MockP2PService) {
				lc.EXPECT().
					GetAction(gomock.Any(), "action123").
					Return(&actiontypes.QueryGetActionResponse{
						Action: &actiontypes.Action{
							ActionID:    "action123",
							Creator:     "creator1",
							BlockHeight: 100,
							Metadata:    encodedCascadeMetadata(b64Hash, t),
							Price: &sdk.Coin{
								Denom:  "ulume",
								Amount: sdkmath.NewInt(1000),
							},
						},
					}, nil)

				lc.EXPECT().GetActionFee(gomock.Any(), "10").Return(&actiontypes.QueryGetActionFeeResponse{Amount: "1000"}, nil)

				lc.EXPECT().
					GetTopSupernodes(gomock.Any(), uint64(100)).
					Return(&sntypes.QueryGetTopSuperNodesForBlockResponse{
						Supernodes: []*sntypes.SuperNode{
							{SupernodeAccount: "other-supernode"},
						},
					}, nil)
			},
			expectedError:  "not eligible supernode",
			expectedEvents: 2, // fails after fee verified
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockLumera := cascadeadaptormocks.NewMockLumeraClient(ctrl)
			mockCodec := cascadeadaptormocks.NewMockCodecService(ctrl)
			mockP2P := cascadeadaptormocks.NewMockP2PService(ctrl)

			tt.setupMocks(mockLumera, mockCodec, mockP2P)

			config := &cascade.Config{Config: common.Config{
				SupernodeAccountAddress: "lumera1abcxyz",
			},
			}

			service := cascade.NewCascadeService(
				config,
				nil, nil, nil, nil,
			)

			service.LumeraClient = mockLumera
			service.P2P = mockP2P
			service.RQ = mockCodec
			// Inject mocks for adaptors
			task := cascade.NewCascadeRegistrationTask(service)

			req := &cascade.RegisterRequest{
				TaskID:   "task1",
				ActionID: "action123",
				DataHash: rawHash,
				DataSize: 10240,
				FilePath: tmpFile.Name(),
			}

			var events []cascade.RegisterResponse
			err := task.Register(context.Background(), req, func(resp *cascade.RegisterResponse) error {
				events = append(events, *resp)
				return nil
			})

			if tt.expectedError != "" {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, events, tt.expectedEvents)
			}
		})
	}
}

func encodedCascadeMetadata(hash string, t *testing.T) []byte {
	t.Helper()

	// Fake layout signature for new index file format
	fakeLayoutSig := base64.StdEncoding.EncodeToString([]byte("fakelayoutsignature"))

	// Create index file structure
	indexFile := map[string]any{
		"layout_ids":       []string{"layout_id_1", "layout_id_2"},
		"layout_signature": fakeLayoutSig,
	}
	indexFileJSON, _ := json.Marshal(indexFile)
	fakeIndexFile := base64.StdEncoding.EncodeToString(indexFileJSON)

	// Fake creators signature - this is what the chain uses for index ID generation
	fakeCreatorsSig := base64.StdEncoding.EncodeToString([]byte("fakecreatorssignature"))

	metadata := &actiontypes.CascadeMetadata{
		DataHash:   hash,
		FileName:   "file.txt",
		RqIdsIc:    2,
		RqIdsMax:   4,
		RqIdsIds:   []string{"id1", "id2"},
		Signatures: fakeIndexFile + "." + fakeCreatorsSig,
	}

	bytes, err := proto.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal CascadeMetadata: %v", err)
	}

	return bytes
}

func blake3HashRawAndBase64(t *testing.T, path string) ([]byte, string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	hash := blake3.Sum256(data)
	raw := hash[:]
	b64 := base64.StdEncoding.EncodeToString(raw)
	return raw, b64
}

func decodeHexOrDie(hexStr string) []byte {
	bz, err := hex.DecodeString(hexStr)
	if err != nil {
		panic(err)
	}
	return bz
}
