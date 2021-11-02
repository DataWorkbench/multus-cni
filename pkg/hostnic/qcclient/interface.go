package qcclient

import "github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	//node info
	GetInstanceID() string

	//bootstrap
	GetCreatedNics(num, offsite int) ([]*rpc.NicInfo, error)

	//vxnet info
	GetVxNets([]string) (map[string]*rpc.VxNetInfo, error)

	//job info
	DescribeNicJobs(ids []string) ([]string, map[string]bool, error)

	//nic operations
	CreateNicsAndAttach(vxnet *rpc.VxNetInfo, num int, ips []string) ([]*rpc.NicInfo, string, error)
	GetNics(nics []string) (map[string]*rpc.NicInfo, error)
	DeleteNics(nicIDs []string) error
	DeattachNics(nicIDs []string, sync bool) (string, error)
	AttachNics(nicIDs []string) (string, error)
	GetAttachedNics() ([]*rpc.NicInfo, error)
}

var (
	QClient QingCloudAPI
)
