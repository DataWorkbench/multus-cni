package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/vip"
	"github.com/DataWorkbench/multus-cni/pkg/k8sclient"
	"github.com/DataWorkbench/multus-cni/pkg/multus"
	v1 "k8s.io/api/core/v1"
	"os"
)

const userRWPermission = 0600

const (
	NamespaceName  = "namespace"
	configmapName  = "configmap"
	KubeconfigPath = "kubeconfig-file-host"
)

const (
	defaultKubeconfigPath = "/etc/cni/net.d/multus.d/multus.kubeconfig"
)

func main() {
	versionOpt := false
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	Namespace := flag.String(NamespaceName, "", "Namespace of vip configmap")
	Configmap := flag.String(configmapName, "", "Name of vip configmap")
	Kubeconfig := flag.String(KubeconfigPath, defaultKubeconfigPath, "The path to the kubeconfig")
	flag.BoolVar(&versionOpt, "version", false, "Show application version")
	flag.BoolVar(&versionOpt, "v", false, "Show application version")

	flag.Parse()
	if versionOpt == true {
		fmt.Printf("%s\n", multus.PrintVersionString())
		return
	}
	if *Namespace == "" {
		fmt.Printf("error namespace: %s\n", *Namespace)
		return
	}
	if *Configmap == "" {
		fmt.Printf("error configmap: %s\n", *Configmap)
		return
	}
	fmt.Println(*Namespace, *Configmap)
	client, err := k8sclient.GetK8sClient(*Kubeconfig, nil)
	if err != nil {
		fmt.Printf("failed to get k8s client: %v\n", err)
		return
	}
	var configmap *v1.ConfigMap
	configmap, err = client.GetConfigMap(*Configmap, *Namespace)
	if err != nil {
		fmt.Printf("failed to get configmap %s of namespace %s: %v\n", err, *Configmap, *Namespace)
		return
	}
	if configmap != nil {
		dataMapJson := configmap.Data[constants.VIPConfName]
		fmt.Printf("config map %s [namespace %s] %s: %s\n", *Configmap, *Namespace, constants.VIPConfName, dataMapJson)
		if dataMapJson == "" {
			return
		}
		dataMap := &vip.VIPAllocMap{}
		err = json.Unmarshal([]byte(dataMapJson), dataMap)
		if err != nil {
			fmt.Printf("failed to parse Data Json [%s], err: %v\n", dataMapJson, err)
			return
		}
		fixedIPs := []string{}
		for ip, info := range dataMap.VIPDetailInfo {
			podName := info.RefPodName
			if podName != "" {
				pod, _ := client.GetPod(*Namespace, podName)
				if pod == nil {
					err = vip.ReleasePodIP(client, *Configmap, *Namespace, podName)
					if err != nil {
						fmt.Printf("failed to release pod %s ip %s of namespace %s\n", podName, ip, *Namespace)
					}else {
						fixedIPs = append(fixedIPs, ip)
					}
				}
			}
		}
		fmt.Printf("fix vips: %v\n", fixedIPs)
	}
}
