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
    SupernodeEventTypeFinalizeSimulated       SupernodeEventType = 10
    SupernodeEventTypeArtefactsStored         SupernodeEventType = 11
    SupernodeEventTypeActionFinalized         SupernodeEventType = 12
    SupernodeEventTypeArtefactsDownloaded     SupernodeEventType = 13
    SupernodeEventTypeFinalizeSimulationFailed SupernodeEventType = 14
    // Download phase markers
    SupernodeEventTypeNetworkRetrieveStarted  SupernodeEventType = 15
    SupernodeEventTypeDecodeCompleted         SupernodeEventType = 16
    SupernodeEventTypeServeReady              SupernodeEventType = 17
)
