package qcclient

import (
	"encoding/json"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/pkg/errors"
	"io/ioutil"
	"time"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	rpc "github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/utils"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"
)

const (
	instanceIDFile      = "/etc/qingcloud/instance-id"
	defaultOpTimeout    = 180 * time.Second
	defaultWaitInterval = 5 * time.Second
)

type Options struct {
	Tag string
}

var _ QingCloudAPI = &qingcloudAPIWrapper{}

type qingcloudAPIWrapper struct {
	nicService      *service.NicService
	vxNetService    *service.VxNetService
	instanceService *service.InstanceService
	jobService      *service.JobService
	tagService      *service.TagService
	vipService      *service.VIPService

	userID     string
	instanceID string
	opts       Options
}

// NewQingCloudClient create a qingcloud client to manipulate cloud resources
func SetupQingCloudClient(opts Options) {
	instanceID, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		logging.Panicf("failed to load instance-id, err: %v", err)
	}

	qsdkconfig, err := config.NewDefault()
	if err != nil {
		logging.Panicf("failed to new sdk default config, err: %v", err)
	}
	if err = qsdkconfig.LoadUserConfig(); err != nil {
		logging.Panicf("failed to load user config, err: %v", err)
	}

	logging.Verbosef("qsdkconfig inited %v", qsdkconfig)

	qcService, err := service.Init(qsdkconfig)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk service, err: %v", err)
	}

	nicService, err := qcService.Nic(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk nic service, err: %v", err)
	}

	vxNetService, err := qcService.VxNet(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk vxnet service, err: %v", err)
	}

	vipService, err := qcService.VIP(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk vip service: %v", err)
	}

	jobService, err := qcService.Job(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk job service, %v", err)
	}

	instanceService, err := qcService.Instance(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk instance service, %v", err)
	}

	tagService, err := qcService.Tag(qsdkconfig.Zone)
	if err != nil {
		logging.Panicf("failed to init qingcloud sdk tag service, %v", err)
	}

	//useid
	api, _ := qcService.Accesskey(qsdkconfig.Zone)
	output, err := api.DescribeAccessKeys(&service.DescribeAccessKeysInput{
		AccessKeys: []*string{&qsdkconfig.AccessKeyID},
	})
	if err != nil {
		logging.Panicf("failed to DescribeAccessKeys, err: %v", err)
	}
	if len(output.AccessKeySet) == 0 {
		logging.Panicf("DescribeAccessKeys is empty")
	}
	userId := *output.AccessKeySet[0].Owner

	QClient = &qingcloudAPIWrapper{
		nicService:      nicService,
		vxNetService:    vxNetService,
		instanceService: instanceService,
		jobService:      jobService,
		tagService:      tagService,
		vipService:      vipService,

		userID:     userId,
		instanceID: string(instanceID),
		opts:       opts,
	}
}

func (q *qingcloudAPIWrapper) GetInstanceID() string {
	return q.instanceID
}

func (q *qingcloudAPIWrapper) GetCreatedNics(num, offset int, vxNet, instanceID string) ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Limit:     &num,
		Offset:    &offset,
		VxNets:    []*string{&vxNet},
		Instances: []*string{&instanceID},
	}

	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		return nil, logging.Errorf("failed to GetCreatedNics: %v", err)
	}

	var (
		nics   []*rpc.HostNic
		netIDs []string
	)
	for _, nic := range output.NICSet {
		if *nic.Role != 0 {
			continue
		}
		nics = append(nics, constructHostnic(&rpc.VxNet{
			ID: *nic.VxNetID,
		}, nic))
		netIDs = append(netIDs, *nic.VxNetID)
	}

	if len(netIDs) > 0 {
		tmp := removeDupByMap(netIDs)
		vxnets, err := q.GetVxNets(tmp)
		if err != nil {
			return nil, err
		}

		for _, nic := range nics {
			nic.VxNet = vxnets[nic.VxNet.ID]
		}
	}

	return nics, nil
}

func (q *qingcloudAPIWrapper) GetAttachedNics() ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Instances: []*string{&q.instanceID},
		Status:    service.String("in-use"),
		Limit:     service.Int(constants.NicNumLimit + 1),
	}

	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		return nil, logging.Errorf("failed to GetPrimaryNIC: %v", err)
	}

	var result []*rpc.HostNic
	for _, nic := range output.NICSet {
		result = append(result, constructHostnic(nil, nic))
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) AttachNics(nicIDs []string) (string, error) {
	input := &service.AttachNicsInput{
		Nics:     service.StringSlice(nicIDs),
		Instance: &q.instanceID,
	}

	output, err := q.nicService.AttachNics(input)
	if err != nil {
		return "", logging.Errorf("failed to AttachNics: %v", err)
	}

	return *output.JobID, nil
}

