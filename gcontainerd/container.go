package gcontainerd

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"code.cloudfoundry.org/garden"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/nu7hatch/gouuid"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

var ErrNotImplemented = errors.New("not implemented")

type UndefinedPropertyError struct {
	Key string
}

func (err UndefinedPropertyError) Error() string {
	return fmt.Sprintf("property does not exist: %s", err.Key)
}

type container struct {
	ctx context.Context

	c containerd.Container

	taskOpts interface{}

	graceTime  time.Duration
	graceTimeL sync.Mutex
}

func (container *container) Handle() string {
	return container.c.ID()
}

func (container *container) Stop(kill bool) error {
	return ErrNotImplemented
}

func (container *container) Info() (garden.ContainerInfo, error) {
	return garden.ContainerInfo{}, ErrNotImplemented
}

func (container *container) StreamIn(spec garden.StreamInSpec) error {
	return ErrNotImplemented
}

func (container *container) StreamOut(spec garden.StreamOutSpec) (io.ReadCloser, error) {
	return nil, ErrNotImplemented
	// if strings.HasSuffix(spec.Path, "/") {
	// 	spec.Path += "."
	// }

	// absoluteSource := container.workDir + string(os.PathSeparator) + filepath.FromSlash(spec.Path)

	// r, w := io.Pipe()

	// errs := make(chan error, 1)
	// go func() {
	// 	errs <- tarfs.Compress(w, filepath.Dir(absoluteSource), filepath.Base(absoluteSource))
	// 	_ = w.Close()
	// }()

	// return waitCloser{
	// 	ReadCloser: r,
	// 	wait:       errs,
	// }, nil
}

// type waitCloser struct {
// 	io.ReadCloser
// 	wait <-chan error
// }

// func (c waitCloser) Close() error {
// 	err := c.ReadCloser.Close()
// 	if err != nil {
// 		return err
// 	}

// 	return <-c.wait
// }

func (container *container) LimitBandwidth(limits garden.BandwidthLimits) error {
	return ErrNotImplemented
}

func (container *container) CurrentBandwidthLimits() (garden.BandwidthLimits, error) {
	return garden.BandwidthLimits{}, ErrNotImplemented
}

func (container *container) LimitCPU(limits garden.CPULimits) error {
	return ErrNotImplemented
}

func (container *container) CurrentCPULimits() (garden.CPULimits, error) {
	return garden.CPULimits{}, ErrNotImplemented
}

func (container *container) LimitDisk(limits garden.DiskLimits) error {
	return ErrNotImplemented
}

func (container *container) CurrentDiskLimits() (garden.DiskLimits, error) {
	return garden.DiskLimits{}, ErrNotImplemented
}

func (container *container) LimitMemory(limits garden.MemoryLimits) error {
	return ErrNotImplemented
}

func (container *container) CurrentMemoryLimits() (garden.MemoryLimits, error) {
	return garden.MemoryLimits{}, ErrNotImplemented
}

func (container *container) NetIn(hostPort, containerPort uint32) (uint32, uint32, error) {
	return 0, 0, ErrNotImplemented
}

func (container *container) NetOut(garden.NetOutRule) error {
	return ErrNotImplemented
}

func (container *container) BulkNetOut([]garden.NetOutRule) error {
	return ErrNotImplemented
}

func (container *container) Run(spec garden.ProcessSpec, processIO garden.ProcessIO) (garden.Process, error) {
	cwd := spec.Dir
	if cwd == "" {
		cwd = "/"
	}

	task, err := container.c.Task(container.ctx, nil)
	if err != nil {
		return nil, err
	}

	cspec, err := container.c.Spec(container.ctx)
	if err != nil {
		return nil, errors.Wrap(err, "container spec")
	}

	pspec := cspec.Process
	if spec.User != "" {
		pspec.User = specs.User{
			Username: spec.User,
		}
	}

	pspec.Args = append([]string{spec.Path}, spec.Args...)
	pspec.Env = append(pspec.Env, spec.Env...)

	if spec.Dir != "" {
		pspec.Cwd = spec.Dir
	}

	cioOpts := []cio.Opt{
		cio.WithStreams(
			processIO.Stdin,
			processIO.Stdout,
			processIO.Stderr,
		),
	}

	if spec.TTY != nil {
		cioOpts = append(cioOpts, cio.WithTerminal)

		pspec.Terminal = true

		if spec.TTY.WindowSize != nil {
			pspec.ConsoleSize = &specs.Box{
				Width:  uint(spec.TTY.WindowSize.Columns),
				Height: uint(spec.TTY.WindowSize.Rows),
			}
		}
	}

	id := spec.ID
	if id == "" {
		uuid, err := uuid.NewV4()
		if err != nil {
			return nil, err
		}

		id = uuid.String()
	}

	process, err := task.Exec(container.ctx, id, pspec, cio.NewCreator(cioOpts...))
	if err != nil {
		return nil, err
	}

	exitStatusC, err := process.Wait(container.ctx)
	if err != nil {
		return nil, err
	}

	err = process.Start(container.ctx)
	if err != nil {
		return nil, err
	}

	// allow stdin to be closed at end of stream
	//
	// (despite the name, this merely *allows* it, it doesn't close it here)
	err = process.CloseIO(container.ctx, containerd.WithStdinCloser)
	if err != nil {
		return nil, err
	}

	return newProcess(container.ctx, process, exitStatusC), nil
}

func (container *container) Attach(processID string, processIO garden.ProcessIO) (garden.Process, error) {
	return nil, ErrNotImplemented
}

func (container *container) Property(name string) (string, error) {
	labels, err := container.c.Labels(container.ctx)
	if err != nil {
		return "", err
	}

	val, found := labels[name]
	if !found {
		return "", UndefinedPropertyError{name}
	}

	return val, nil
}

func (container *container) SetProperty(name string, value string) error {
	_, err := container.c.SetLabels(container.ctx, map[string]string{
		name: value,
	})
	return err
}

func (container *container) RemoveProperty(name string) error {
	return ErrNotImplemented
}

func (container *container) Properties() (garden.Properties, error) {
	return container.c.Labels(container.ctx)
}

func (container *container) Metrics() (garden.Metrics, error) {
	return garden.Metrics{}, nil
}

func (container *container) SetGraceTime(t time.Duration) error {
	container.graceTimeL.Lock()
	container.graceTime = t
	container.graceTimeL.Unlock()
	return nil
}

func (container *container) ioOpts(_ context.Context, client *containerd.Client, r *containerd.TaskInfo) error {
	r.Options = container.taskOpts
	return nil
}

func (container *container) currentGraceTime() time.Duration {
	container.graceTimeL.Lock()
	defer container.graceTimeL.Unlock()
	return container.graceTime
}
