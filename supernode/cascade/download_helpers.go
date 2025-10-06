package cascade

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/codec"
	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
)

// retrieveLayoutFromIDs tries the given layout IDs in order and returns the first valid layout.
func (task *CascadeRegistrationTask) retrieveLayoutFromIDs(ctx context.Context, layoutIDs []string, fields logtrace.Fields) (codec.Layout, int64, int64, int, error) {
	var layout codec.Layout
	var netMS, decMS int64
	attempts := 0
	for _, lid := range layoutIDs {
		attempts++
		nStart := time.Now()
		logtrace.Debug(ctx, "RPC Retrieve layout file", logtrace.Fields{"layout_id": lid})
		raw, err := task.P2PClient.Retrieve(ctx, lid)
		if err != nil || len(raw) == 0 {
			logtrace.Warn(ctx, "Retrieve layout failed or empty", logtrace.Fields{"layout_id": lid, logtrace.FieldError: err})
			continue
		}
		netMS = time.Since(nStart).Milliseconds()
		dStart := time.Now()
		// Layout files are stored as compressed RQ metadata: base64(JSON(layout)).signature.counter
		// Use the cascadekit parser to decompress and decode instead of JSON-unmarshalling raw bytes.
		parsedLayout, _, _, err := cascadekit.ParseRQMetadataFile(raw)
		if err != nil {
			logtrace.Warn(ctx, "Parse layout file failed", logtrace.Fields{"layout_id": lid, logtrace.FieldError: err})
			continue
		}
		layout = parsedLayout
		decMS = time.Since(dStart).Milliseconds()
		if len(layout.Blocks) > 0 {
			return layout, netMS, decMS, attempts, nil
		}
	}
	return codec.Layout{}, netMS, decMS, attempts, nil
}

// retrieveLayoutFromIndex resolves layout IDs in the index file and tries to fetch a valid layout.
func (task *CascadeRegistrationTask) retrieveLayoutFromIndex(ctx context.Context, index cascadekit.IndexFile, fields logtrace.Fields) (codec.Layout, int64, int64, int, error) {
	return task.retrieveLayoutFromIDs(ctx, index.LayoutIDs, fields)
}
