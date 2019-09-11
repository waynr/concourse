package gcontainerd

import (
	"context"
	"io"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/snapshots"
)

//go:generate counterfeiter . Client

// interface to allow us to generate a fake containerd client to be used in writing tests
type Client interface {
	Pull(context.Context, string, ...containerd.RemoteOpt) (containerd.Image, error)
	NewContainer(context.Context, string, ...containerd.NewContainerOpts) (containerd.Container, error)
	LoadContainer(context.Context, string) (containerd.Container, error)
	GetImage(context.Context, string) (containerd.Image, error)
	ImageService() images.Store
	Containers(context.Context, ...string) ([]containerd.Container, error)
	SnapshotService(string) snapshots.Snapshotter
	Import(context.Context, io.Reader, ...containerd.ImportOpt) ([]images.Image, error)
}
