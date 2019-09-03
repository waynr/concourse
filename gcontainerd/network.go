package gcontainerd

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containernetworking/cni/libcni"
)

type Network interface {
	Setup(context.Context, containerd.Task) error
	Teardown(context.Context, containerd.Task) error
}

func NewCNINetwork(configList *libcni.NetworkConfigList, pluginDir, cacheDir string) Network {
	return &cniNetwork{
		cni: libcni.NewCNIConfig([]string{pluginDir}, nil),

		configList: configList,
		cacheDir:   cacheDir,

		ifName: "eth0",
	}
}

type cniNetwork struct {
	cni libcni.CNI

	configList *libcni.NetworkConfigList
	cacheDir   string

	ifName string
}

func (cniNet *cniNetwork) Setup(ctx context.Context, task containerd.Task) error {
	_, err := cniNet.cni.AddNetworkList(ctx, cniNet.configList, &libcni.RuntimeConf{
		ContainerID: task.ID(),

		NetNS:  cniNet.nsPath(task),
		IfName: cniNet.ifName,

		CacheDir: cniNet.cacheDir,
	})
	return err
}

func (cniNet *cniNetwork) Teardown(ctx context.Context, task containerd.Task) error {
	return cniNet.cni.DelNetworkList(ctx, cniNet.configList, &libcni.RuntimeConf{
		ContainerID: task.ID(),

		// by now the task has been removed; there is no namespace to actually
		// clean up, so just give it a blank namespace and let it at least clean up
		// IP reservations
		NetNS:  "",
		IfName: cniNet.ifName,

		CacheDir: cniNet.cacheDir,
	})
}

func (cniNet *cniNetwork) nsPath(task containerd.Task) string {
	return fmt.Sprintf("/proc/%d/ns/net", task.Pid())
}
