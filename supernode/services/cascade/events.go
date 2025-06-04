package cascade

type SupernodeEventType int

const (
	SupernodeEventTypeUNKNOWN                 SupernodeEventType = 0
	SupernodeEventTypeActionRetrieved         SupernodeEventType = 1
	SupernodeEventTypeActionFeeVerified       SupernodeEventType = 2
	SupernodeEventTypeTopSupernodeCheckPassed SupernodeEventType = 3
	SupernodeEventTypeMetadataDecoded         SupernodeEventType = 4
	SupernodeEventTypeDataHashVerified        SupernodeEventType = 5
	SupernodeEventTypeInputEncoded            SupernodeEventType = 6
	SupernodeEventTypeSignatureVerified       SupernodeEventType = 7
	SupernodeEventTypeRQIDsGenerated          SupernodeEventType = 8
	SupernodeEventTypeRqIDsVerified           SupernodeEventType = 9
	SupernodeEventTypeArtefactsStored         SupernodeEventType = 10
	SupernodeEventTypeActionFinalized         SupernodeEventType = 11
	SupernodeEventTypeArtefactsDownloaded     SupernodeEventType = 12
)
