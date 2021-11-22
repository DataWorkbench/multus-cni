package k8s

import (
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"os"

	"github.com/DataWorkbench/multus-cni/pkg/logging"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Helper struct {
	NodeName string
	Client   client.Client

	PodEvent record.EventRecorder
	Mgr      manager.Manager
}

var (
	scheme    = runtime.NewScheme()
	K8sHelper *Helper
)

func init() {
	_ = corev1.AddToScheme(scheme)
}

func SetupK8sHelper() {
	config, err := rest.InClusterConfig()
	if err != nil {
		logging.Panicf("failed to get k8s config: %v", err)
	}

	mgr, err := manager.New(config, manager.Options{Scheme: scheme})
	if err != nil {
		logging.Panicf("failed to new k8s manager: %v", err)
	}

	nodeName := os.Getenv(constants.NodeNameEnvKey)
	if nodeName == "" {
		logging.Panicf("node name should not be empty")
	}

	K8sHelper = &Helper{
		NodeName: nodeName,
		Client:   mgr.GetClient(),
		PodEvent: mgr.GetEventRecorderFor("hostnic"),
		Mgr:      mgr,
	}
}
