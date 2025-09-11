package cascade

import (
    "encoding/json"
    "testing"

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

    // Call the emitter
    task.emitArtefactsStored(t.Context(), metrics, nil, send)

    require.Equal(t, SupernodeEventTypeArtefactsStored, gotType)
    require.NotEmpty(t, gotMsg)

    // Parse JSON payload
    var payload map[string]any
    require.NoError(t, json.Unmarshal([]byte(gotMsg), &payload))

    // Spot check top-level fields
    require.InDelta(t, 93.9, payload["success_rate"].(float64), 0.01)
    require.EqualValues(t, float64(60), payload["total_requests"]) // json numbers decode to float64
    require.EqualValues(t, float64(1200), payload["meta_duration_ms"])
    require.EqualValues(t, float64(8200), payload["sym_duration_ms"])

    // Validate meta_nodes
    metaCalls, ok := payload["meta_nodes"].([]any)
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
    require.EqualValues(t, float64(1), mc0["successes"]) // one success on nodeA
    callSyms0 := mc0["call_symbols"].([]any)
    require.EqualValues(t, float64(2), callSyms0[0])

    mc1 := metaByIP["10.0.0.3"]
    require.NotNil(t, mc1)
    require.EqualValues(t, float64(1), mc1["failures"]) // one failure on nodeB

    // Validate sym_nodes
    symCalls, ok := payload["sym_nodes"].([]any)
    require.True(t, ok)
    require.Len(t, symCalls, 2)
    symByIP := toByIP(symCalls)
    sc := symByIP["10.0.0.4"]
    require.NotNil(t, sc)
    require.EqualValues(t, float64(40), sc["total_keys"]) 
}
