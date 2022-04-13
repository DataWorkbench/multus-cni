package vip

import (
	"encoding/json"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/utils"
	"github.com/DataWorkbench/multus-cni/pkg/k8sclient"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	"github.com/matoous/go-nanoid/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"strings"
	"time"
)

// Data Field Value in ConfigMap
type VIPAllocMap struct {
	TagToDelete    bool
	IPStart        string
	IPEnd          string
	VIPDetailInfo  map[string]*VIPInfo
	LastUpdateTime int64
	NodeUUID       string
}

type VIPInfo struct {
	ID         string
	RefPodName string
}

func (v *VIPAllocMap) CreateCMData() string {
	newCM := &VIPAllocMap{
		TagToDelete:    false,
		IPStart:        v.IPStart,
		IPEnd:          v.IPEnd,
		VIPDetailInfo:  v.VIPDetailInfo,
		LastUpdateTime: time.Now().Unix(),
	}
	newCMJson, _ := json.Marshal(newCM)
	return string(newCMJson)
}

func (v *VIPAllocMap) CheckFreeVIP() error {
	for _, _info := range v.VIPDetailInfo {
		if _info.RefPodName == "" {
			return nil
		}
	}

	return logging.Errorf("no free VIP found")
}

func (v *VIPAllocMap) GetPodCount() int {
	count := 0
	for _, _info := range v.VIPDetailInfo {
		if _info.RefPodName != "" {
			count++
		}
	}
	return count
}

func (v *VIPAllocMap) PrepareToDelete(nodeUUID string) []string {
	v.TagToDelete = true
	v.NodeUUID = nodeUUID
	VIPs := []string{}
	for _, _info := range v.VIPDetailInfo {
		VIPs = append(VIPs, _info.ID)
	}
	return VIPs
}

func (v *VIPAllocMap) InitRefPodVIPInfo(VIPInfoMap map[string]*VIPInfo) {
	v.VIPDetailInfo = VIPInfoMap
}

func (v *VIPAllocMap) IsInitialized() bool {
	return len(v.VIPDetailInfo) > 0
}

func (v *VIPAllocMap) ReadyToDelete(nodeUUID string) bool {
	return v.NodeUUID == nodeUUID && v.TagToDelete
}

func (v *VIPAllocMap) AddRefPodVIPInfo(podName string) (allocIP string, err error) {
	for addr, _info := range v.VIPDetailInfo {
		if _info.RefPodName == "" {
			_info.RefPodName = podName
			allocIP = addr
			v.TagToDelete = false
			v.LastUpdateTime = time.Now().Unix()
			logging.Debugf("VIP [%s] is allocated to Pod [%s]", addr, podName)
			return
		}
	}

	err = logging.Errorf("No available IP!")
	return
}

func (v *VIPAllocMap) RemoveRefPodVIPInfo(podName string) {
	for addr, _info := range v.VIPDetailInfo {
		if _info.RefPodName == podName {
			_info.RefPodName = ""
			logging.Verbosef("remove RefPod [%s] from addr [%s]", podName, addr)
		}
	}
	v.LastUpdateTime = time.Now().Unix()
}

func CreateVIPCMName(NADName string) string {
	return "vip-" + strings.ToLower(NADName)
}

func PrintErrReason(err error, desc string) {
	logging.Verbosef("%s, Error Reason: [%s]", desc, string(k8serrors.ReasonForError(err)))
}

func GetVIPConfForVxNet(NADName, namespace, IPStart, IPEnd, vxNet string) (map[string]string, error) {
	VIPCMName := CreateVIPCMName(NADName)
	configMap, err := k8s.K8sHelper.Client.GetConfigMap(VIPCMName, namespace)
	if err == nil {
		return configMap.Data, nil
	}

	PrintErrReason(err, "Try to get ConfigMap failed")
	if k8serrors.IsNotFound(err) {
		for i := 0; i < constants.MaxRetry; i++ {
			err = createConfigMap(VIPCMName, namespace, IPStart, IPEnd, vxNet)
			if err == nil {
				break
			}
		}
	}

	configMap, err = k8s.K8sHelper.Client.GetConfigMap(VIPCMName, namespace)
	if err != nil {
		_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] after creating",
			VIPCMName, namespace)
		return nil, err
	}

	return configMap.Data, nil
}

