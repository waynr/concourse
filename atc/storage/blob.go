package storage

import (
	"context"
	"io"
)

type Blob interface {
	Handle() string

	StreamIn(ctx context.Context, path string, tarStream io.Reader) error
	StreamOut(ctx context.Context, path string) (io.ReadCloser, error)

	Destroy() error
}
