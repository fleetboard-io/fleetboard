package dedinic

import (
	"context"
	"os"
	"time"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/kubeovn/kube-ovn/pkg/request"
	"k8s.io/klog/v2"

	"github.com/nauti-io/nauti/pkg/known"
)

type CNIPlugin struct {
	Stub stub.Stub
	Mask stub.EventMask
}

var (
	csh *cniHandler
	_   = stub.ConfigureInterface(&CNIPlugin{})
)

func InitNRIPlugin(config *Configuration, controller *Controller) {
	var (
		err  error
		opts []stub.Option
	)
	flag := os.Getenv("NRI_ENABLE")
	if flag != "true" {
		klog.Infof("NRI plugin is no enabled")
		return
	}
	pluginName := "hydra"
	opts = append(opts, stub.WithPluginName(pluginName))
	pluginIdx := "00"
	opts = append(opts, stub.WithPluginIdx(pluginIdx))

	p := &CNIPlugin{}
	events := "runpodsandbox,stoppodsandbox,removepodsandbox"
	klog.Info("nri start ....")

	if p.Mask, err = api.ParseEventMask(events); err != nil {
		klog.Errorf("nri failed to parse events: %v", err)
	}

	if p.Stub, err = stub.New(p, append(opts, stub.WithOnClose(p.OnClose))...); err != nil {
		klog.Errorf("nri failed to create nri stub: %v", err)
	}

	csh = createCniHandler(config, controller)
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
		err := addPodToCNIQueue(pod)
		if err != nil {
			klog.Errorf("put pod: %v into cni queue failed: %v", pod, err)
		}
	}
	return nil, nil
}

func (p *CNIPlugin) Shutdown() {
	//dump("Shutdown")
}

func (p *CNIPlugin) RunPodSandbox(pod *api.PodSandbox) (err error) {
	klog.Infof("[RunPodSandbox]: the pod is %s/%s", pod.Namespace, pod.Name)
	return addPodToCNIQueue(pod)
}

func (p *CNIPlugin) StopPodSandbox(pod *api.PodSandbox) error {
	klog.Infof("[StopPodSandbox]: the pod is %s--%s", pod.Namespace, pod.Name)
	nsPath := GetNSPathFromPod(pod)
	if nsPath == "" {
		klog.Info("the namespace path is hostnetwork  or cant fount the ns")
		return nil
	}
	klog.Infof("the namespace path is: %s ", nsPath)
	podRequest := &request.CniRequest{
		CniType:      "kube-ovn",
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		ContainerID:  pod.GetId(),
		NetNs:        nsPath,
		IfName:       "eth-ovn",
		Provider:     "ovn",
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

func GetNSPathFromPod(pod *api.PodSandbox) (nsPath string) {
	for _, ns := range pod.Linux.Namespaces {
		if ns.Type == "network" {
			nsPath = ns.Path
			break
		}
	}
	return
}

func addPodToCNIQueue(pod *api.PodSandbox) error {
	nsPath := GetNSPathFromPod(pod)
	if nsPath == "" {
		klog.V(5).Info("the namespace path is hostnetwork ")
		return nil
	}
	klog.V(5).Infof("the namespace path is: %s ", nsPath)
	klog.V(5).Infof("the pod annotation is: %s ", pod.Annotations)

	podRequest := &request.CniRequest{
		CniType:      "kube-ovn",
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		ContainerID:  pod.GetId(),
		NetNs:        nsPath,
		IfName:       "eth-ovn",
		Provider:     known.NautiPrefix,
	}
	DelayQueue.Put(time.Now().Add(time.Second*3), podRequest)
	return nil
}
