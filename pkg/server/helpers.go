package server

import "context"

type IServer interface {
	Start(ctx context.Context)
	Drain()
}
