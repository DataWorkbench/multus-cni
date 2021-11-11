package server

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/allocator"
	conf2 "github.com/DataWorkbench/multus-cni/pkg/hostnic/conf"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
	"google.golang.org/grpc"
)

type NICMServer struct {
	rpc.UnimplementedCNIBackendServer
	conf conf2.ServerConf
}

func NewNICMServer(conf conf2.ServerConf) *NICMServer {
	return &NICMServer{
		conf: conf,
	}
}

func (s *NICMServer) Start(stopCh <-chan struct{}) error {
	go s.run(stopCh)
	return nil
}

// run starting the GRPC server
func (s *NICMServer) run(stopCh <-chan struct{}) {
	socketFilePath := s.conf.ServerPath

	err := os.Remove(socketFilePath)
	if err != nil {
		logging.Verbosef("cannot remove file %s", socketFilePath)
	}

	listener, err := net.Listen("unix", socketFilePath)
	if err != nil {
		logging.Panicf("Failed to listen to %s", socketFilePath)
	}

	//start up server rpc routine
	grpcServer := grpc.NewServer()
	rpc.RegisterCNIBackendServer(grpcServer, s)
	go func() {
		grpcServer.Serve(listener)
	}()

	logging.Verbosef("server grpc server started")
	<-stopCh
	grpcServer.Stop()
	logging.Verbosef("server grpc server stopped")
}

// AddNetwork handle add pod request
func (s *NICMServer) AddNetwork(context context.Context, in *rpc.NICMMessage) (*rpc.NICMMessage, error) {
	var (
		err  error
		info *rpc.PodInfo
	)

	info, err = k8s.K8sHelper.GetPodInfo(in.Args.Namespace, in.Args.Name)
	if err != nil {
		err = fmt.Errorf("cannot get podinfo %s/%s: %v", in.Args.Namespace, in.Args.Name, err)
		return nil, err
	}
	in.Args.NicType = info.NicType
	in.Args.VxNet = info.VxNet
	in.Args.PodIP = info.PodIP

	logging.Verbosef("handle server add request (%v)", in.Args)
	defer func() {
		logging.Verbosef("handle server add reply (%v)", in.Nic)
	}()

	in.Nic, err = allocator.Alloc.AllocHostNic(in.Args)

	return in, err
}

// DelNetwork handle del pod request
func (s *NICMServer) DelNetwork(context context.Context, in *rpc.NICMMessage) (*rpc.NICMMessage, error) {
	var (
		err     error
		podInfo *rpc.PodInfo
	)

	logging.Verbosef("handle server delete request (%v)", in.Args)
	defer func() {
		logging.Verbosef("handle server delete reply (%v), err: %v", in.Nic, err)
	}()

	podInfo, err = k8s.K8sHelper.GetPodInfo(in.Args.Namespace, in.Args.Name)
	if err != nil {
		err = fmt.Errorf("cannot get podinfo %s/%s: %v", in.Args.Namespace, in.Args.Name, err)
		return nil, err
	}

	in.Nic, err = allocator.Alloc.FreeHostNic(in.Args, podInfo.VxNet)

	return in, nil
}

func (s *NICMServer) GetNicStat(context context.Context, args *rpc.NicStatMessage) (*rpc.NicStatMessage, error) {
	logging.Verbosef("handle GetNicStat request (%v)", args)

	nicCount := allocator.Alloc.GetNicStat(args)
	return &rpc.NicStatMessage{Count: nicCount, NodeName: k8s.K8sHelper.NodeName}, nil
}
