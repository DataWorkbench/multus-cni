package allocator

import (
	"encoding/json"
	"os"
	"path"
	"sync"
	"time"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/conf"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/db"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
)

type nicStatus struct {
	nic  *rpc.HostNic
	info *rpc.PodInfo
}

type VipJobInfo struct {
	VxNetID   string
	IPStart   string
	IPEnd     string
	Namespace string
	VIPs      []string
}

type Allocator struct {
	lock          sync.RWMutex
	vipLock       sync.Mutex
	jobs          []string
	nics          map[string]*nicStatus
	conf          conf.PoolConf
	validNicCount int32
	deletingNic   map[string]bool
	vipJobs       map[string]*VipJobInfo
	StopCh        <-chan struct{}
}

func (a *Allocator) addNicStatus(nic *rpc.HostNic, info *rpc.PodInfo) error {
	nic.Status = rpc.Status_USING
	status := &nicStatus{
		nic:  nic,
		info: info,
	}

	err := db.SetNetworkInfo(status.nic.ID, &rpc.NICMMessage{
		Args: status.info,
		Nic:  status.nic,
	})
	if err != nil {
		return err
	}
	a.nics[status.nic.ID] = status

	err = db.AddRefPodInfo(nic.ID, info.Name, info.Namespace)
	if err != nil {
		return err
	}
	// touch nic addr file
	if _, err = os.Create(path.Join(constants.DefaultMultusNicDevicesLocation, nic.ID)); err != nil {
		return err
	}
	return nil
}

// update cache, remove nic infos that are not found in db
func (a *Allocator) removeNicStatus(status *nicStatus) error {
	err := db.DeleteNetworkInfo(status.nic.ID)
	if err != nil {
		return err
	}
	delete(a.nics, status.nic.ID)
	// remove nic addr file
	if err = os.Remove(path.Join(constants.DefaultMultusNicDevicesLocation, status.nic.ID)); err != nil {
		return err
	}
	return nil
}

func (a *Allocator) canAlloc() int {
	return a.conf.MaxNic - len(a.nics)
}

func (a *Allocator) getVxnets(vxnet string) (*rpc.VxNet, error) {
	if vxnet == "" {
		return nil, logging.Errorf("vxNet cannot be empty!")
	}

	for _, nic := range a.nics {
		if nic.nic.VxNet.ID == vxnet {
			return nic.nic.VxNet, nil
		}
	}

	result, err := qcclient.QClient.GetVxNets([]string{vxnet})
	if err != nil {
		return nil, err
	}
	logging.Verbosef("get vxnet %s", vxnet)
	return result[vxnet], nil
}

func (a *Allocator) getValidNic(vxNet string) *nicStatus {
	for _, nic := range a.nics {
		if a.IsDeleting(nic.nic.ID) {
			logging.Verbosef("nic [%s] is being deleting status", nic.nic.ID)
			continue
		}

		if nic.nic.VxNet.ID == vxNet {
			return nic
		}
	}
	return nil
}

func (a *Allocator) createAndAttachNewNic(args *rpc.PodInfo) (*rpc.HostNic, error) {
	if a.canAlloc() <= 0 {
		return nil, constants.ErrNoAvailableNIC
	}

	var ips []string
	if args.PodIP != "" {
		ips = append(ips, args.PodIP)
	}
	vxnet, err := a.getVxnets(args.VxNet)
	if err != nil {
		return nil, err
	}
	nics, jobID, err := qcclient.QClient.CreateNicsAndAttach(vxnet, 1, ips)
	if err != nil {
		return nil, err
	}
	logging.Verbosef("create and attach nic %v, job id %s", nics, jobID)
	a.jobs = append(a.jobs, jobID)
	return nics[0], nil
}

func (a *Allocator) CreateVIPs(vxNetID, IPStart, IPEnd, namespace string) error {
	jobID, vipIDs, err := qcclient.QClient.CreateVIPs(vxNetID, IPStart, IPEnd)
	if err != nil {
		_ = logging.Errorf("create VIPs failed, err: %v", err)
		_ = logging.Errorf("ignore Error For Test")
		return nil
	}

	a.vipLock.Lock()
	defer a.vipLock.Unlock()
	newVipJob := &VipJobInfo{
		VxNetID:   vxNetID,
		IPStart:   IPStart,
		IPEnd:     IPEnd,
		VIPs:      vipIDs,
		Namespace: namespace,
	}
	a.vipJobs[jobID] = newVipJob
	return nil
}

func (a *Allocator) TryToFreeVxNetVIPs(vxNetID, namespace string) {
	err := TryFreeVIP(vxNetID, namespace, a.StopCh)
	if err != nil {
		_ = logging.Errorf("Try to free VIP failed, err: %v", err)
	}
}

