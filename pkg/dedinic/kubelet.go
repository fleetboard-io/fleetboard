package dedinic

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

var defaultAPIAuthTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
var defaultKubeletCertFile = "/opt/ioi/pki/kubelet.crt"

type KubeletStub interface {
	GetAllPods() (corev1.PodList, error)
}

type kubeletStub struct {
	addr       string
	port       int
	scheme     string
	httpClient *http.Client
	token      string
}

func (k kubeletStub) GetAllPods() (corev1.PodList, error) {
	url := url.URL{
		Scheme: k.scheme,
		Host:   net.JoinHostPort(k.addr, strconv.Itoa(k.port)),
		Path:   "/pods/",
	}
	podList := corev1.PodList{}

	var bearer = "Bearer " + k.token
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		klog.Errorf("Construct http request failed, %v", err)
	}
	req.Header.Add("Authorization", bearer)
	req.Header.Add("Accept", "application/json")
	rsp, err := k.httpClient.Do(req)
	if err != nil {
		klog.Errorf("http get pods err is %v", err)
		return podList, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		klog.Errorf("response status is not http.StatusOK, err is %v, rsp is %v", err, rsp)
		return podList, fmt.Errorf("request %s failed, code %d", url.String(), rsp.StatusCode)
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		klog.Errorf("http parse response body error, err is %v", err)
		return podList, err
	}

	// parse json data
	err = json.Unmarshal(body, &podList)
	if err != nil {
		return podList, fmt.Errorf("parse kubelet pod list failed, err: %v", err)
	}
	return podList, nil
}

func NewKubeletStub(addr string, port int, scheme string, timeout time.Duration) (KubeletStub, error) {
	token, err := os.ReadFile(defaultAPIAuthTokenFile)
	if err != nil {
		klog.Errorf("no token file, %v", err)
	}

	cert, err := os.ReadFile(defaultKubeletCertFile)
	if err != nil {
		klog.Errorf("no cert file, %v", err)
		return nil, fmt.Errorf("no cert file, %v", err)
	}

	// Create a certificate pool and add the kubelet certificate
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(cert)

	// Create a TLS configuration with the loaded certificate and private key
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{cert},
			//              PrivateKey:  key,
		}},
		RootCAs: caCertPool,
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return &kubeletStub{
		httpClient: client,
		addr:       addr,
		port:       port,
		scheme:     scheme,
		token:      string(token),
	}, nil
}
