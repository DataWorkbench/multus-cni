package k8s

import (
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"k8s.io/api/core/v1"
)

// GetCurrentNodePods return the list of pods running on the local nodes
func (k *Helper) GetCurrentNodePods() ([]*rpc.PodInfo, error) {
	var localPods []*rpc.PodInfo

	pods, err := k.Client.ListPods("")
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName != k.NodeName {
			continue
		}

		_pod := pod
		localPods = append(localPods, getPodInfo(&_pod))
	}

	return localPods, nil
}

func getPodInfo(pod *v1.Pod) *rpc.PodInfo {
	tmp := &rpc.PodInfo{
		Name:      pod.GetName(),
		Namespace: pod.GetNamespace(),
	}
	annotations := pod.GetAnnotations()
	if annotations != nil {
		tmp.VxNet = annotations[constants.AnnoHostNicVxnet]
		tmp.HostNic = annotations[constants.AnnoHostNic]
		tmp.PodIP = annotations[constants.AnnoHostNicIP]
		tmp.NicType = annotations[constants.AnnoHostNicType]
	}

	return tmp
}

func (k *Helper) GetPodInfo(namespace, name string) (*rpc.PodInfo, error) {
	pod, err := k.Client.GetPod(namespace, name)
	if err != nil {
		return nil, err
	}

	return getPodInfo(pod), nil
}
