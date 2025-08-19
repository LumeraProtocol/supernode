package storagechallenge

import (
	"encoding/json"

	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// SCSummaryStatsRes is the struct for metrics
type SCSummaryStatsRes struct {
	SCSummaryStats SCSummaryStats
}

type SCSummaryStats struct {
	TotalChallenges                      int
	TotalChallengesProcessed             int
	TotalChallengesEvaluatedByChallenger int
	TotalChallengesVerified              int
	SlowResponsesObservedByObservers     int
	InvalidSignaturesObservedByObservers int
	InvalidEvaluationObservedByObservers int
}

// Hash returns the hash of the SCSummaryStats
func (ss *SCSummaryStatsRes) Hash() string {
	data, _ := json.Marshal(ss)
	hash, _ := utils.Blake3Hash(data)

	return string(hash)
}
