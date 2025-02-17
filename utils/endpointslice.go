package utils

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	discoverylisterv1 "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"

	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/metal-stack/go-ipam"
)

type IPAM struct {
	ipamer ipam.Ipamer
	prefix *ipam.Prefix
}

// NewIPAM create new ipamer
func NewIPAM() *IPAM {
	ctx := context.Background()
	return &IPAM{
		ipamer: ipam.New(ctx),
	}
}

// GetServiceCIDRFromIP get existing virtual service cidr from existing endpointslices
func GetServiceCIDRFromIP(ip string) (string, error) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return "", fmt.Errorf("invalid IP address: %s", ip)
	}

	ipNet := &net.IPNet{
		IP:   parsedIP.Mask(net.CIDRMask(24, 32)),
		Mask: net.CIDRMask(24, 32),
	}
	return ipNet.String(), nil
}

func GenerateRandomServiceCIDR(existingCIDRs ...string) (string, error) {
	// RFC1918 private address pool
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}

	for i := 0; i < 100; i++ {
		maxLen := big.NewInt(int64(len(privateRanges)))
		index, err := rand.Int(rand.Reader, maxLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random index: %v", err)
		}
		selectedRange := privateRanges[index.Int64()]

		_, cidrNet, err := net.ParseCIDR(selectedRange)
		if err != nil {
			return "", fmt.Errorf("failed to parse private range: %v", err)
		}

		randomIP := make(net.IP, len(cidrNet.IP.To4()))
		copy(randomIP, cidrNet.IP.To4())

		thirdByte, err := rand.Int(rand.Reader, big.NewInt(256))
		if err != nil {
			return "", fmt.Errorf("failed to generate random third byte: %v", err)
		}
		randomIP[2] = byte(thirdByte.Int64())
		randomIP[3] = 0
		randomCIDR := fmt.Sprintf("%s/24", randomIP.String())

		conflict := false
		for _, existing := range existingCIDRs {
			if isOverlappingCIDR(randomCIDR, existing) {
				conflict = true
				break
			}
		}

		if !conflict {
			return randomCIDR, nil
		}
	}

	return "", errors.New("failed to generate a non-conflicting /24 CIDR after 100 attempts")
}

func NewRandomServiceCIDR(kubeClientSet kubernetes.Interface) (string, error) {
	// random generate /24 CIDR
	serviceCIDR, err := FindClusterServiceIPRange(kubeClientSet)
	if err != nil {
		return "", fmt.Errorf("failed to find service CIDR: %v", err)
	}
	podCIDR, err := FindClusterPodIPRange(kubeClientSet)
	if err != nil {
		return "", fmt.Errorf("failed to find pod CIDR: %v", err)
	}
	klog.Infof("Detected cluster service/pod CIDR: %s, %s", serviceCIDR, podCIDR)

	newCIDR, err := GenerateRandomServiceCIDR(serviceCIDR, podCIDR)
	if err != nil {
		return "", fmt.Errorf("failed to generate random CIDR: %v", err)
	}
	return newCIDR, nil
}

// GetServiceCIDRFromCNFPod get existing virtual service CIDR form cnf pod annotations
func GetServiceCIDRFromCNFPod(kubeClientSet kubernetes.Interface) (string, error) {
	podList, err := kubeClientSet.CoreV1().Pods(known.FleetboardSystemNamespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: known.LabelCNFPod,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list cnf pods: %v", err)
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		cidrs := GetSpecificAnnotation(pod, known.FleetboardServiceCIDR)
		if len(cidrs) > 0 && len(cidrs[0]) > 0 {
			return cidrs[0], nil
		}
	}
	return "", nil
}

// GetServiceCIDR get existing virtual service CIDR and IPs
func GetServiceCIDR(mcsClientSet *mcsclientset.Clientset, targetNamespace string,
	kubeClientSet kubernetes.Interface) (string, []string, error) {
	var virtualServiceIPs []string
	if localSIList, err := mcsClientSet.MulticlusterV1alpha1().ServiceImports(targetNamespace).
		List(context.Background(), metav1.ListOptions{}); err != nil {
		return "", nil, fmt.Errorf("failed to list service import: %v", err)
	} else {
		for _, localServiceImport := range localSIList.Items {
			// has virtual service IP
			if localServiceImport.Spec.Type == v1alpha1.ClusterSetIP && len(localServiceImport.Spec.IPs) != 0 &&
				len(localServiceImport.Spec.IPs[0]) != 0 {
				virtualServiceIPs = append(virtualServiceIPs, localServiceImport.Spec.IPs[0])
			}
		}
	}

	if len(virtualServiceIPs) > 0 {
		// if we get one IP, use the first one to determine /24 CIDR
		newCIDR, err := GetServiceCIDRFromIP(virtualServiceIPs[0])
		if err != nil {
			return "", nil, fmt.Errorf("failed to get service CIDR from IP: %v", err)
		}
		return newCIDR, virtualServiceIPs, nil
	}

	// get cidr from cnf pod annotation to avoid regenerating every new leader selected
	newCIDR, err := GetServiceCIDRFromCNFPod(kubeClientSet)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get service CIDR from cnf pod: %v", err)
	}

	return newCIDR, virtualServiceIPs, nil
}

