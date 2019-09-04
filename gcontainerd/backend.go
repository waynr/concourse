package gcontainerd

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"code.cloudfoundry.org/garden"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/defaults"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Backend struct {
	ctx context.Context

	client  *containerd.Client
	network Network

	ociSpecOpts []oci.SpecOpts

	maxUid uint32
	maxGid uint32
}

func NewBackend(
	client *containerd.Client,
	namespace string,
	ociSpecOpts []oci.SpecOpts,
	network Network,
) (*Backend, error) {
	maxUid, err := defaultUIDMap.MaxValid()
	if err != nil {
		return nil, err
	}

	maxGid, err := defaultGIDMap.MaxValid()
	if err != nil {
		return nil, err
	}

	return &Backend{
		ctx: namespaces.WithNamespace(context.Background(), namespace),

		client:  client,
		network: network,

		ociSpecOpts: ociSpecOpts,

		maxUid: maxUid,
		maxGid: maxGid,
	}, nil
}

func (backend *Backend) Start() error {
	return nil
}

func (backend *Backend) Stop() {}

func (backend *Backend) GraceTime(c garden.Container) time.Duration {
	return c.(*container).currentGraceTime()
}

func (backend *Backend) Ping() error {
	// XXX: ping containerd?
	return nil
}

func (backend *Backend) Capacity() (garden.Capacity, error) {
	println("NOT IMPLEMENTED: Capacity")
	return garden.Capacity{}, nil
}

func (backend *Backend) Create(spec garden.ContainerSpec) (garden.Container, error) {
	ctx := backend.ctx

	client := backend.client

	uri := spec.Image.URI
	if uri == "" {
		// backwards compatibility
		uri = spec.RootFSPath
	}

	rootfsURI, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	var image containerd.Image
	switch rootfsURI.Scheme {
	case "oci":
		image, err = importImage(ctx, spec.Handle, client, rootfsURI.Path)
	case "docker":
		image, err = client.Pull(ctx, rootfsURI.Host+rootfsURI.Path+":"+rootfsURI.Fragment, containerd.WithPullUnpack)
	default:
		return nil, fmt.Errorf("unknown rootfs uri: %s", spec.RootFSPath)
	}
	if err != nil {
		return nil, err
	}

	mounts := []specs.Mount{
		{
			Destination: "/sys/fs/cgroup",
			Type:        "cgroup",
			Source:      "cgroup",
			Options:     []string{"ro", "nosuid", "noexec", "nodev"},
		},
	}

	for _, m := range spec.BindMounts {
		mount := specs.Mount{
			Destination: m.DstPath,
			Source:      m.SrcPath,
			Type:        "bind",
			Options:     []string{"bind"},
		}

		if m.Mode == garden.BindMountModeRO {
			mount.Options = append(mount.Options, "ro")
		}

		mounts = append(mounts, mount)
	}

	specOpts := []oci.SpecOpts{
		// carry over garden defaults
		oci.WithDefaultUnixDevices,
		oci.WithLinuxDevice("/dev/fuse", "rwm"),

		// wire up mounts
		oci.WithMounts(mounts),

		// enable user namespaces
		oci.WithLinuxNamespace(specs.LinuxNamespace{
			Type: specs.UserNamespace,
		}),
		withRemappedRoot(backend.maxUid, backend.maxGid),

		// set handle as hostname
		oci.WithHostname(spec.Handle),

		// inherit image config
		oci.WithImageConfig(image),

		// propagate env
		oci.WithEnv(spec.Env),
	}

	if spec.Privileged {
		specOpts = append(specOpts,
			// minimum required caps for running buildkit
			oci.WithAddedCapabilities([]string{
				"CAP_SYS_ADMIN",
				"CAP_NET_ADMIN",
			}),
		)
	}

	specOpts = append(specOpts, backend.ociSpecOpts...)

	cont, err := client.NewContainer(
		ctx,
		spec.Handle,
		containerd.WithContainerLabels(spec.Properties),
		withRemappedSnapshot(spec.Handle, image, backend.maxUid, backend.maxGid),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return nil, errors.Wrap(err, "new container")
	}

	task, err := cont.NewTask(backend.ctx, cio.NullIO)
	if err != nil {
		return nil, errors.Wrap(err, "new task")
	}

	err = backend.network.Setup(backend.ctx, task)
	if err != nil {
		return nil, errors.Wrap(err, "setup network")
	}

	return backend.newContainer(cont)
}

