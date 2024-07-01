package dedinic

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/inotify"

	"github.com/kubeovn/kube-ovn/pkg/request"
	"github.com/nauti-io/nauti/pkg/known"
)

type EventType int

const (
	InsecureKubeletTLS  = false
	KubeletReadOnlyPort = 10250
	HTTPScheme          = "http"
	HTTPSScheme         = "https"
	cgroupRootGAPath    = "kubepods.slice"
	cgroupRootBTPath    = "kubepods.slice/kubepods-burstable.slice"
	cgroupRootBEPath    = "kubepods.slice/kubepods-besteffort.slice"

	DirCreated  = 0
	DirRemoved  = 1
	UnknownType = 2

	FileCreated = 0
	FileUpdated = 1

	PodAdded   = 0
	PodDeleted = 1

	ContainerAdded      = 0
	ContainerDeleted    = 1
	ContainerTaskIDDone = 2
)

type OobImpl struct {
	cgroupRootPath   string
	podWatcher       *inotify.Watcher
	containerWatcher *inotify.Watcher
	taskIDWatcher    *inotify.Watcher
	podEvents        chan *PodEvent
	containerEvents  chan *ContainerEvent
	kubeletStub      KubeletStub
	pods             map[string]corev1.Pod
}

var (
	OOBInstance *OobImpl
	err         error
)

func InitOOb() {
	OOBInstance, err = NewOobServer("/opt/dedinic/cgroup")
	if err != nil {
		klog.Fatalf("out of band engin start failed.")
	}
	go OOBInstance.Run(StopCh)
	klog.Info("oob engin started >>>>>>> ")
}

type PodEvent struct {
	eventType  int
	podID      string
	cgroupPath string
}

type ContainerEvent struct {
	eventType   int
	podID       string
	containerID string
	cgroupPath  string
	// netns       string
}

func NewOobServer(cgroupRootPath string) (*OobImpl, error) {
	stub, err := newKubeletStub()
	if err != nil {
		klog.Errorf("%v", err)
	}
	podWatcher, err := inotify.NewWatcher()
	if err != nil {
		klog.Error("create pod watcher failed", err)
	}

	containerWatcher, err := inotify.NewWatcher()
	if err != nil {
		klog.Error("create container watcher failed", err)
	}

	taskIDWatcher, err := inotify.NewWatcher()
	if err != nil {
		klog.Error("create taskId watcher failed", err)
	}

	o := &OobImpl{
		cgroupRootPath:   cgroupRootPath,
		podWatcher:       podWatcher,
		containerWatcher: containerWatcher,
		taskIDWatcher:    taskIDWatcher,
		kubeletStub:      stub,
		podEvents:        make(chan *PodEvent, 128),
		containerEvents:  make(chan *ContainerEvent, 128),
		pods:             make(map[string]corev1.Pod),
	}
	return o, nil
}

func newKubeletStub() (KubeletStub, error) {
	port := KubeletReadOnlyPort
	var scheme string
	if InsecureKubeletTLS {
		scheme = HTTPScheme
	} else {
		scheme = HTTPSScheme
	}
	nodeName := os.Getenv("KUBE_NODE_NAME")
	return NewKubeletStub(nodeName, port, scheme, 30*time.Second)
}

// TypeOf tell the type of event
func TypeOf(event *inotify.Event) EventType {
	if event.Mask&inotify.InCreate != 0 && event.Mask&inotify.InIsdir != 0 {
		return DirCreated
	}
	if event.Mask&inotify.InDelete != 0 && event.Mask&inotify.InIsdir != 0 {
		return DirRemoved
	}
	if event.Mask&inotify.InCreate != 0 && event.Mask&inotify.InIsdir == 0 {
		return FileCreated
	}
	if event.Mask&inotify.InModify != 0 && event.Mask&inotify.InIsdir == 0 {
		return FileUpdated
	}
	return UnknownType
}

