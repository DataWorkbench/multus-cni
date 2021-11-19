package allocator

import (
	"context"
	"encoding/json"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/utils"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// Data Field Value in ConfigMap
type VIPAllocMap struct {
	LockToDelete   bool
	IPStart        string
	IPEnd          string
	VIPInfo        map[string]*VIPInfo
	LastUpdateTime int64
}

type VIPInfo struct {
	ID         string
	RefPodName string
}

func (v *VIPAllocMap) CreateCMData() string {
	newCM := &VIPAllocMap{
		LockToDelete:   false,
		IPStart:        v.IPStart,
		IPEnd:          v.IPEnd,
		VIPInfo:        make(map[string]*VIPInfo),
		LastUpdateTime: time.Now().Unix(),
	}
	newCMJson, _ := json.Marshal(newCM)
	return string(newCMJson)
}

func (v *VIPAllocMap) CheckVIPAvailable() bool {
	count := utils.IPRangeCount(v.IPStart, v.IPEnd)
	if len(v.VIPInfo) < count {
		return true
	}

	for _, _info := range v.VIPInfo {
		if _info.RefPodName == "" {
			return true
		}
	}

	return false
}

func (v *VIPAllocMap) InitRefPodVIPInfo(VIPInfoMap map[string]*VIPInfo) {

}

func (v *VIPAllocMap) AddRefPodVIPInfo(IPAddr, podName string) {
	for addr, _info := range v.VIPInfo {
		if addr == IPAddr {
			if _info.RefPodName != "" {
				_ = logging.Errorf("VIP [%s] had been attached to Pod [%s]", addr, _info.RefPodName)
			}
			_info.RefPodName = podName
		}
	}

	v.LastUpdateTime = time.Now().Unix()
}

func (v *VIPAllocMap) RemoveRefPodVIPInfo(podName string) {
	for addr, _info := range v.VIPInfo {
		if _info.RefPodName == podName {
			_info.RefPodName = ""
			logging.Verbosef("remove RefPod [%s] from addr [%s]", podName, addr)
		}
	}
	v.LastUpdateTime = time.Now().Unix()
}

func CreateVIPCMName(vxNetID string) string {
	return "VIP-" + vxNetID
}

func CheckVIPAllocExhausted(dataMap map[string]string) (isValid bool) {
	VIPAllocMapJson := dataMap[constants.VIPConfName]
	if VIPAllocMapJson == "" {
		isValid = true
		return
	}

	vipAllocMap := &VIPAllocMap{}
	err := json.Unmarshal([]byte(VIPAllocMapJson), vipAllocMap)
	if err != nil {
		_ = logging.Errorf("Parse vipAllocMap %v failed, err %v", VIPAllocMapJson, err)
		return
	}

	isValid = vipAllocMap.CheckVIPAvailable()
	return
}

func GetVIPConfForVxNet(vxNetID, namespace, IPStart, IPEnd string) (map[string]string, bool, error) {
	var needToCreateVIP bool
	VIPCMName := CreateVIPCMName(vxNetID)
	configMap, err := getConfigMap(VIPCMName, namespace)
	if err == nil {
		return configMap.Data, needToCreateVIP, nil
	}

	if k8serrors.IsNotFound(err) {
		for i := 0; i < constants.MaxRetry; i++ {
			err = createConfigMap(VIPCMName, namespace, IPStart, IPEnd)
			if err == nil {
				needToCreateVIP = true
				break
			}
		}
	}

	configMap, err = getConfigMap(VIPCMName, namespace)
	if err != nil {
		_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] after creating",
			VIPCMName, namespace)
		return nil, needToCreateVIP, err
	}

	return configMap.Data, needToCreateVIP, nil
}

