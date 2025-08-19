package storagechallenge

import (
	"encoding/json"

	"github.com/LumeraProtocol/supernode/v2/pkg/utils"
)

// HCSummaryStatsRes is the struct for metrics
type HCSummaryStatsRes struct {
	HCSummaryStats HCSummaryStats
}

type HCSummaryStats struct {
	TotalChallenges                      int
	TotalChallengesProcessed             int
	TotalChallengesEvaluatedByChallenger int
	TotalChallengesVerified              int
	SlowResponsesObservedByObservers     int
	InvalidSignaturesObservedByObservers int
	InvalidEvaluationObservedByObservers int
}

// Hash returns the hash of the SCSummaryStats
func (ss *HCSummaryStatsRes) Hash() string {
	data, _ := json.Marshal(ss)
	hash, _ := utils.Blake3Hash(data)

	return string(hash)
}
