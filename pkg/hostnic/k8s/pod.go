package k8s

import (
	"context"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetCurrentNodePods return the list of pods running on the local nodes
func (k *Helper) GetCurrentNodePods() ([]*rpc.PodInfo, error) {
	var localPods []*rpc.PodInfo

	pods := &corev1.PodList{}
	err := k.Client.List(context.Background(), pods, &client.ListOptions{
		//FieldSelector: fields.OneTermEqualSelector("spec.NodeName", k.NodeName),
	})
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

func getPodInfo(pod *corev1.Pod) *rpc.PodInfo {
	tmp := &rpc.PodInfo{
		Name:      pod.GetName(),
		Namespace: pod.GetNamespace(),
	}
	annotations := pod.GetAnnotations()
	if annotations != nil {
		tmp.VxNet = annotations[AnnoHostNicVxnet]
		tmp.HostNic = annotations[AnnoHostNic]
		tmp.PodIP = annotations[AnnoHostNicIP]
		tmp.NicType = annotations[AnnoHostNicType]
	}

	return tmp
}

func (k *Helper) needSetVxnetForNode(vxnets []string) (error, bool) {
	node := &corev1.Node{}
	err := k.Client.Get(context.Background(), client.ObjectKey{
		Name: k.NodeName,
	}, node)
	if err != nil {
		return err, false
	}

	return nil, needSetAnnotation(node.Annotations, vxnets)
}

func needSetAnnotation(annos map[string]string, vxnets []string) bool {
	if annos == nil || annos[AnnoHostNicVxnet] == "" {
		return true
	}

	vxnet := annos[AnnoHostNicVxnet]
	need := true
	for _, tmp := range vxnets {
		if tmp == vxnet {
			need = false
			break
		}
	}

	return need
}

func (k *Helper) getNodeVxnetUsage(vxnets []string) (error, map[string]int, bool) {
	result := make(map[string]int)
	var latest *corev1.Node

	nodes := &corev1.NodeList{}
	err := k.Client.List(context.Background(), nodes)
	if err != nil {
		return err, nil, false
	}
	for _, node := range nodes.Items {
		if needSetAnnotation(node.Annotations, vxnets) {
			if latest == nil {
				latest = &node
			} else {
				if node.CreationTimestamp.Before(&latest.CreationTimestamp) {
					latest = &node
				}
			}
			continue
		}
		result[node.Annotations[AnnoHostNicVxnet]]++
	}

	if latest.Name == k.NodeName {
		return nil, result, false
	} else {
		return nil, result, true
	}
}

func (k *Helper) updateNodeVxnet(vxnet string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		node := &corev1.Node{}
		err := k.Client.Get(context.Background(), client.ObjectKey{Name: k.NodeName}, node)
		if err != nil {
			return err
		}
		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}
		node.Annotations[AnnoHostNicVxnet] = vxnet
		return k.Client.Update(context.Background(), node)
	})
}

func (k *Helper) GetPodInfo(namespace, name string) (*rpc.PodInfo, error) {
	pod := &corev1.Pod{}

	err := k.Client.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, pod)
	if err != nil {
		return nil, err
	}

	return getPodInfo(pod), nil
}
