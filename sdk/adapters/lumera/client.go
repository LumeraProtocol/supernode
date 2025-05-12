package lumera

import "context"

//go:generate mockgen -source=client.go -destination=mocks/client_mock.go -package=mocks
type Client interface {
	GetAction(ctx context.Context, actionID string) (Action, error)
	GetSupernodes(ctx context.Context, height int64) ([]Supernode, error)
}
