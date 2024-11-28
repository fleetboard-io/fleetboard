// Package app does all of the work necessary to configure and run a
// Kubernetes app process.
package app

import (
	goflag "flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/wait"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/events"
	cliflag "k8s.io/component-base/cli/flag"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/component-base/configz"
	"k8s.io/component-base/logs"
	logsapi "k8s.io/component-base/logs/api/v1"
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/version"
	"k8s.io/component-base/version/verflag"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/klog/v2"
	"k8s.io/kube-proxy/config/v1alpha1"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/kubelet/qos"
	"k8s.io/kubernetes/pkg/proxy/apis"
	kubeproxyconfig "k8s.io/kubernetes/pkg/proxy/apis/config"
	proxyconfigscheme "k8s.io/kubernetes/pkg/proxy/apis/config/scheme"
	"k8s.io/kubernetes/pkg/proxy/apis/config/validation"
	"k8s.io/kubernetes/pkg/proxy/healthcheck"
	"k8s.io/kubernetes/pkg/util/filesystem"
	utilflag "k8s.io/kubernetes/pkg/util/flag"
	utilnode "k8s.io/kubernetes/pkg/util/node"
	"k8s.io/kubernetes/pkg/util/oom"
	netutils "k8s.io/utils/net"
	"k8s.io/utils/pointer" //nolint:all
	mcsclientset "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"
	mcsInformers "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions"

	"github.com/fleetboard-io/fleetboard/pkg/known"
	"github.com/fleetboard-io/fleetboard/pkg/proxy"
	"github.com/fleetboard-io/fleetboard/pkg/proxy/config"
)

