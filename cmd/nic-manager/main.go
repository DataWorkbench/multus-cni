package main

import (
	"flag"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/allocator"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/conf"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/db"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/k8s/controllers"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/networkutils"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/server"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/signals"
	"github.com/DataWorkBench/multus-cni/pkg/logging"
)

func main() {
	dbOpts := db.NewLevelDBOptions()
	dbOpts.AddFlags()
	flag.Parse()
	err := db.SetupLevelDB(dbOpts)
	if err != nil {
		logging.Panicf("Failed to init LevelDB, err: %v", err)
	}
	defer func() {
		db.CloseDB()
	}()

	// set up signals so we handle the first shutdown signals gracefully
	stopCh := signals.SetupSignalHandler()

	// load ipam server config
	config, err := conf.TryLoadFromDisk(constants.DefaultConfigName, constants.DefaultConfigPath)
	if err != nil {
		logging.Panicf("failed to load config: %v", err)
	}

	if config == nil {
		logging.Panicf("config is NIL")
	}

	logging.Verbosef("hostnic config is %v", config)

	// setup qcclient, k8s
	qcclient.SetupQingCloudClient(qcclient.Options{
		Tag: config.Pool.Tag,
	})
	k8s.SetupK8sHelper()
	networkutils.SetupNetworkHelper()
	allocator.SetupAllocator(config.Pool)

	// add daemon
	k8s.K8sHelper.Mgr.Add(allocator.Alloc)
	k8s.K8sHelper.Mgr.Add(server.NewNICMServer(config.Server))

	//add controllers
	nodeReconciler := &controllers.NodeReconciler{}
	err = nodeReconciler.SetupWithManager(k8s.K8sHelper.Mgr)
	if err != nil {
		logging.Panicf("failed to setup node reconciler, %v", err)
	}

	logging.Verbosef("all setup done, startup daemon")
	if err := k8s.K8sHelper.Mgr.Start(stopCh); err != nil {
		logging.Panicf("failed to start daemon: %v", err)
	}
	logging.Verbosef("daemon exited")

}
