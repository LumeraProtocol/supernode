package cmd

import (
	"context"
	"fmt"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	audittypes "github.com/LumeraProtocol/lumera/x/audit/v1/types"
	"github.com/LumeraProtocol/supernode/v2/p2p"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
	"github.com/LumeraProtocol/supernode/v2/pkg/lumera"
)

// p2pArtifactReader is the recipient-side adapter that satisfies the
// transport/grpc/storage_challenge ArtifactReader interface by retrieving
// the full artifact bytes from the local p2p store and slicing the
// requested range. The PR3 path is correct-but-not-optimal: a future
// iteration can replace this with a range-scoped reader.
type p2pArtifactReader struct {
	p2p p2p.P2P
}

func newP2PArtifactReader(p p2p.P2P) *p2pArtifactReader {
	return &p2pArtifactReader{p2p: p}
}

// ReadArtifactRange returns bytes [start, end) for the given key. class is
// currently informational; storage is content-addressed by key alone.
func (r *p2pArtifactReader) ReadArtifactRange(ctx context.Context, _ audittypes.StorageProofArtifactClass, key string, start, end uint64) ([]byte, error) {
	if r == nil || r.p2p == nil {
		return nil, fmt.Errorf("p2pArtifactReader: nil p2p service")
	}
	if end <= start {
		return nil, fmt.Errorf("p2pArtifactReader: invalid range [%d,%d)", start, end)
	}
	data, err := r.p2p.Retrieve(ctx, key, true)
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) < end {
		return nil, fmt.Errorf("p2pArtifactReader: range [%d,%d) out of bounds (size=%d)", start, end, len(data))
	}
	out := make([]byte, end-start)
	copy(out, data[start:end])
	return out, nil
}

// cascadeMetaProvider implements storage_challenge.CascadeMetaProvider via
// the lumera Action module. It fetches the on-chain action, decodes its
// CascadeMetadata, and returns it alongside the finalized action FileSizeKbs
// needed for exact artifact-size derivation.
type cascadeMetaProvider struct {
	client lumera.Client
}

func newCascadeMetaProvider(c lumera.Client) *cascadeMetaProvider {
	return &cascadeMetaProvider{client: c}
}

func (m *cascadeMetaProvider) GetCascadeMetadata(ctx context.Context, ticketID string) (*actiontypes.CascadeMetadata, uint64, error) {
	if m == nil || m.client == nil || m.client.Action() == nil {
		return nil, 0, fmt.Errorf("cascadeMetaProvider: nil action module")
	}
	resp, err := m.client.Action().GetAction(ctx, ticketID)
	if err != nil || resp == nil {
		return nil, 0, fmt.Errorf("get action %q: %w", ticketID, err)
	}
	act := resp.GetAction()
	if act == nil {
		return nil, 0, fmt.Errorf("get action %q: nil action", ticketID)
	}
	meta, err := cascadekit.UnmarshalCascadeMetadata(act.Metadata)
	if err != nil {
		return nil, 0, fmt.Errorf("decode cascade metadata for %q: %w", ticketID, err)
	}
	return &meta, uint64(act.FileSizeKbs), nil
}