func (backend *Backend) newContainer(cont containerd.Container) (garden.Container, error) {
	info, err := cont.Info(backend.ctx)
	if err != nil {
		return nil, err
	}

	var taskOpts interface{}
	if containerd.CheckRuntime(info.Runtime.Name, "io.containerd.runc") {
		taskOpts = &options.Options{
			IoUid: backend.maxUid,
			IoGid: backend.maxGid,
		}
	} else {
		taskOpts = &runctypes.CreateOptions{
			IoUid: backend.maxUid,
			IoGid: backend.maxGid,
		}
	}

	return &container{
		ctx: backend.ctx,

		c: cont,

		taskOpts: taskOpts,
	}, nil
}

func (backend *Backend) Destroy(handle string) error {
	cont, err := backend.client.LoadContainer(backend.ctx, handle)
	if err != nil {
		return errors.Wrap(err, "load container")
	}

	task, err := cont.Task(backend.ctx, nil)
	if err != nil {
		return errors.Wrap(err, "get task")
	}

	_, err = task.Delete(backend.ctx, containerd.WithProcessKill)
	if err != nil {
		return errors.Wrap(err, "delete task")
	}

	// remove imported container image
	_, err = backend.client.GetImage(backend.ctx, handle)
	if err == nil {
		err = backend.client.ImageService().Delete(backend.ctx, handle)
		if err != nil {
			return errors.Wrap(err, "delete image")
		}
	}

	err = cont.Delete(backend.ctx, containerd.WithSnapshotCleanup)
	if err != nil {
		return errors.Wrap(err, "delete container")
	}

	err = backend.network.Teardown(backend.ctx, task)
	if err != nil {
		return errors.Wrap(err, "remove network")
	}

	return nil
}

func (backend *Backend) Containers(filter garden.Properties) ([]garden.Container, error) {
	var filters []string
	for k, v := range filter {
		filters = append(filters, fmt.Sprintf("labels.%q==%s", k, v))
	}

	containers, err := backend.client.Containers(backend.ctx, filters...)
	if err != nil {
		return nil, err
	}

	var cs []garden.Container
	for _, cont := range containers {
		c, err := backend.newContainer(cont)
		if err != nil {
			return nil, err
		}

		cs = append(cs, c)
	}

	return cs, nil
}

func (backend *Backend) BulkInfo(handles []string) (map[string]garden.ContainerInfoEntry, error) {
	return map[string]garden.ContainerInfoEntry{}, nil
}

func (backend *Backend) BulkMetrics(handles []string) (map[string]garden.ContainerMetricsEntry, error) {
	return map[string]garden.ContainerMetricsEntry{}, nil
}

func (backend *Backend) Lookup(handle string) (garden.Container, error) {
	cont, err := backend.client.LoadContainer(backend.ctx, handle)
	if err != nil {
		return nil, err
	}

	return backend.newContainer(cont)
}

func withRemappedRoot(maxUid, maxGid uint32) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		s.Linux.UIDMappings = []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      maxUid,
				Size:        1,
			},
			{
				ContainerID: 1,
				HostID:      1,
				Size:        maxUid - 1,
			},
		}

		s.Linux.GIDMappings = []specs.LinuxIDMapping{
			{
				ContainerID: 0,
				HostID:      maxGid,
				Size:        1,
			},
			{
				ContainerID: 1,
				HostID:      1,
				Size:        maxGid - 1,
			},
		}

		return nil
	}
}

