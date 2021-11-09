package qcclient

import "github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"

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
}

var (
	QClient QingCloudAPI
)
