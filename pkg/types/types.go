package types

import (
	"context"
	"time"

	"github.com/LumeraProtocol/supernode/pkg/logtrace"

	json "github.com/json-iterator/go"
)

// MeshedSuperNode represents meshed sn
type MeshedSuperNode struct {
	SessID string
	NodeID string
}

// TaskHistory represents task history
type TaskHistory struct {
	ID        int
	TaskID    string
	CreatedAt time.Time
	Status    string
	Details   *Details
}

// StorageChallengeStatus represents possible storage challenge statuses
type StorageChallengeStatus int

const (
	//UndefinedStorageChallengeStatus represents invalid storage challenge type
	UndefinedStorageChallengeStatus StorageChallengeStatus = iota
	//GeneratedStorageChallengeStatus represents when the challenge is stored after generation
	GeneratedStorageChallengeStatus
	//ProcessedStorageChallengeStatus represents when the challenge is stored after processing
	ProcessedStorageChallengeStatus
	//VerifiedStorageChallengeStatus represents when the challenge is stored after verification
	VerifiedStorageChallengeStatus
)

// StorageChallenge represents storage challenge log
type StorageChallenge struct {
	ID              int64
	ChallengeID     string
	FileHash        string
	ChallengingNode string
	RespondingNode  string
	VerifyingNodes  string
	GeneratedHash   string
	Status          StorageChallengeStatus
	StartingIndex   int
	EndingIndex     int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SelfHealingStatus represents possible self-healing statuses of failed challenge
type SelfHealingStatus string

const (
	//UndefinedSelfHealingStatus represents invalid status for self-healing operation
	UndefinedSelfHealingStatus SelfHealingStatus = "Undefined"
	//CreatedSelfHealingStatus represents when the failed challenge gets stored in DB
	CreatedSelfHealingStatus SelfHealingStatus = "Created"
	//InProgressSelfHealingStatus represents when the challenge is retrieved for self-healing
	InProgressSelfHealingStatus SelfHealingStatus = "InProgress"
	//FailedSelfHealingStatus represents when the reconstruction has been completed
	FailedSelfHealingStatus SelfHealingStatus = "Failed"
	//CompletedSelfHealingStatus represents when the reconstruction has been completed
	CompletedSelfHealingStatus SelfHealingStatus = "Completed"
	//ReconstructionNotRequiredSelfHealingStatus represents when the reconstruction has been completed
	ReconstructionNotRequiredSelfHealingStatus SelfHealingStatus = "ReconstructionNotRequired"
)

// SelfHealingChallenge represents self-healing challenge
type SelfHealingChallenge struct {
	ID                    int64
	ChallengeID           string
	MerkleRoot            string
	FileHash              string
	ChallengingNode       string
	RespondingNode        string
	VerifyingNode         string
	ReconstructedFileHash []byte
	Status                SelfHealingStatus
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Fields represents status log
type Fields map[string]interface{}

// Details represents status log details with additional fields
type Details struct {
	Message string
	Fields  Fields
}

func (d *Details) Stringify() string {
	details, err := json.Marshal(&d)
	if err != nil {
		logtrace.Error(context.Background(), "unable to marshal task history details", logtrace.Fields{
			logtrace.FieldError: err.Error(),
		})
		return ""
	}

	return string(details)
}

// NewDetails initializes and return the valid detail object
func NewDetails(msg string, fields Fields) *Details {
	return &Details{
		Message: msg,
		Fields:  fields,
	}
}

// IsValid checks if the status log map is not empty
func (f Fields) IsValid() bool {
	return len(f) != 0
}