// InitNewCIDR init a CIDR to allocate local-cluster-range ip for imported multi-cluster virtual services
func (i *IPAM) InitNewCIDR(mcsClientSet *mcsclientset.Clientset, targetNamespace string,
	kubeClientSet kubernetes.Interface) (string, error) {
	newCIDR, virtualServiceIPs, err := GetServiceCIDR(mcsClientSet, targetNamespace, kubeClientSet)
	if err != nil {
		return "", fmt.Errorf("failed to get service CIDR: %v", err)
	}

	if newCIDR != "" {
		klog.Infof("Using existing service CIDR and IPs: %s, %v", newCIDR, virtualServiceIPs)
	} else {
		cidr, err := NewRandomServiceCIDR(kubeClientSet)
		if err != nil {
			return "", fmt.Errorf("failed to new service CIDR: %v", err)
		}
		newCIDR = cidr
		klog.Infof("New random non-conflicting service CIDR: %s", newCIDR)
	}

	if err := i.InitPrefix(newCIDR); err != nil {
		klog.Errorf("failed to initialize prefix: %v", err)
		return "", err
	}

	initExistingErr := i.InitializeExistingIPs(virtualServiceIPs)
	if initExistingErr != nil {
		klog.Errorf("failed to initialize existing ips: %v", initExistingErr)
		return "", initExistingErr
	}

	return newCIDR, nil
}

// InitPrefix Init prefix
func (i *IPAM) InitPrefix(cidr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefix, err := i.ipamer.NewPrefix(ctx, cidr)
	if err != nil {
		klog.Errorf("Init CIDR %s: with error %v", cidr, err)
		return err
	}

	i.prefix = prefix
	return nil
}

// InitializeExistingIPs add allocated ips into IPAM cache
func (i *IPAM) InitializeExistingIPs(ips []string) error {
	ctx := context.Background()
	for _, ip := range ips {
		_, err := i.ipamer.AcquireSpecificIP(ctx, i.prefix.Cidr, ip)
		if err != nil {
			klog.Infof("Failed to initialize IP %s: %v", ip, err)
			return err
		} else {
			klog.Infof("Initialized existing IP: %s", ip)
		}
	}

	return nil
}

func (i *IPAM) AllocateIP() (string, error) {
	ctx := context.Background()
	ip, err := i.ipamer.AcquireIP(ctx, i.prefix.Cidr)
	if err != nil {
		klog.Errorf("failed to allocate IP: %v", err)
		return "", err
	}
	klog.Infof("Successfully allocated IP from %s: %s", i.prefix.Cidr, ip.IP.String())
	return ip.IP.String(), nil
}

func (i *IPAM) ReleaseIP(ipAddr string) error {
	ctx := context.Background()
	ip, err := netip.ParseAddr(ipAddr)
	if err != nil {
		klog.Errorf("failed to release IP %s: %v", ipAddr, err)
		return err
	}
	ipaddr := ipam.IP{
		IP:           ip,
		ParentPrefix: i.prefix.Cidr,
	}
	_, err = i.ipamer.ReleaseIP(ctx, &ipaddr)
	if err != nil {
		klog.Errorf("failed to release IP %s: %v", ipAddr, err)
		return err
	}

	klog.Infof("IP %s has been released", ipAddr)
	return nil
}

// ApplyEndPointSliceWithRetry create or update existed slices.
func ApplyEndPointSliceWithRetry(client kubernetes.Interface, slice *discoveryv1.EndpointSlice) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		var lastError error
		_, lastError = client.DiscoveryV1().EndpointSlices(slice.GetNamespace()).
			Create(context.TODO(), slice, metav1.CreateOptions{})
		if lastError == nil {
			return nil
		}
		if !k8serrors.IsAlreadyExists(lastError) {
			return lastError
		}

		curObj, err := client.DiscoveryV1().EndpointSlices(slice.GetNamespace()).
			Get(context.TODO(), slice.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		lastError = nil

		if ResourceNeedResync(curObj, slice, false) {
			// try to update slice
			curObj.Ports = slice.Ports
			curObj.Endpoints = slice.Endpoints
			curObj.AddressType = slice.AddressType
			_, lastError = client.DiscoveryV1().EndpointSlices(slice.GetNamespace()).
				Update(context.TODO(), curObj, metav1.UpdateOptions{})
			if lastError == nil {
				return nil
			}
		}
		return lastError
	})
}

func RemoveNonexistentEndpointslice(
	srcLister discoverylisterv1.EndpointSliceLister,
	srcClusterID string,
	srcNamespace string,
	labelMap labels.Set,
	targetClient kubernetes.Interface,
	targetNamespace string,
	dstLabelMap labels.Set,
	nameChanged bool,
) ([]*discoveryv1.EndpointSlice, error) {
	srcEndpointSliceList, err := srcLister.EndpointSlices(srcNamespace).List(
		labels.SelectorFromSet(labelMap))
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return nil, err
		}
	}
	// remove endpoint slices exist in delicate ns but not in target ns
	srcEndpointSliceMap := make(map[string]bool)
	for _, item := range srcEndpointSliceList {
		// change name in a pattern
		if nameChanged {
			srcEndpointSliceMap[fmt.Sprintf("%s-%s-%s", srcClusterID, item.Namespace, item.Name)] = true
		} else {
			srcEndpointSliceMap[item.Name] = true
		}
	}

	var targetEndpointSliceList *discoveryv1.EndpointSliceList
	targetEndpointSliceList, err = targetClient.DiscoveryV1().EndpointSlices(targetNamespace).List(
		context.TODO(),
		metav1.ListOptions{
			LabelSelector: labels.SelectorFromSet(dstLabelMap).String(),
		},
	)
	if err == nil {
		for _, item := range targetEndpointSliceList.Items {
			if !srcEndpointSliceMap[item.Name] {
				if err = targetClient.DiscoveryV1().EndpointSlices(targetNamespace).
					Delete(context.TODO(), item.Name, metav1.DeleteOptions{}); err != nil {
					utilruntime.HandleError(fmt.Errorf("the endpointclise"+
						" '%s/%s' in target namespace deleted failed", item.Namespace, item.Name))
					return nil, err
				}
			}
		}
	}
	return srcEndpointSliceList, nil
}
