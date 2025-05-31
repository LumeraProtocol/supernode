package action_msg

import (
	"fmt"

	actionapi "github.com/LumeraProtocol/lumera/api/lumera/action"
	actiontypes "github.com/LumeraProtocol/lumera/x/action/v1/types"
	"google.golang.org/protobuf/encoding/protojson"
)

func validateRequestActionParams(actionType, metadata, price, expirationTime string) error {
	if actionType == "" {
		return fmt.Errorf("action type cannot be empty")
	}
	if metadata == "" {
		return fmt.Errorf("metadata cannot be empty")
	}
	if price == "" {
		return fmt.Errorf("price cannot be empty")
	}
	if expirationTime == "" {
		return fmt.Errorf("expiration time cannot be empty")
	}
	return nil
}

func validateFinalizeActionParams(actionId string, rqIdsIds []string) error {
	if actionId == "" {
		return fmt.Errorf("action ID cannot be empty")
	}
	if len(rqIdsIds) == 0 {
		return fmt.Errorf("rq_ids_ids cannot be empty for cascade action")
	}
	return nil
}

func createRequestActionMessage(creator, actionType, metadata, price, expirationTime string) *actiontypes.MsgRequestAction {
	return &actiontypes.MsgRequestAction{
		Creator:        creator,
		ActionType:     actionType,
		Metadata:       metadata,
		Price:          price,
		ExpirationTime: expirationTime,
	}
}

func createFinalizeActionMessage(creator, actionId string, rqIdsIds []string) (*actiontypes.MsgFinalizeAction, error) {
	cascadeMeta := actionapi.CascadeMetadata{
		RqIdsIds: rqIdsIds,
	}

	metadataBytes, err := protojson.Marshal(&cascadeMeta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cascade metadata: %w", err)
	}

	return &actiontypes.MsgFinalizeAction{
		Creator:    creator,
		ActionId:   actionId,
		ActionType: "CASCADE",
		Metadata:   string(metadataBytes),
	}, nil
}
