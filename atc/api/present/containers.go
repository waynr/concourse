package present

import (
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db"
)

func Containers(savedContainers []db.Container) []atc.Container {
	containers := make([]atc.Container, len(savedContainers))

	for i := range savedContainers {
		containers[i] = Container(savedContainers[i], ) // TODO by ZOE: more args here - update in the atc/api/container.go
	}

	return containers
}
