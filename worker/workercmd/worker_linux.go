package workercmd

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/concourse/atc"
	concourseCmd "github.com/concourse/concourse/cmd"
	"github.com/concourse/flag"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/containernetworking/cni/libcni"
	"github.com/jessevdk/go-flags"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/concourse/concourse/gcontainerd"
)

type Certs struct {
	Dir string `long:"certs-dir" description:"Directory to use when creating the resource certificates volume."`
}

type GardenBackend struct {
	UseHoudini bool `long:"use-houdini" description:"Use the insecure Houdini Garden backend."`

	CNIPluginDir flag.Dir `long:"cni-plugin-dir" default:"/opt/cni/bin" description:"Directory containing CNI plugins."`

	DNS DNSConfig `group:"DNS Proxy Configuration" namespace:"dns-proxy"`
}

func (cmd WorkerCommand) LessenRequirements(prefix string, command *flags.Command) {
	// configured as work-dir/volumes
	command.FindOptionByLongName(prefix + "baggageclaim-volumes").Required = false
}

func (cmd *WorkerCommand) gardenRunner(logger lager.Logger) (atc.Worker, ifrit.Runner, error) {
	err := cmd.checkRoot()
	if err != nil {
		return atc.Worker{}, nil, err
	}

	worker := cmd.Worker.Worker()
	worker.Platform = "linux"

	if cmd.Certs.Dir != "" {
		worker.CertsPath = &cmd.Certs.Dir
	}

	worker.ResourceTypes, err = cmd.loadResources(logger.Session("load-resources"))
	if err != nil {
		return atc.Worker{}, nil, err
	}

	worker.Name, err = cmd.workerName()
	if err != nil {
		return atc.Worker{}, nil, err
	}

	var runner ifrit.Runner
	if cmd.Garden.UseHoudini {
		runner, err = cmd.houdiniRunner(logger)
	} else {
		runner, err = cmd.containerdRunner(logger)
	}
	if err != nil {
		return atc.Worker{}, nil, err
	}

	return worker, runner, nil
}

func (cmd *WorkerCommand) containerdRunner(logger lager.Logger) (ifrit.Runner, error) {
	members := grouper.Members{}

	containerdSock := filepath.Join(cmd.WorkDir.Path(), "containerd.sock")

	containerdArgs := []string{
		"--address", containerdSock,
		"--root", filepath.Join(cmd.WorkDir.Path(), "containerd"),
	}

	containerdCmd := exec.Command("containerd", containerdArgs...)
	containerdCmd.Stdout = os.Stdout
	containerdCmd.Stderr = os.Stderr
	containerdCmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGKILL,
	}

	members = append(members, grouper.Member{
		Name: "containerd",
		Runner: NewLoggingRunner(
			logger.Session("containerd-runner"),
			cmdRunner{
				cmd: containerdCmd,
				check: func() error {
					client, err := containerd.New(containerdSock)
					if err != nil {
						return err
					}

					client.Close()
					return nil
				},
			},
		),
	})

	members = append(members, grouper.Member{
		Name:   "garden-containerd",
		Runner: cmd.gardenContainerdRunner(logger, containerdSock),
	})

	if cmd.Garden.DNS.Enable {
		dnsProxyRunner, err := cmd.dnsProxyRunner(logger.Session("dns-proxy"))
		if err != nil {
			return nil, err
		}

		members = append(members, grouper.Member{
			Name: "dns-proxy",
			Runner: concourseCmd.NewLoggingRunner(
				logger.Session("dns-proxy-runner"),
				dnsProxyRunner,
			),
		})
	}

	return grouper.NewOrdered(os.Interrupt, members), nil
}