func withRemappedSnapshot(id string, i containerd.Image, uid, gid uint32) containerd.NewContainerOpts {
	return func(ctx context.Context, client *containerd.Client, c *containers.Container) error {
		diffIDs, err := i.RootFS(ctx)
		if err != nil {
			return err
		}

		var (
			parent   = identity.ChainID(diffIDs).String()
			usernsID = fmt.Sprintf("%s-%d-%d", parent, uid, gid)
		)

		c.Snapshotter, err = resolveSnapshotterName(client, ctx, c.Snapshotter)
		if err != nil {
			return err
		}

		snapshotter := client.SnapshotService(c.Snapshotter)

		logrus.Infof("snapshotting %s", usernsID)

		if _, err := snapshotter.Stat(ctx, usernsID); err == nil {
			logrus.Infof("FOUND EXISTING SNAPSHOT FOR %s", usernsID)
			if _, err := snapshotter.Prepare(ctx, id, usernsID); err == nil {
				logrus.Infof("PREPARED %s FOR %s", usernsID, id)
				c.SnapshotKey = id
				c.Image = i.Name()
				return nil
			} else if !errdefs.IsNotFound(err) {
				return err
			}
		}

		logrus.Infof("EXISTING SNAPSHOT NOT FOUND FOR %s", usernsID)
		mounts, err := snapshotter.Prepare(ctx, usernsID+"-remap", parent)
		if err != nil {
			return err
		}

		logrus.Infof("REMAPPING FOR %s", usernsID)
		if err := remapRootFS(ctx, mounts, uid, gid); err != nil {
			snapshotter.Remove(ctx, usernsID)
			return err
		}

		logrus.Infof("COMMITTING %s AS %s", usernsID+"-remap", usernsID)
		if err := snapshotter.Commit(ctx, usernsID, usernsID+"-remap"); err != nil {
			return err
		}

		logrus.Infof("PREPARING %s FOR %s", usernsID, id)
		_, err = snapshotter.Prepare(ctx, id, usernsID)
		if err != nil {
			return err
		}

		logrus.Info("DONE")

		c.SnapshotKey = id
		c.Image = i.Name()

		return nil
	}
}

func resolveSnapshotterName(c *containerd.Client, ctx context.Context, name string) (string, error) {
	if name == "" {
		label, err := c.GetLabel(ctx, defaults.DefaultSnapshotterNSLabel)
		if err != nil {
			return "", err
		}

		if label != "" {
			name = label
		} else {
			name = containerd.DefaultSnapshotter
		}
	}

	return name, nil
}

func remapRootFS(ctx context.Context, mounts []mount.Mount, uid, gid uint32) error {
	return mount.WithTempMount(ctx, mounts, func(root string) error {
		return filepath.Walk(root, remapRoot(root, uid, gid))
	})
}

func remapRoot(root string, toUid, toGid uint32) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		stat := info.Sys().(*syscall.Stat_t)

		var remap bool

		uid := stat.Uid
		if uid == 0 {
			remap = true
			uid = toUid
		}

		gid := stat.Gid
		if gid == 0 {
			remap = true
			gid = toGid
		}

		if !remap {
			return nil
		}

		// be sure the lchown the path as to not de-reference the symlink to a host file
		return os.Lchown(path, int(uid), int(gid))
	}
}

func importImage(ctx context.Context, handle string, client *containerd.Client, path string) (containerd.Image, error) {
	imageFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer imageFile.Close()

	logrus.Info("importing")

	images, err := client.Import(ctx, imageFile, containerd.WithIndexName(handle))
	if err != nil {
		return nil, err
	}

	var image containerd.Image
	for _, i := range images {
		image = containerd.NewImage(client, i)

		err = image.Unpack(ctx, containerd.DefaultSnapshotter)
		if err != nil {
			return nil, err
		}
	}

	logrus.Debug("image ready")

	if image == nil {
		return nil, fmt.Errorf("no image found in archive: %s", path)
	}

	return image, nil
}
