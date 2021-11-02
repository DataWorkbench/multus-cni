package multus

import (
	"context"
	"fmt"
	"time"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/netutils"
	"github.com/DataWorkbench/multus-cni/pkg/types"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
)

func AddNetworkInterface(k8sArgs *types.K8sArgs, delegate *types.DelegateNetConf) error {
	// Set up a connection to the NICM server.
	conn, err := grpc.Dial(constants.DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("failed to connect server, err=%v", err)
	}
	defer conn.Close()

	c := rpc.NewCNIBackendClient(conn)
	r, err := c.AddNetwork(context.Background(),
		&rpc.NICMMessage{
			Args: &rpc.PodInfo{
				Name:       string(k8sArgs.K8S_POD_NAME),
				Namespace:  string(k8sArgs.K8S_POD_NAMESPACE),
				Containter: string(k8sArgs.K8S_POD_INFRA_CONTAINER_ID),
			},
		})
	if err != nil {
		return err
	}

	//wait for nic attach
	var link netlink.Link
	for {
		link, err = netutils.LinkByMacAddr(r.Args.HostNic)
		if err != nil {
			return err
		}
		if link != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if link.Attrs().Name != delegate.Conf.Master {
		err = netlink.LinkSetName(link, delegate.Conf.Master)
		if err != nil {
			return fmt.Errorf("failed to set link %s name to %s: %v", r.Args.HostNic, delegate.Conf.Master, err)
		}
	}
	return nil
}

func DelNetworkInterface(k8sArgs *types.K8sArgs) error {
	return nil
}
