package multus

import (
	"context"
	"fmt"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	cnitypes "github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
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
			NAD:     delegate.Conf.Name,
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

func DelNetworkInterface(k8sArgs *types.K8sArgs, delegate *types.DelegateNetConf) error {
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
			NAD: delegate.Conf.Name,
		})
	if err != nil {
		return err
	}
	logging.Debugf("DelNetworkInterface finish args: %v", k8sArgs)
	return nil
}

func ConfigureK8sRoute(n *types.NetConf, args *skel.CmdArgs, ifName string, res *cnitypes.Result) (*current.Result, error) {
	logging.Debugf("ConfigureK8sRoute begin interface name: %s args: %v", ifName, args)
	result, err := current.NewResultFromResult(*res)
	if err != nil {
		return nil, logging.Errorf("ConfigureK8sRoute: Error creating new from current CNI result: %v", err)
	}
	var newResultDefaultRoutes []*cnitypes.Route
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return result, err
	}
	// get node master if
	nodeIf, err := net.InterfaceByName("eth0")
	if err != nil {
		logging.Errorf("interface %q not found: %v", "eth0", err)
		return result, err
	}
	nodeAddrs, err := nodeIf.Addrs()
	if err != nil {
		logging.Errorf("address %q not found: %v", "eth0", err)
		return result, err
	}
	nodeAddr, _, _ := net.ParseCIDR(nodeAddrs[0].String())
	var link netlink.Link
	defer netns.Close()
	// Do this within the net namespace.
	err = netns.Do(func(_ ns.NetNS) error {
		link, err = netlink.LinkByName(ifName)
		if err != nil {
			return logging.Errorf("link %q not found: %v", ifName, err)
		}
		// add route
		configureIPNets := []string{n.PodCIDR, n.ServiceCIDR, string(nodeAddr) + "/32"}
		for _, IPNet := range configureIPNets {
			_, ipNet, _ := net.ParseCIDR(IPNet)
			dst := &net.IPNet{
				IP:   ipNet.IP,
				Mask: ipNet.Mask,
			}
			gw := net.ParseIP("169.254.1.1")
			route := &netlink.Route{
				LinkIndex: link.Attrs().Index,
				Dst:       dst,
				Gw:        gw,
			}
			if err := netlink.RouteAdd(route); err != nil && !os.IsExist(err) {
				return logging.Errorf("failed to add route %v: %v", route, err)
			}
			newResultDefaultRoutes = append(newResultDefaultRoutes, &cnitypes.Route{Dst: *dst, GW: gw})
		}

		return nil
	})
	if err != nil {
		return result, err
	}
	result.Routes = newResultDefaultRoutes
	// add ip neigh
	k8sLink, err := netlink.LinkByName(result.Interfaces[0].Name)
	if err != nil {
		return result, err
	}
	neigh := netlink.Neigh{
		IP:           result.IPs[0].Address.IP,
		HardwareAddr: link.Attrs().HardwareAddr,
		LinkIndex:    k8sLink.Attrs().Index,
		State:        netlink.NUD_PERMANENT,
	}
	logging.Debugf("add ip neigh %v+, %v+", neigh, link)
	if err := netlink.NeighAdd(&neigh); err != nil {
		logging.Errorf("failed to add ip neigh %v: %v", neigh, err)
		return result, err
	}
	logging.Debugf("ConfigureK8sRoute finish interface name: %s args: %v", ifName, args)
	return result, nil
}
