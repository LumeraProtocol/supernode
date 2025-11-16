package lumera

// ACTION_STATE represents the possible states of an action
type ACTION_STATE string

const (
	ACTION_STATE_UNSPECIFIED ACTION_STATE = "ACTION_STATE_UNSPECIFIED"
	ACTION_STATE_PENDING     ACTION_STATE = "ACTION_STATE_PENDING"
	ACTION_STATE_DONE        ACTION_STATE = "ACTION_STATE_DONE"
	ACTION_STATE_APPROVED    ACTION_STATE = "ACTION_STATE_APPROVED"
	ACTION_STATE_REJECTED    ACTION_STATE = "ACTION_STATE_REJECTED"
	ACTION_STATE_FAILED      ACTION_STATE = "ACTION_STATE_FAILED"
)

// SUPERNODE_STATE represents the possible states of a supernode
type SUPERNODE_STATE string

const (
	SUPERNODE_STATE_UNSPECIFIED SUPERNODE_STATE = "SUPERNODE_STATE_UNSPECIFIED"
	SUPERNODE_STATE_ACTIVE      SUPERNODE_STATE = "SUPERNODE_STATE_ACTIVE"
	SUPERNODE_STATE_DISABLED    SUPERNODE_STATE = "SUPERNODE_STATE_DISABLED"
	SUPERNODE_STATE_STOPPED     SUPERNODE_STATE = "SUPERNODE_STATE_STOPPED"
	SUPERNODE_STATE_PENALIZED   SUPERNODE_STATE = "SUPERNODE_STATE_PENALIZED"
)

// Action represents an action registered on the Lumera blockchain
type Action struct {
	ID             string
	State          ACTION_STATE
	Height         int64
	ExpirationTime int64
	ActionType     string // Type of the action (e.g., CASCADE)
	Metadata       []byte // Raw metadata bytes
	Creator        string // Creator of the action
}

type Supernodes []Supernode

// Supernode represents information about a supernode in the network
type Supernode struct {
	CosmosAddress string          // Blockchain identity of the supernode
	GrpcEndpoint  string          // Network endpoint for gRPC communication
	State         SUPERNODE_STATE // Current state of the supernode
}

func (s Supernodes) String() string {
	result := "["
	for i, sn := range s {
		result += sn.CosmosAddress + "@" + sn.GrpcEndpoint

		if i < len(s)-1 {
			result += ", "
		}
	}
	result += "]"
	return result
}
