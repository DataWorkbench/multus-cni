package main

import (
	"flag"

	"github.com/DataWorkbench/multus-cni/pkg/hostnic/allocator"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/conf"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/db"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/k8s/controllers"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/qcclient"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/server"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/signals"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
)

const (
	cniConfigDirVarName           = "cni-config-dir"
	multusAdditionalBinDirVarName = "additional-bin-dir"
	multusAutoconfigDirVarName    = "multus-autoconfig-dir"
	multusCNIVersion              = "cni-version"
	multusConfigFileVarName       = "multus-conf-file"
	multusGlobalNamespaces        = "global-namespaces"
	multusLogFile                 = "multus-log-file"
	multusLogLevel                = "multus-log-level"
	multusLogToStdErr             = "multus-log-to-stderr"
	multusKubeconfigPath          = "multus-kubeconfig-file-host"
	multusMasterCNIFileVarName    = "multus-master-cni-file"
	multusNamespaceIsolation      = "namespace-isolation"
	multusReadinessIndicatorFile  = "readiness-indicator-file"
)

const (
	defaultCniConfigDir                 = "/etc/cni/net.d"
	defaultMultusAdditionalBinDir       = ""
	defaultMultusCNIVersion             = ""
	defaultMultusConfigFile             = "auto"
	defaultMultusGlobalNamespaces       = ""
	defaultMultusKubeconfigPath         = "/etc/cni/net.d/multus.d/multus.kubeconfig"
	defaultMultusLogFile                = ""
	defaultMultusLogLevel               = ""
	defaultMultusLogToStdErr            = false
	defaultMultusMasterCNIFile          = ""
	defaultMultusNamespaceIsolation     = false
	defaultMultusReadinessIndicatorFile = ""
)

func main() {

	logToStdErr := flag.Bool(multusLogToStdErr, defaultMultusLogToStdErr, "If the multus logs are also to be echoed to stderr.")
	logLevel := flag.String(multusLogLevel, defaultMultusLogLevel, "One of: debug/verbose/error/panic. Used only with --multus-conf-file=auto.")
	logFile := flag.String(multusLogFile, defaultMultusLogFile, "Path where to multus will log. Used only with --multus-conf-file=auto.")
	if *logToStdErr {
		logging.SetLogStderr(*logToStdErr)
	}
	if *logFile != defaultMultusLogFile {
		logging.SetLogFile(*logFile)
	}
	if *logLevel != defaultMultusLogLevel {
		logging.SetLogLevel(*logLevel)
	}
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
