package cascade

import (
	"context"
	"encoding/json"
	"fmt"

	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"github.com/LumeraProtocol/supernode/v2/pkg/cascadekit"
)

// DebugReverseEngineerAction fetches an action by ID, decodes its Cascade
// metadata and embedded index information, and prints a detailed breakdown.
// It performs read-only inspection and is intended for internal debugging.
func (task *CascadeRegistrationTask) DebugReverseEngineerAction(ctx context.Context, actionID string) error {
	_ = ctx // kept for parity with other methods; not used here

	if actionID == "" {
		fmt.Println("DebugReverseEngineerAction: empty actionID, nothing to do")
		return nil
	}

	fmt.Printf("DebugReverseEngineerAction: fetching action %s\n", actionID)

	// Step 1: Fetch the action from the chain
	action, err := task.fetchAction(context.Background(), actionID, nil)
	if err != nil {
		fmt.Printf("DebugReverseEngineerAction: error fetching action %s: %v\n", actionID, err)
		return err
	}

	// Step 2: Print basic action summary
	fmt.Println("=== Action Summary ===")
	fmt.Printf("Action ID       : %s\n", action.ActionID)
	fmt.Printf("Creator         : %s\n", action.Creator)
	fmt.Printf("Action Type     : %s\n", action.ActionType.String())
	fmt.Printf("State           : %s\n", action.State.String())
	fmt.Printf("Block Height    : %d\n", action.BlockHeight)
	fmt.Printf("Price           : %s\n", action.Price)
	fmt.Printf("Expiration (unix): %d\n", action.ExpirationTime)
	fmt.Printf("Metadata bytes  : %d\n", len(action.Metadata))
	fmt.Printf("Supernodes (%d) : %v\n", len(action.SuperNodes), action.SuperNodes)

	// Only Cascade actions carry CascadeMetadata
	if action.ActionType != actiontypes.ActionTypeCascade {
		fmt.Println("Action is not of type CASCADE; skipping cascade-specific decoding.")
		return nil
	}

	if len(action.Metadata) == 0 {
		fmt.Println("Cascade action has empty metadata; nothing to decode.")
		return nil
	}

	// Step 3: Decode Cascade metadata
	cascadeMeta, err := cascadekit.UnmarshalCascadeMetadata(action.Metadata)
	if err != nil {
		fmt.Printf("Failed to unmarshal cascade metadata: %v\n", err)
		return err
	}

	fmt.Println("\n=== Cascade Metadata (summary) ===")
	fmt.Printf("Data hash (b64) : %s\n", cascadeMeta.DataHash)
	fmt.Printf("File name       : %s\n", cascadeMeta.FileName)
	fmt.Printf("rq_ids_ic       : %d\n", cascadeMeta.RqIdsIc)
	fmt.Printf("rq_ids_max      : %d\n", cascadeMeta.RqIdsMax)
	fmt.Printf("rq_ids_ids (#)  : %d\n", len(cascadeMeta.RqIdsIds))
	fmt.Printf("Public          : %v\n", cascadeMeta.Public)
	if len(cascadeMeta.RqIdsIds) > 0 {
		fmt.Printf("rq_ids_ids      : %v\n", cascadeMeta.RqIdsIds)
	}
	fmt.Printf("Signatures len  : %d\n", len(cascadeMeta.Signatures))

	if metaJSON, mErr := json.MarshalIndent(cascadeMeta, "", "  "); mErr == nil {
		fmt.Println("\n=== Cascade Metadata (JSON) ===")
		fmt.Println(string(metaJSON))
	}

	// Step 4: Decode index information from the signatures field
	if cascadeMeta.Signatures == "" {
		fmt.Println("\nCascade metadata has empty signatures field; cannot decode index.")
		return nil
	}

	indexB64, creatorSigB64, err := cascadekit.ExtractIndexAndCreatorSig(cascadeMeta.Signatures)
	if err != nil {
		fmt.Printf("Failed to extract index and creator signature: %v\n", err)
		return err
	}

	fmt.Println("\n=== Index Signature Components ===")
	fmt.Printf("index_b64 length       : %d\n", len(indexB64))
	fmt.Printf("creator_sig_b64 length : %d\n", len(creatorSigB64))
	if len(indexB64) > 0 {
		previewLen := 64
		if len(indexB64) < previewLen {
			previewLen = len(indexB64)
		}
		fmt.Printf("index_b64 prefix       : %s\n", indexB64[:previewLen])
	}
	if len(creatorSigB64) > 0 {
		previewLen := 64
		if len(creatorSigB64) < previewLen {
			previewLen = len(creatorSigB64)
		}
		fmt.Printf("creator_sig_b64 prefix : %s\n", creatorSigB64[:previewLen])
	}

	// Step 5: Decode the logical index file (base64(JSON(IndexFile)))
	indexFile, err := cascadekit.DecodeIndexB64(indexB64)
	if err != nil {
		fmt.Printf("Failed to decode index file from base64: %v\n", err)
		return err
	}

	fmt.Println("\n=== Index File (summary) ===")
	fmt.Printf("Version               : %d\n", indexFile.Version)
	fmt.Printf("Layout IDs (#)        : %d\n", len(indexFile.LayoutIDs))
	fmt.Printf("Layout IDs            : %v\n", indexFile.LayoutIDs)
	fmt.Printf("Layout signature len  : %d\n", len(indexFile.LayoutSignature))
	if layoutSig := indexFile.LayoutSignature; layoutSig != "" {
		previewLen := 64
		if len(layoutSig) < previewLen {
			previewLen = len(layoutSig)
		}
		fmt.Printf("Layout signature prefix: %s\n", layoutSig[:previewLen])
	}

	if indexJSON, iErr := json.MarshalIndent(indexFile, "", "  "); iErr == nil {
		fmt.Println("\n=== Index File (JSON) ===")
		fmt.Println(string(indexJSON))
	}

	fmt.Println("\nDebugReverseEngineerAction: completed successfully.")

	return nil
}
