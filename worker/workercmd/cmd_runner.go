package workercmd

import (
	"os"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
)

type cmdRunner struct {
	cmd   *exec.Cmd
	check func() error
}

func (runner cmdRunner) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	err := runner.cmd.Start()
	if err != nil {
		return err
	}

	for {
		err := runner.check()
		if err == nil {
			break
		}

		logrus.Warnf("check failed; trying again in 1s: %s", err)
		time.Sleep(time.Second)
	}

	close(ready)

	waitErr := make(chan error, 1)

	go func() {
		waitErr <- runner.cmd.Wait()
	}()

	for {
		select {
		case sig := <-signals:
			runner.cmd.Process.Signal(sig)
		case err := <-waitErr:
			return err
		}
	}
}
