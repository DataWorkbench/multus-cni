package main

import (
	"flag"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/db"
	//"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	//"github.com/DataWorkbench/multus-cni/pkg/logging"
)

func main() {
	dbOpts := db.NewLevelDBOptions()
	dbOpts.AddFlags()
	flag.Parse()
	db.SetupLevelDB(dbOpts)
	defer func() {
		db.CloseDB()
	}()
	//log.Setup(logOpts)
	//
	//// set up signals so we handle the first shutdown signals gracefully
	//stopCh := signals.SetupSignalHandler()
	//
	//// load ipam server config
	//conf, err := conf.TryLoadFromDisk(constants.DefaultConfigName, constants.DefaultConfigPath)
	//if err != nil {
	//	logging.Panicf("failed to load config")
	//}
	//logrus.Infof("hostnic config is %v", conf)
	//
	//// setup qcclient, k8s
	//qcclient.SetupQingCloudClient(qcclient.Options{
	//	Tag: conf.Pool.Tag,
	//})
	//k8s.SetupK8sHelper()
	//networkutils.SetupNetworkHelper()
	//allocator.SetupAllocator(conf.Pool)
	//
	//// add daemon
	//k8s.K8sHelper.Mgr.Add(allocator.Alloc)
	//k8s.K8sHelper.Mgr.Add(server.NewIPAMServer(conf.Server))
	//
	////add controllers
	//nodeReconciler := &controllers.NodeReconciler{}
	//err = nodeReconciler.SetupWithManager(k8s.K8sHelper.Mgr)
	//if err != nil {
	//	logrus.Fatalf("failed to setup node reconciler")
	//}
	//
	//logrus.Info("all setup done, startup daemon")
	//if err := k8s.K8sHelper.Mgr.Start(stopCh); err != nil {
	//	logrus.WithError(err).Errorf("failed to start daemon")
	//	os.Exit(1)
	//}
	//logrus.Info("daemon exited")
}
