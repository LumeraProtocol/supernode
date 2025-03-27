package common

import (
	"bytes"
	"context"
	"sync"

	"github.com/LumeraProtocol/supernode/pkg/errors"
	"github.com/LumeraProtocol/supernode/pkg/log"
	"github.com/LumeraProtocol/supernode/pkg/lumera"
	"github.com/LumeraProtocol/supernode/pkg/raptorq"
	"github.com/LumeraProtocol/supernode/pkg/utils"
)

const (
	SeparatorByte = 46
)

// RegTaskHelper common operations related to (any) Ticket registration
type RegTaskHelper struct {
	*SuperNodeTask

	NetworkHandler *NetworkHandler
	LumeraHandler  *lumera.Client

	peersTicketSignatureMtx  *sync.Mutex
	PeersTicketSignature     map[string][]byte
	AllSignaturesReceivedChn chan struct{}
}

// NewRegTaskHelper creates instance of RegTaskHelper
func NewRegTaskHelper(task *SuperNodeTask,
	lumeraClient lumera.Client,
	NetworkHandler *NetworkHandler,
) *RegTaskHelper {
	return &RegTaskHelper{
		SuperNodeTask:            task,
		LumeraHandler:            &lumeraClient,
		NetworkHandler:           NetworkHandler,
		peersTicketSignatureMtx:  &sync.Mutex{},
		PeersTicketSignature:     make(map[string][]byte),
		AllSignaturesReceivedChn: make(chan struct{}),
	}
}

// AddPeerTicketSignature waits for ticket signatures from other SNs and adds them into internal array
func (h *RegTaskHelper) AddPeerTicketSignature(nodeID string, signature []byte, reqStatus Status) error {
	h.peersTicketSignatureMtx.Lock()
	defer h.peersTicketSignatureMtx.Unlock()

	if err := h.RequiredStatus(reqStatus); err != nil {
		return err
	}

	var err error

	<-h.NewAction(func(ctx context.Context) error {
		log.WithContext(ctx).Debugf("receive NFT ticket signature from node %s", nodeID)
		if node := h.NetworkHandler.Accepted.ByID(nodeID); node == nil {
			log.WithContext(ctx).WithField("node", nodeID).Errorf("node is not in Accepted list")
			err = errors.Errorf("node %s not in Accepted list", nodeID)
			return nil
		}

		h.PeersTicketSignature[nodeID] = signature
		if len(h.PeersTicketSignature) == len(h.NetworkHandler.Accepted) {
			log.WithContext(ctx).Debug("all signature received")
			go func() {
				close(h.AllSignaturesReceivedChn)
			}()
		}
		return nil
	})
	return err
}

// ValidateIDFiles validates received (IDs) file and its (50) IDs:
//  1. checks signatures
//  2. generates list of 50 IDs and compares them to received
func (h *RegTaskHelper) ValidateIDFiles(ctx context.Context,
	data []byte, ic uint32, max uint32, ids []string, numSignRequired int,
	snAccAddresses []string,
	lumeraClient lumera.Client,
	creatorSignaure []byte,
) ([]byte, [][]byte, error) {

	dec, err := utils.B64Decode(data)
	if err != nil {
		return nil, nil, errors.Errorf("decode data: %w", err)
	}

	decData, err := utils.Decompress(dec)
	if err != nil {
		return nil, nil, errors.Errorf("decompress: %w", err)
	}

	splits := bytes.Split(decData, []byte{SeparatorByte})
	if len(splits) != numSignRequired+1 {
		return nil, nil, errors.New("invalid data")
	}

	file, err := utils.B64Decode(splits[0])
	if err != nil {
		return nil, nil, errors.Errorf("decode file: %w", err)
	}

	verifications := 0
	verifiedNodes := make(map[int]bool)
	for i := 1; i < numSignRequired+1; i++ {
		for j := 0; j < len(snAccAddresses); j++ {
			if _, ok := verifiedNodes[j]; ok {
				continue
			}

			err := lumeraClient.Node().Verify(snAccAddresses[j], file, creatorSignaure) // TODO : verify the signature
			if err != nil {
				return nil, nil, errors.Errorf("verify file signature %w", err)
			}

			verifiedNodes[j] = true
			verifications++
			break
		}
	}

	if verifications != numSignRequired {
		return nil, nil, errors.Errorf("file verification failed: need %d verifications, got %d", numSignRequired, verifications)
	}

	gotIDs, idFiles, err := raptorq.GetIDFiles(ctx, decData, ic, max)
	if err != nil {
		return nil, nil, errors.Errorf("get ids: %w", err)
	}

	if err := utils.EqualStrList(gotIDs, ids); err != nil {
		return nil, nil, errors.Errorf("IDs don't match: %w", err)
	}

	return file, idFiles, nil
}