// vxnet should not be nil
func constructHostnic(vxnet *rpc.VxNet, nic *service.NIC) *rpc.HostNic {
	if vxnet == nil {
		vxnet = &rpc.VxNet{
			ID: *nic.VxNetID,
		}
	}

	hostnic := &rpc.HostNic{
		ID:             *nic.NICID,
		VxNet:          vxnet,
		HardwareAddr:   *nic.NICID,
		PrimaryAddress: *nic.PrivateIP,
	}

	if *nic.Role == 1 {
		hostnic.IsPrimary = true
	}

	if *nic.Status == "in-use" {
		hostnic.Using = true
	}

	return hostnic
}

func (q *qingcloudAPIWrapper) GetNics(nics []string) (map[string]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Nics:  service.StringSlice(nics),
		Limit: service.Int(constants.NicNumLimit),
	}

	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		return nil, logging.Errorf("failed to GetNics: %v", err)
	}

	result := make(map[string]*rpc.HostNic)
	for _, nic := range output.NICSet {
		result[*nic.NICID] = constructHostnic(nil, nic)
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) CreateVIPs(vxNetID, IPStart, IPEnd string) (string, []string, error) {
	vipName := constants.NicPrefix + vxNetID
	vipRange := fmt.Sprintf("%s-%s", IPStart, IPEnd)
	count := utils.IPRangeCount(IPStart, IPEnd)
	input := &service.CreateVIPsInput{
		Count:    &count,
		VIPName:  &vipName,
		VxNetID:  &vxNetID,
		VIPRange: &vipRange,
	}

	output, err := q.vipService.CreateVIPs(input)
	if err != nil {
		_ = logging.Errorf("failed to CreateVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", []string{}, err
	}

	vipIDs := []string{}
	for _, id := range output.VIPs {
		vipIDs = append(vipIDs, *id)
	}

	return *output.JobID, vipIDs, nil
}

func (q *qingcloudAPIWrapper) CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string) ([]*rpc.HostNic, string, error) {
	nicName := constants.NicPrefix + q.instanceID
	input := &service.CreateNicsInput{
		Count:      service.Int(num),
		VxNet:      &vxnet.ID,
		PrivateIPs: nil,
		NICName:    service.String(nicName),
	}
	if ips != nil {
		input.Count = service.Int(len(ips))
		input.PrivateIPs = service.StringSlice(ips)
	}
	output, err := q.nicService.CreateNics(input)
	if err != nil {
		return nil, "", logging.Errorf("failed to create nics: %v", err)
	}

	var (
		result []*rpc.HostNic
		nics   []string
	)
	for _, nic := range output.Nics {
		result = append(result, &rpc.HostNic{
			ID:             *nic.NICID,
			VxNet:          vxnet,
			HardwareAddr:   *nic.NICID,
			PrimaryAddress: *nic.PrivateIP,
		})
		nics = append(nics, *nic.NICID)
	}

	//may need to tag the card later.
	q.attachNicTag(nics)

	job, err := q.AttachNics(nics)
	if err != nil {
		_ = q.DeleteNics(nics)
		return nil, "", err
	}

	return result, job, nil
}

func (q *qingcloudAPIWrapper) DeattachNics(nicIDs []string, sync bool) (string, error) {
	if len(nicIDs) <= 0 {
		return "", nil
	}

	input := &service.DetachNicsInput{
		Nics: service.StringSlice(nicIDs),
	}
	output, err := q.nicService.DetachNics(input)
	if err != nil {
		return "", logging.Errorf("failed to DeattachNics: %v", err)
	}

	if sync {
		return "", client.WaitJob(q.jobService, *output.JobID,
			defaultOpTimeout,
			defaultWaitInterval)
	}

	return *output.JobID, nil
}

func (q *qingcloudAPIWrapper) DeleteNics(nicIDs []string) error {
	if len(nicIDs) <= 0 {
		return nil
	}

	input := &service.DeleteNicsInput{
		Nics: service.StringSlice(nicIDs),
	}
	_, err := q.nicService.DeleteNics(input)
	if err != nil {
		return logging.Errorf("failed to DeleteNics: %v", err)
	}

	return nil
}

type nics struct {
	IDs []string `json:"nics"`
}

func (q *qingcloudAPIWrapper) DescribeNicJobs(ids []string) ([]string, map[string]bool, error) {
	input := &service.DescribeJobsInput{
		Jobs:  service.StringSlice(ids),
		Limit: service.Int(constants.NicNumLimit),
	}
	output, err := q.jobService.DescribeJobs(input)
	if err != nil {
		return nil, nil, logging.Errorf("failed to GetJobs, %v", err)
	}

	working := make(map[string]bool)
	var left []string
	for _, j := range output.JobSet {
		if *j.JobAction == "DetachNics" || *j.JobAction == "AttachNics" {
			if *j.Status == "working" || *j.Status == "pending" {
				left = append(left, *j.JobID)
				tmp := nics{}
				json.Unmarshal([]byte(*j.Directive), &tmp)
				for _, id := range tmp.IDs {
					working[id] = true
				}
			}
		}
	}

	return left, working, nil
}

