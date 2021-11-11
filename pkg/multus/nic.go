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
		})
	if err != nil {
		return err
	}

	//wait for nic attach
	var link netlink.Link
	try := 30
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
	if link == nil{
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
	return nil
}

func DelNetworkInterface(k8sArgs *types.K8sArgs) error {
	// Set up a connection to the NICM server.
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
	return nil
}

func PrintRouteList(args *skel.CmdArgs, ifName string) error {
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netns.Close()

	// Do this within the net namespace.
	err = netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil {
			return err
		}
		routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
		if err != nil {
			return err
		}
		for _, route := range routes {
			logging.Debugf("ifname :%s route rule: %v", ifName, route)
		}
		return nil
	})
	return nil
}

func ConfigureK8sRoute(args *skel.CmdArgs, ifName string) error {
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return err
	}
	defer netns.Close()

	// Do this within the net namespace.
	err = netns.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(ifName)
		if err != nil{
			return err
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("failed to set %q UP: %v", link.Attrs().Name, err)
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
				return fmt.Errorf("failed to add route %v: %v", route, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
