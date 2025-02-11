package configs

import (
	"github.com/LumeraProtocol/supernode/supernode/node/grpc/server"
	"github.com/LumeraProtocol/supernode/supernode/services/cascaderegister"
	"github.com/LumeraProtocol/supernode/supernode/services/collectionregister"
	"github.com/LumeraProtocol/supernode/supernode/services/download"
	"github.com/LumeraProtocol/supernode/supernode/services/healthcheckchallenge"
	"github.com/LumeraProtocol/supernode/supernode/services/nftregister"
	"github.com/LumeraProtocol/supernode/supernode/services/selfhealing"
	"github.com/LumeraProtocol/supernode/supernode/services/senseregister"
	"github.com/LumeraProtocol/supernode/supernode/services/storagechallenge"
)

// Node contains the SuperNode configuration itself.
type Node struct {
	// `squash` field cannot be pointer
	NftRegister        nftregister.Config        `mapstructure:",squash" json:"nft_register,omitempty"`
	SenseRegister      senseregister.Config      `mapstructure:",squash" json:"sense_register,omitempty"`
	CascadeRegister    cascaderegister.Config    `mapstructure:",squash" json:"cascade_register,omitempty"`
	CollectionRegister collectionregister.Config `mapstructure:",squash" json:"collection_register,omitempty"`
	Server             *server.Config            `mapstructure:"server" json:"server,omitempty"`
	PastelID           string                    `mapstructure:"pastel_id" json:"pastel_id,omitempty"`
	PassPhrase         string                    `mapstructure:"pass_phrase" json:"pass_phrase,omitempty"`

	NumberConnectedNodes       int `mapstructure:"number_connected_nodes" json:"number_connected_nodes,omitempty"`
	PreburntTxMinConfirmations int `mapstructure:"preburnt_tx_min_confirmations" json:"preburnt_tx_min_confirmations,omitempty"`

	NftDownload          download.Config             `mapstructure:",squash" json:"nft_download,omitempty"`
	StorageChallenge     storagechallenge.Config     `mapstructure:",squash" json:"storage_challenge,omitempty"`
	HealthCheckChallenge healthcheckchallenge.Config `mapstructure:",squash" json:"healthcheck_challenge,omitempty"`
	SelfHealingChallenge selfhealing.Config          `mapstructure:",squash" json:"self_healing,omitempty"`
}

// NewNode returns a new Node instance
func NewNode() Node {
	return Node{
		NftRegister:        *nftregister.NewConfig(),
		SenseRegister:      *senseregister.NewConfig(),
		CascadeRegister:    *cascaderegister.NewConfig(),
		NftDownload:        *download.NewConfig(),
		CollectionRegister: *collectionregister.NewConfig(),
		// UserdataProcess: *userdataprocess.NewConfig(),
		Server: server.NewConfig(),
	}
}