func init() {
	utilruntime.Must(logsapi.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
}

// proxyRun defines the interface to run a specified ProxyServer
type proxyRun interface {
	Run() error
}

// Options contains everything necessary to create and run a proxy server.
type Options struct {
	// ConfigFile is the location of the proxy server's configuration file.
	ConfigFile string
	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string
	// CleanupAndExit, when true, makes the proxy server clean up iptables and ipvs rules, then exit.
	CleanupAndExit bool
	// config is the proxy server's configuration object.
	config *kubeproxyconfig.KubeProxyConfiguration
	// watcher is used to watch on the update change of ConfigFile
	watcher filesystem.FSWatcher
	// proxyServer is the interface to run the proxy server
	proxyServer proxyRun
	// errCh is the channel that errors will be sent
	errCh chan error

	// The fields below here are placeholders for flags that can't be directly mapped into
	// config.KubeProxyConfiguration.
	//
	// TODO remove these fields once the deprecated flags are removed.

	// master is used to override the kubeconfig's URL to the apiserver.
	master string

	// hostnameOverride, if set from the command line flag, takes precedence over the `HostnameOverride`
	// value from the config file
	hostnameOverride string
}

// AddFlags adds flags to fs and binds them to options.
//
//nolint:all
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.ConfigFile, "config", o.ConfigFile, "The path to the configuration file.")
	fs.StringVar(&o.WriteConfigTo, "write-config-to", o.WriteConfigTo, "If set, write the default configuration values to this file and exit.")
	fs.StringVar(&o.config.ClientConnection.Kubeconfig, "kubeconfig", o.config.ClientConnection.Kubeconfig, "Path to kubeconfig file with authorization information (the master location can be overridden by the master flag).")
	fs.StringVar(&o.config.ClusterCIDR, "cluster-cidr", o.config.ClusterCIDR, "The CIDR range of pods in the cluster. When configured, traffic sent to a Service cluster IP from outside this range will be masqueraded and traffic sent from pods to an external LoadBalancer IP will be directed to the respective cluster IP instead. "+
		"For dual-stack clusters, a comma-separated list is accepted with at least one CIDR per IP family (IPv4 and IPv6). This parameter is ignored if a config file is specified by --config.")
	fs.StringVar(&o.config.ClientConnection.ContentType, "kube-api-content-type", o.config.ClientConnection.ContentType, "Content type of requests sent to apiserver.")
	fs.StringVar(&o.master, "master", o.master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&o.hostnameOverride, "hostname-override", o.hostnameOverride, "If non-empty, will use this string as identification instead of the actual hostname.")
	fs.StringVar(&o.config.IPVS.Scheduler, "ipvs-scheduler", o.config.IPVS.Scheduler, "The ipvs scheduler type when proxy mode is ipvs")

	fs.StringSliceVar(&o.config.IPVS.ExcludeCIDRs, "ipvs-exclude-cidrs", o.config.IPVS.ExcludeCIDRs, "A comma-separated list of CIDR's which the ipvs proxier should not touch when cleaning up IPVS rules.")
	fs.StringSliceVar(&o.config.NodePortAddresses, "nodeport-addresses", o.config.NodePortAddresses,
		"A string slice of values which specify the addresses to use for NodePorts. Values may be valid IP blocks (e.g. 1.2.3.0/24, 1.2.3.4/32). The default empty string slice ([]) means to use all local addresses. This parameter is ignored if a config file is specified by --config.")

	fs.BoolVar(&o.CleanupAndExit, "cleanup", o.CleanupAndExit, "If true cleanup iptables and ipvs rules and exit.")

	fs.Var(&utilflag.IPVar{Val: &o.config.BindAddress}, "bind-address", "The IP address for the proxy server to serve on (set to '0.0.0.0' for all IPv4 interfaces and '::' for all IPv6 interfaces). This parameter is ignored if a config file is specified by --config.")
	fs.Var(&utilflag.IPPortVar{Val: &o.config.HealthzBindAddress}, "healthz-bind-address", "The IP address with port for the health check server to serve on (set to '0.0.0.0:10256' for all IPv4 interfaces and '[::]:10256' for all IPv6 interfaces). Set empty to disable. This parameter is ignored if a config file is specified by --config.")
	fs.Var(&utilflag.IPPortVar{Val: &o.config.MetricsBindAddress}, "metrics-bind-address", "The IP address with port for the metrics server to serve on (set to '0.0.0.0:10249' for all IPv4 interfaces and '[::]:10249' for all IPv6 interfaces). Set empty to disable. This parameter is ignored if a config file is specified by --config.")
	fs.BoolVar(&o.config.BindAddressHardFail, "bind-address-hard-fail", o.config.BindAddressHardFail, "If true kube-proxy will treat failure to bind to a port as fatal and exit")
	fs.Var(utilflag.PortRangeVar{Val: &o.config.PortRange}, "proxy-port-range", "Range of host ports (beginPort-endPort, single port or beginPort+offset, inclusive) that may be consumed in order to proxy service traffic. If (unspecified, 0, or 0-0) then ports will be randomly chosen.")
	fs.Var(&o.config.Mode, "proxy-mode", "Which proxy mode to use: on Linux this can be 'iptables' (default) or 'ipvs'. On Windows the only supported value is 'kernelspace'."+
		"This parameter is ignored if a config file is specified by --config.")
	fs.Var(cliflag.NewMapStringBool(&o.config.FeatureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(utilfeature.DefaultFeatureGate.KnownFeatures(), "\n")+"\n"+
		"This parameter is ignored if a config file is specified by --config.")

	fs.Int32Var(o.config.OOMScoreAdj, "oom-score-adj", pointer.Int32Deref(o.config.OOMScoreAdj, int32(qos.KubeProxyOOMScoreAdj)), "The oom-score-adj value for kube-proxy process. Values must be within the range [-1000, 1000]. This parameter is ignored if a config file is specified by --config.")
	fs.Int32Var(o.config.IPTables.MasqueradeBit, "iptables-masquerade-bit", pointer.Int32Deref(o.config.IPTables.MasqueradeBit, 14), "If using the pure iptables proxy, the bit of the fwmark space to mark packets requiring SNAT with.  Must be within the range [0, 31].")
	fs.Int32Var(o.config.Conntrack.MaxPerCore, "conntrack-max-per-core", *o.config.Conntrack.MaxPerCore,
		"Maximum number of NAT connections to track per CPU core (0 to leave the limit as-is and ignore conntrack-min).")
	fs.Int32Var(o.config.Conntrack.Min, "conntrack-min", *o.config.Conntrack.Min,
		"Minimum number of conntrack entries to allocate, regardless of conntrack-max-per-core (set conntrack-max-per-core=0 to leave the limit as-is).")
	fs.Int32Var(&o.config.ClientConnection.Burst, "kube-api-burst", o.config.ClientConnection.Burst, "Burst to use while talking with kubernetes apiserver")

	fs.DurationVar(&o.config.IPTables.SyncPeriod.Duration, "iptables-sync-period", o.config.IPTables.SyncPeriod.Duration, "The maximum interval of how often iptables rules are refreshed (e.g. '5s', '1m', '2h22m').  Must be greater than 0.")
	fs.DurationVar(&o.config.IPTables.MinSyncPeriod.Duration, "iptables-min-sync-period", o.config.IPTables.MinSyncPeriod.Duration, "The minimum interval of how often the iptables rules can be refreshed as endpoints and services change (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.SyncPeriod.Duration, "ipvs-sync-period", o.config.IPVS.SyncPeriod.Duration, "The maximum interval of how often ipvs rules are refreshed (e.g. '5s', '1m', '2h22m').  Must be greater than 0.")
	fs.DurationVar(&o.config.IPVS.MinSyncPeriod.Duration, "ipvs-min-sync-period", o.config.IPVS.MinSyncPeriod.Duration, "The minimum interval of how often the ipvs rules can be refreshed as endpoints and services change (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.TCPTimeout.Duration, "ipvs-tcp-timeout", o.config.IPVS.TCPTimeout.Duration, "The timeout for idle IPVS TCP connections, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.TCPFinTimeout.Duration, "ipvs-tcpfin-timeout", o.config.IPVS.TCPFinTimeout.Duration, "The timeout for IPVS TCP connections after receiving a FIN packet, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.IPVS.UDPTimeout.Duration, "ipvs-udp-timeout", o.config.IPVS.UDPTimeout.Duration, "The timeout for IPVS UDP packets, 0 to leave as-is. (e.g. '5s', '1m', '2h22m').")
	fs.DurationVar(&o.config.Conntrack.TCPEstablishedTimeout.Duration, "conntrack-tcp-timeout-established", o.config.Conntrack.TCPEstablishedTimeout.Duration, "Idle timeout for established TCP connections (0 to leave as-is)")
	fs.DurationVar(
		&o.config.Conntrack.TCPCloseWaitTimeout.Duration, "conntrack-tcp-timeout-close-wait",
		o.config.Conntrack.TCPCloseWaitTimeout.Duration,
		"NAT timeout for TCP connections in the CLOSE_WAIT state")
	fs.DurationVar(&o.config.ConfigSyncPeriod.Duration, "config-sync-period", o.config.ConfigSyncPeriod.Duration, "How often configuration from the apiserver is refreshed.  Must be greater than 0.")

	fs.BoolVar(&o.config.IPVS.StrictARP, "ipvs-strict-arp", o.config.IPVS.StrictARP, "Enable strict ARP by setting arp_ignore to 1 and arp_announce to 2")
	fs.BoolVar(&o.config.IPTables.MasqueradeAll, "masquerade-all", o.config.IPTables.MasqueradeAll, "If using the pure iptables proxy, SNAT all traffic sent via Service cluster IPs (this not commonly needed)")
	fs.BoolVar(o.config.IPTables.LocalhostNodePorts, "iptables-localhost-nodeports", pointer.BoolDeref(o.config.IPTables.LocalhostNodePorts, true), "If false Kube-proxy will disable the legacy behavior of allowing NodePort services to be accessed via localhost, This only applies to iptables mode and ipv4.")
	fs.BoolVar(&o.config.EnableProfiling, "profiling", o.config.EnableProfiling, "If true enables profiling via web interface on /debug/pprof handler. This parameter is ignored if a config file is specified by --config.")

	fs.Float32Var(&o.config.ClientConnection.QPS, "kube-api-qps", o.config.ClientConnection.QPS, "QPS to use while talking with kubernetes apiserver")
	fs.Var(&o.config.DetectLocalMode, "detect-local-mode", "Mode to use to detect local traffic. This parameter is ignored if a config file is specified by --config.")
	fs.StringVar(&o.config.DetectLocal.BridgeInterface, "pod-bridge-interface", o.config.DetectLocal.BridgeInterface, "A bridge interface name in the cluster. Kube-proxy considers traffic as local if originating from an interface which matches the value. This argument should be set if DetectLocalMode is set to BridgeInterface.")
	fs.StringVar(&o.config.DetectLocal.InterfaceNamePrefix, "pod-interface-name-prefix", o.config.DetectLocal.InterfaceNamePrefix, "An interface prefix in the cluster. Kube-proxy considers traffic as local if originating from interfaces that match the given prefix. This argument should be set if DetectLocalMode is set to InterfaceNamePrefix.")
	logsapi.AddFlags(&o.config.Logging, fs)
}

// newKubeProxyConfiguration returns a KubeProxyConfiguration with default values
func newKubeProxyConfiguration() *kubeproxyconfig.KubeProxyConfiguration {
	versionedConfig := &v1alpha1.KubeProxyConfiguration{}
	proxyconfigscheme.Scheme.Default(versionedConfig)
	internalConfig, err := proxyconfigscheme.Scheme.ConvertToVersion(versionedConfig, kubeproxyconfig.SchemeGroupVersion)
	if err != nil {
		panic(fmt.Sprintf("Unable to create default config: %v", err))
	}

	return internalConfig.(*kubeproxyconfig.KubeProxyConfiguration)
}

// NewOptions returns initialized Options
func NewOptions() *Options {
	return &Options{
		config: newKubeProxyConfiguration(),
		errCh:  make(chan error),
	}
}

// Complete completes all the required options.
func (o *Options) Complete(fs *pflag.FlagSet) error {
	o.platformApplyDefaults(o.config)

	if err := o.processHostnameOverrideFlag(); err != nil {
		return err
	}

	return utilfeature.DefaultMutableFeatureGate.SetFromMap(o.config.FeatureGates)
}

// processHostnameOverrideFlag processes hostname-override flag
func (o *Options) processHostnameOverrideFlag() error {
	// Check if hostname-override flag is set and use value since configFile always overrides
	if len(o.hostnameOverride) > 0 {
		hostName := strings.TrimSpace(o.hostnameOverride)
		if len(hostName) == 0 {
			return fmt.Errorf("empty hostname-override is invalid")
		}
		o.config.HostnameOverride = strings.ToLower(hostName)
	}

	return nil
}

// Validate validates all the required options.
func (o *Options) Validate() error {
	if errs := validation.Validate(o.config); len(errs) != 0 {
		return errs.ToAggregate()
	}

	return nil
}

// Run runs the specified ProxyServer.
func (o *Options) Run() error {
	defer close(o.errCh)
	if len(o.WriteConfigTo) > 0 {
		return o.writeConfigFile()
	}

	if o.CleanupAndExit {
		return cleanupAndExit()
	}

	klog.Infof("try to create proxyServer")
	proxyServer, err := newProxyServer(o.config, o.master)
	if err != nil {
		return err
	}

	o.proxyServer = proxyServer
	return o.runLoop()
}

// runLoop will watch on the update change of the proxy server's configuration file.
// Return an error when updated
func (o *Options) runLoop() error {
	if o.watcher != nil {
		o.watcher.Run()
	}

	// run the proxy in goroutine
	go func() {
		err := o.proxyServer.Run()
		o.errCh <- err
	}()

	for {
		err := <-o.errCh
		if err != nil {
			return err
		}
	}
}

func (o *Options) writeConfigFile() (err error) {
	const mediaType = runtime.ContentTypeYAML
	info, ok := runtime.SerializerInfoForMediaType(proxyconfigscheme.Codecs.SupportedMediaTypes(), mediaType)
	if !ok {
		return fmt.Errorf("unable to locate encoder -- %q is not a supported media type", mediaType)
	}

	encoder := proxyconfigscheme.Codecs.EncoderForVersion(info.Serializer, v1alpha1.SchemeGroupVersion)

	configFile, err := os.Create(o.WriteConfigTo)
	if err != nil {
		return err
	}

	defer func() {
		ferr := configFile.Close()
		if ferr != nil && err == nil {
			err = ferr
		}
	}()

	if err = encoder.Encode(o.config, configFile); err != nil {
		return err
	}

	klog.InfoS("Wrote configuration", "file", o.WriteConfigTo)

	return nil
}

// NewProxyCommand creates a *cobra.Command object with default parameters
func NewProxyCommand() *cobra.Command {
	opts := NewOptions()

	cmd := &cobra.Command{
		Use: "kube-proxy",
		Long: `The Kubernetes network proxy runs on each node. This
reflects services as defined in the Kubernetes API on each node and can do simple
TCP, UDP, and SCTP stream forwarding or round robin TCP, UDP, and SCTP forwarding across a set of backends.
Service cluster IPs and ports are currently found through Docker-links-compatible
environment variables specifying ports opened by the service proxy. There is an optional
addon that provides cluster DNS for these cluster IPs. The user must create a service
with the apiserver API to configure the proxy.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			verflag.PrintAndExitIfRequested()

			if err := opts.Complete(cmd.Flags()); err != nil {
				return fmt.Errorf("failed complete: %w", err)
			}

			logs.InitLogs()
			if err := logsapi.ValidateAndApplyAsField(&opts.config.Logging, utilfeature.DefaultFeatureGate,
				field.NewPath("logging")); err != nil {
				return fmt.Errorf("initialize logging: %v", err)
			}

			cliflag.PrintFlags(cmd.Flags())

			if err := opts.Validate(); err != nil {
				return fmt.Errorf("failed validate: %w", err)
			}
			// add feature enablement metrics
			utilfeature.DefaultMutableFeatureGate.AddMetrics()
			if err := opts.Run(); err != nil {
				klog.ErrorS(err, "Error running ProxyServer")
				return err
			}

			return nil
		},
		Args: func(cmd *cobra.Command, args []string) error {
			for _, arg := range args {
				if len(arg) > 0 {
					return fmt.Errorf("%q does not take any arguments, got %q", cmd.CommandPath(), args)
				}
			}
			return nil
		},
	}

	fs := cmd.Flags()
	opts.AddFlags(fs)
	fs.AddGoFlagSet(goflag.CommandLine) // for --boot-id-file and --machine-id-file

	_ = cmd.MarkFlagFilename("config", "yaml", "yml", "json")

	return cmd
}

// ProxyServer represents all the parameters required to start the Kubernetes proxy server. All
// fields are required.
type ProxyServer struct {
	Config *kubeproxyconfig.KubeProxyConfiguration

	KubeConfig      *rest.Config
	Client          clientset.Interface
	Broadcaster     events.EventBroadcaster
	Recorder        events.EventRecorder
	NodeRef         *v1.ObjectReference
	HealthzServer   healthcheck.ProxierHealthUpdater
	Hostname        string
	PrimaryIPFamily v1.IPFamily
	NodeIPs         map[v1.IPFamily]net.IP

	podCIDRs []string // only used for LocalModeNodeCIDR

	Proxier proxy.Provider
}

// newProxyServer creates a ProxyServer based on the given config
func newProxyServer(config *kubeproxyconfig.KubeProxyConfiguration, master string) (*ProxyServer, error) {
	s := &ProxyServer{Config: config}

	cz, err := configz.New(kubeproxyconfig.GroupName)
	if err != nil {
		return nil, fmt.Errorf("unable to register configz: %s", err)
	}
	cz.Set(config)

	if len(config.ShowHiddenMetricsForVersion) > 0 {
		metrics.SetShowHidden()
	}

	s.Hostname, err = nodeutil.GetHostname(config.HostnameOverride)
	if err != nil {
		return nil, err
	}

	s.Client, s.KubeConfig, err = createClient(config.ClientConnection, master)
	if err != nil {
		return nil, err
	}

	s.PrimaryIPFamily, s.NodeIPs = detectNodeIPs(s.Client, s.Hostname, config.BindAddress)

	s.Broadcaster = events.NewBroadcaster(&events.EventSinkImpl{Interface: s.Client.EventsV1()})
	s.Recorder = s.Broadcaster.NewRecorder(proxyconfigscheme.Scheme, "kube-proxy")

	s.NodeRef = &v1.ObjectReference{
		Kind:      "Node",
		Name:      s.Hostname,
		UID:       types.UID(s.Hostname),
		Namespace: "",
	}

	if len(config.HealthzBindAddress) > 0 {
		s.HealthzServer = healthcheck.NewProxierHealthServer(config.HealthzBindAddress,
			2*config.IPTables.SyncPeriod.Duration, s.Recorder, s.NodeRef)
	}

	err = s.platformSetup()
	if err != nil {
		return nil, err
	}

	ipv4Supported, ipv6Supported, dualStackSupported, err := s.platformCheckSupported()
	//nolint:all
	if err != nil {
		return nil, err
	} else if (s.PrimaryIPFamily == v1.IPv4Protocol && !ipv4Supported) ||
		(s.PrimaryIPFamily == v1.IPv6Protocol && !ipv6Supported) {
		return nil, fmt.Errorf("no support for primary IP family %q", s.PrimaryIPFamily)
	} else if dualStackSupported {
		klog.InfoS("kube-proxy running in dual-stack mode", "primary ipFamily", s.PrimaryIPFamily)
	} else {
		klog.InfoS("kube-proxy running in single-stack mode", "ipFamily", s.PrimaryIPFamily)
	}

	err, fatal := checkIPConfig(s, dualStackSupported)
	if err != nil {
		if fatal {
			return nil, fmt.Errorf("kube-proxy configuration is incorrect: %v", err)
		}
		klog.ErrorS(err, "Kube-proxy configuration may be incomplete or incorrect")
	}

	s.Proxier, err = s.createProxier(config)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// checkIPConfig confirms that s has proper configuration for its primary IP family.
func checkIPConfig(s *ProxyServer, dualStackSupported bool) (error, bool) {
	var errors []error
	var badFamily netutils.IPFamily

	if s.PrimaryIPFamily == v1.IPv4Protocol {
		badFamily = netutils.IPv6
	} else {
		badFamily = netutils.IPv4
	}

	var clusterType string
	if dualStackSupported {
		clusterType = fmt.Sprintf("%s-primary", s.PrimaryIPFamily)
	} else {
		clusterType = fmt.Sprintf("%s-only", s.PrimaryIPFamily)
	}

	// Historically, we did not check most of the config options, so we cannot
	// retroactively make IP family mismatches in those options be fatal. When we add
	// new options to check here, we should make problems with those options be fatal.
	fatal := false

	if s.Config.ClusterCIDR != "" {
		clusterCIDRs := strings.Split(s.Config.ClusterCIDR, ",")
		if badCIDRs(clusterCIDRs, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but clusterCIDRs contains only IPv%s addresses",
				clusterType, badFamily))
			if s.Config.DetectLocalMode == kubeproxyconfig.LocalModeClusterCIDR && !dualStackSupported {
				// This has always been a fatal error
				fatal = true
			}
		}
	}

	if badCIDRs(s.Config.NodePortAddresses, badFamily) {
		errors = append(errors, fmt.Errorf("cluster is %s but nodePortAddresses contains only IPv%s addresses",
			clusterType, badFamily))
	}

	if badCIDRs(s.podCIDRs, badFamily) {
		errors = append(errors, fmt.Errorf("cluster is %s but node.spec.podCIDRs contains only IPv%s addresses",
			clusterType, badFamily))
		if s.Config.DetectLocalMode == kubeproxyconfig.LocalModeNodeCIDR {
			// This has always been a fatal error
			fatal = true
		}
	}

	if netutils.IPFamilyOfString(s.Config.Winkernel.SourceVip) == badFamily {
		errors = append(errors,
			fmt.Errorf("cluster is %s but winkernel.sourceVip is IPv%s", clusterType, badFamily))
	}

	// In some cases, wrong-IP-family is only a problem when the secondary IP family
	// isn't present at all.
	if !dualStackSupported {
		if badCIDRs(s.Config.IPVS.ExcludeCIDRs, badFamily) {
			errors = append(errors,
				fmt.Errorf("cluster is %s but ipvs.excludeCIDRs contains only IPv%s addresses",
					clusterType, badFamily))
		}

		if badBindAddress(s.Config.HealthzBindAddress, badFamily) {
			errors = append(errors,
				fmt.Errorf("cluster is %s but healthzBindAddress is IPv%s", clusterType, badFamily))
		}
		if badBindAddress(s.Config.MetricsBindAddress, badFamily) {
			errors = append(errors, fmt.Errorf("cluster is %s but metricsBindAddress is IPv%s",
				clusterType, badFamily))
		}
	}

	return utilerrors.NewAggregate(errors), fatal
}

// badCIDRs returns true if cidrs is a non-empty list of CIDRs, all of wrongFamily.
func badCIDRs(cidrs []string, wrongFamily netutils.IPFamily) bool {
	if len(cidrs) == 0 {
		return false
	}
	for _, cidr := range cidrs {
		if netutils.IPFamilyOfCIDRString(cidr) != wrongFamily {
			return false
		}
	}
	return true
}

// badBindAddress returns true if bindAddress is an "IP:port" string where IP is a
// non-zero IP of wrongFamily.
func badBindAddress(bindAddress string, wrongFamily netutils.IPFamily) bool {
	if host, _, _ := net.SplitHostPort(bindAddress); host != "" {
		ip := netutils.ParseIPSloppy(host)
		if ip != nil && netutils.IPFamilyOf(ip) == wrongFamily && !ip.IsUnspecified() {
			return true
		}
	}
	return false
}

// createClient creates a kube client from the given config and masterOverride.
// TODO remove masterOverride when CLI flags are removed.
func createClient(config componentbaseconfig.ClientConnectionConfiguration, masterOverride string) (
	clientset.Interface, *rest.Config, error) {
	var kubeConfig *rest.Config
	var err error

	if len(config.Kubeconfig) == 0 && len(masterOverride) == 0 {
		klog.InfoS("Neither kubeconfig file nor master URL was specified, falling back to in-cluster config")
		kubeConfig, err = rest.InClusterConfig()
		if err != nil {
			kubeConfigPath := "~/.kube/config"
			// fallback to kube config
			if val := os.Getenv("KUBECONFIG"); len(val) != 0 {
				kubeConfigPath = val
			}
			kubeConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
			if err != nil {
				logrus.Fatalf("The kubeconfig cannot be loaded: %v\n", err)
			}
		}
	} else {
		// This creates a client, first loading any specified kubeconfig
		// file, and then overriding the Master flag, if non-empty.
		kubeConfig, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
			&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	}
	if err != nil {
		return nil, nil, err
	}

	kubeConfig.AcceptContentTypes = config.AcceptContentTypes
	kubeConfig.ContentType = config.ContentType
	kubeConfig.QPS = config.QPS
	kubeConfig.Burst = int(config.Burst)

	client, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, err
	}

	return client, kubeConfig, nil
}

// Run runs the specified ProxyServer.  This should never exit (unless CleanupAndExit is set).
// TODO: At the moment, Run() cannot return a nil error, otherwise it's caller will never exit.
// update callers of Run to handle nil errors.
func (s *ProxyServer) Run() error {
	// To help debugging, immediately log version
	klog.InfoS("Version info", "version", version.Get())

	klog.InfoS("Golang settings", "GOGC", os.Getenv("GOGC"), "GOMAXPROCS",
		os.Getenv("GOMAXPROCS"), "GOTRACEBACK", os.Getenv("GOTRACEBACK"))

	// TODO(vmarmol): Use container config for this.
	var oomAdjuster *oom.OOMAdjuster
	if s.Config.OOMScoreAdj != nil {
		oomAdjuster = oom.NewOOMAdjuster()
		if err := oomAdjuster.ApplyOOMScoreAdj(0, int(*s.Config.OOMScoreAdj)); err != nil {
			klog.V(2).InfoS("Failed to apply OOMScore", "err", err)
		}
	}

	if s.Broadcaster != nil {
		stopCh := make(chan struct{})
		s.Broadcaster.StartRecordingToSink(stopCh)
	}

	// TODO(thockin): make it possible for healthz and metrics to be on the same port.

	var errCh chan error
	if s.Config.BindAddressHardFail {
		errCh = make(chan error)
	}

	noProxyName, err := labels.NewRequirement(apis.LabelServiceProxyName, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	noHeadlessEndpoints, err := labels.NewRequirement(v1.IsHeadlessService, selection.DoesNotExist, nil)
	if err != nil {
		return err
	}

	labelSelector := labels.NewSelector()
	labelSelector = labelSelector.Add(*noProxyName, *noHeadlessEndpoints)

	// Make informers that filter out objects that want a non-default service proxy.
	informerFactory := informers.NewSharedInformerFactoryWithOptions(s.Client, s.Config.ConfigSyncPeriod.Duration,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labelSelector.String()
		}))

	mcsClientSet := mcsclientset.NewForConfigOrDie(s.KubeConfig)
	mcsInformerFactory := mcsInformers.NewSharedInformerFactory(mcsClientSet, known.DefaultResync)

	// Create configs (i.e. Watches for Services and EndpointSlices)
	// Note: RegisterHandler() calls need to happen before creation of Sources because sources
	// only notify on changes, and the initial update (on process start) may be lost if no handlers
	// are registered yet.
	serviceImportConfig := config.NewServiceImportConfig(mcsInformerFactory.Multicluster().V1alpha1().ServiceImports(),
		s.Config.ConfigSyncPeriod.Duration)
	serviceImportConfig.RegisterEventHandler(s.Proxier)
	go serviceImportConfig.Run(wait.NeverStop)

	endpointSliceConfig := config.NewEndpointSliceConfig(informerFactory.Discovery().V1().EndpointSlices(),
		s.Config.ConfigSyncPeriod.Duration)
	endpointSliceConfig.RegisterEventHandler(s.Proxier)
	go endpointSliceConfig.Run(wait.NeverStop)

	// This has to start after the calls to NewServiceConfig because that
	// function must configure its shared informer event handlers first.
	informerFactory.Start(wait.NeverStop)
	mcsInformerFactory.Start(wait.NeverStop)

	// Birth Cry after the birth is successful
	s.birthCry()

	go s.Proxier.SyncLoop()

	return <-errCh
}

func (s *ProxyServer) birthCry() {
	s.Recorder.Eventf(s.NodeRef, nil, api.EventTypeNormal, "Starting", "StartKubeProxy", "")
}

// detectNodeIPs returns the proxier's "node IP" or IPs, and the IP family to use if the
// node turns out to be incapable of dual-stack. (Note that kube-proxy normally runs as
// dual-stack if the backend is capable of supporting both IP families, regardless of
// whether the node is *actually* configured as dual-stack or not.)

// (Note that on Linux, the node IPs are used only to determine whether a given
// LoadBalancerSourceRanges value matches the node or not. In particular, they are *not*
// used for NodePort handling.)
//
// The order of precedence is:
//  1. if bindAddress is not 0.0.0.0 or ::, then it is used as the primary IP.
//  2. if the Node object can be fetched, then its primary IP is used as the primary IP
//     (and its secondary IP, if any, is just ignored).
//  3. otherwise the primary node IP is 127.0.0.1.
//
// In all cases, the secondary IP is the zero IP of the other IP family.
func detectNodeIPs(client clientset.Interface, hostname, bindAddress string) (v1.IPFamily, map[v1.IPFamily]net.IP) {
	nodeIP := netutils.ParseIPSloppy(bindAddress)
	if nodeIP.IsUnspecified() {
		nodeIP = utilnode.GetNodeIP(client, hostname)
	}
	if nodeIP == nil {
		klog.InfoS("can't determine this node's IP," +
			"assuming 127.0.0.1; if this is incorrect, please set the --bind-address flag")
		nodeIP = netutils.ParseIPSloppy("127.0.0.1")
	}

	if netutils.IsIPv4(nodeIP) {
		return v1.IPv4Protocol, map[v1.IPFamily]net.IP{
			v1.IPv4Protocol: nodeIP,
			v1.IPv6Protocol: net.IPv6zero,
		}
	} else {
		return v1.IPv6Protocol, map[v1.IPFamily]net.IP{
			v1.IPv4Protocol: net.IPv4zero,
			v1.IPv6Protocol: nodeIP,
		}
	}
}