func (q *qingcloudAPIWrapper) DescribeVIPJobs(ids []string) (err error, successJobs []string, failedJobs []string) {
	input := &service.DescribeJobsInput{
		Jobs:  service.StringSlice(ids),
		Limit: service.Int(constants.NicNumLimit),
	}
	output, err := q.jobService.DescribeJobs(input)
	if err != nil {
		return logging.Errorf("failed to GetJobs, %v", err), nil, nil
	}

	for _, j := range output.JobSet {
		if *j.JobAction == "AllocateVips" {
			if *j.Status == "failed" {
				failedJobs = append(failedJobs, *j.JobID)
			} else if *j.Status == "successful" || *j.Status == "done with failure" {
				successJobs = append(successJobs, *j.JobID)
			}
		}
	}

	return nil, successJobs, failedJobs
}

func _getStringSlice(param []string) []*string {
	resp := []*string{}
	for _, element := range param {
		_element := element
		resp = append(resp, &_element)
	}
	return resp
}

func (q *qingcloudAPIWrapper) DescribeVIPs(vxNetID string, VIPs []string, Addrs []string) (*service.DescribeVxNetsVIPsOutput, error) {
	_VIPs := _getStringSlice(VIPs)
	_addrs := _getStringSlice(Addrs)

	input := &service.DescribeVxNetsVIPsInput{
		VxNets: []*string{&vxNetID},
		Limit:  service.Int(constants.VIPNumLimit),
	}

	if len(_VIPs) > 0 {
		input.VIPs = _VIPs
	}

	if len(_addrs) > 0 {
		input.VIPAddrs = _addrs
	}

	output, err := q.vipService.DescribeVxNetsVIPs(input)
	if err != nil {
		_ = logging.Errorf("failed to DescribeVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	return output, nil
}

func (q *qingcloudAPIWrapper) DeleteVIPs(vips []string) (string, error) {
	if len(vips) <= 0 {
		return "", nil
	}

	input := &service.DeleteVIPsInput{
		VIPs: service.StringSlice(vips),
	}

	output, err := q.vipService.DeleteVIPs(input)
	if err != nil {
		_ = logging.Errorf("failed to DeleteVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	return *output.JobID, nil
}

// If NICs are belong to users, it is necessary to add Owner to VxNet
func (q *qingcloudAPIWrapper) getVxNets(ids []string, public bool) ([]*rpc.VxNet, error) {
	input := &service.DescribeVxNetsInput{
		VxNets: service.StringSlice(ids),
		Limit:  service.Int(constants.NicNumLimit),
	}
	if public {
		input.VxNetType = service.Int(2)
	}
	output, err := q.vxNetService.DescribeVxNets(input)
	if err != nil {
		return nil, logging.Errorf("failed to GetVxNets, %v", err)
	}

	var vxNets []*rpc.VxNet
	for _, qcVxNet := range output.VxNetSet {
		vxnetItem := &rpc.VxNet{
			ID: *qcVxNet.VxNetID,
		}

		if qcVxNet.Router != nil {
			vxnetItem.Gateway = *qcVxNet.Router.ManagerIP
			vxnetItem.Network = *qcVxNet.Router.IPNetwork
		} else {
			return nil, fmt.Errorf("vxnet %s should bind to vpc", *qcVxNet.VxNetID)
		}

		vxNets = append(vxNets, vxnetItem)
	}

	return vxNets, nil
}

func (q *qingcloudAPIWrapper) GetVxNets(ids []string) (map[string]*rpc.VxNet, error) {
	if len(ids) <= 0 {
		return nil, errors.WithStack(fmt.Errorf("GetVxNets should not have empty input"))
	}

	vxnets, err := q.getVxNets(ids, false)
	if err != nil {
		return nil, err
	}

	var left []string
	result := make(map[string]*rpc.VxNet, 0)
	for _, vxNet := range vxnets {
		result[vxNet.ID] = vxNet
	}
	for _, id := range ids {
		if result[id] == nil {
			left = append(left, id)
		}
	}
	if len(left) > 0 {
		vxnets, err := q.getVxNets(left, true)
		if err != nil {
			return nil, err
		}
		for _, vxNet := range vxnets {
			result[vxNet.ID] = vxNet
		}
	}

	return result, nil
}

func removeDupByMap(slc []string) []string {
	result := []string{}
	tempMap := map[string]byte{}
	for _, e := range slc {
		l := len(tempMap)
		tempMap[e] = 0
		if len(tempMap) != l {
			result = append(result, e)
		}
	}
	return result
}

func (q *qingcloudAPIWrapper) attachNicTag(nics []string) {
	if q.opts.Tag == "" {
		return
	}
	tagID := q.opts.Tag

	for _, nic := range nics {
		input := &service.AttachTagsInput{
			ResourceTagPairs: []*service.ResourceTagPair{
				&service.ResourceTagPair{
					ResourceID:   &nic,
					ResourceType: service.String(string(constants.ResourceTypeNic)),
					TagID:        service.String(tagID),
				},
			},
		}
		_, _ = q.tagService.AttachTags(input)
	}

	return
}