func (a *Allocator) AllocHostNic(args *rpc.PodInfo) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	_nicStatus := a.getValidNic(args.VxNet)
	if _nicStatus != nil {
		logging.Verbosef("Nic [%s] is found using in vxNet [%s]", _nicStatus.nic.ID, args.VxNet)
		return _nicStatus.nic, nil
	}

	targetNic, err := a.createAndAttachNewNic(args)
	if err != nil {
		_ = logging.Errorf("Allocate Host Nic for Args %v failed, err: %v", args, err)
		return nil, err
	}

	err = a.addNicStatus(targetNic, args)
	if err != nil {
		_ = logging.Errorf("Add Nic Status to DB failed, Nic [%s], err: %v", targetNic.ID, err)
		return nil, err
	}
	// touch nic name
	return targetNic, nil
}

func (a *Allocator) FreeHostNic(args *rpc.PodInfo, vxNetID string) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	var result *nicStatus
	result = a.getValidNic(vxNetID)
	if result == nil {
		return nil, nil
	}

	err := db.DeleteRefPodInfo(result.nic.ID, args.Name, args.Namespace)
	if err != nil {
		_ = logging.Errorf("delete Ref Pod info from DB failed, err: %v", err)
		return nil, err
	}

	args.NicType = result.info.NicType
	args.Netns = result.info.Netns
	args.Containter = result.info.Containter
	args.IfName = result.info.IfName
	return result.nic, nil
}

func (a *Allocator) GetNicStat(args *rpc.NicStatMessage) int32 {
	a.lock.Lock()
	defer a.lock.Unlock()
	return a.validNicCount
}

func (a *Allocator) MarkToDelete(nicID string) {
	a.deletingNic[nicID] = true
}

func (a *Allocator) IsDeleting(nicID string) bool {
	_, ok := a.deletingNic[nicID]
	return ok
}

func (a *Allocator) CleanDeletingMark(nicID string) {
	delete(a.deletingNic, nicID)
}

func (a *Allocator) GetCurrentNodePods() map[string]bool {
	infoMap := make(map[string]bool)
	currentNodePodInfo, err := k8s.K8sHelper.GetCurrentNodePods()
	if err != nil {
		_ = logging.Errorf("get current node pods failed, err: %v", err)
		return infoMap
	}

	for _, podInfo := range currentNodePodInfo {
		podUniName := db.GetPodUniName(podInfo.Name, podInfo.Namespace)
		infoMap[podUniName] = true
	}

	return infoMap
}

func (a *Allocator) CheckVipJobs() {
	a.vipLock.Lock()
	defer a.vipLock.Unlock()

	if len(a.vipJobs) <= 0 {
		logging.Debugf("vipJobs is empty, skip..")
		return
	}

	logging.Verbosef("period check VIP jobs")
	currVipJobIDs := []string{}
	for _jobID, _ := range a.vipJobs {
		currVipJobIDs = append(currVipJobIDs, _jobID)
	}

	err, succJobs, failedJobs := qcclient.QClient.DescribeVIPJobs(currVipJobIDs)
	if err != nil {
		return
	}
	for _, _succJob := range succJobs {
		jobInfo := a.vipJobs[_succJob]
		logging.Verbosef("Job [%s] succeed, Init vipDetailInfo", _succJob)
		err = InitVIP(jobInfo.VxNetID, jobInfo.Namespace, jobInfo.VIPs)
		if err != nil {
			_ = logging.Errorf("init configMap failed with Info [%v]", jobInfo)
		} else {
			delete(a.vipJobs, _succJob)
		}
	}

	retryJobs := []*VipJobInfo{}
	for _, _failedJob := range failedJobs {
		failedJobInfo := a.vipJobs[_failedJob]
		retryJobs = append(retryJobs, failedJobInfo)
		delete(a.vipJobs, _failedJob)
	}

	go func() {
		for _, jobInfo := range retryJobs {
			err := a.CreateVIPs(jobInfo.VxNetID, jobInfo.IPStart, jobInfo.IPEnd, jobInfo.Namespace)
			if err != nil {
				_ = logging.Errorf("retry create vip failed, JobInfo %v", jobInfo)
				continue
			}
		}
	}()
}

