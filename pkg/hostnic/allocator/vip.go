package allocator

import (
	"context"
	"encoding/json"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	"github.com/matoous/go-nanoid/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		VIPDetailInfo:  make(map[string]*VIPInfo),
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
			_ = logging.Errorf("VIP [%s] had been attached to Pod [%s]", addr, _info.RefPodName)
			_info.RefPodName = podName
			allocIP = addr
			v.TagToDelete = false
			v.LastUpdateTime = time.Now().Unix()
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

func CreateVIPCMName(vxNetID string) string {
	return "vip-" + strings.ToLower(vxNetID)
}

func PrintErrReason(err error, desc string) {
	logging.Verbosef("%s, Error Reason: [%s]", desc, string(k8serrors.ReasonForError(err)))
}

func GetVIPConfForVxNet(vxNetID, namespace, IPStart, IPEnd string) (map[string]string, bool, error) {
	var needToCreateVIP bool
	VIPCMName := CreateVIPCMName(vxNetID)
	configMap, err := getConfigMap(VIPCMName, namespace)
	if err == nil {
		return configMap.Data, needToCreateVIP, nil
	}

	PrintErrReason(err, "Try to get ConfigMap failed")
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
	logging.Verbosef("Set out to init VIPs, vxNetId [%s], namespace [%s], VIPs %v", vxNetID, namespace, VIPs)
	output, err := qcclient.QClient.DescribeVIPs(vxNetID, VIPs)
	if err != nil {
		_ = logging.Errorf("Query DescribeVIPs [%v] failed, err: %v", VIPs, err)
		return err
	}

	VIPInfoMap := make(map[string]*VIPInfo)
	for _, vip := range output.VIPSet {
		vipItem := &VIPInfo{
			ID: *vip.VIPID,
		}
		VIPInfoMap[*vip.VIPAddr] = vipItem
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		VIPCMName := CreateVIPCMName(vxNetID)
		configMap, err := getConfigMap(VIPCMName, namespace)
		if err != nil {
			_ = logging.Errorf("failed to get ConfigMap for Name [%s] Namespace [%s] for initializing",
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

		if dataMap.IsInitialized() {
			return logging.Errorf("ConfigMap Data [%s] had been initialized", dataMapJson)
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

func TryFreeVIP(vxNetID, namespace string) error {
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
			return err
		}
		if !dataMap.IsInitialized() {
			return logging.Errorf("ConfigMap not initialized")
		}

		podCount := dataMap.GetPodCount()
		if podCount > 0 {
			logging.Verbosef("There are [%d] pod attached", podCount)
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
		err = k8s.K8sHelper.Client.Update(context.Background(), configMap)
		if err == nil {
			logging.Verbosef("set out to delete ConfigMap [%s], Namespace [%s]", VIPCMName, namespace)
			go ClearVIPConf(vxNetID, VIPCMName, namespace, nodeUUID, ids)
		}
		return err
	})
}

func ClearVIPConf(vxNetID, cmName, namespace, nodeUUID string, VIPs []string) {
	delDelay := time.NewTimer(time.Duration(30) * time.Second)
	<-delDelay.C

	VIPCMName := CreateVIPCMName(vxNetID)
	configMap, err := getConfigMap(VIPCMName, namespace)
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

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		toDeleteCM := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ConfigMap",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
			},
		}
		return k8s.K8sHelper.Client.Delete(context.Background(), toDeleteCM)
	})
	if err != nil {
		_ = logging.Errorf("delete ConfigMap [%s], Namespace [%s] failed", cmName, namespace)
	}

	if len(VIPs) <= 0 {
		logging.Verbosef("VIPs is empty in ConfigMap!")
		return
	}
	for i := 0; i < constants.MaxRetry; i++ {
		_, err = qcclient.QClient.DeleteVIPs(VIPs)
		if err == nil {
			return
		}

		_ = logging.Errorf("[%d] Delete VIPs %s failed, err: %s", i, VIPs, err)
	}
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
