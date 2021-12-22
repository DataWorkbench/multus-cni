package multus

import (
	"context"
	"fmt"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	"net"
	"os"
	"time"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/netutils"
	"github.com/DataWorkbench/multus-cni/pkg/types"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
)

func AddNetworkInterface(k8sArgs *types.K8sArgs, delegate *types.DelegateNetConf) error {
	// Set up a connection to the NICM server.
	logging.Debugf("AddNetworkInterface begin delegate: %v args: %v", delegate, k8sArgs)
	conn, err := grpc.Dial(constants.DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		return logging.Errorf("failed to connect server, err=%v", err)
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
			IPStart: delegate.Conf.IPAM.RangeStart,
			IPEnd:   delegate.Conf.IPAM.RangeEnd,
		})
	if err != nil {
		return err
	}

	//wait for nic attach
	var link netlink.Link
	try := 300
	for i := 0; i < try; i++ {
		link, err = netutils.LinkByMacAddr(r.Nic.HardwareAddr)
		if err != nil && err != constants.ErrNicNotFound {
			return err
		}
		if link != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	if link == nil {
		return constants.ErrNicNotFound
	}
	if link.Attrs().Name != delegate.Conf.Master {
		err = netlink.LinkSetName(link, delegate.Conf.Master)
		if err != nil {
			return logging.Errorf("failed to set link %s name to %s: %v", link.Attrs().Name, delegate.Conf.Master, err)
		}
		err = netlink.LinkSetUp(link)
		if err != nil {
			return logging.Errorf("failed to set link %s up: %v", link.Attrs().Name, err)
		}
	}
	logging.Debugf("AddNetworkInterface finish delegate: %v args: %v", delegate, k8sArgs)
	return nil
}

func DelNetworkInterface(k8sArgs *types.K8sArgs) error {
	// Set up a connection to the NICM server.
	logging.Debugf("DelNetworkInterface begin args: %v", k8sArgs)
	conn, err := grpc.Dial(constants.DefaultUnixSocketPath, grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("failed to connect server, err=%v", err)
	}
	defer conn.Close()

	c := rpc.NewCNIBackendClient(conn)
	_, err = c.DelNetwork(context.Background(),
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
	logging.Debugf("DelNetworkInterface finish args: %v", k8sArgs)
	return nil
}

func ConfigureK8sRoute(args *skel.CmdArgs, ifName string) error {
	logging.Debugf("ConfigureK8sRoute begin interface name: %s args: %v", ifName, args)
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netns.Close()

	// Do this within the net namespace.
	err = netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return logging.Errorf("link %q not found: %v", ifName, err)
		}
		// add route
		configureIPNets := []string{"10.10.0.0/16", "10.96.0.0/16"}
		for _, IPNet := range configureIPNets {
			_, ipNet, _ := net.ParseCIDR(IPNet)
			route := &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst: &net.IPNet{
					IP:   ipNet.IP,
					Mask: ipNet.Mask,
				},
				Gw: net.ParseIP("169.254.1.1"),
			}
			if err := netlink.RouteReplace(route); err != nil && !os.IsExist(err) {
				return logging.Errorf("failed to add route %v: %v", route, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	logging.Debugf("ConfigureK8sRoute finish interface name: %s args: %v", ifName, args)
	return nil
}
