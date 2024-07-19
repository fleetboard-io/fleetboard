package dedinic

import (
	"context"
	"time"

	"github.com/cfanbo/delayqueue"
	"k8s.io/klog/v2"
)

var DelayQueue *delayqueue.Queue

func InitDelayQueue() {
	klog.Info("delay queue started 1")
	DelayQueue = delayqueue.New()
	DelayQueue.Run(context.Background(), consume)
	klog.Error(" delay queue crashed")
}

func consume(entry delayqueue.Entry) {
	klog.Info("delay queue consume pod", entry.Body())

	podRequest := entry.Body().(*CniRequest)

	err := csh.handleAdd(podRequest)
	if err != nil {
		klog.Errorf("add interface failed for pod: %v", podRequest)
		DelayQueue.Put(time.Now().Add(time.Second*3), podRequest)
	}
}