func TryFreeVIP(NADName, namespace string, stopCh <-chan struct{}) error {
	return retry.RetryOnConflict(utils.RetryConf, func() error {
		VIPCMName := CreateVIPCMName(NADName)
		configMap, err := k8s.K8sHelper.Client.GetConfigMap(VIPCMName, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				VIPCMName, namespace)
			return err
		}

		dataMapJson := configMap.Data[constants.VIPConfName]
		dataMap := &VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			return err
		}
		if !dataMap.IsInitialized() {
			return logging.Errorf("ConfigMap not initialized")
		}

		podCount := dataMap.GetPodCount()
		logging.Verbosef("There are [%d] pods attached", podCount)
		logging.Verbosef("DataMapJson: [%s]", dataMapJson)
		if podCount > 0 {
			logging.Verbosef("[Refuse to release VIP] podCount [%d] ConfigMap VIPDetail [%s]", podCount, dataMapJson)
			return nil
		}

		if dataMap.TagToDelete {
			logging.Verbosef("ConfigMap had been tagged to delete")
			return nil
		}

		nodeUUID, _ := gonanoid.New()
		ids := dataMap.PrepareToDelete(nodeUUID)
		newDataMapJson, err := json.Marshal(dataMap)
		if err != nil {
			_ = logging.Errorf("failed to Get Data Json after setting delete Tag")
			return err
		}
		configMap.Data[constants.VIPConfName] = string(newDataMapJson)
		_, err = k8s.K8sHelper.Client.UpdateConfigMap(namespace, configMap)
		if err == nil {
			logging.Debugf("set out to delete ConfigMap [%s], Namespace [%s]", VIPCMName, namespace)
			go ClearVIPConf(NADName, VIPCMName, namespace, nodeUUID, ids, stopCh)
		}
		return err
	})
}

func ClearVIPConf(NADName, cmName, namespace, nodeUUID string, VIPs []string, stopCh <-chan struct{}) {
	delDelay := time.NewTimer(time.Duration(30) * time.Second)
	select {
	case <-delDelay.C:
	case <-stopCh:
		logging.Verbosef("receive stop signal, try to clear VIPs directly")
	}
	VIPCMName := CreateVIPCMName(NADName)
	configMap, err := k8s.K8sHelper.Client.GetConfigMap(VIPCMName, namespace)
	if err != nil {
		_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
			VIPCMName, namespace)
		return
	}

	dataMapJson := configMap.Data[constants.VIPConfName]
	dataMap := &VIPAllocMap{}
	err = json.Unmarshal([]byte(dataMapJson), dataMap)
	if err != nil {
		return
	}

	if !dataMap.ReadyToDelete(nodeUUID) {
		logging.Verbosef("ConfigMap [%s] cannot be deleted in current goroutine..", VIPCMName)
		return
	}

	err = retry.RetryOnConflict(utils.RetryConf, func() error {
		return k8s.K8sHelper.Client.DeleteConfigMap(namespace, cmName)
	})
	if err != nil {
		_ = logging.Errorf("delete ConfigMap [%s], Namespace [%s] failed, err: %v", cmName, namespace, err)
	}
}

func createConfigMap(name, namespace, IPStart, IPEnd, vxNetID string) error {
	ipAddrs := utils.GetIPsFromRange(IPStart, IPEnd)
	output, err := qcclient.QClient.DescribeVIPs(vxNetID, []string{}, ipAddrs)
	if err != nil {
		_ = logging.Errorf("Query DescribeVIPs [%v] failed, err: %v", ipAddrs, err)
		return err
	}

	if len(output.VIPSet) == 0 {
		return logging.Errorf("VIPs with Addresses [%v] not found", ipAddrs)
	}

	detailMap := make(map[string]*VIPInfo)
	for _, vip := range output.VIPSet {
		vipItem := &VIPInfo{
			ID: *vip.VIPID,
		}
		detailMap[*vip.VIPAddr] = vipItem
	}

	vipAllocMap := &VIPAllocMap{
		IPStart:       IPStart,
		IPEnd:         IPEnd,
		VIPDetailInfo: detailMap,
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
	_, err = k8s.K8sHelper.Client.CreateConfigMap(namespace, newConfigMap)
	if err != nil {
		PrintErrReason(err, "Create ConfigMap failed")
		if k8serrors.IsAlreadyExists(err) || k8serrors.IsConflict(err) {
			logging.Verbosef("ConfigMap for Name [%s] Namespace [%s] IPStart [%s] IPEnd [%s] exist",
				name, namespace, IPStart, IPEnd)
			return nil
		}
		return err
	}

	return nil
}

func AllocatePodIP(client *k8sclient.ClientInfo, name, namespace, podName string) (string, error) {
	var allocIP string
	err := retry.RetryOnConflict(utils.RetryConf, func() error {
		configMap, err := client.GetConfigMap(name, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				name, namespace)
			return err
		}

		dataMapJson := configMap.Data[constants.VIPConfName]
		if dataMapJson == "" {
			err = logging.Errorf("configMap not initialized")
			return err
		}
		dataMap := &VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			_ = logging.Errorf("failed to parse Data Json [%s], err: %v", dataMapJson, err)
			return err
		}
		allocIP, err = dataMap.AddRefPodVIPInfo(podName)
		if err != nil {
			return err
		}
		newDataMapJson, err := json.Marshal(dataMap)
		if err != nil {
			_ = logging.Errorf("failed to Get Data Json after adding PodName, err: %v", err)
			return err
		}
		configMap.Data[constants.VIPConfName] = string(newDataMapJson)
		configMap, err = client.UpdateConfigMap(namespace, configMap)
		return err
	})
	return allocIP, err
}

// Detach Pod Ref VIP info
func ReleasePodIP(client *k8sclient.ClientInfo, name, namespace, podName string) error {
	return retry.RetryOnConflict(utils.RetryConf, func() error {
		configMap, err := client.GetConfigMap(name, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for deleting",
				name, namespace)
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
		configMap, err = client.UpdateConfigMap(namespace, configMap)
		return err
	})
}
