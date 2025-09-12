package cascade

import (
	"encoding/json"
	"testing"

	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/supernode/services/cascade/adaptors"
	"github.com/stretchr/testify/require"
)

// Test that emitArtefactsStored emits a JSON message containing
// success/failure counts, durations, and per-RPC call distributions.
func TestEmitArtefactsStored_JSONPayload(t *testing.T) {
	task := &CascadeRegistrationTask{}

	metrics := adaptors.StoreArtefactsMetrics{
		MetaRate:       97.5,
		MetaRequests:   10,
		MetaCount:      3,
		SymRate:        93.0,
		SymRequests:    50,
		SymCount:       100,
		AggregatedRate: 93.9,
		TotalRequests:  60,
		MetaDurationMS: 1200,
		SymDurationMS:  8200,
		MetaCalls: []adaptors.StoreCallMetric{
			{IP: "10.0.0.2", ID: "nodeA", Address: "nodeA-10.0.0.2:4445", Keys: 2, Success: true, DurationMS: 110},
			{IP: "10.0.0.3", ID: "nodeB", Address: "nodeB-10.0.0.3:4445", Keys: 1, Success: false, Error: "timeout", DurationMS: 750},
		},
		SymCalls: []adaptors.StoreCallMetric{
			{IP: "10.0.0.4", ID: "nodeC", Address: "nodeC-10.0.0.4:4445", Keys: 40, Success: true, DurationMS: 2600},
			{IP: "10.0.0.5", ID: "nodeD", Address: "nodeD-10.0.0.5:4445", Keys: 60, Success: true, DurationMS: 4100},
		},
	}

	var gotMsg string
	var gotType SupernodeEventType

	send := func(resp *RegisterResponse) error {
		gotType = resp.EventType
		gotMsg = resp.Message
		return nil
	}

	// Build a minimal layout with 103 symbols and Size implying K=17
	layout := codec.Layout{Blocks: []codec.Block{{BlockID: 0, Size: 17*65535 - 1, Symbols: make([]string, 103)}}}
	// Call the emitter with layout (helper computes T/K)
	task.emitArtefactsStored(t.Context(), metrics, nil, layout, send)

	require.Equal(t, SupernodeEventTypeArtefactsStored, gotType)
	require.NotEmpty(t, gotMsg)

	// Parse JSON payload
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(gotMsg), &payload))

	// Spot check sections
	layoutJSON, ok := payload["layout"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, float64(103), layoutJSON["symbols_total"]) // numbers decode to float64
	require.InDelta(t, float64(17), layoutJSON["min_required_symbols"].(float64), 1.0)

	network, ok := payload["network"].(map[string]any)
	require.True(t, ok)
	require.InDelta(t, 93.9, network["success_rate_pct"].(float64), 0.01)
	require.EqualValues(t, float64(60), network["total_requests"]) // json numbers decode to float64

	meta, ok := payload["metadata"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, float64(1200), meta["duration_ms"])

	sym, ok := payload["symbols"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, float64(8200), sym["duration_ms"])

	// Validate nodes arrays (full, no cap)
	metaCalls, ok := meta["nodes"].([]any)
	require.True(t, ok)
	require.Len(t, metaCalls, 2)

	// Build lookup by IP to avoid order dependency
	toByIP := func(arr []any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, it := range arr {
			m := it.(map[string]any)
			ip := m["ip"].(string)
			out[ip] = m
		}
		return out
	}
	metaByIP := toByIP(metaCalls)
	mc0 := metaByIP["10.0.0.2"]
	require.NotNil(t, mc0)
	require.EqualValues(t, float64(1), mc0["successes"])  // one success on nodeA
	require.EqualValues(t, float64(2), mc0["keys_total"]) // aggregated keys

	mc1 := metaByIP["10.0.0.3"]
	require.NotNil(t, mc1)
	require.EqualValues(t, float64(1), mc1["failures"]) // one failure on nodeB

	// Validate sym nodes
	symCalls, ok := sym["nodes"].([]any)
	require.True(t, ok)
	require.Len(t, symCalls, 2)
	symByIP := toByIP(symCalls)
	sc := symByIP["10.0.0.4"]
	require.NotNil(t, sc)
	require.EqualValues(t, float64(40), sc["keys_total"])
}
