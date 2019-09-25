package resource

import (
	"context"
	"github.com/concourse/concourse/atc/storage"

	"github.com/concourse/concourse/atc"
)

type getRequest struct {
	Source  atc.Source  `json:"source"`
	Params  atc.Params  `json:"params,omitempty"`
	Version atc.Version `json:"version,omitempty"`
}

func (resource *resource) Get(
	ctx context.Context,
	blob storage.Blob,
	ioConfig IOConfig,
	source atc.Source,
	params atc.Params,
	version atc.Version,
) (VersionedSource, error) {
	var vr VersionResult

	err := resource.runScript(
		ctx,
		"/opt/resource/in",
		[]string{ResourcesDir("get")},
		getRequest{source, params, version},
		&vr,
		ioConfig.Stderr,
		true,
	)
	if err != nil {
		return nil, err
	}

	return NewGetVersionedSource(blob, vr.Version, vr.Metadata), nil
}