func GetNetNs(ctx context.Context, cgroupPath string) string {
	for {
		select {
		case <-ctx.Done():
			return ""
		default:
			file, err := os.Open(cgroupPath)
			// defer file.Close()
			if err != nil {
				klog.Infof("no cgroup path now, %v %s", err, cgroupPath)
				return ""
			}
			rd := bufio.NewReader(file)
			txt, _, err := rd.ReadLine()
			if err != nil {
				klog.Errorf("file %s readline failed %v", cgroupPath, err)
			}
			pid := string(txt)

			link, err := os.Readlink(fmt.Sprintf("/opt/dedinic/proc/%s/ns/net", pid))
			if err != nil {
				klog.Errorf("Can't read link file %v:", err)
			}
			re := regexp.MustCompile(`\d+`)
			match := re.FindString(link)
			root := "/var/run/netns/"
			var netns string
			err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					klog.Infof("access path error: %v\n", err)
					return nil
				}

				if info.Mode()&os.ModeSymlink != 0 {
					return nil
				}

				str := strconv.FormatUint(info.Sys().(*syscall.Stat_t).Ino, 10)
				if str == match {
					klog.Infof("find path is %s", path)
					netns = "/var/run/netns/" + filepath.Base(path)
					return filepath.SkipDir
				}
				return nil
			})

			if err != nil {
				klog.Errorf("Walk dir error: %v", err)
			}
			klog.Infof("netns is %s", netns)
			if netns != "" {
				return netns
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (o *OobImpl) runEventHandler(stoptCh <-chan struct{}) {
	for {
		select {
		case event := <-o.podEvents:
			switch event.eventType {
			case PodAdded:
				klog.Infof("PodAdded, %s", event.podID)
				_, err := o.GetAllPods()
				if err != nil {
					klog.Errorf("Get all pods failed %v", err)
				}
			case PodDeleted:
				klog.Infof("PodDeleted, %s", event.podID)
			}
		case event := <-o.containerEvents:
			switch event.eventType {
			case ContainerAdded:
				klog.Infof("ContainerAdded, %s %s", event.podID, event.containerID)
			case ContainerDeleted:
				klog.Infof("ContainerDeleted, %s %s", event.podID, event.containerID)
			case ContainerTaskIDDone:
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				// defer cancel()
				_, err := o.GetAllPods()
				if err != nil {
					klog.Errorf("Get all pods failed %v", err)
				}
				netns := GetNetNs(ctx, event.cgroupPath)
				cancel()
				if pod, ok := o.pods[event.podID]; ok {
					klog.Infof("add dedinic to the pod:%v", pod)
					podRequest := &request.CniRequest{
						CniType:      "kube-ovn",
						PodName:      pod.Name,
						PodNamespace: pod.Namespace,
						ContainerID:  string(pod.GetUID()),
						NetNs:        netns,
						IfName:       "eth-nauti",
						Provider:     known.NautiPrefix,
					}
					DelayQueue.Put(time.Now().Add(time.Second*3), podRequest)
				} else {
					klog.Errorf("cant find the pod info: %v", event.podID)
				}
				klog.Infof("netns is %s", netns)
				klog.Infof("ContainerTaskIDDone, %s %s %s", event.podID, event.containerID, netns)
			}
		case <-stoptCh:
			return
		}
	}
}

func (o *OobImpl) Run(stopCh <-chan struct{}) {
	cgroupGAPath := path.Join(o.cgroupRootPath, cgroupRootGAPath)
	err := o.podWatcher.AddWatch(cgroupGAPath, inotify.InCreate|inotify.InDelete)
	if err != nil {
		klog.Errorf("failed to watch path %s, err %v", cgroupGAPath, err)
		return
	}
	klog.Infof("add GAPath to watcher, %s", cgroupGAPath)
	cgroupBTPath := path.Join(o.cgroupRootPath, cgroupRootBTPath)
	err = o.podWatcher.AddWatch(cgroupBTPath, inotify.InCreate|inotify.InDelete)
	if err != nil {
		klog.Errorf("failed to watch path %s, err %v", cgroupBTPath, err)
		return
	}
	klog.Infof("add BTPath to watcher, %s", cgroupBTPath)
	cgroupBEPath := path.Join(o.cgroupRootPath, cgroupRootBEPath)
	err = o.podWatcher.AddWatch(cgroupBEPath, inotify.InCreate|inotify.InDelete)
	if err != nil {
		klog.Errorf("failed to watch path %s, err %v", cgroupBEPath, err)
		return
	}
	klog.Infof("add BEPath to watcher, %s", cgroupBEPath)
	defer func() {
		err := o.podWatcher.RemoveWatch(cgroupGAPath)
		if err != nil {
			klog.Errorf("failed to remove watch path %s, err %v", cgroupGAPath, err)
		}
		err = o.podWatcher.RemoveWatch(cgroupBTPath)
		if err != nil {
			klog.Errorf("failed to remove watch path %s, err %v", cgroupBTPath, err)
		}
		err = o.podWatcher.RemoveWatch(cgroupBEPath)
		if err != nil {
			klog.Errorf("failed to remove watch path %s, err %v", cgroupBEPath, err)
		}
	}()

	go o.runEventHandler(stopCh)
	for {
		select {
		case event := <-o.podWatcher.Event:
			switch TypeOf(event) {
			case DirCreated:
				podID, err := ParsePodID(filepath.Base(event.Name))
				if err != nil {
					klog.Errorf("failed to parse pod id from %s", event.Name)
				}
				err = o.containerWatcher.AddWatch(event.Name, inotify.InCreate|inotify.InDelete)
				if err != nil {
					klog.Errorf("failed to watch path %s, err %v", event.Name, err)
				}
				o.podEvents <- newPodEvent(podID, PodAdded, event.Name)
			case DirRemoved:
				podID, err := ParsePodID(filepath.Base(event.Name))
				if err != nil {
					klog.Errorf("failed to parse pod id from %s", event.Name)
				}
				err = o.containerWatcher.RemoveWatch(event.Name)
				if err != nil {
					klog.Errorf("failed to remove watch path %s, err %v", event.Name, err)
				}
				o.podEvents <- newPodEvent(podID, PodDeleted, event.Name)
				klog.Infof("dir delete, %s", event.Name)
			default:
				klog.Infof("Unkown type")
			}
		case err := <-o.podWatcher.Error:
			klog.Errorf("read pods event error: %v", err)
		case event := <-o.containerWatcher.Event:
			switch TypeOf(event) {
			case DirCreated:
				containerID, err := ParseContainerID(filepath.Base(event.Name))
				if err != nil {
					klog.Infof("get containerID failed")
					continue
				}
				podID, err := ParsePodID(filepath.Base(filepath.Dir(event.Name)))
				if err != nil {
					klog.Infof("get podID failed, %v", err)
					continue
				}
				err = o.taskIDWatcher.AddWatch(path.Join(event.Name, "cgroup.procs"),
					inotify.InCreate|inotify.InModify|inotify.InAllEvents)
				if err != nil {
					klog.Errorf("failed to watch path %s, err %v", event.Name+"/cgroup.procs", err)
				}
				o.containerEvents <- newContainerEvent(podID, containerID, ContainerAdded, event.Name)
				klog.Infof("dir create: %v, %v, %v", event.Name, podID, containerID)
			case DirRemoved:
				containerID, err := ParseContainerID(filepath.Base(event.Name))
				if err != nil {
					klog.Infof("get containerID failed")
					continue
				}
				podID, err := ParsePodID(filepath.Base(filepath.Dir(event.Name)))
				if err != nil {
					klog.Infof("get podID failed")
					continue
				}
				o.containerEvents <- newContainerEvent(podID, containerID, ContainerDeleted, event.Name)
				klog.Infof("dir delete, %s", event.Name)
			default:
				klog.Infof("Unkown type")
			}
		case event := <-o.taskIDWatcher.Event:
			switch TypeOf(event) {
			case FileCreated:
				klog.Infof("cgroup.procs file created %v", event)

				containerDir := filepath.Dir(event.Name)
				containerID, err := ParseContainerID(filepath.Base(containerDir))
				if err != nil {
					klog.Infof("get containerID failed")
					continue
				}
				podID, err := ParsePodID(filepath.Base(filepath.Dir(containerDir)))
				if err != nil {
					klog.Infof("get podID failed, %v", err)
					continue
				}
				o.containerEvents <- newContainerEvent(podID, containerID, ContainerTaskIDDone, event.Name)
				klog.Infof("dir create: %v, %v, %v", event.Name, podID, containerID)
			case FileUpdated:
				klog.Infof("cgroup.procs file updated %v", event)

				containerDir := filepath.Dir(event.Name)
				containerID, err := ParseContainerID(filepath.Base(containerDir))
				if err != nil {
					klog.Infof("get containerID failed")
					continue
				}
				podID, err := ParsePodID(filepath.Base(filepath.Dir(containerDir)))
				if err != nil {
					klog.Infof("get podID failed, %v", err)
					continue
				}
				o.containerEvents <- newContainerEvent(podID, containerID, ContainerTaskIDDone, event.Name)
				err = o.taskIDWatcher.RemoveWatch(event.Name)
				if err != nil {
					klog.Errorf("failed to remove watch path %s, err %v", event.Name, err)
				}
				klog.Infof("dir create: %v, %v, %v", event.Name, podID, containerID)
			}

		case <-stopCh:
			return
		}
	}
}

func ParsePodID(basename string) (string, error) {
	patterns := []struct {
		prefix string
		suffix string
	}{
		{
			prefix: "kubepods-besteffort-pod",
			suffix: ".slice",
		},
		{
			prefix: "kubepods-burstable-pod",
			suffix: ".slice",
		},

		{
			prefix: "kubepods-pod",
			suffix: ".slice",
		},
	}

	for i := range patterns {
		if strings.HasPrefix(basename, patterns[i].prefix) && strings.HasSuffix(basename, patterns[i].suffix) {
			podIDStr := basename[len(patterns[i].prefix) : len(basename)-len(patterns[i].suffix)]
			return strings.ReplaceAll(podIDStr, "_", "-"), nil
		}
	}
	return "", fmt.Errorf("fail to parse pod id: %v", basename)
}

func ParseContainerID(basename string) (string, error) {
	patterns := []struct {
		prefix string
		suffix string
	}{
		{
			prefix: "docker-",
			suffix: ".scope",
		},
		{
			prefix: "cri-containerd-",
			suffix: ".scope",
		},
	}

	for i := range patterns {
		if strings.HasPrefix(basename, patterns[i].prefix) && strings.HasSuffix(basename, patterns[i].suffix) {
			return basename[len(patterns[i].prefix) : len(basename)-len(patterns[i].suffix)], nil
		}
	}
	return "", fmt.Errorf("fail to parse container id: %v", basename)
}

func newPodEvent(podID string, eventType int, cgroupPath string) *PodEvent {
	return &PodEvent{eventType: eventType, podID: podID, cgroupPath: cgroupPath}
}

func newContainerEvent(podID string, containerID string, eventType int, cgroupPath string) *ContainerEvent {
	return &ContainerEvent{podID: podID, containerID: containerID, eventType: eventType, cgroupPath: cgroupPath}
}

func (o *OobImpl) GetAllPods() (corev1.PodList, error) {
	klog.V(5).Infof("Update the PodList")
	pods, err := o.kubeletStub.GetAllPods()
	if err != nil {
		return pods, err
	}
	for _, p := range pods.Items {
		if _, ok := o.pods[string(p.GetObjectMeta().GetUID())]; !ok {
			o.pods[string(p.GetObjectMeta().GetUID())] = p
		}
	}
	return pods, err
}
