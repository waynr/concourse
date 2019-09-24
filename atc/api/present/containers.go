package present

import (
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db"
	"time"
)

func Containers(savedContainers []db.Container) []atc.Container {
	containers := make([]atc.Container, len(savedContainers))

	for i := range savedContainers {
		containers[i] = Container(savedContainers[i], time.Now())
	}

	return containers
}
