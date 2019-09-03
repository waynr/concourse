package gcontainerd

import (
	"context"
	"syscall"

	"code.cloudfoundry.org/garden"
	"github.com/containerd/containerd"
	"github.com/pkg/errors"
)

type process struct {
	ctx context.Context

	p containerd.Process
	t containerd.Task

	exitStatusC <-chan containerd.ExitStatus
}

func newProcess(
	ctx context.Context,
	p containerd.Process,
	exitStatusC <-chan containerd.ExitStatus,
) garden.Process {
	return &process{
		ctx: ctx,

		p: p,

		exitStatusC: exitStatusC,
	}
}

func (p *process) ID() string {
	return p.p.ID()
}

func (p *process) Wait() (int, error) {
	<-p.exitStatusC

	status, err := p.p.Delete(p.ctx)
	if err != nil {
		return 0, errors.Wrap(err, "delete process")
	}

	return int(status.ExitCode()), nil
}

func (p *process) SetTTY(spec garden.TTYSpec) error {
	if spec.WindowSize == nil {
		return nil
	}

	return p.p.Resize(
		p.ctx,
		uint32(spec.WindowSize.Columns),
		uint32(spec.WindowSize.Rows),
	)
}

func (p *process) Signal(sig garden.Signal) error {
	s := syscall.SIGTERM
	if sig == garden.SignalKill {
		s = syscall.SIGKILL
	}

	return p.p.Kill(p.ctx, s)
}
