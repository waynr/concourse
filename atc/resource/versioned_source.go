package resource

import (
	"context"
	"github.com/concourse/concourse/atc/storage"
	"io"
	"path"

	"github.com/concourse/concourse/atc"
)

//go:generate counterfeiter . VersionedSource

type VersionedSource interface {
	Version() atc.Version
	Metadata() []atc.MetadataField

	StreamOut(context.Context, string) (io.ReadCloser, error)
	StreamIn(context.Context, string, io.Reader) error

	Volume() storage.Blob
}

type VersionResult struct {
	Version atc.Version `json:"version"`

	Metadata []atc.MetadataField `json:"metadata,omitempty"`
}

func NewGetVersionedSource(blob storage.Blob, version atc.Version, metadata []atc.MetadataField) VersionedSource {
	return &getVersionedSource{
		blob:      blob,
		resourceDir: ResourcesDir("get"),

		versionResult: VersionResult{
			Version:  version,
			Metadata: metadata,
		},
	}
}

type getVersionedSource struct {
	versionResult VersionResult

	blob      storage.Blob
	resourceDir string
}

func (vs *getVersionedSource) Version() atc.Version {
	return vs.versionResult.Version
}

func (vs *getVersionedSource) Metadata() []atc.MetadataField {
	return vs.versionResult.Metadata
}

func (vs *getVersionedSource) StreamOut(ctx context.Context, src string) (io.ReadCloser, error) {
	readCloser, err := vs.blob.StreamOut(ctx, src)
	if err != nil {
		return nil, err
	}

	return readCloser, err
}

func (vs *getVersionedSource) StreamIn(ctx context.Context, dst string, src io.Reader) error {
	return vs.blob.StreamIn(ctx, path.Join(vs.resourceDir, dst), src)
}

func (vs *getVersionedSource) Volume() storage.Blob {
	return vs.blob
}
