package cascade

import (
	"context"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	sntypes "github.com/LumeraProtocol/lumera/x/supernode/v1/types"
	"github.com/LumeraProtocol/supernode/v2/supernode/adaptors"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/stretchr/testify/assert"
)

// mockLumeraClient implements the LumeraClient interface for testing
type mockLumeraClient struct {
	baseActionFee math.Int
	feePerKbyte   math.Int
}

// Compile-time check to ensure mockLumeraClient implements adaptors.LumeraClient
var _ adaptors.LumeraClient = (*mockLumeraClient)(nil)

func newMockLumeraClient(baseFee, perKB int64) *mockLumeraClient {
	return &mockLumeraClient{
		baseActionFee: math.NewInt(baseFee),
		feePerKbyte:   math.NewInt(perKB),
	}
}

func (m *mockLumeraClient) GetActionFee(ctx context.Context, dataSizeKB string) (*actiontypes.QueryGetActionFeeResponse, error) {
	// Parse KB as int64
	var kb int64
	_, err := fmt.Sscanf(dataSizeKB, "%d", &kb)
	if err != nil {
		return nil, fmt.Errorf("invalid dataSizeKB: %s", dataSizeKB)
	}

	// Fee calculation: (FeePerKbyte × KB) + BaseActionFee
	perByteCost := m.feePerKbyte.MulRaw(kb)
	totalAmount := perByteCost.Add(m.baseActionFee)

	return &actiontypes.QueryGetActionFeeResponse{
		Amount: totalAmount.String(),
	}, nil
}

func (m *mockLumeraClient) GetAction(ctx context.Context, actionID string) (*actiontypes.QueryGetActionResponse, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockLumeraClient) GetTopSupernodes(ctx context.Context, blockHeight uint64) (*sntypes.QueryGetTopSuperNodesForBlockResponse, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockLumeraClient) Verify(ctx context.Context, address string, msg []byte, sig []byte) error {
	return fmt.Errorf("not implemented in mock")
}

func (m *mockLumeraClient) SimulateFinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.SimulateResponse, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *mockLumeraClient) FinalizeAction(ctx context.Context, actionID string, rqids []string) (*sdktx.BroadcastTxResponse, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

// TestVerifyActionFee_EdgeCases tests the verifyActionFee function with edge cases
func TestVerifyActionFee_EdgeCases(t *testing.T) {
	testCases := []struct {
		name        string
		dataBytes   int    // Input data size in bytes
		actionPrice string // Price set on the action
		baseFee     int64  // Base action fee param
		feePerKB    int64  // Fee per KB param
		shouldPass  bool   // Should validation pass?
		description string
	}{
		{
			name:        "0 bytes - exact fee should pass",
			dataBytes:   0,
			actionPrice: "10000ulume", // 0 KB → fee = 10000
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  true,
			description: "0 bytes rounds to 0 KB, fee = base only",
		},
		{
			name:        "0 bytes - underpayment should fail",
			dataBytes:   0,
			actionPrice: "9999ulume", // Less than required 10000
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  false,
			description: "Paying less than base fee should fail",
		},
		{
			name:        "1 byte - exact fee should pass",
			dataBytes:   1,
			actionPrice: "10010ulume", // 1 byte → 1 KB → fee = 10010
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  true,
			description: "1 byte rounds to 1 KB, fee = 10 + 10000",
		},
		{
			name:        "1 byte - underpayment should fail",
			dataBytes:   1,
			actionPrice: "10009ulume", // Less than required 10010
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  false,
			description: "Paying less than required fee should fail",
		},
		{
			name:        "1025 bytes (1 chunk + 1 byte) - exact fee should pass",
			dataBytes:   1025,
			actionPrice: "10020ulume", // 1025 bytes → 2 KB → fee = 20 + 10000
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  true,
			description: "1025 bytes rounds to 2 KB, fee = 20 + 10000",
		},
		{
			name:        "1025 bytes - underpayment should fail",
			dataBytes:   1025,
			actionPrice: "10019ulume", // Less than required 10020
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  false,
			description: "Paying less than 2 KB worth should fail",
		},
		{
			name:        "1024 bytes (exactly 1 KB) - exact fee should pass",
			dataBytes:   1024,
			actionPrice: "10010ulume", // 1024 bytes → 1 KB → fee = 10 + 10000
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  true,
			description: "Exactly 1 KB should charge for 1 KB",
		},
		{
			name:        "overpayment is allowed",
			dataBytes:   1,
			actionPrice: "50000ulume", // Much more than required 10010
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  true,
			description: "Users can pay more than the minimum",
		},
		{
			name:        "wrong denom should fail",
			dataBytes:   1,
			actionPrice: "10010usdt", // Wrong denom
			baseFee:     10000,
			feePerKB:    10,
			shouldPass:  false,
			description: "Only ulume denom should be accepted",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Create mock Lumera client with test params
			mockClient := newMockLumeraClient(tc.baseFee, tc.feePerKB)

			// Create task with mock client
			task := &CascadeRegistrationTask{
				CascadeService: &CascadeService{
					LumeraClient: mockClient,
				},
			}

			// Create action with the price (stored as string in proto)
			action := &actiontypes.Action{
				Price: tc.actionPrice,
			}

			// Call verifyActionFee
			err := task.verifyActionFee(ctx, action, tc.dataBytes, nil)

			// Check result
			if tc.shouldPass {
				assert.NoError(t, err, "Expected validation to pass but got error: %v\nDescription: %s", err, tc.description)
			} else {
				assert.Error(t, err, "Expected validation to fail\nDescription: %s", tc.description)
			}

			t.Logf("Test: %s\n  Data: %d bytes\n  Price: %s\n  Result: %v\n  Description: %s",
				tc.name, tc.dataBytes, tc.actionPrice, err, tc.description)
		})
	}
}