func (a *Allocator) SyncHostNic(node bool) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if !node {
		if len(a.jobs) <= 0 {
			return
		}
	}

	working := make(map[string]bool)
	aliveJobs := []string{}
	toDetach := []string{}
	toDelete := []string{}
	toAttach := []string{}
	all := []string{}
	currValidNicCount := 0
	currentNodePodNameMap := a.GetCurrentNodePods()

	for _, nic := range a.nics {
		all = append(all, nic.nic.ID)
	}

	nics, err := qcclient.QClient.GetNics(all)
	if err != nil {
		return
	}

	if len(a.jobs) >= 0 {
		aliveJobs, working, err = qcclient.QClient.DescribeNicJobs(a.jobs)
		if err != nil {
			return
		}
		a.jobs = aliveJobs
	}

	for _, id := range all {
		if nics[id] == nil {
			logging.Verbosef("nic missing , remove nic %s", id)
			err = a.removeNicStatus(a.nics[id])
			if err != nil {
				_ = logging.Errorf("remove NIC [%s] failed, err: %v", a.nics[id], err)
			}
			continue
		}

		if working[id] {
			continue
		}

		podUniNames, err := db.GetActivePodsByRefNicID(id, currentNodePodNameMap)
		if err != nil {
			_ = logging.Errorf("get related containers for nic [%s] failed, err: %v", id, err)
		}

		if len(podUniNames) == 0 {
			a.MarkToDelete(id)
			if nics[id].Using {
				toDetach = append(toDetach, id)
			} else {
				toDelete = append(toDelete, id)
			}
		} else if !nics[id].Using {
			// Attach Action Job failed, retry
			toAttach = append(toAttach, id)
		} else {
			currValidNicCount += 1
		}

	}

	if len(toDelete) > 0 {
		err = qcclient.QClient.DeleteNics(toDelete)
		if err == nil {
			logging.Verbosef("try to delete nic %v", toDelete)
			for _, id := range toDelete {
				logging.Verbosef("nic %s deleted, remove from status", id)
				err = a.removeNicStatus(a.nics[id])
				if err != nil {
					_ = logging.Errorf("remove NIC [%s] failed, err: %v", a.nics[id], err)
				} else {
					a.CleanDeletingMark(id)
				}
			}
		} else {
			_ = logging.Errorf("failed to delete nics %v", toDelete)
		}
	}

	if len(toDetach) > 0 {
		jobID, err := qcclient.QClient.DeattachNics(toDetach, false)
		if err == nil {
			logging.Verbosef("try to detach nic %v", toDetach)
			a.jobs = append(a.jobs, jobID)
		} else {
			_ = logging.Errorf("failed to deattach nics %v", toDetach)
		}
	}

	if len(toAttach) > 0 {
		jobID, err := qcclient.QClient.AttachNics(toAttach)
		if err == nil {
			logging.Verbosef("try to attach nic %v", toAttach)
			a.jobs = append(a.jobs, jobID)
		} else {
			_ = logging.Errorf("failed to attach nic %v", toAttach)
		}
	}
}

func (a *Allocator) Start(stopCh <-chan struct{}) error {
	a.StopCh = stopCh
	go a.run(stopCh)
	return nil
}

func (a *Allocator) run(stopCh <-chan struct{}) {
	nodeTimer := time.NewTicker(time.Duration(a.conf.NodeSync) * time.Second).C
	jobTimer := time.NewTicker(time.Duration(a.conf.Sync) * time.Second).C
	vipJobTimer := time.NewTicker(time.Duration(a.conf.VipSync) * time.Second).C
	for {
		select {
		case <-stopCh:
			logging.Verbosef("allocator receive stop signal!")
			return
		case <-jobTimer:
			a.SyncHostNic(false)
		case <-nodeTimer:
			logging.Debugf("period node sync")
			a.SyncHostNic(true)
		case <-vipJobTimer:
			a.CheckVipJobs()
		}
	}
}

var (
	Alloc *Allocator
)

func SetupAllocator(conf conf.PoolConf) {
	Alloc = &Allocator{
		nics:        make(map[string]*nicStatus),
		conf:        conf,
		deletingNic: make(map[string]bool),
		vipJobs:     make(map[string]*VipJobInfo),
	}

	logging.Verbosef("begin to load data from LevelDB")

	err := db.Iterator(func(key, value []byte) error {
		info := rpc.NICMMessage{}
		_err := json.Unmarshal(value, &info)
		if _err != nil {
			return _err
		}

		logging.Verbosef("restore Nic %s Status %d from DB", info.Nic.ID, info.Nic.Status)

		Alloc.nics[info.Nic.ID] = &nicStatus{
			nic:  info.Nic,
			info: info.Args,
		}
		return nil
	})
	if err != nil {
		logging.Panicf("failed restore allocator from leveldb, err: %v", err)
	}

	Alloc.SyncHostNic(true)
}