func (cmd *WorkerCommand) gardenContainerdRunner(logger lager.Logger, containerdSock string) ifrit.Runner {
	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		client, err := containerd.New(containerdSock, containerd.WithTimeout(time.Minute))
		if err != nil {
			return err
		}

		etcResolvconfFile := filepath.Join(cmd.WorkDir.Path(), "resolv.conf")
		err = ioutil.WriteFile(etcResolvconfFile, []byte(etcResolvconf), 0644)
		if err != nil {
			return err
		}

		etcHostsFile := filepath.Join(cmd.WorkDir.Path(), "hosts")
		err = ioutil.WriteFile(etcHostsFile, []byte(etcHosts), 0644)
		if err != nil {
			return err
		}

		ociOpts := []oci.SpecOpts{
			oci.WithMounts([]specs.Mount{
				{
					Destination: "/etc/resolv.conf",
					Type:        "bind",
					Source:      etcResolvconfFile,
					Options:     []string{"rbind", "ro"},
				},
				{
					Destination: "/etc/hosts",
					Type:        "bind",
					Source:      etcHostsFile,
					Options:     []string{"rbind", "ro"},
				},
			}),
		}

		networkConf, err := libcni.ConfListFromBytes([]byte(cniNetworkList))
		if err != nil {
			return err
		}

		networkCacheDir := filepath.Join(cmd.WorkDir.Path(), "network")
		network := gcontainerd.NewCNINetwork(networkConf, cmd.Garden.CNIPluginDir.Path(), networkCacheDir)
		backend, err := gcontainerd.NewBackend(client, "concourse", ociOpts, network)
		if err != nil {
			return err
		}

		return cmd.backendRunner(logger, backend).Run(signals, ready)
	})
}

var ErrNotRoot = errors.New("worker must be run as root")

func (cmd *WorkerCommand) checkRoot() error {
	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	if currentUser.Uid != "0" {
		return ErrNotRoot
	}

	return nil
}

func (cmd *WorkerCommand) dnsProxyRunner(logger lager.Logger) (ifrit.Runner, error) {
	server, err := cmd.Garden.DNS.Server()
	if err != nil {
		return nil, err
	}

	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		server.NotifyStartedFunc = func() {
			close(ready)
			logger.Info("started")
		}

		serveErr := make(chan error, 1)

		go func() {
			serveErr <- server.ListenAndServe()
		}()

		for {
			select {
			case err := <-serveErr:
				return err
			case <-signals:
				server.Shutdown()
			}
		}
	}), nil
}

func (cmd *WorkerCommand) loadResources(logger lager.Logger) ([]atc.WorkerResourceType, error) {
	var types []atc.WorkerResourceType

	if cmd.ResourceTypes != "" {
		basePath := cmd.ResourceTypes.Path()

		entries, err := ioutil.ReadDir(basePath)
		if err != nil {
			logger.Error("failed-to-read-resources-dir", err)
			return nil, err
		}

		for _, e := range entries {
			meta, err := ioutil.ReadFile(filepath.Join(basePath, e.Name(), "resource_metadata.json"))
			if err != nil {
				logger.Error("failed-to-read-resource-type-metadata", err)
				return nil, err
			}

			var t atc.WorkerResourceType
			err = json.Unmarshal(meta, &t)
			if err != nil {
				logger.Error("failed-to-unmarshal-resource-type-metadata", err)
				return nil, err
			}

			t.Image = filepath.Join(basePath, e.Name(), "image.tar")

			types = append(types, t)
		}
	}

	return types, nil
}

const cniNetworkList = `{
  "cniVersion": "0.4.0",
  "name": "bridge-firewalld",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "concourse0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.80.0.0/16",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    },
    {
      "type": "firewall"
    }
  ]
}
`

const etcResolvconf = "nameserver 8.8.8.8\n"
const etcHosts = "127.0.0.1 localhost\n::1 localhost\n"

var gardenEnvPrefix = "CONCOURSE_GARDEN_"

func detectGardenFlags(logger lager.Logger) []string {
	env := os.Environ()

	flags := []string{}
	for _, e := range env {
		spl := strings.SplitN(e, "=", 2)
		if len(spl) != 2 {
			logger.Info("bogus-env", lager.Data{"env": spl})
			continue
		}

		name := spl[0]
		val := spl[1]

		if !strings.HasPrefix(name, gardenEnvPrefix) {
			continue
		}

		strip := strings.Replace(name, gardenEnvPrefix, "", 1)
		flag := flagify(strip)

		logger.Info("forwarding-garden-env-var", lager.Data{
			"env":  name,
			"flag": flag,
		})

		vals := strings.Split(val, ",")

		for _, v := range vals {
			flags = append(flags, "--"+flag, v)
		}

		// clear out env (as twentythousandtonnesofcrudeoil does)
		_ = os.Unsetenv(name)
	}

	return flags
}

func flagify(env string) string {
	return strings.Replace(strings.ToLower(env), "_", "-", -1)
}