// TestVerifyActionFee_NilAction tests error handling when action.Price is empty
func TestVerifyActionFee_NilAction(t *testing.T) {
	ctx := context.Background()

	mockClient := newMockLumeraClient(10000, 10)
	task := &CascadeRegistrationTask{
		CascadeService: &CascadeService{
			LumeraClient: mockClient,
		},
	}

	// Action with empty price (string)
	action := &actiontypes.Action{
		Price: "",
	}

	err := task.verifyActionFee(ctx, action, 100, nil)
	assert.Error(t, err, "Should fail when action.Price is empty")
	assert.Contains(t, err.Error(), "insufficient fee", "Error should mention insufficient fee")
}

// TestVerifyActionFee_CompareWithClientSDK verifies that supernode uses
// same rounding as client SDK (from supernode/sdk/action/client.go:302-304)
func TestVerifyActionFee_CompareWithClientSDK(t *testing.T) {
	testCases := []struct {
		fileBytes int64
		expectedKB int64
	}{
		{0, 0},
		{1, 1},
		{1024, 1},
		{1025, 2},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d bytes", tc.fileBytes), func(t *testing.T) {
			// Both client SDK and supernode now use the SAME centralized function
			// Client uses RoundBytesToKB64 for int64 inputs
			clientKB := int64(actiontypes.RoundBytesToKB64(tc.fileBytes))

			// Supernode uses RoundBytesToKB for int inputs
			supernodeKB := int64(actiontypes.RoundBytesToKB(int(tc.fileBytes)))

			assert.Equal(t, tc.expectedKB, clientKB,
				"Client SDK rounding incorrect for %d bytes", tc.fileBytes)
			assert.Equal(t, tc.expectedKB, supernodeKB,
				"Supernode rounding incorrect for %d bytes", tc.fileBytes)
			assert.Equal(t, clientKB, supernodeKB,
				"Client and supernode rounding must match for %d bytes", tc.fileBytes)

			t.Logf("%d bytes → Client: %d KB, Supernode: %d KB %s",
				tc.fileBytes, clientKB, supernodeKB,
				map[bool]string{true: "✓ CONSISTENT", false: "✗ INCONSISTENT"}[clientKB == supernodeKB])
		})
	}
}
