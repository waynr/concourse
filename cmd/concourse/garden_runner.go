package main

import (
	"fmt"

	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/garden/server"
	"code.cloudfoundry.org/lager"
	"github.com/tedsuo/ifrit"
)

func (cmd *WorkerCommand) backendRunner(logger lager.Logger, backend garden.Backend) ifrit.Runner {
	server := server.New(
		"tcp",
		cmd.bindAddr(),
		0,
		backend,
		logger,
	)

	return gardenServerRunner{logger, server}
}

func (cmd *WorkerCommand) bindAddr() string {
	return fmt.Sprintf("%s:%d", cmd.BindIP.IP, cmd.BindPort)
}
