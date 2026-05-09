package bifrost

import (
	"context"
	"net"
)

type StreamDialer interface {
	OpenStream(context.Context) (net.Conn, error)
}

type StreamAcceptor interface {
	AcceptStream(context.Context) (net.Conn, error)
}
