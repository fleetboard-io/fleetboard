package plugin

import (
	"context"
	"fmt"
	"net"

	"github.com/nauti-io/nauti/pkg/known"
	"github.com/pkg/errors"
	v1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/labels"
	discoverylisterv1 "k8s.io/client-go/listers/discovery/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/request"
	"github.com/dixudx/yacht"
	"github.com/miekg/dns"
)

type CrossDNS struct {
	Next                 plugin.Handler
	Fall                 fall.F
	Zones                []string
	endpointSlicesLister discoverylisterv1.EndpointSliceLister
	epsSynced            cache.InformerSynced
	yachtController      *yacht.Controller
}

type DNSRecord struct {
	IP          string
	HostName    string
	ClusterName string
}

func (c CrossDNS) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := &request.Request{W: w, Req: r}
	qname := state.QName()

	zone := plugin.Zones(c.Zones).Matches(qname)
	if zone == "" {
		klog.Infof("Request does not match configured zones %v", c.Zones)
		return plugin.NextOrFailure(c.Name(), c.Next, ctx, state.W, r) // nolint:wrapcheck // Let the caller wrap it.
	}

	klog.Info("Request received for %q", qname)
	if state.QType() != dns.TypeA && state.QType() != dns.TypeAAAA && state.QType() != dns.TypeSRV {
		msg := fmt.Sprintf("Query of type %d is not supported", state.QType())
		klog.Info(msg)
		return plugin.NextOrFailure(c.Name(), c.Next, ctx, state.W, r) // nolint:wrapcheck // Let the caller wrap it.
	}
	zone = qname[len(qname)-len(zone):] // maintain case of original query
	state.Zone = zone

	pReq, pErr := parseRequest(state)

	if pErr != nil || pReq.podOrSvc != Svc {
		// We only support svc type queries i.e. *.svc.*
		klog.Info("Request type %q is not a 'svc' type query - err was %v", pReq.podOrSvc, pErr)
		return plugin.NextOrFailure(c.Name(), c.Next, ctx, state.W, r) // nolint:wrapcheck // Let the caller wrap it.
	}

	return c.getDNSRecord(ctx, zone, state, w, r, pReq)
}

func (c *CrossDNS) getDNSRecord(ctx context.Context, zone string, state *request.Request, w dns.ResponseWriter,
	r *dns.Msg, pReq *recordRequest,
) (int, error) {
	// wait for endpoint slice synced.
	if !cache.WaitForCacheSync(ctx.Done(), c.epsSynced) {
		klog.Fatal("unable to sync caches for endpointslices")
	}

	var dnsRecords []DNSRecord
	var err error
	var srcEndpointSliceList []*v1.EndpointSlice
	if pReq.cluster != "" {
		srcEndpointSliceList, err = c.endpointSlicesLister.EndpointSlices("syncer-operator").List(
			labels.SelectorFromSet(
				labels.Set{
					known.LabelServiceNameSpace: pReq.namespace,
					known.LabelServiceName:      pReq.service,
					known.LabelClusterID:        pReq.cluster,
				}))
	} else {
		srcEndpointSliceList, err = c.endpointSlicesLister.EndpointSlices("syncer-operator").List(
			labels.SelectorFromSet(
				labels.Set{
					known.LabelServiceNameSpace: pReq.namespace,
					known.LabelServiceName:      pReq.service,
				}))
	}

	if err != nil {
		klog.Errorf("Failed to write message %v", err)
		return dns.RcodeServerFailure, errors.New("failed to write response")
	}
	record := c.getAllRecordsFromEndpointslice(srcEndpointSliceList)
	dnsRecords = append(dnsRecords, record...)
	if len(dnsRecords) == 0 {
		klog.Infof("Couldn't find a connected cluster or valid IPs for %q", state.QName())
		return c.emptyResponse(state)
	}

	records := make([]dns.RR, 0)

	if state.QType() == dns.TypeA {
		records = c.createARecords(dnsRecords, state)
	}

	a := new(dns.Msg)
	a.SetReply(r)
	a.Authoritative = true
	a.Answer = append(a.Answer, records...)
	klog.Infof("Responding to query with '%s'", a.Answer)

	wErr := w.WriteMsg(a)
	if wErr != nil {
		// Error writing reply msg
		klog.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, errors.New("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (c CrossDNS) getAllRecordsFromEndpointslice(slices []*v1.EndpointSlice) []DNSRecord {
	records := make([]DNSRecord, 0)
	for _, eps := range slices {
		for _, endpoint := range eps.Endpoints {
			record := DNSRecord{
				IP:          endpoint.Addresses[0],
				ClusterName: eps.GetLabels()[known.LabelClusterID],
			}
			records = append(records, record)
		}
	}
	return records
}

func (c CrossDNS) Name() string {
	return "crossdns"
}

func (c CrossDNS) emptyResponse(state *request.Request) (int, error) {
	a := new(dns.Msg)
	a.SetReply(state.Req)

	return writeResponse(state, a)
}

func writeResponse(state *request.Request, a *dns.Msg) (int, error) {
	a.Authoritative = true

	wErr := state.W.WriteMsg(a)
	if wErr != nil {
		klog.Errorf("Failed to write message %#v: %v", a, wErr)
		return dns.RcodeServerFailure, errors.New("failed to write response")
	}

	return dns.RcodeSuccess, nil
}

func (c CrossDNS) createARecords(dnsrecords []DNSRecord, state *request.Request) []dns.RR {
	records := make([]dns.RR, 0)

	for _, record := range dnsrecords {
		dnsRecord := &dns.A{Hdr: dns.RR_Header{
			Name: state.QName(), Rrtype: dns.TypeA, Class: state.QClass(),
			Ttl: uint32(5),
		}, A: net.ParseIP(record.IP).To4()}
		records = append(records, dnsRecord)
	}

	return records
}

var _ plugin.Handler = &CrossDNS{}
