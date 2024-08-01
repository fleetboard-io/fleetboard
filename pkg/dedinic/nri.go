package dedinic

import (
	"context"
	"fmt"
	"os"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/known"
)

var (
	CNFBridgeName = "nauti"
)

var (
	NodeCIDR        string
	GlobalCIDR      string
	CNFPodName      string
	CNFPodNamespace string
	CNFPodIP        string
	CNFBridgeIP     string
)

type CNIPlugin struct {
	Stub       stub.Stub
	Mask       stub.EventMask
	kubeClient *kubernetes.Clientset
}

var (
	csh *cniHandler
	_   = stub.ConfigureInterface(&CNIPlugin{})
)

func InitNRIPlugin(kubeClient *kubernetes.Clientset) {
	var (
		err  error
		opts []stub.Option
	)

	pluginName := "hydra"
	opts = append(opts, stub.WithPluginName(pluginName))
	pluginIdx := "00"
	opts = append(opts, stub.WithPluginIdx(pluginIdx))

	p := &CNIPlugin{
		kubeClient: kubeClient,
	}
	events := "runpodsandbox,stoppodsandbox,removepodsandbox"
	klog.Info("nri start ....")

	if p.Mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("nri failed to parse events: %v", err)
	}

	if p.Stub, err = stub.New(p, append(opts, stub.WithOnClose(p.OnClose))...); err != nil {
		klog.Errorf("nri failed to create nri stub: %v", err)
	}

	csh = createCniHandler(kubeClient)
	klog.Info(">>>>>>>>>>>>>>>>>>>>>  nri CNI Plugin Started - Version Tag 0.0.1 <<<<<<<<<<<<<<<<<<<<<<<<<<")

	err = p.Stub.Run(context.Background())
	if err != nil {
		klog.Errorf("nri CNIPlugin exited with error %v", err)
	}
}

func (p *CNIPlugin) Configure(config, runtime, version string) (stub.EventMask, error) {
	klog.Infof("got configuration data: %q from runtime %s %s", config, runtime, version)

	return p.Mask, nil
}

func (p *CNIPlugin) Synchronize(pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	for _, pod := range pods {
		klog.Infof("[Synchronize]: %v/%v", pod.Namespace, pod.Name)
		if isCNFSelf(pod.Namespace, pod.Name) {
			klog.Infof("skip the cnf releated pod: %v/%v", pod.Namespace, pod.Name)
			continue
		}
		nsPath, err := GetNSPathFromPod(pod)
		if err != nil {
			klog.Infof("the namespace path is host-network or cant fount the ns: %v ", err)
			continue
		}

		klog.V(6).Infof("deal the pod: %v/%v,the namespace path is: %s ", pod.Namespace, pod.Name, nsPath)

		podRequest := &CniRequest{
			PodName:      pod.Name,
			PodNamespace: pod.Namespace,
			ContainerID:  pod.GetId(),
			NetNs:        nsPath,
			IfName:       "eth-nauti",
		}

		if err := csh.handleDel(podRequest); err != nil {
			klog.Errorf("delete exist network failed: %v", err)
		}
		if err := csh.handleAdd(podRequest); err != nil {
			klog.Errorf("add network failed: %v", err)
		}
	}
	return nil, nil
}

func (p *CNIPlugin) Shutdown() {
	// dump("Shutdown")
}

func (p *CNIPlugin) RunPodSandbox(pod *api.PodSandbox) (err error) {
	klog.Infof("[RunPodSandbox]: the pod is %s/%s", pod.Namespace, pod.Name)

	nsPath, err := GetNSPathFromPod(pod)
	if err != nil {
		klog.V(5).Info("the namespace path is hostnetwork ")
		return err
	}
	klog.V(5).Infof("the namespace path is: %s ", nsPath)
	klog.V(5).Infof("the pod annotation is: %s ", pod.Annotations)

	podRequest := &CniRequest{
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		ContainerID:  pod.GetId(),
		NetNs:        nsPath,
		IfName:       "eth-nauti",
		Provider:     known.NautiPrefix,
	}

	err = csh.handleAdd(podRequest)
	if err != nil {
		klog.Errorf("add interface failed for pod: %v", podRequest)
	}
	return err
}

func (p *CNIPlugin) StopPodSandbox(pod *api.PodSandbox) error {
	klog.Infof("[StopPodSandbox]: the pod is %s--%s", pod.Namespace, pod.Name)
	nsPath, err := GetNSPathFromPod(pod)
	if err != nil {
		klog.Info("the namespace path is host-network or cant fount the ns")
		return err
	}
	klog.Infof("the namespace path is: %s ", nsPath)
	podRequest := &CniRequest{
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		ContainerID:  pod.GetId(),
		NetNs:        nsPath,
		IfName:       "eth-nauti",
	}

	return csh.handleDel(podRequest)
}

func (p *CNIPlugin) RemovePodSandbox(pod *api.PodSandbox) error {
	klog.Infof("[RemovePodSandbox]: the pod is %s--%s", pod.Namespace, pod.Name)
	return nil
}

func (p *CNIPlugin) OnClose() {
	klog.Errorf("cni plugin closed")
	os.Exit(0)
}

func GetNSPathFromPod(pod *api.PodSandbox) (nsPath string, err error) {
	for _, ns := range pod.Linux.Namespaces {
		if ns.Type == "network" {
			nsPath = ns.Path
			break
		}
	}
	if nsPath == "" {
		klog.V(6).Infof("pod: %v/%v, linux: %v", pod.Namespace, pod.Name, pod.Linux)
		return "", fmt.Errorf("nsPath is empty for pod: %s/%s", pod.Namespace, pod.Name)
	}
	return nsPath, nil
}
