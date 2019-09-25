package runner

import (
	"code.cloudfoundry.org/lager"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/storage"
)

type Client interface {
	FindVolumeForResourceCache(logger lager.Logger, resourceCache db.UsedResourceCache) (storage.Blob, bool, error)
}


type Runnable interface {

}