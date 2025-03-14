package task

type CascadeTicket struct {
	Creator          string   `json:"creator"`
	CreatorSignature []byte   `json:"creator_signature"`
	DataHash         string   `json:"data_hash"`
	ActionID         string   `json:"action_id"`
	BlockHeight      int64    `json:"block_height"`
	BlockHash        []byte   `json:"block_hash"`
	RQIDsIC          uint32   `json:"rqids_ic"`
	RQIDsMax         int32    `json:"rqids_max"`
	RQIDs            []string `json:"rq_ids"`
}
