package qcclient

import (
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/yunify/qingcloud-sdk-go/service"
)

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	//node info
	GetInstanceID() string

	//bootstrap
	GetCreatedNics(num, offsite int, vxNet, instanceID string) ([]*rpc.HostNic, error)

	//vxnet info
	GetVxNets([]string) (map[string]*rpc.VxNet, error)

	//job info
	DescribeNicJobs(ids []string) ([]string, map[string]bool, error)

	//nic operations
	CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string) ([]*rpc.HostNic, string, error)
	GetNics(nics []string) (map[string]*rpc.HostNic, error)
	DeleteNics(nicIDs []string) error
	DeattachNics(nicIDs []string, sync bool) (string, error)
	AttachNics(nicIDs []string) (string, error)
	GetAttachedNics() ([]*rpc.HostNic, error)

	// VIP
	CreateVIPs(vxNetID, IPStart, IPEnd string) (string, []string, error)
	DeleteVIPs(vips []string) (string, error)
	DescribeVIPJobs(ids []string) (error, []string, []string)

	DescribeVIPs(vxNetID string, VIPs []string) (*service.DescribeVxNetsVIPsOutput, error)
}

var (
	QClient QingCloudAPI
)
