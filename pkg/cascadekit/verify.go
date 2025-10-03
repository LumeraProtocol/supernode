package cascadekit

import (
    "github.com/LumeraProtocol/supernode/v2/pkg/codec"
    "github.com/LumeraProtocol/supernode/v2/pkg/errors"
    "github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// VerifySingleBlockIDs enforces single-block layouts and verifies that the
// symbols and block hash of ticket and local layouts match for block 0.
func VerifySingleBlockIDs(ticket, local codec.Layout) error {
    if len(ticket.Blocks) != 1 || len(local.Blocks) != 1 {
        return errors.New("layout must contain exactly one block")
    }
    if err := utils.EqualStrList(ticket.Blocks[0].Symbols, local.Blocks[0].Symbols); err != nil {
        return errors.Errorf("symbol identifiers don't match: %w", err)
    }
    if ticket.Blocks[0].Hash != local.Blocks[0].Hash {
        return errors.New("block hashes don't match")
    }
    return nil
}