func InitVIP(vxNetID, namespace string, VIPs []string) (err error) {
	VIPInfoMap, err := qcclient.QClient.DescribeVIPs(vxNetID, VIPs)
	if err != nil {
		_ = logging.Errorf("Query DescribeVIPs [%v] failed, err: %v", VIPs, err)
		return err
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		VIPCMName := CreateVIPCMName(vxNetID)
		configMap, err := getConfigMap(VIPCMName, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				VIPCMName, namespace)
			return err
		}

		dataMapJson := configMap.Data[constants.VIPConfName]
		if dataMapJson != "" {
			return logging.Errorf("ConfigMap Data [%s] had been initialized", dataMapJson)
		}
		dataMap := &VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			_ = logging.Errorf("failed to parse Data Json [%s], err: %v", dataMapJson, err)
			return err
		}

		dataMap.InitRefPodVIPInfo(VIPInfoMap)
		newDataMapJson, err := json.Marshal(dataMap)
		if err != nil {
			_ = logging.Errorf("failed to Get Data Json after removing PodName, err: %v", err)
			return err
		}
		configMap.Data[constants.VIPConfName] = string(newDataMapJson)
		return k8s.K8sHelper.Client.Update(context.Background(), configMap)
	})
}

// Add PodRef
func AddPodVIP(vxNetID, namespace, podName, IPAddr string) (err error) {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		VIPCMName := CreateVIPCMName(vxNetID)
		configMap, err := getConfigMap(VIPCMName, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				VIPCMName, namespace)
			return err
		}

		dataMapJson := configMap.Data[constants.VIPConfName]
		dataMap := &VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			_ = logging.Errorf("failed to parse Data Json [%s], err: %v", dataMapJson, err)
			return err
		}
		dataMap.AddRefPodVIPInfo(IPAddr, podName)
		newDataMapJson, err := json.Marshal(dataMap)
		if err != nil {
			_ = logging.Errorf("failed to Get Data Json after removing PodName, err: %v", err)
			return err
		}
		configMap.Data[constants.VIPConfName] = string(newDataMapJson)
		return k8s.K8sHelper.Client.Update(context.Background(), configMap)
	})
}

// Delete Pod Ref VIP info
func DeletePodVIP(vxNetID, namespace, podName string) (err error) {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		VIPCMName := CreateVIPCMName(vxNetID)
		configMap, err := getConfigMap(VIPCMName, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				VIPCMName, namespace)
			return err
		}

		dataMapJson := configMap.Data[constants.VIPConfName]
		dataMap := &VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			_ = logging.Errorf("failed to parse Data Json [%s], err: %v", dataMapJson, err)
			return err
		}

		dataMap.RemoveRefPodVIPInfo(podName)
		newDataMapJson, err := json.Marshal(dataMap)
		if err != nil {
			_ = logging.Errorf("failed to Get Data Json after removing PodName, err: %v", err)
			return err
		}
		configMap.Data[constants.VIPConfName] = string(newDataMapJson)
		return k8s.K8sHelper.Client.Update(context.Background(), configMap)
	})
}

func getConfigMap(name, namespace string) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}

	err := k8s.K8sHelper.Client.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, configMap)

	if err != nil {
		return nil, err
	}
	return configMap, nil
}

func createConfigMap(name, namespace, IPStart, IPEnd string) error {
	vipAllocMap := VIPAllocMap{
		IPStart: IPStart,
		IPEnd:   IPEnd,
	}
	dataMap := make(map[string]string)
	dataMap[constants.VIPConfName] = vipAllocMap.CreateCMData()
	newConfigMap := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: dataMap,
	}
	err := k8s.K8sHelper.Client.Create(context.Background(), newConfigMap)
	if err != nil {
		if k8serrors.IsAlreadyExists(err) || k8serrors.IsConflict(err) {
			logging.Verbosef("ConfigMap for Name [%s] Namespace [%s] IPStart [%s] IPEnd [%s] exist",
				name, namespace, IPStart, IPEnd)
			return nil
		}
		return err
	}

	return nil
}
