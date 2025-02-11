package senseregister

import (
	"github.com/LumeraProtocol/supernode/walletnode/api/gen/sense"
	"github.com/LumeraProtocol/supernode/walletnode/services/common"
)

// FromStartProcessingPayload convert StartProcessingPayload to ActionRegistrationRequest
func FromStartProcessingPayload(payload *sense.StartProcessingPayload) *common.ActionRegistrationRequest {
	req := &common.ActionRegistrationRequest{
		BurnTxID:              payload.BurnTxid,
		AppPastelID:           payload.AppPastelID,
		AppPastelIDPassphrase: payload.Key,
		GroupID:               payload.OpenAPIGroupID,
	}

	if payload.SpendableAddress != nil {
		req.SpendableAddress = *payload.SpendableAddress
	}

	if payload.CollectionActTxid != nil {
		req.CollectionTxID = *payload.CollectionActTxid
	}

	return req
}
