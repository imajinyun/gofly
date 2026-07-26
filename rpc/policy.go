package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imajinyun/gofly/core/callstats"
	"github.com/imajinyun/gofly/core/controlplane"
	"github.com/imajinyun/gofly/core/governance"
	coreruntime "github.com/imajinyun/gofly/core/runtime"
	controladmin "github.com/imajinyun/gofly/ops/admin"
)

const (
	RPCBalancerRoundRobin         = "round_robin"
	RPCBalancerWeightedRoundRobin = "weighted_round_robin"
	RPCBalancerP2C                = "p2c"
	RPCBalancerConsistentHash     = "consistent_hash"
	RPCBalancerHealth             = "health"
)

// RPCPolicy is the transport-neutral policy contract used by RPC clients,
// servers and generated manifests. It intentionally mirrors governance.Policy
// without tying future endpoint-chain work to a single rule source.
type RPCPolicy struct {
	Timeout     time.Duration            `json:"timeout,omitempty"`
	Retry       governance.RetryPolicy   `json:"retry,omitempty"`
	Hedge       RPCHedgePolicy           `json:"hedge,omitempty"`
	Fallback    RPCFallbackPolicy        `json:"fallback,omitempty"`
	Breaker     governance.BreakerPolicy `json:"breaker,omitempty"`
	LoadShedder RPCLoadShedderPolicy     `json:"loadShedder,omitempty"`
	Balancer    RPCBalancerPolicy        `json:"balancer,omitempty"`
	Metadata    map[string]string        `json:"metadata,omitempty"`
	Headers     map[string]string        `json:"headers,omitempty"`
	Methods     map[string]RPCPolicy     `json:"methods,omitempty"`
}

type RPCHedgePolicy struct {
	Enabled  bool          `json:"enabled,omitempty"`
	Delay    time.Duration `json:"delay,omitempty"`
	Attempts int           `json:"attempts,omitempty"`
}

type RPCFallbackPolicy struct {
	Enabled bool   `json:"enabled,omitempty"`
	Target  string `json:"target,omitempty"`
	Method  string `json:"method,omitempty"`
}

type RPCLoadShedderPolicy struct {
	Enabled        bool          `json:"enabled,omitempty"`
	MaxConcurrency int           `json:"maxConcurrency,omitempty"`
	MaxInflight    int           `json:"maxInflight,omitempty"`
	MinWindow      time.Duration `json:"minWindow,omitempty"`
}

type RPCBalancerPolicy struct {
	Name    string         `json:"name,omitempty"`
	Weights map[string]int `json:"weights,omitempty"`
	Key     string         `json:"key,omitempty"`
}

type RPCPolicyRuntimeSnapshot struct {
	Policy            RPCPolicy                     `json:"policy,omitempty"`
	State             RPCPolicyRuntimeState         `json:"state"`
	Cache             RPCPolicyRuntimeCacheSnapshot `json:"cache,omitempty"`
	MethodPolicyCount int                           `json:"methodPolicyCount,omitempty"`
	MethodPolicyKeys  []string                      `json:"methodPolicyKeys,omitempty"`
	Priority          []string                      `json:"priority,omitempty"`
	Capabilities      []string                      `json:"capabilities,omitempty"`
}

type RPCEffectivePolicySnapshot struct {
	Method         string                `json:"method,omitempty"`
	MethodKey      string                `json:"methodKey,omitempty"`
	Policy         RPCPolicy             `json:"policy"`
	State          RPCPolicyRuntimeState `json:"state"`
	Priority       []string              `json:"priority,omitempty"`
	GovernanceRule string                `json:"governanceRule,omitempty"`
}

type RPCDiagnosisProbe struct {
	Target       string                      `json:"target,omitempty"`
	Service      string                      `json:"service,omitempty"`
	Method       string                      `json:"method,omitempty"`
	Endpoint     string                      `json:"endpoint,omitempty"`
	ConnectionID string                      `json:"connectionId,omitempty"`
	PoolSlot     int                         `json:"poolSlot,omitempty"`
	FlowControl  string                      `json:"flowControl,omitempty"`
	EventFamily  string                      `json:"eventFamily,omitempty"`
	Event        string                      `json:"event,omitempty"`
	Matched      bool                        `json:"matched"`
	Diagnosis    RPCDiagnosisSnapshot        `json:"diagnosis"`
	Policy       RPCEffectivePolicySnapshot  `json:"policy,omitempty"`
	Discovery    RPCDiscoveryRuntimeSnapshot `json:"discovery,omitempty"`
	GeneratedAt  time.Time                   `json:"generatedAt"`
}

type RPCRuntimeSnapshot struct {
	Target      string                      `json:"target,omitempty"`
	Codec       string                      `json:"codec,omitempty"`
	Transport   RPCHTTPTransportSnapshot    `json:"transport,omitempty"`
	Middlewares RPCEndpointChainSnapshot    `json:"middlewares,omitempty"`
	Resolver    RPCResolverRuntimeSnapshot  `json:"resolver,omitempty"`
	Balancer    string                      `json:"balancer,omitempty"`
	ConnPool    ConnPoolManagerSnapshot     `json:"connPool,omitempty"`
	Policy      RPCPolicyRuntimeSnapshot    `json:"policy"`
	Discovery   RPCDiscoveryRuntimeSnapshot `json:"discovery,omitempty"`
	Stats       callstats.Snapshot          `json:"stats,omitempty"`
	Warmup      RPCWarmupSnapshot           `json:"warmup,omitempty"`
	Diagnosis   RPCDiagnosisSnapshot        `json:"diagnosis,omitempty"`
}

type RPCWarmupConfig struct {
	Enabled      bool          `json:"enabled"`
	Timeout      time.Duration `json:"timeout,omitempty"`
	ConnPool     bool          `json:"connPool,omitempty"`
	MaxEndpoints int           `json:"maxEndpoints,omitempty"`
}

type RPCWarmupSnapshot struct {
	Enabled         bool          `json:"enabled"`
	Attempted       bool          `json:"attempted"`
	Completed       bool          `json:"completed"`
	ConnPoolEnabled bool          `json:"connPoolEnabled,omitempty"`
	Endpoints       []string      `json:"endpoints,omitempty"`
	Selected        string        `json:"selected,omitempty"`
	ConnPoolWarmed  int           `json:"connPoolWarmed,omitempty"`
	Duration        time.Duration `json:"duration,omitempty"`
	Error           string        `json:"error,omitempty"`
	At              time.Time     `json:"at,omitempty"`
}

type RPCEndpointChainSnapshot struct {
	Unary  int `json:"unary,omitempty"`
	Stream int `json:"stream,omitempty"`
}

type RPCHTTPTransportSnapshot struct {
	Timeout             time.Duration               `json:"timeout,omitempty"`
	DialTimeout         time.Duration               `json:"dialTimeout,omitempty"`
	KeepAlive           time.Duration               `json:"keepAlive,omitempty"`
	IdleConnTimeout     time.Duration               `json:"idleConnTimeout,omitempty"`
	StreamIdleTimeout   time.Duration               `json:"streamIdleTimeout,omitempty"`
	StreamConnPolicy    RPCStreamConnPolicySnapshot `json:"streamConnPolicy,omitempty"`
	CloseIdleOnEndpoint bool                        `json:"closeIdleOnEndpointChange"`
	Stream              RPCStreamTransportSnapshot  `json:"stream,omitempty"`
}

type RPCStreamTransportSnapshot struct {
	Active          int64            `json:"active,omitempty"`
	Dials           int64            `json:"dials,omitempty"`
	DedicatedConns  int64            `json:"dedicatedConns,omitempty"`
	Closes          int64            `json:"closes,omitempty"`
	LastTarget      string           `json:"lastTarget,omitempty"`
	LastDialedAt    time.Time        `json:"lastDialedAt,omitempty"`
	LastClosedAt    time.Time        `json:"lastClosedAt,omitempty"`
	LastCloseCode   Code             `json:"lastCloseCode,omitempty"`
	LastCloseReason string           `json:"lastCloseReason,omitempty"`
	CloseCodes      map[Code]int64   `json:"closeCodes,omitempty"`
	CloseReasons    map[string]int64 `json:"closeReasons,omitempty"`
}

type RPCStreamConnPolicySnapshot struct {
	Mode              string `json:"mode,omitempty"`
	MaxStreamsPerConn int    `json:"maxStreamsPerConn,omitempty"`
	Reuse             bool   `json:"reuse"`
	Multiplexed       bool   `json:"multiplexed"`
}

type RPCDiagnosisSnapshot struct {
	Transport RPCHTTPTransportSnapshot     `json:"transport,omitempty"`
	Mux       RPCMuxTransportDiagnosis     `json:"mux,omitempty"`
	ConnPool  ConnPoolManagerSnapshot      `json:"connPool,omitempty"`
	Retry     RPCRetryDiagnosisSnapshot    `json:"retry,omitempty"`
	Resolver  RPCResolverRuntimeSnapshot   `json:"resolver,omitempty"`
	Balancer  RPCBalancerDiagnosisSnapshot `json:"balancer,omitempty"`
}

type RPCMuxTransportDiagnosis struct {
	Enabled     bool                             `json:"enabled"`
	Mode        string                           `json:"mode,omitempty"`
	Candidate   ExperimentalMuxCandidateSnapshot `json:"candidate,omitempty"`
	Adapter     ExperimentalMuxAdapterSnapshot   `json:"adapter,omitempty"`
	Transport   ExperimentalMuxTransportSnapshot `json:"transport,omitempty"`
	Negotiation RPCMuxNegotiationDiagnosis       `json:"negotiation,omitempty"`
	FlowControl RPCMuxFlowControlDiagnosis       `json:"flowControl,omitempty"`
	Keepalive   RPCMuxKeepaliveDiagnosis         `json:"keepalive,omitempty"`
	Drain       RPCMuxDrainDiagnosis             `json:"drain,omitempty"`
	Manager     RPCMuxConnectionManagerDiagnosis `json:"manager,omitempty"`
	Events      []RPCMuxDiagnosisEvent           `json:"events,omitempty"`
}

type RPCMuxNegotiationDiagnosis struct {
	Failures            int64  `json:"failures,omitempty"`
	TLSFailure          int64  `json:"tls_failure,omitempty"`
	ALPNMismatch        int64  `json:"alpn_mismatch,omitempty"`
	PrefaceMismatch     int64  `json:"preface_mismatch,omitempty"`
	ProtocolMismatch    int64  `json:"protocol_mismatch,omitempty"`
	FramePolicyMismatch int64  `json:"frame_policy_mismatch,omitempty"`
	PolicyRiskRejected  int64  `json:"fragment_window_policy_risk,omitempty"`
	LastEvent           string `json:"lastEvent,omitempty"`
	LastPhase           string `json:"lastPhase,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	PeerProtocol        string `json:"peerProtocol,omitempty"`
}

type RPCMuxConnectionManagerDiagnosis struct {
	Enabled                 bool                                    `json:"enabled"`
	Mode                    string                                  `json:"mode,omitempty"`
	Candidate               ExperimentalMuxCandidateSnapshot        `json:"candidate,omitempty"`
	FlowControl             RPCMuxFlowControlDiagnosis              `json:"flowControl,omitempty"`
	RefillProfile           RPCMuxRefillProfile                     `json:"refillProfile,omitempty"`
	RefillProfiles          []RPCMuxRefillProfile                   `json:"refillProfiles,omitempty"`
	IdleTimeout             time.Duration                           `json:"idleTimeout,omitempty"`
	MaxStreamsPerConn       int                                     `json:"maxStreamsPerConn,omitempty"`
	MaxConnsPerEndpoint     int                                     `json:"maxConnsPerEndpoint,omitempty"`
	MaxIdleConnsPerEndpoint int                                     `json:"maxIdleConnsPerEndpoint,omitempty"`
	HealthFailureThreshold  int                                     `json:"healthFailureThreshold,omitempty"`
	HealthEjectionDuration  time.Duration                           `json:"healthEjectionDuration,omitempty"`
	HealthBackoffMultiplier int                                     `json:"healthBackoffMultiplier,omitempty"`
	HealthMaxCooldown       time.Duration                           `json:"healthMaxCooldown,omitempty"`
	MaxOpenRetries          int                                     `json:"maxOpenRetries,omitempty"`
	OpenRetryReasons        []string                                `json:"openRetryReasons,omitempty"`
	JanitorInterval         time.Duration                           `json:"janitorInterval,omitempty"`
	Endpoints               []ExperimentalMuxEndpointSnapshot       `json:"endpoints,omitempty"`
	Health                  []ExperimentalMuxEndpointHealthSnapshot `json:"health,omitempty"`
	RetiredAdapters         int                                     `json:"retiredAdapters,omitempty"`
	WatchUpdates            int64                                   `json:"watchUpdates,omitempty"`
	Removed                 []string                                `json:"removed,omitempty"`
	ClosedAdapters          int64                                   `json:"closedAdapters,omitempty"`
	UnhealthyAdapters       int64                                   `json:"unhealthyAdapters,omitempty"`
	PoolExhaustions         int64                                   `json:"poolExhaustions,omitempty"`
	DialFailures            int64                                   `json:"dialFailures,omitempty"`
	EndpointEjections       int64                                   `json:"endpointEjections,omitempty"`
	EndpointRecoveries      int64                                   `json:"endpointRecoveries,omitempty"`
	OpenRetries             int64                                   `json:"openRetries,omitempty"`
	LastRetriedFrom         string                                  `json:"lastRetriedFrom,omitempty"`
	LastRetriedTo           string                                  `json:"lastRetriedTo,omitempty"`
	RetryReasons            map[string]int64                        `json:"retryReasons,omitempty"`
	JanitorRuns             int64                                   `json:"janitorRuns,omitempty"`
	CloseReasons            map[string]int64                        `json:"closeReasons,omitempty"`
	DrainReasons            map[string]int64                        `json:"drainReasons,omitempty"`
	LastUpdated             time.Time                               `json:"lastUpdated,omitempty"`
}

type RPCMuxFlowControlDiagnosis struct {
	ReceiveQueueSize                        int                               `json:"receiveQueueSize,omitempty"`
	ConnectionWindow                        int                               `json:"connectionWindow,omitempty"`
	ConnectionCreditWaits                   int64                             `json:"connectionCreditWaits,omitempty"`
	StreamCreditWaits                       int64                             `json:"streamCreditWaits,omitempty"`
	CreditWaitTimeouts                      int64                             `json:"creditWaitTimeouts,omitempty"`
	WriteTimeouts                           int64                             `json:"writeTimeouts,omitempty"`
	ConnectionWindowExhausted               int64                             `json:"connectionWindowExhausted,omitempty"`
	FragmentFramesIn                        int64                             `json:"fragmentFramesIn,omitempty"`
	FragmentFramesOut                       int64                             `json:"fragmentFramesOut,omitempty"`
	FragmentBackpressure                    int64                             `json:"fragmentBackpressure,omitempty"`
	FragmentStreamWindowUpdatePolicy        string                            `json:"fragmentStreamWindowUpdatePolicy,omitempty"`
	FragmentConnectionWindowUpdatePolicy    string                            `json:"fragmentConnectionWindowUpdatePolicy,omitempty"`
	FragmentStreamWindowRefillRatio         float64                           `json:"fragmentStreamWindowRefillRatio,omitempty"`
	FragmentConnectionWindowRefillRatio     float64                           `json:"fragmentConnectionWindowRefillRatio,omitempty"`
	FragmentMaxDeferredFragments            int                               `json:"fragmentMaxDeferredFragments,omitempty"`
	FragmentWindowRefills                   int64                             `json:"fragmentWindowRefills,omitempty"`
	FragmentWindowRefillLatencyTotal        time.Duration                     `json:"fragmentWindowRefillLatencyTotal,omitempty"`
	FragmentWindowRefillLatencyMax          time.Duration                     `json:"fragmentWindowRefillLatencyMax,omitempty"`
	FragmentWindowRefillLatencyAvg          time.Duration                     `json:"fragmentWindowRefillLatencyAvg,omitempty"`
	FragmentDeferredStreamWindowUpdates     int64                             `json:"fragmentDeferredStreamWindowUpdates,omitempty"`
	FragmentDeferredConnectionWindowUpdates int64                             `json:"fragmentDeferredConnectionWindowUpdates,omitempty"`
	FragmentWindowPolicyRisk                bool                              `json:"fragmentWindowPolicyRisk,omitempty"`
	FragmentWindowPolicyRiskReason          string                            `json:"fragmentWindowPolicyRiskReason,omitempty"`
	FragmentWindowPolicyRiskMode            string                            `json:"fragmentWindowPolicyRiskMode,omitempty"`
	FragmentEstimatedMaxFragments           int                               `json:"fragmentEstimatedMaxFragments,omitempty"`
	WindowFramesIn                          int64                             `json:"windowFramesIn,omitempty"`
	WindowFramesOut                         int64                             `json:"windowFramesOut,omitempty"`
	ConnectionWindowIn                      int64                             `json:"connectionWindowIn,omitempty"`
	ConnectionWindowOut                     int64                             `json:"connectionWindowOut,omitempty"`
	BackpressureEvents                      int64                             `json:"backpressureEvents,omitempty"`
	LastFlowControlEvent                    string                            `json:"lastFlowControlEvent,omitempty"`
	LastFlowControlEventAt                  time.Time                         `json:"lastFlowControlEventAt,omitempty"`
	LastBackpressureEvent                   string                            `json:"lastBackpressureEvent,omitempty"`
	LastBackpressureEventAt                 time.Time                         `json:"lastBackpressureEventAt,omitempty"`
	Events                                  []RPCMuxFlowControlEventDiagnosis `json:"events,omitempty"`
}

type RPCMuxFlowControlEventDiagnosis struct {
	Event string `json:"event"`
	Count int64  `json:"count"`
}

type RPCMuxRefillProfile struct {
	Endpoint                        string        `json:"endpoint,omitempty"`
	ConnectionID                    string        `json:"connectionId,omitempty"`
	PoolSlot                        int           `json:"poolSlot,omitempty"`
	ReceiveQueueSize                int           `json:"receiveQueueSize,omitempty"`
	ConnectionWindow                int           `json:"connectionWindow,omitempty"`
	StreamWindowUpdatePolicy        string        `json:"streamWindowUpdatePolicy,omitempty"`
	ConnectionWindowUpdatePolicy    string        `json:"connectionWindowUpdatePolicy,omitempty"`
	StreamWindowRefillRatio         float64       `json:"streamWindowRefillRatio,omitempty"`
	ConnectionWindowRefillRatio     float64       `json:"connectionWindowRefillRatio,omitempty"`
	MaxDeferredFragments            int           `json:"maxDeferredFragments,omitempty"`
	Refills                         int64         `json:"refills,omitempty"`
	RefillLatencyTotal              time.Duration `json:"refillLatencyTotal,omitempty"`
	RefillLatencyMax                time.Duration `json:"refillLatencyMax,omitempty"`
	RefillLatencyAvg                time.Duration `json:"refillLatencyAvg,omitempty"`
	DeferredStreamWindowUpdates     int64         `json:"deferredStreamWindowUpdates,omitempty"`
	DeferredConnectionWindowUpdates int64         `json:"deferredConnectionWindowUpdates,omitempty"`
	BackpressureEvents              int64         `json:"backpressureEvents,omitempty"`
	FragmentBackpressure            int64         `json:"fragmentBackpressure,omitempty"`
	LastFlowControlEvent            string        `json:"lastFlowControlEvent,omitempty"`
	LastFlowControlEventAt          time.Time     `json:"lastFlowControlEventAt,omitempty"`
	LastBackpressureEvent           string        `json:"lastBackpressureEvent,omitempty"`
	LastBackpressureEventAt         time.Time     `json:"lastBackpressureEventAt,omitempty"`
	PolicyRisk                      bool          `json:"policyRisk,omitempty"`
	PolicyRiskReason                string        `json:"policyRiskReason,omitempty"`
	PolicyRiskMode                  string        `json:"policyRiskMode,omitempty"`
	EstimatedMaxFragments           int           `json:"estimatedMaxFragments,omitempty"`
}

type RPCMuxDiagnosisEvent struct {
	Family       string        `json:"family"`
	Event        string        `json:"event"`
	Count        int64         `json:"count,omitempty"`
	Endpoint     string        `json:"endpoint,omitempty"`
	ConnectionID string        `json:"connectionId,omitempty"`
	PoolSlot     int           `json:"poolSlot,omitempty"`
	PeerProtocol string        `json:"peerProtocol,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	From         string        `json:"from,omitempty"`
	To           string        `json:"to,omitempty"`
	Cooldown     time.Duration `json:"cooldown,omitempty"`
	Direction    string        `json:"direction,omitempty"`
}

func NormalizeRPCMuxFlowControlEvent(event string) string {
	event = strings.TrimSpace(strings.ToLower(event))
	event = strings.ReplaceAll(event, "-", "_")
	switch event {
	case "write_timeout", "credit_wait_timeout", "connection_window_exhausted", "fragment_backpressure", "fragment_window_refill", "fragment_window_policy_risk":
		return event
	default:
		return ""
	}
}

func FilterRPCMuxDiagnosisByFlowControlEvent(diagnosis RPCMuxTransportDiagnosis, event string) RPCMuxTransportDiagnosis {
	return FilterRPCMuxDiagnosis(diagnosis, RPCMuxDiagnosisFilter{FlowControlEvent: event})
}

type RPCMuxDiagnosisFilter struct {
	Endpoint         string
	ConnectionID     string
	PoolSlot         int
	FlowControlEvent string
	EventFamily      string
	Event            string
}

func FilterRPCMuxDiagnosis(diagnosis RPCMuxTransportDiagnosis, filter RPCMuxDiagnosisFilter) RPCMuxTransportDiagnosis {
	event := NormalizeRPCMuxFlowControlEvent(filter.FlowControlEvent)
	endpoint := normalizeMuxDiagnosisEndpoint(filter.Endpoint)
	connectionID := normalizeMuxDiagnosisConnectionID(filter.ConnectionID)
	diagnosis = withRPCMuxNegotiationDiagnosis(diagnosis)
	if event == "" {
		diagnosis.FlowControl = withRPCMuxFlowControlEvents(diagnosis.FlowControl, "")
		diagnosis.Manager = filterRPCMuxManagerDiagnosis(diagnosis.Manager, endpoint, connectionID, filter.PoolSlot, "")
		diagnosis.Events = filterRPCMuxDiagnosisEvents(RPCMuxDiagnosisEvents(diagnosis), filter)
		return diagnosis
	}
	diagnosis.FlowControl = filterRPCMuxFlowControlDiagnosis(diagnosis.FlowControl, event)
	diagnosis.Adapter = filterRPCMuxAdapterFlowControl(diagnosis.Adapter, event)
	diagnosis.Manager = filterRPCMuxManagerDiagnosis(diagnosis.Manager, endpoint, connectionID, filter.PoolSlot, event)
	diagnosis.Events = filterRPCMuxDiagnosisEvents(RPCMuxDiagnosisEvents(diagnosis), filter)
	return diagnosis
}

func rpcMuxDiagnosisEventView(diagnosis RPCMuxTransportDiagnosis, filter RPCMuxDiagnosisFilter) []RPCMuxDiagnosisEvent {
	events := RPCMuxDiagnosisEvents(diagnosis)
	events = annotateRPCMuxDiagnosisEventsWithFilterContext(events, filter)
	events = filterRPCMuxDiagnosisEvents(events, filter)
	return compactRPCMuxDiagnosisEventView(events, filter)
}

func annotateRPCMuxDiagnosisEventsWithFilterContext(events []RPCMuxDiagnosisEvent, filter RPCMuxDiagnosisFilter) []RPCMuxDiagnosisEvent {
	endpoint := normalizeMuxDiagnosisEndpoint(filter.Endpoint)
	if endpoint == "" {
		return events
	}
	annotated := make([]RPCMuxDiagnosisEvent, len(events))
	copy(annotated, events)
	for i := range annotated {
		if annotated[i].Endpoint == "" && annotated[i].From == "" && annotated[i].To == "" && filter.ConnectionID == "" && filter.PoolSlot <= 0 {
			annotated[i].Endpoint = endpoint
		}
	}
	return annotated
}

func filterRPCMuxDiagnosisEvents(events []RPCMuxDiagnosisEvent, filter RPCMuxDiagnosisFilter) []RPCMuxDiagnosisEvent {
	eventFamily := normalizeMuxDiagnosisEventField(filter.EventFamily)
	eventName := normalizeMuxDiagnosisEventField(filter.Event)
	endpoint := normalizeMuxDiagnosisEndpoint(filter.Endpoint)
	connectionID := normalizeMuxDiagnosisConnectionID(filter.ConnectionID)
	if eventFamily == "" && eventName == "" && endpoint == "" && connectionID == "" && filter.PoolSlot <= 0 {
		return events
	}
	filtered := make([]RPCMuxDiagnosisEvent, 0, len(events))
	for _, item := range events {
		if eventFamily != "" && normalizeMuxDiagnosisEventField(item.Family) != eventFamily {
			continue
		}
		if eventName != "" && normalizeMuxDiagnosisEventField(item.Event) != eventName {
			continue
		}
		if endpoint != "" && !rpcMuxDiagnosisEventEndpointMatched(item, endpoint) {
			continue
		}
		if connectionID != "" && normalizeMuxDiagnosisConnectionID(item.ConnectionID) != connectionID {
			continue
		}
		if filter.PoolSlot > 0 && item.PoolSlot != filter.PoolSlot {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func compactRPCMuxDiagnosisEventView(events []RPCMuxDiagnosisEvent, filter RPCMuxDiagnosisFilter) []RPCMuxDiagnosisEvent {
	if normalizeMuxDiagnosisEndpoint(filter.Endpoint) == "" || len(events) < 2 {
		return events
	}
	specific := make(map[string]struct{}, len(events))
	for _, item := range events {
		if item.ConnectionID == "" && item.PoolSlot <= 0 {
			continue
		}
		specific[rpcMuxDiagnosisEventSpecificityKey(item)] = struct{}{}
	}
	if len(specific) == 0 {
		return events
	}
	compact := make([]RPCMuxDiagnosisEvent, 0, len(events))
	for _, item := range events {
		if item.ConnectionID == "" && item.PoolSlot <= 0 {
			if _, ok := specific[rpcMuxDiagnosisEventSpecificityKey(item)]; ok {
				continue
			}
		}
		compact = append(compact, item)
	}
	return compact
}

func rpcMuxDiagnosisEventSpecificityKey(event RPCMuxDiagnosisEvent) string {
	return strings.Join([]string{
		normalizeMuxDiagnosisEventField(event.Family),
		normalizeMuxDiagnosisEventField(event.Event),
		normalizeMuxDiagnosisEndpoint(event.Endpoint),
		normalizeMuxDiagnosisEventField(event.PeerProtocol),
		normalizeMuxDiagnosisEventField(event.Reason),
		normalizeMuxDiagnosisEndpoint(event.From),
		normalizeMuxDiagnosisEndpoint(event.To),
		event.Direction,
	}, "\x00")
}

func rpcMuxDiagnosisEventEndpointMatched(event RPCMuxDiagnosisEvent, endpoint string) bool {
	endpoint = normalizeMuxDiagnosisEndpoint(endpoint)
	if endpoint == "" {
		return true
	}
	for _, candidate := range []string{event.Endpoint, event.From, event.To} {
		if normalizeMuxDiagnosisEndpoint(candidate) == endpoint {
			return true
		}
	}
	return false
}

func normalizeMuxDiagnosisEndpoint(endpoint string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

func normalizeMuxDiagnosisConnectionID(connectionID string) string {
	return strings.TrimSpace(connectionID)
}

func normalizeMuxDiagnosisEventField(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func filterRPCMuxAdapterFlowControl(snapshot ExperimentalMuxAdapterSnapshot, event string) ExperimentalMuxAdapterSnapshot {
	snapshot.Transport = filterExperimentalMuxTransportFlowControl(snapshot.Transport, event)
	return snapshot
}

func filterExperimentalMuxTransportFlowControl(snapshot ExperimentalMuxTransportSnapshot, event string) ExperimentalMuxTransportSnapshot {
	switch event {
	case "write_timeout":
		snapshot.CreditWaitTimeouts = 0
		snapshot.ConnectionWindowExhausted = 0
		snapshot.BackpressureEvents = 0
		snapshot.FragmentFramesIn = 0
		snapshot.FragmentFramesOut = 0
		snapshot.FragmentStreamWindowUpdatePolicy = ""
		snapshot.FragmentConnectionWindowUpdatePolicy = ""
		snapshot.FragmentStreamWindowRefillRatio = 0
		snapshot.FragmentConnectionWindowRefillRatio = 0
		snapshot.FragmentMaxDeferredFragments = 0
		snapshot.FragmentWindowRefills = 0
		snapshot.FragmentWindowRefillLatencyTotal = 0
		snapshot.FragmentWindowRefillLatencyMax = 0
		snapshot.FragmentWindowRefillLatencyAvg = 0
		snapshot.FragmentDeferredStreamWindowUpdates = 0
		snapshot.FragmentDeferredConnectionWindowUpdates = 0
		snapshot.FragmentWindowPolicyRisk = false
		snapshot.FragmentWindowPolicyRiskReason = ""
		snapshot.FragmentWindowPolicyRiskMode = ""
		snapshot.FragmentEstimatedMaxFragments = 0
	case "credit_wait_timeout":
		snapshot.WriteTimeouts = 0
		snapshot.ConnectionWindowExhausted = 0
		snapshot.BackpressureEvents = 0
		snapshot.FragmentFramesIn = 0
		snapshot.FragmentFramesOut = 0
		snapshot.FragmentStreamWindowUpdatePolicy = ""
		snapshot.FragmentConnectionWindowUpdatePolicy = ""
		snapshot.FragmentStreamWindowRefillRatio = 0
		snapshot.FragmentConnectionWindowRefillRatio = 0
		snapshot.FragmentMaxDeferredFragments = 0
		snapshot.FragmentWindowRefills = 0
		snapshot.FragmentWindowRefillLatencyTotal = 0
		snapshot.FragmentWindowRefillLatencyMax = 0
		snapshot.FragmentWindowRefillLatencyAvg = 0
		snapshot.FragmentDeferredStreamWindowUpdates = 0
		snapshot.FragmentDeferredConnectionWindowUpdates = 0
		snapshot.FragmentWindowPolicyRisk = false
		snapshot.FragmentWindowPolicyRiskReason = ""
		snapshot.FragmentWindowPolicyRiskMode = ""
		snapshot.FragmentEstimatedMaxFragments = 0
	case "connection_window_exhausted":
		snapshot.WriteTimeouts = 0
		snapshot.CreditWaitTimeouts = 0
		snapshot.BackpressureEvents = 0
		snapshot.FragmentFramesIn = 0
		snapshot.FragmentFramesOut = 0
		snapshot.FragmentStreamWindowUpdatePolicy = ""
		snapshot.FragmentConnectionWindowUpdatePolicy = ""
		snapshot.FragmentStreamWindowRefillRatio = 0
		snapshot.FragmentConnectionWindowRefillRatio = 0
		snapshot.FragmentMaxDeferredFragments = 0
		snapshot.FragmentWindowRefills = 0
		snapshot.FragmentWindowRefillLatencyTotal = 0
		snapshot.FragmentWindowRefillLatencyMax = 0
		snapshot.FragmentWindowRefillLatencyAvg = 0
		snapshot.FragmentDeferredStreamWindowUpdates = 0
		snapshot.FragmentDeferredConnectionWindowUpdates = 0
		snapshot.FragmentWindowPolicyRisk = false
		snapshot.FragmentWindowPolicyRiskReason = ""
		snapshot.FragmentWindowPolicyRiskMode = ""
		snapshot.FragmentEstimatedMaxFragments = 0
	case "fragment_backpressure":
		snapshot.WriteTimeouts = 0
		snapshot.CreditWaitTimeouts = 0
		snapshot.ConnectionWindowExhausted = 0
		snapshot.FragmentWindowRefills = 0
		snapshot.FragmentWindowRefillLatencyTotal = 0
		snapshot.FragmentWindowRefillLatencyMax = 0
		snapshot.FragmentWindowRefillLatencyAvg = 0
		snapshot.FragmentWindowPolicyRisk = false
		snapshot.FragmentWindowPolicyRiskReason = ""
		snapshot.FragmentWindowPolicyRiskMode = ""
		snapshot.FragmentEstimatedMaxFragments = 0
	case "fragment_window_refill":
		snapshot.WriteTimeouts = 0
		snapshot.CreditWaitTimeouts = 0
		snapshot.ConnectionWindowExhausted = 0
		snapshot.BackpressureEvents = 0
		snapshot.FragmentFramesIn = 0
		snapshot.FragmentFramesOut = 0
		snapshot.FragmentWindowPolicyRisk = false
		snapshot.FragmentWindowPolicyRiskReason = ""
		snapshot.FragmentWindowPolicyRiskMode = ""
		snapshot.FragmentEstimatedMaxFragments = 0
	case "fragment_window_policy_risk":
		snapshot.WriteTimeouts = 0
		snapshot.CreditWaitTimeouts = 0
		snapshot.ConnectionWindowExhausted = 0
		snapshot.BackpressureEvents = 0
		snapshot.FragmentFramesIn = 0
		snapshot.FragmentFramesOut = 0
		snapshot.FragmentDeferredStreamWindowUpdates = 0
		snapshot.FragmentDeferredConnectionWindowUpdates = 0
	}
	return snapshot
}

func filterRPCMuxManagerDiagnosis(diagnosis RPCMuxConnectionManagerDiagnosis, endpoint string, connectionID string, poolSlot int, event string) RPCMuxConnectionManagerDiagnosis {
	diagnosis = filterRPCMuxManagerEndpoints(diagnosis, endpoint, connectionID, poolSlot)
	diagnosis.FlowControl = filterRPCMuxFlowControlDiagnosis(diagnosis.FlowControl, event)
	diagnosis.RefillProfile, diagnosis.RefillProfiles = muxManagerRefillProfiles(diagnosis.Endpoints, diagnosis.FlowControl)
	for i := range diagnosis.Endpoints {
		diagnosis.Endpoints[i].Adapter = filterRPCMuxAdapterFlowControl(diagnosis.Endpoints[i].Adapter, event)
	}
	return diagnosis
}

func filterRPCMuxManagerEndpoints(diagnosis RPCMuxConnectionManagerDiagnosis, endpoint string, connectionID string, poolSlot int) RPCMuxConnectionManagerDiagnosis {
	endpoint = normalizeMuxDiagnosisEndpoint(endpoint)
	connectionID = normalizeMuxDiagnosisConnectionID(connectionID)
	if endpoint == "" && connectionID == "" && poolSlot <= 0 {
		diagnosis.FlowControl = muxManagerFlowControlDiagnosis(diagnosis.Endpoints)
		return diagnosis
	}
	endpoints := make([]ExperimentalMuxEndpointSnapshot, 0, len(diagnosis.Endpoints))
	for _, item := range diagnosis.Endpoints {
		if rpcMuxEndpointSnapshotMatched(item, endpoint, connectionID, poolSlot) {
			endpoints = append(endpoints, item)
		}
	}
	health := make([]ExperimentalMuxEndpointHealthSnapshot, 0, len(diagnosis.Health))
	if endpoint != "" {
		for _, item := range diagnosis.Health {
			if normalizeMuxDiagnosisEndpoint(item.Endpoint) == endpoint {
				health = append(health, item)
			}
		}
	}
	diagnosis.Endpoints = endpoints
	diagnosis.Health = health
	diagnosis.FlowControl = muxManagerFlowControlDiagnosis(endpoints)
	return diagnosis
}

func rpcMuxEndpointSnapshotMatched(snapshot ExperimentalMuxEndpointSnapshot, endpoint string, connectionID string, poolSlot int) bool {
	if endpoint != "" && normalizeMuxDiagnosisEndpoint(snapshot.Endpoint) != endpoint {
		return false
	}
	if connectionID != "" && normalizeMuxDiagnosisConnectionID(snapshot.ConnectionID) != connectionID {
		return false
	}
	if poolSlot > 0 && snapshot.PoolSlot != poolSlot {
		return false
	}
	return true
}

func filterRPCMuxFlowControlDiagnosis(diagnosis RPCMuxFlowControlDiagnosis, event string) RPCMuxFlowControlDiagnosis {
	diagnosis = withRPCMuxFlowControlEvents(diagnosis, event)
	switch event {
	case "write_timeout":
		diagnosis.CreditWaitTimeouts = 0
		diagnosis.ConnectionWindowExhausted = 0
		diagnosis.FragmentBackpressure = 0
		diagnosis.FragmentFramesIn = 0
		diagnosis.FragmentFramesOut = 0
		diagnosis.FragmentStreamWindowUpdatePolicy = ""
		diagnosis.FragmentConnectionWindowUpdatePolicy = ""
		diagnosis.FragmentStreamWindowRefillRatio = 0
		diagnosis.FragmentConnectionWindowRefillRatio = 0
		diagnosis.FragmentMaxDeferredFragments = 0
		diagnosis.FragmentWindowRefills = 0
		diagnosis.FragmentWindowRefillLatencyTotal = 0
		diagnosis.FragmentWindowRefillLatencyMax = 0
		diagnosis.FragmentWindowRefillLatencyAvg = 0
		diagnosis.FragmentDeferredStreamWindowUpdates = 0
		diagnosis.FragmentDeferredConnectionWindowUpdates = 0
		diagnosis.FragmentWindowPolicyRisk = false
		diagnosis.FragmentWindowPolicyRiskReason = ""
		diagnosis.FragmentWindowPolicyRiskMode = ""
		diagnosis.FragmentEstimatedMaxFragments = 0
		diagnosis.BackpressureEvents = 0
	case "credit_wait_timeout":
		diagnosis.WriteTimeouts = 0
		diagnosis.ConnectionWindowExhausted = 0
		diagnosis.FragmentBackpressure = 0
		diagnosis.FragmentFramesIn = 0
		diagnosis.FragmentFramesOut = 0
		diagnosis.FragmentStreamWindowUpdatePolicy = ""
		diagnosis.FragmentConnectionWindowUpdatePolicy = ""
		diagnosis.FragmentStreamWindowRefillRatio = 0
		diagnosis.FragmentConnectionWindowRefillRatio = 0
		diagnosis.FragmentMaxDeferredFragments = 0
		diagnosis.FragmentWindowRefills = 0
		diagnosis.FragmentWindowRefillLatencyTotal = 0
		diagnosis.FragmentWindowRefillLatencyMax = 0
		diagnosis.FragmentWindowRefillLatencyAvg = 0
		diagnosis.FragmentDeferredStreamWindowUpdates = 0
		diagnosis.FragmentDeferredConnectionWindowUpdates = 0
		diagnosis.FragmentWindowPolicyRisk = false
		diagnosis.FragmentWindowPolicyRiskReason = ""
		diagnosis.FragmentWindowPolicyRiskMode = ""
		diagnosis.FragmentEstimatedMaxFragments = 0
		diagnosis.BackpressureEvents = 0
	case "connection_window_exhausted":
		diagnosis.WriteTimeouts = 0
		diagnosis.CreditWaitTimeouts = 0
		diagnosis.FragmentBackpressure = 0
		diagnosis.FragmentFramesIn = 0
		diagnosis.FragmentFramesOut = 0
		diagnosis.FragmentStreamWindowUpdatePolicy = ""
		diagnosis.FragmentConnectionWindowUpdatePolicy = ""
		diagnosis.FragmentStreamWindowRefillRatio = 0
		diagnosis.FragmentConnectionWindowRefillRatio = 0
		diagnosis.FragmentMaxDeferredFragments = 0
		diagnosis.FragmentWindowRefills = 0
		diagnosis.FragmentWindowRefillLatencyTotal = 0
		diagnosis.FragmentWindowRefillLatencyMax = 0
		diagnosis.FragmentWindowRefillLatencyAvg = 0
		diagnosis.FragmentDeferredStreamWindowUpdates = 0
		diagnosis.FragmentDeferredConnectionWindowUpdates = 0
		diagnosis.FragmentWindowPolicyRisk = false
		diagnosis.FragmentWindowPolicyRiskReason = ""
		diagnosis.FragmentWindowPolicyRiskMode = ""
		diagnosis.FragmentEstimatedMaxFragments = 0
		diagnosis.BackpressureEvents = 0
	case "fragment_backpressure":
		diagnosis.WriteTimeouts = 0
		diagnosis.CreditWaitTimeouts = 0
		diagnosis.ConnectionWindowExhausted = 0
		diagnosis.FragmentWindowRefills = 0
		diagnosis.FragmentWindowRefillLatencyTotal = 0
		diagnosis.FragmentWindowRefillLatencyMax = 0
		diagnosis.FragmentWindowRefillLatencyAvg = 0
		diagnosis.FragmentWindowPolicyRisk = false
		diagnosis.FragmentWindowPolicyRiskReason = ""
		diagnosis.FragmentWindowPolicyRiskMode = ""
		diagnosis.FragmentEstimatedMaxFragments = 0
	case "fragment_window_refill":
		diagnosis.WriteTimeouts = 0
		diagnosis.CreditWaitTimeouts = 0
		diagnosis.ConnectionWindowExhausted = 0
		diagnosis.FragmentBackpressure = 0
		diagnosis.FragmentFramesIn = 0
		diagnosis.FragmentFramesOut = 0
		diagnosis.BackpressureEvents = 0
		diagnosis.FragmentWindowPolicyRisk = false
		diagnosis.FragmentWindowPolicyRiskReason = ""
		diagnosis.FragmentWindowPolicyRiskMode = ""
		diagnosis.FragmentEstimatedMaxFragments = 0
	case "fragment_window_policy_risk":
		diagnosis.WriteTimeouts = 0
		diagnosis.CreditWaitTimeouts = 0
		diagnosis.ConnectionWindowExhausted = 0
		diagnosis.FragmentBackpressure = 0
		diagnosis.FragmentFramesIn = 0
		diagnosis.FragmentFramesOut = 0
		diagnosis.FragmentDeferredStreamWindowUpdates = 0
		diagnosis.FragmentDeferredConnectionWindowUpdates = 0
		diagnosis.BackpressureEvents = 0
	}
	return diagnosis
}

func withRPCMuxManagerFlowControlEvents(diagnosis RPCMuxConnectionManagerDiagnosis, event string) RPCMuxConnectionManagerDiagnosis {
	diagnosis.FlowControl = withRPCMuxFlowControlEvents(diagnosis.FlowControl, event)
	return diagnosis
}

func withRPCMuxFlowControlEvents(diagnosis RPCMuxFlowControlDiagnosis, event string) RPCMuxFlowControlDiagnosis {
	diagnosis.Events = rpcMuxFlowControlEventsFromCounts(
		event,
		diagnosis.WriteTimeouts,
		diagnosis.CreditWaitTimeouts,
		diagnosis.ConnectionWindowExhausted,
		diagnosis.FragmentBackpressure,
		diagnosis.FragmentWindowRefills,
		diagnosis.FragmentWindowPolicyRisk,
	)
	return diagnosis
}

func rpcMuxFlowControlEventsFromCounts(
	event string,
	writeTimeouts int64,
	creditWaitTimeouts int64,
	connectionWindowExhausted int64,
	fragmentBackpressure int64,
	fragmentWindowRefills int64,
	fragmentWindowPolicyRisk bool,
) []RPCMuxFlowControlEventDiagnosis {
	event = NormalizeRPCMuxFlowControlEvent(event)
	events := make([]RPCMuxFlowControlEventDiagnosis, 0, 5)
	add := func(name string, count int64) {
		if count <= 0 {
			return
		}
		if event != "" && event != name {
			return
		}
		events = append(events, RPCMuxFlowControlEventDiagnosis{Event: name, Count: count})
	}
	add("write_timeout", writeTimeouts)
	add("credit_wait_timeout", creditWaitTimeouts)
	add("connection_window_exhausted", connectionWindowExhausted)
	add("fragment_backpressure", fragmentBackpressure)
	add("fragment_window_refill", fragmentWindowRefills)
	if fragmentWindowPolicyRisk {
		add("fragment_window_policy_risk", 1)
	}
	return events
}

func RPCMuxDiagnosisEvents(diagnosis RPCMuxTransportDiagnosis) []RPCMuxDiagnosisEvent {
	events := make([]RPCMuxDiagnosisEvent, 0, 8)
	events = appendMuxCandidateNegotiationDiagnosisEvents(events, diagnosis.Candidate)
	events = appendMuxFlowControlDiagnosisEvents(events, diagnosis.FlowControl)
	events = appendMuxDrainDiagnosisEvents(events, diagnosis.Drain)
	events = appendMuxManagerDiagnosisEvents(events, diagnosis.Manager)
	return events
}

func withRPCMuxNegotiationDiagnosis(diagnosis RPCMuxTransportDiagnosis) RPCMuxTransportDiagnosis {
	snapshot := diagnosis.Candidate
	if !snapshot.Enabled && diagnosis.Manager.Candidate.Enabled {
		snapshot = diagnosis.Manager.Candidate
	}
	diagnosis.Negotiation = muxNegotiationDiagnosisFromCandidate(snapshot)
	return diagnosis
}

func muxNegotiationDiagnosisFromCandidate(snapshot ExperimentalMuxCandidateSnapshot) RPCMuxNegotiationDiagnosis {
	if !snapshot.Enabled || snapshot.NegotiationFailures <= 0 {
		return RPCMuxNegotiationDiagnosis{}
	}
	diagnosis := RPCMuxNegotiationDiagnosis{
		Failures:     snapshot.NegotiationFailures,
		LastEvent:    muxCandidateNegotiationDiagnosisEvent(snapshot.LastNegotiationPhase),
		LastPhase:    snapshot.LastNegotiationPhase,
		LastError:    snapshot.LastNegotiationError,
		PeerProtocol: snapshot.PeerProtocol,
	}
	if len(snapshot.NegotiationFailureEvents) == 0 {
		setMuxNegotiationDiagnosisCount(&diagnosis, diagnosis.LastEvent, snapshot.NegotiationFailures)
		return diagnosis
	}
	var summed int64
	for _, event := range sortedStringInt64Keys(snapshot.NegotiationFailureEvents) {
		count := snapshot.NegotiationFailureEvents[event]
		if count <= 0 {
			continue
		}
		summed += count
		setMuxNegotiationDiagnosisCount(&diagnosis, event, count)
	}
	if diagnosis.Failures <= 0 {
		diagnosis.Failures = summed
	}
	return diagnosis
}

func setMuxNegotiationDiagnosisCount(diagnosis *RPCMuxNegotiationDiagnosis, event string, count int64) {
	if diagnosis == nil || count <= 0 {
		return
	}
	switch normalizeMuxDiagnosisEventField(event) {
	case "tls_failure":
		diagnosis.TLSFailure = count
	case "alpn_mismatch":
		diagnosis.ALPNMismatch = count
	case "preface_mismatch":
		diagnosis.PrefaceMismatch = count
	case "protocol_mismatch":
		diagnosis.ProtocolMismatch = count
	case "frame_policy_mismatch":
		diagnosis.FramePolicyMismatch = count
	case experimentalMuxCandidateFailurePolicyRisk:
		diagnosis.PolicyRiskRejected = count
	}
}

func appendMuxCandidateNegotiationDiagnosisEvents(events []RPCMuxDiagnosisEvent, snapshot ExperimentalMuxCandidateSnapshot) []RPCMuxDiagnosisEvent {
	if !snapshot.Enabled || snapshot.NegotiationFailures <= 0 {
		return events
	}
	if len(snapshot.NegotiationFailureEvents) > 0 {
		lastEvent := muxCandidateNegotiationDiagnosisEvent(snapshot.LastNegotiationPhase)
		for _, event := range sortedStringInt64Keys(snapshot.NegotiationFailureEvents) {
			count := snapshot.NegotiationFailureEvents[event]
			if count <= 0 {
				continue
			}
			item := RPCMuxDiagnosisEvent{
				Family: "negotiation",
				Event:  event,
				Count:  count,
			}
			if event == lastEvent {
				item.PeerProtocol = snapshot.PeerProtocol
				item.Reason = snapshot.LastNegotiationError
			}
			events = append(events, item)
		}
		return events
	}
	event := muxCandidateNegotiationDiagnosisEvent(snapshot.LastNegotiationPhase)
	if event == "" {
		return events
	}
	return append(events, RPCMuxDiagnosisEvent{
		Family:       "negotiation",
		Event:        event,
		Count:        snapshot.NegotiationFailures,
		PeerProtocol: snapshot.PeerProtocol,
		Reason:       snapshot.LastNegotiationError,
	})
}

func muxCandidateNegotiationDiagnosisEvent(phase string) string {
	switch normalizeMuxDiagnosisEventField(phase) {
	case experimentalMuxCandidateFailureTLS:
		return "tls_failure"
	case experimentalMuxCandidateFailureALPN:
		return "alpn_mismatch"
	case experimentalMuxCandidateFailurePreface:
		return "preface_mismatch"
	case experimentalMuxCandidateFailureProtocol:
		return "protocol_mismatch"
	case experimentalMuxCandidateFailureFramePolicy:
		return "frame_policy_mismatch"
	case experimentalMuxCandidateFailurePolicyRisk:
		return experimentalMuxCandidateFailurePolicyRisk
	default:
		return ""
	}
}

func appendMuxFlowControlDiagnosisEvents(events []RPCMuxDiagnosisEvent, diagnosis RPCMuxFlowControlDiagnosis) []RPCMuxDiagnosisEvent {
	for _, item := range withRPCMuxFlowControlEvents(diagnosis, "").Events {
		events = append(events, RPCMuxDiagnosisEvent{
			Family: "flow_control",
			Event:  item.Event,
			Count:  item.Count,
		})
	}
	return events
}

func appendMuxDrainDiagnosisEvents(events []RPCMuxDiagnosisEvent, diagnosis RPCMuxDrainDiagnosis) []RPCMuxDiagnosisEvent {
	if diagnosis.GoAwayFramesOut > 0 {
		events = append(events, RPCMuxDiagnosisEvent{
			Family:    "drain",
			Event:     "goaway_out",
			Count:     diagnosis.GoAwayFramesOut,
			Reason:    diagnosis.DrainReason,
			Direction: "out",
		})
	}
	if diagnosis.GoAwayFramesIn > 0 {
		events = append(events, RPCMuxDiagnosisEvent{
			Family:    "drain",
			Event:     "goaway_in",
			Count:     diagnosis.GoAwayFramesIn,
			Reason:    diagnosis.RemoteDrainReason,
			Direction: "in",
		})
	}
	if diagnosis.DrainRejects > 0 {
		events = append(events, RPCMuxDiagnosisEvent{
			Family: "drain",
			Event:  "drain_reject",
			Count:  diagnosis.DrainRejects,
			Reason: diagnosis.DrainReason,
		})
	}
	return events
}

func appendMuxManagerDiagnosisEvents(events []RPCMuxDiagnosisEvent, diagnosis RPCMuxConnectionManagerDiagnosis) []RPCMuxDiagnosisEvent {
	if !diagnosis.Enabled {
		return events
	}
	events = appendMuxManagerFlowControlDiagnosisEvents(events, diagnosis.FlowControl, diagnosis.Endpoints)
	if diagnosis.OpenRetries > 0 {
		events = append(events, RPCMuxDiagnosisEvent{
			Family: "retry",
			Event:  "open_before_retry",
			Count:  diagnosis.OpenRetries,
			From:   diagnosis.LastRetriedFrom,
			To:     diagnosis.LastRetriedTo,
		})
	}
	for _, reason := range sortedStringInt64Keys(diagnosis.RetryReasons) {
		count := diagnosis.RetryReasons[reason]
		if count <= 0 {
			continue
		}
		events = append(events, RPCMuxDiagnosisEvent{
			Family: "retry",
			Event:  "retry_reason",
			Reason: reason,
			Count:  count,
			From:   diagnosis.LastRetriedFrom,
			To:     diagnosis.LastRetriedTo,
		})
	}
	for _, item := range diagnosis.Health {
		if item.Endpoint == "" || (item.Reason == "" && !item.Ejected && item.Cooldown <= 0) {
			continue
		}
		events = append(events, RPCMuxDiagnosisEvent{
			Family:   "health",
			Event:    "endpoint_cooldown",
			Endpoint: item.Endpoint,
			Reason:   item.Reason,
			Cooldown: item.Cooldown,
		})
	}
	for _, reason := range sortedStringInt64Keys(diagnosis.CloseReasons) {
		count := diagnosis.CloseReasons[reason]
		if count > 0 {
			events = append(events, RPCMuxDiagnosisEvent{Family: "lifecycle", Event: "close", Reason: reason, Count: count})
		}
	}
	for _, reason := range sortedStringInt64Keys(diagnosis.DrainReasons) {
		count := diagnosis.DrainReasons[reason]
		if count > 0 {
			events = append(events, RPCMuxDiagnosisEvent{Family: "drain", Event: "manager_drain", Reason: reason, Count: count})
		}
	}
	return events
}

func appendMuxManagerFlowControlDiagnosisEvents(events []RPCMuxDiagnosisEvent, diagnosis RPCMuxFlowControlDiagnosis, endpoints []ExperimentalMuxEndpointSnapshot) []RPCMuxDiagnosisEvent {
	for _, item := range withRPCMuxFlowControlEvents(diagnosis, "").Events {
		event := RPCMuxDiagnosisEvent{
			Family: "flow_control",
			Event:  item.Event,
			Count:  item.Count,
		}
		if len(endpoints) == 1 {
			event.Endpoint = endpoints[0].Endpoint
			event.ConnectionID = endpoints[0].ConnectionID
			event.PoolSlot = endpoints[0].PoolSlot
		}
		events = append(events, event)
	}
	if len(endpoints) <= 1 {
		return events
	}
	for _, endpoint := range endpoints {
		endpointDiagnosis := rpcMuxFlowControlDiagnosisFromTransport(endpoint.Adapter.Transport)
		for _, item := range withRPCMuxFlowControlEvents(endpointDiagnosis, "").Events {
			events = append(events, RPCMuxDiagnosisEvent{
				Family:       "flow_control",
				Event:        item.Event,
				Count:        item.Count,
				Endpoint:     endpoint.Endpoint,
				ConnectionID: endpoint.ConnectionID,
				PoolSlot:     endpoint.PoolSlot,
			})
		}
	}
	return events
}

func rpcMuxFlowControlDiagnosisFromTransport(transport ExperimentalMuxTransportSnapshot) RPCMuxFlowControlDiagnosis {
	return RPCMuxFlowControlDiagnosis{
		ReceiveQueueSize:                        transport.ReceiveQueueSize,
		ConnectionWindow:                        transport.ConnectionWindow,
		ConnectionCreditWaits:                   transport.ConnectionCreditWaits,
		StreamCreditWaits:                       transport.CreditWaits,
		CreditWaitTimeouts:                      transport.CreditWaitTimeouts,
		WriteTimeouts:                           transport.WriteTimeouts,
		ConnectionWindowExhausted:               transport.ConnectionWindowExhausted,
		FragmentFramesIn:                        transport.FragmentFramesIn,
		FragmentFramesOut:                       transport.FragmentFramesOut,
		FragmentBackpressure:                    experimentalMuxFragmentBackpressure(transport),
		FragmentStreamWindowUpdatePolicy:        transport.FragmentStreamWindowUpdatePolicy,
		FragmentConnectionWindowUpdatePolicy:    transport.FragmentConnectionWindowUpdatePolicy,
		FragmentStreamWindowRefillRatio:         transport.FragmentStreamWindowRefillRatio,
		FragmentConnectionWindowRefillRatio:     transport.FragmentConnectionWindowRefillRatio,
		FragmentMaxDeferredFragments:            transport.FragmentMaxDeferredFragments,
		FragmentWindowRefills:                   transport.FragmentWindowRefills,
		FragmentWindowRefillLatencyTotal:        transport.FragmentWindowRefillLatencyTotal,
		FragmentWindowRefillLatencyMax:          transport.FragmentWindowRefillLatencyMax,
		FragmentWindowRefillLatencyAvg:          transport.FragmentWindowRefillLatencyAvg,
		FragmentDeferredStreamWindowUpdates:     transport.FragmentDeferredStreamWindowUpdates,
		FragmentDeferredConnectionWindowUpdates: transport.FragmentDeferredConnectionWindowUpdates,
		FragmentWindowPolicyRisk:                transport.FragmentWindowPolicyRisk,
		FragmentWindowPolicyRiskReason:          transport.FragmentWindowPolicyRiskReason,
		FragmentWindowPolicyRiskMode:            transport.FragmentWindowPolicyRiskMode,
		FragmentEstimatedMaxFragments:           transport.FragmentEstimatedMaxFragments,
		WindowFramesIn:                          transport.WindowFramesIn,
		WindowFramesOut:                         transport.WindowFramesOut,
		ConnectionWindowIn:                      transport.ConnectionWindowFramesIn,
		ConnectionWindowOut:                     transport.ConnectionWindowFramesOut,
		BackpressureEvents:                      transport.BackpressureEvents,
		LastFlowControlEvent:                    transport.LastFlowControlEvent,
		LastFlowControlEventAt:                  transport.LastFlowControlEventAt,
		LastBackpressureEvent:                   transport.LastBackpressureEvent,
		LastBackpressureEventAt:                 transport.LastBackpressureEventAt,
	}
}

func sortedStringInt64Keys(values map[string]int64) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type RPCMuxKeepaliveDiagnosis struct {
	Liveness           string        `json:"liveness,omitempty"`
	Interval           time.Duration `json:"interval,omitempty"`
	Idle               time.Duration `json:"idle,omitempty"`
	PingFramesIn       int64         `json:"pingFramesIn,omitempty"`
	PingFramesOut      int64         `json:"pingFramesOut,omitempty"`
	PongFramesIn       int64         `json:"pongFramesIn,omitempty"`
	PongFramesOut      int64         `json:"pongFramesOut,omitempty"`
	IdleTimeouts       int64         `json:"idleTimeouts,omitempty"`
	LastPingAt         time.Time     `json:"lastPingAt,omitempty"`
	LastPongAt         time.Time     `json:"lastPongAt,omitempty"`
	LastFrameReadAt    time.Time     `json:"lastFrameReadAt,omitempty"`
	LastFrameWrittenAt time.Time     `json:"lastFrameWrittenAt,omitempty"`
}

type RPCMuxDrainDiagnosis struct {
	Draining          bool   `json:"draining,omitempty"`
	RemoteDraining    bool   `json:"remoteDraining,omitempty"`
	DrainReason       string `json:"drainReason,omitempty"`
	RemoteDrainReason string `json:"remoteDrainReason,omitempty"`
	GoAwayFramesIn    int64  `json:"goAwayFramesIn,omitempty"`
	GoAwayFramesOut   int64  `json:"goAwayFramesOut,omitempty"`
	DrainRejects      int64  `json:"drainRejects,omitempty"`
}

type RPCRetryDiagnosisSnapshot struct {
	Enabled  bool          `json:"enabled"`
	Attempts int           `json:"attempts,omitempty"`
	Backoff  time.Duration `json:"backoff,omitempty"`
}

type RPCBalancerDiagnosisSnapshot struct {
	Name string `json:"name,omitempty"`
}

type RPCResolverRuntimeSnapshot struct {
	Type     string            `json:"type,omitempty"`
	Watch    bool              `json:"watch"`
	Snapshot *ResolverSnapshot `json:"snapshot,omitempty"`
}

type RPCDiscoveryRuntimeSnapshot struct {
	WatchEnabled   bool      `json:"watchEnabled"`
	Updates        int64     `json:"updates,omitempty"`
	LastUpdated    time.Time `json:"lastUpdated,omitempty"`
	Endpoints      []string  `json:"endpoints,omitempty"`
	Added          []string  `json:"added,omitempty"`
	Removed        []string  `json:"removed,omitempty"`
	Updated        []string  `json:"updated,omitempty"`
	CloseIdleCalls int64     `json:"closeIdleCalls,omitempty"`
	WatchError     string    `json:"watchError,omitempty"`
}

type RPCPolicyRuntimeState struct {
	TimeoutEnforced     bool          `json:"timeoutEnforced"`
	EffectiveTimeout    time.Duration `json:"effectiveTimeout,omitempty"`
	RetryAttempts       int           `json:"retryAttempts,omitempty"`
	RetryBackoff        time.Duration `json:"retryBackoff,omitempty"`
	BreakerEnabled      bool          `json:"breakerEnabled"`
	Balancer            string        `json:"balancer,omitempty"`
	LoadShedderEnabled  bool          `json:"loadShedderEnabled"`
	LoadShedderLimit    int           `json:"loadShedderLimit,omitempty"`
	LoadShedderMode     string        `json:"loadShedderMode,omitempty"`
	LoadShedderWindow   time.Duration `json:"loadShedderWindow,omitempty"`
	FallbackEnabled     bool          `json:"fallbackEnabled"`
	FallbackTarget      string        `json:"fallbackTarget,omitempty"`
	HedgeEnabled        bool          `json:"hedgeEnabled"`
	HedgeAttempts       int           `json:"hedgeAttempts,omitempty"`
	GovernanceBacked    bool          `json:"governanceBacked"`
	ExplicitPolicyBound bool          `json:"explicitPolicyBound"`
	DynamicPolicyBound  bool          `json:"dynamicPolicyBound"`
}

type RPCPolicyRuntimeCacheSnapshot struct {
	RateLimiters        int `json:"rateLimiters,omitempty"`
	ConcurrencyLimiters int `json:"concurrencyLimiters,omitempty"`
	Breakers            int `json:"breakers,omitempty"`
	Balancers           int `json:"balancers,omitempty"`
}

type RPCPolicyRuntimeSnapshotSource interface {
	PolicyRuntimeSnapshot() RPCPolicyRuntimeSnapshot
}

type RPCRuntimeSnapshotSource interface {
	RuntimeSnapshot() RPCRuntimeSnapshot
}

type RPCPolicyRuntimeContributor struct {
	Name   string
	Client RPCPolicyRuntimeSnapshotSource
}

type RPCRuntimeContributor struct {
	Name   string
	Client RPCRuntimeSnapshotSource
}

func (c RPCPolicyRuntimeContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil || c.Client == nil {
		return nil
	}
	runtimeSnapshot := c.Client.PolicyRuntimeSnapshot()
	data, err := json.Marshal(runtimeSnapshot)
	if err != nil {
		return fmt.Errorf("marshal rpc policy runtime snapshot: %w", err)
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage, 1)
	}
	key := "rpc.policy.runtime"
	if strings.TrimSpace(c.Name) != "" {
		key += "." + strings.TrimSpace(c.Name)
	}
	snapshot.Configs[key] = append(json.RawMessage(nil), data...)
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string, 2)
	}
	snapshot.Metadata["rpc.policy.runtime"] = "available"
	snapshot.Metadata["rpc.policy.runtime.enforcement"] = "timeout,retry,breaker,balancer,load_shedder,fallback,hedge"
	return nil
}

func (c RPCRuntimeContributor) ContributeSnapshot(ctx context.Context, snapshot *controlplane.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil || c.Client == nil {
		return nil
	}
	runtimeSnapshot := c.Client.RuntimeSnapshot()
	data, err := json.Marshal(runtimeSnapshot)
	if err != nil {
		return fmt.Errorf("marshal rpc runtime snapshot: %w", err)
	}
	if snapshot.Configs == nil {
		snapshot.Configs = make(map[string]json.RawMessage, 1)
	}
	key := "rpc.runtime"
	if strings.TrimSpace(c.Name) != "" {
		key += "." + strings.TrimSpace(c.Name)
	}
	snapshot.Configs[key] = append(json.RawMessage(nil), data...)
	if snapshot.Metadata == nil {
		snapshot.Metadata = make(map[string]string, 1)
	}
	snapshot.Metadata["rpc.runtime"] = "available"
	return nil
}

func (c *HTTPClient) RuntimeSnapshot() RPCRuntimeSnapshot {
	if c == nil {
		return RPCRuntimeSnapshot{}
	}
	return RPCRuntimeSnapshot{
		Target:      c.target,
		Codec:       c.opts.codec.Name(),
		Transport:   c.httpTransportSnapshot(),
		Middlewares: RPCEndpointChainSnapshot{Unary: len(c.opts.middlewares), Stream: len(c.opts.streamMiddlewares)},
		Resolver:    c.resolverRuntimeSnapshot(),
		Balancer:    c.effectiveBalancerName(RPCBalancerPolicy{}),
		ConnPool:    c.connPoolSnapshot(),
		Policy:      c.PolicyRuntimeSnapshot(),
		Discovery:   c.discovery.Snapshot(),
		Stats:       c.callStatsSnapshot(),
		Warmup:      c.warmupSnapshot(),
		Diagnosis:   c.diagnosisSnapshot(),
	}
}

func (c *HTTPClient) RuntimeComponentSnapshot(ctx context.Context) coreruntime.ComponentSnapshot {
	if err := ctx.Err(); err != nil {
		return coreruntime.ComponentSnapshot{
			Name:   "rpc.http.client",
			Kind:   "client",
			Owner:  "rpc",
			Target: c.target,
			Status: "error",
			Error:  err.Error(),
		}
	}
	snapshot := c.RuntimeSnapshot()
	return coreruntime.ComponentSnapshot{
		Name:   "rpc.http.client",
		Kind:   "client",
		Owner:  "rpc",
		Target: snapshot.Target,
		Status: "ok",
		Middleware: &coreruntime.MiddlewareSnapshot{
			Unary:  middlewareCountLayers("client_middleware", snapshot.Middlewares.Unary),
			Stream: middlewareCountLayers("client_stream_middleware", snapshot.Middlewares.Stream),
		},
		Governance: snapshot.Policy,
		Resolver:   snapshot.Resolver,
		Balancer:   snapshot.Balancer,
		ConnPool:   snapshot.ConnPool,
		Retry:      snapshot.Policy.State.RetryAttempts,
		Breaker:    snapshot.Policy.State.BreakerEnabled,
		Details:    snapshot,
	}
}

func (c *HTTPClient) DiagnosisProbe(ctx context.Context, service string, method string, endpoint string) RPCDiagnosisProbe {
	return c.DiagnosisProbeWithOptions(ctx, RPCDiagnosisProbeOptions{
		Service:  service,
		Method:   method,
		Endpoint: endpoint,
	})
}

type RPCDiagnosisProbeOptions struct {
	Service          string
	Method           string
	Endpoint         string
	ConnectionID     string
	PoolSlot         int
	FlowControlEvent string
	EventFamily      string
	Event            string
}

func (c *HTTPClient) DiagnosisProbeWithOptions(ctx context.Context, opts RPCDiagnosisProbeOptions) RPCDiagnosisProbe {
	if ctx == nil {
		ctx = context.Background()
	}
	service := strings.Trim(strings.TrimSpace(opts.Service), "/")
	method := strings.Trim(strings.TrimSpace(opts.Method), "/")
	endpoint := strings.TrimRight(strings.TrimSpace(opts.Endpoint), "/")
	connectionID := normalizeMuxDiagnosisConnectionID(opts.ConnectionID)
	flowControlEvent := NormalizeRPCMuxFlowControlEvent(opts.FlowControlEvent)
	eventFamily := normalizeMuxDiagnosisEventField(opts.EventFamily)
	eventName := normalizeMuxDiagnosisEventField(opts.Event)
	fullMethod := method
	if service != "" && method != "" && !strings.Contains(method, "/") {
		fullMethod = service + "/" + method
	}
	runtimeSnapshot := c.RuntimeSnapshot()
	runtimeSnapshot.Diagnosis.Mux.Events = RPCMuxDiagnosisEvents(runtimeSnapshot.Diagnosis.Mux)
	probe := RPCDiagnosisProbe{
		Target:       runtimeSnapshot.Target,
		Service:      service,
		Method:       fullMethod,
		Endpoint:     endpoint,
		ConnectionID: connectionID,
		PoolSlot:     opts.PoolSlot,
		FlowControl:  flowControlEvent,
		EventFamily:  eventFamily,
		Event:        eventName,
		Diagnosis:    runtimeSnapshot.Diagnosis,
		Discovery:    runtimeSnapshot.Discovery,
		GeneratedAt:  time.Now(),
	}
	if flowControlEvent != "" || eventFamily != "" || eventName != "" || endpoint != "" || connectionID != "" || opts.PoolSlot > 0 {
		probe.Diagnosis.Mux = FilterRPCMuxDiagnosis(probe.Diagnosis.Mux, RPCMuxDiagnosisFilter{
			Endpoint:         endpoint,
			ConnectionID:     connectionID,
			PoolSlot:         opts.PoolSlot,
			FlowControlEvent: flowControlEvent,
			EventFamily:      eventFamily,
			Event:            eventName,
		})
	}
	probe.Diagnosis.Mux.Events = rpcMuxDiagnosisEventView(probe.Diagnosis.Mux, RPCMuxDiagnosisFilter{
		Endpoint:     endpoint,
		ConnectionID: connectionID,
		PoolSlot:     opts.PoolSlot,
		EventFamily:  eventFamily,
		Event:        eventName,
	})
	if fullMethod != "" {
		probe.Policy = c.EffectivePolicySnapshot(ctx, fullMethod)
	}
	probe.Matched = rpcDiagnosisProbeFilterMatched(runtimeSnapshot, RPCMuxDiagnosisFilter{
		Endpoint:     endpoint,
		ConnectionID: connectionID,
		PoolSlot:     opts.PoolSlot,
		EventFamily:  eventFamily,
		Event:        eventName,
	})
	return probe
}

func (c *HTTPClient) ServeDiagnosis(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		controladmin.WriteJSON(w, http.StatusOK, c.DiagnosisProbe(context.Background(), "", "", ""))
		return
	}
	query := r.URL.Query()
	controladmin.WriteJSON(w, http.StatusOK, c.DiagnosisProbeWithOptions(r.Context(), RPCDiagnosisProbeOptions{
		Service:          query.Get("service"),
		Method:           query.Get("method"),
		Endpoint:         query.Get("endpoint"),
		ConnectionID:     query.Get("connectionId"),
		PoolSlot:         parsePositiveIntQuery(query.Get("poolSlot")),
		FlowControlEvent: query.Get("flowControlEvent"),
		EventFamily:      query.Get("eventFamily"),
		Event:            query.Get("event"),
	}))
}

func (c *HTTPClient) DiagnosisHandler() http.Handler {
	return http.HandlerFunc(c.ServeDiagnosis)
}

func parsePositiveIntQuery(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func rpcDiagnosisProbeFilterMatched(snapshot RPCRuntimeSnapshot, filter RPCMuxDiagnosisFilter) bool {
	endpoint := normalizeMuxDiagnosisEndpoint(filter.Endpoint)
	connectionID := normalizeMuxDiagnosisConnectionID(filter.ConnectionID)
	eventFamily := normalizeMuxDiagnosisEventField(filter.EventFamily)
	eventName := normalizeMuxDiagnosisEventField(filter.Event)
	if endpoint == "" && connectionID == "" && filter.PoolSlot <= 0 && eventFamily == "" && eventName == "" {
		return true
	}
	if eventFamily != "" || eventName != "" {
		diagnosis := FilterRPCMuxDiagnosis(snapshot.Diagnosis.Mux, filter)
		if len(rpcMuxDiagnosisEventView(diagnosis, filter)) == 0 {
			return false
		}
		if endpoint != "" && !rpcDiagnosisProbeMatched(snapshot, endpoint) {
			return false
		}
		return true
	}
	if connectionID != "" || filter.PoolSlot > 0 {
		return rpcMuxManagerDiagnosisHasConnection(snapshot.Diagnosis.Mux.Manager, endpoint, connectionID, filter.PoolSlot)
	}
	if endpoint != "" && rpcDiagnosisProbeMatched(snapshot, endpoint) {
		return true
	}
	return false
}

func rpcDiagnosisProbeMatched(snapshot RPCRuntimeSnapshot, endpoint string) bool {
	if endpoint == "" {
		return true
	}
	if strings.TrimRight(snapshot.Target, "/") == endpoint {
		return true
	}
	if slicesContainsString(snapshot.Discovery.Endpoints, endpoint) {
		return true
	}
	if snapshot.Resolver.Snapshot != nil && slicesContainsString(snapshot.Resolver.Snapshot.Endpoints, endpoint) {
		return true
	}
	if rpcMuxManagerDiagnosisHasEndpoint(snapshot.Diagnosis.Mux.Manager, endpoint) {
		return true
	}
	return false
}

func rpcMuxManagerDiagnosisHasConnection(diagnosis RPCMuxConnectionManagerDiagnosis, endpoint string, connectionID string, poolSlot int) bool {
	endpoint = normalizeMuxDiagnosisEndpoint(endpoint)
	connectionID = normalizeMuxDiagnosisConnectionID(connectionID)
	if endpoint == "" && connectionID == "" && poolSlot <= 0 {
		return false
	}
	for _, item := range diagnosis.Endpoints {
		if rpcMuxEndpointSnapshotMatched(item, endpoint, connectionID, poolSlot) {
			return true
		}
	}
	return false
}

func rpcMuxManagerDiagnosisHasEndpoint(diagnosis RPCMuxConnectionManagerDiagnosis, endpoint string) bool {
	endpoint = normalizeMuxDiagnosisEndpoint(endpoint)
	if endpoint == "" {
		return false
	}
	for _, item := range diagnosis.Endpoints {
		if normalizeMuxDiagnosisEndpoint(item.Endpoint) == endpoint {
			return true
		}
	}
	for _, item := range diagnosis.Health {
		if normalizeMuxDiagnosisEndpoint(item.Endpoint) == endpoint {
			return true
		}
	}
	return false
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimRight(strings.TrimSpace(value), "/") == want {
			return true
		}
	}
	return false
}

func (c *HTTPClient) callStatsSnapshot() callstats.Snapshot {
	if c == nil || c.stats == nil {
		return callstats.Snapshot{}
	}
	return c.stats.Snapshot()
}

func (c *HTTPClient) warmupSnapshot() RPCWarmupSnapshot {
	if c == nil {
		return RPCWarmupSnapshot{}
	}
	c.warmupMu.Lock()
	defer c.warmupMu.Unlock()
	return cloneRPCWarmupSnapshot(c.warmup)
}

func (c *HTTPClient) connPoolSnapshot() ConnPoolManagerSnapshot {
	if c == nil || c.opts.connPool == nil {
		return ConnPoolManagerSnapshot{}
	}
	return c.opts.connPool.Snapshot()
}

func (c *HTTPClient) diagnosisSnapshot() RPCDiagnosisSnapshot {
	if c == nil {
		return RPCDiagnosisSnapshot{}
	}
	policy := c.effectiveRPCPolicy(governance.Policy{})
	state := c.policyRuntimeState(policy)
	snapshot := RPCDiagnosisSnapshot{
		Transport: c.httpTransportSnapshot(),
		ConnPool:  c.connPoolSnapshot(),
		Retry: RPCRetryDiagnosisSnapshot{
			Enabled:  state.RetryAttempts > 1,
			Attempts: state.RetryAttempts,
			Backoff:  state.RetryBackoff,
		},
		Resolver: c.resolverRuntimeSnapshot(),
		Balancer: RPCBalancerDiagnosisSnapshot{Name: state.Balancer},
	}
	if c.opts.muxClientAdapter != nil {
		snapshot.Mux = c.opts.muxClientAdapter.DiagnosisSnapshot()
	}
	if c.opts.muxManager != nil {
		managerMux := muxDiagnosisFromManager(c.opts.muxManager.DiagnosisSnapshot())
		if snapshot.Mux.Mode == "" {
			snapshot.Mux = managerMux
		} else {
			snapshot.Mux.Enabled = snapshot.Mux.Enabled || managerMux.Enabled
			snapshot.Mux.Manager = managerMux.Manager
			if snapshot.Mux.Candidate.Protocol == "" {
				snapshot.Mux.Candidate = managerMux.Candidate
			}
			if snapshot.Mux.FlowControl.Events == nil {
				snapshot.Mux.FlowControl = managerMux.FlowControl
			}
			snapshot.Mux = withRPCMuxNegotiationDiagnosis(snapshot.Mux)
			snapshot.Mux.Events = RPCMuxDiagnosisEvents(snapshot.Mux)
		}
	}
	snapshot.Mux = withRPCMuxNegotiationDiagnosis(snapshot.Mux)
	snapshot.Mux.Events = RPCMuxDiagnosisEvents(snapshot.Mux)
	return snapshot
}

func middlewareCountLayers(name string, count int) []coreruntime.MiddlewareLayer {
	if count <= 0 {
		return nil
	}
	layers := make([]coreruntime.MiddlewareLayer, 0, count)
	for i := range count {
		layers = append(layers, coreruntime.MiddlewareLayer{
			Name:   name,
			Source: "user",
			Order:  i,
		})
	}
	return layers
}

func (c *HTTPClient) PolicyRuntimeSnapshot() RPCPolicyRuntimeSnapshot {
	if c == nil {
		return RPCPolicyRuntimeSnapshot{}
	}
	policy := c.effectiveRPCPolicy(governance.Policy{})
	state := c.policyRuntimeState(policy)
	return RPCPolicyRuntimeSnapshot{
		Policy:            cloneRPCPolicy(policy),
		State:             state,
		Cache:             c.policyRuntimeCacheSnapshot(),
		MethodPolicyCount: len(policy.Methods),
		MethodPolicyKeys:  rpcMethodPolicyKeys(policy.Methods),
		Priority:          rpcPolicyPriority(),
		Capabilities:      rpcPolicyRuntimeCapabilities(),
	}
}

func (c *HTTPClient) EffectivePolicySnapshot(ctx context.Context, method string) RPCEffectivePolicySnapshot {
	if ctx == nil {
		ctx = context.Background()
	}
	governanceReq := c.rpcGovernanceRequest(ctx, method)
	decision := c.governanceDecision(ctx, governanceReq)
	policy := c.effectiveRPCPolicy(decision.Policy)
	if c.opts.rpcPolicyProvider != nil {
		if dynamicPolicy, err := c.opts.rpcPolicyProvider.RPCPolicy(ctx, governanceReq); err == nil {
			policy = mergeRPCPolicy(policy, dynamicPolicy)
		}
	}
	methodPolicy, methodKey, ok := matchRPCMethodPolicyWithKey(policy.Methods, method)
	policy.Methods = nil
	if ok {
		methodPolicy.Methods = nil
		policy = mergeRPCPolicy(policy, methodPolicy)
	}
	return RPCEffectivePolicySnapshot{
		Method:         strings.Trim(strings.TrimSpace(method), "/"),
		MethodKey:      methodKey,
		Policy:         cloneRPCPolicy(policy),
		State:          c.policyRuntimeState(policy),
		Priority:       rpcPolicyPriority(),
		GovernanceRule: decision.RuleName,
	}
}

func (c *HTTPClient) httpTransportSnapshot() RPCHTTPTransportSnapshot {
	if c == nil || c.hc == nil {
		return RPCHTTPTransportSnapshot{}
	}
	transport := normalizeTransportConfig(c.opts.transport)
	snapshot := RPCHTTPTransportSnapshot{
		CloseIdleOnEndpoint: c.watchCancel != nil,
		DialTimeout:         transport.DialTimeout,
		KeepAlive:           transport.KeepAlive,
		IdleConnTimeout:     transport.IdleConnTimeout,
		StreamIdleTimeout:   c.opts.streamIdleTimeout,
		StreamConnPolicy:    dedicatedStreamConnPolicySnapshot(),
	}
	snapshot.Timeout = c.hc.Timeout
	if c.streams != nil {
		snapshot.Stream = c.streams.Snapshot()
	}
	return snapshot
}

func dedicatedStreamConnPolicySnapshot() RPCStreamConnPolicySnapshot {
	return RPCStreamConnPolicySnapshot{
		Mode:              "dedicated",
		MaxStreamsPerConn: 1,
		Reuse:             false,
		Multiplexed:       false,
	}
}

func (c *HTTPClient) resolverRuntimeSnapshot() RPCResolverRuntimeSnapshot {
	if c == nil || c.opts.resolver == nil {
		return RPCResolverRuntimeSnapshot{}
	}
	snapshot := RPCResolverRuntimeSnapshot{
		Type:  fmt.Sprintf("%T", c.opts.resolver),
		Watch: c.watchCancel != nil,
	}
	if source, ok := c.opts.resolver.(interface{ Snapshot() ResolverSnapshot }); ok {
		resolverSnapshot := source.Snapshot()
		snapshot.Snapshot = &resolverSnapshot
	}
	return snapshot
}

type clientDiscoveryRuntime struct {
	mu             sync.Mutex
	watchEnabled   bool
	updates        int64
	lastUpdated    time.Time
	endpoints      []string
	added          []string
	removed        []string
	updated        []string
	closeIdleCalls int64
	watchErr       error
}

func newClientDiscoveryRuntime(resolver Resolver) *clientDiscoveryRuntime {
	_, watchEnabled := resolver.(WatchResolver)
	return &clientDiscoveryRuntime{watchEnabled: watchEnabled}
}

func (r *clientDiscoveryRuntime) recordUpdate(endpoints []string, removed []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints = append([]string(nil), endpoints...)
	r.added = nil
	r.removed = append([]string(nil), removed...)
	r.updated = nil
	r.updates++
	r.lastUpdated = time.Now()
	r.watchErr = nil
}

func (r *clientDiscoveryRuntime) recordEvent(endpoints []string, added []string, removed []string, updated []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endpoints = append([]string(nil), endpoints...)
	r.added = append([]string(nil), added...)
	r.removed = append([]string(nil), removed...)
	r.updated = append([]string(nil), updated...)
	r.updates++
	r.lastUpdated = time.Now()
	r.watchErr = nil
}

func (r *clientDiscoveryRuntime) recordWatchError(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watchErr = err
}

func (r *clientDiscoveryRuntime) recordCloseIdle() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeIdleCalls++
}

func (r *clientDiscoveryRuntime) Snapshot() RPCDiscoveryRuntimeSnapshot {
	if r == nil {
		return RPCDiscoveryRuntimeSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := RPCDiscoveryRuntimeSnapshot{
		WatchEnabled:   r.watchEnabled,
		Updates:        r.updates,
		LastUpdated:    r.lastUpdated,
		Endpoints:      append([]string(nil), r.endpoints...),
		Added:          append([]string(nil), r.added...),
		Removed:        append([]string(nil), r.removed...),
		Updated:        append([]string(nil), r.updated...),
		CloseIdleCalls: r.closeIdleCalls,
	}
	if r.watchErr != nil {
		snapshot.WatchError = r.watchErr.Error()
	}
	return snapshot
}

func (c *HTTPClient) policyRuntimeState(policy RPCPolicy) RPCPolicyRuntimeState {
	timeout := c.opts.timeout
	if policy.Timeout > 0 {
		timeout = policy.Timeout
	}
	retryAttempts := c.opts.retry
	retryBackoff := c.opts.retryPolicy.Backoff
	if c.opts.retryPolicy.Attempts > 0 {
		retryAttempts = c.opts.retryPolicy.Attempts
	}
	if policy.Retry.Attempts > 0 {
		retryAttempts = policy.Retry.Attempts
	}
	if policy.Retry.Backoff > 0 {
		retryBackoff = policy.Retry.Backoff
	}
	return RPCPolicyRuntimeState{
		TimeoutEnforced:     timeout > 0,
		EffectiveTimeout:    timeout,
		RetryAttempts:       retryAttempts,
		RetryBackoff:        retryBackoff,
		BreakerEnabled:      policy.Breaker.Enabled || c.opts.breaker != nil || c.opts.adaptive != nil,
		Balancer:            c.effectiveBalancerName(policy.Balancer),
		LoadShedderEnabled:  policy.LoadShedder.Enabled,
		LoadShedderLimit:    rpcLoadShedderLimit(policy.LoadShedder),
		LoadShedderMode:     rpcLoadShedderMode(policy.LoadShedder),
		LoadShedderWindow:   policy.LoadShedder.MinWindow,
		FallbackEnabled:     policy.Fallback.Enabled,
		FallbackTarget:      policy.Fallback.Target,
		HedgeEnabled:        policy.Hedge.Enabled,
		HedgeAttempts:       policy.Hedge.Attempts,
		GovernanceBacked:    c.opts.rules != nil || c.opts.manager != nil,
		ExplicitPolicyBound: c.opts.rpcPolicy != nil,
		DynamicPolicyBound:  c.opts.rpcPolicyProvider != nil,
	}
}

func (c *HTTPClient) policyRuntimeCacheSnapshot() RPCPolicyRuntimeCacheSnapshot {
	if c == nil || c.runtime == nil {
		return RPCPolicyRuntimeCacheSnapshot{}
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	return RPCPolicyRuntimeCacheSnapshot{
		RateLimiters:        len(c.runtime.rateLimits),
		ConcurrencyLimiters: len(c.runtime.concurrency),
		Breakers:            len(c.runtime.breakers),
		Balancers:           len(c.runtime.balancers),
	}
}

func (c *HTTPClient) effectiveBalancerName(policy RPCBalancerPolicy) string {
	if strings.TrimSpace(policy.Name) != "" {
		return strings.TrimSpace(policy.Name)
	}
	switch c.opts.balancer.(type) {
	case *WeightedRoundRobinBalancer:
		return RPCBalancerWeightedRoundRobin
	case *P2CBalancer, *P2CEWMABalancer:
		return RPCBalancerP2C
	case *ConsistentHashBalancer:
		return RPCBalancerConsistentHash
	case *HealthBalancer:
		return RPCBalancerHealth
	default:
		return RPCBalancerRoundRobin
	}
}

func rpcLoadShedderLimit(policy RPCLoadShedderPolicy) int {
	if policy.MaxConcurrency > 0 {
		return policy.MaxConcurrency
	}
	return policy.MaxInflight
}

func rpcLoadShedderMode(policy RPCLoadShedderPolicy) string {
	if !policy.Enabled {
		return ""
	}
	return "static_concurrency"
}

func rpcPolicyRuntimeCapabilities() []string {
	return []string{"timeout", "retry", "breaker", "balancer", "load_shedder", "fallback", "hedge", "method_policy", "dynamic_policy", "endpoint_chain", "kitex_interceptor", "observability_interceptor"}
}

func rpcPolicyPriority() []string {
	return []string{"client_default", "governance_rule", "dynamic_policy", "method_policy"}
}

func RPCPolicyFromGovernance(policy governance.Policy) RPCPolicy {
	return RPCPolicy{
		Timeout:  policy.Timeout,
		Retry:    policy.Retry,
		Breaker:  policy.Breaker,
		Metadata: cloneRPCPolicyStringMap(policy.Metadata),
		Headers:  cloneRPCPolicyStringMap(policy.Headers),
	}
}

func cloneRPCPolicy(policy RPCPolicy) RPCPolicy {
	policy.Metadata = cloneRPCPolicyStringMap(policy.Metadata)
	policy.Headers = cloneRPCPolicyStringMap(policy.Headers)
	policy.Balancer.Weights = cloneRPCPolicyIntMap(policy.Balancer.Weights)
	policy.Retry.Statuses = append([]int(nil), policy.Retry.Statuses...)
	policy.Retry.Methods = append([]string(nil), policy.Retry.Methods...)
	policy.Methods = cloneRPCPolicyMethods(policy.Methods)
	return policy
}

func mergeRPCPolicy(base RPCPolicy, override RPCPolicy) RPCPolicy {
	merged := cloneRPCPolicy(base)
	if override.Timeout > 0 {
		merged.Timeout = override.Timeout
	}
	if override.Retry.Attempts > 0 {
		merged.Retry.Attempts = override.Retry.Attempts
	}
	if override.Retry.Backoff > 0 {
		merged.Retry.Backoff = override.Retry.Backoff
	}
	if len(override.Retry.Statuses) > 0 {
		merged.Retry.Statuses = append([]int(nil), override.Retry.Statuses...)
	}
	if len(override.Retry.Methods) > 0 {
		merged.Retry.Methods = append([]string(nil), override.Retry.Methods...)
	}
	if override.Breaker.Enabled {
		merged.Breaker = override.Breaker
	}
	if override.Hedge.Enabled || override.Hedge.Delay > 0 || override.Hedge.Attempts > 0 {
		merged.Hedge = override.Hedge
	}
	if override.Fallback.Enabled || strings.TrimSpace(override.Fallback.Target) != "" || strings.TrimSpace(override.Fallback.Method) != "" {
		merged.Fallback = override.Fallback
	}
	if override.LoadShedder.Enabled || override.LoadShedder.MaxConcurrency > 0 || override.LoadShedder.MaxInflight > 0 || override.LoadShedder.MinWindow > 0 {
		merged.LoadShedder = override.LoadShedder
	}
	if strings.TrimSpace(override.Balancer.Name) != "" || len(override.Balancer.Weights) > 0 || strings.TrimSpace(override.Balancer.Key) != "" {
		merged.Balancer = override.Balancer
		merged.Balancer.Weights = cloneRPCPolicyIntMap(override.Balancer.Weights)
	}
	merged.Metadata = mergeRPCPolicyStringMap(merged.Metadata, override.Metadata)
	merged.Headers = mergeRPCPolicyStringMap(merged.Headers, override.Headers)
	merged.Methods = mergeRPCPolicyMethods(merged.Methods, override.Methods)
	return merged
}

func (p RPCPolicy) Validate() error {
	if p.Timeout < 0 {
		return errors.New("rpc policy timeout must be non-negative")
	}
	if p.Retry.Attempts < 0 {
		return errors.New("rpc policy retry attempts must be non-negative")
	}
	if p.Retry.Backoff < 0 {
		return errors.New("rpc policy retry backoff must be non-negative")
	}
	if p.Hedge.Delay < 0 {
		return errors.New("rpc policy hedge delay must be non-negative")
	}
	if p.Hedge.Attempts < 0 {
		return errors.New("rpc policy hedge attempts must be non-negative")
	}
	if p.Hedge.Enabled && p.Hedge.Attempts == 1 {
		return errors.New("rpc policy hedge attempts must be zero or greater than one")
	}
	if p.Fallback.Enabled && strings.TrimSpace(p.Fallback.Target) == "" {
		return errors.New("rpc policy fallback target is required when fallback is enabled")
	}
	if p.Breaker.OpenTimeout < 0 {
		return errors.New("rpc policy breaker open timeout must be non-negative")
	}
	if p.Breaker.Window < 0 {
		return errors.New("rpc policy breaker window must be non-negative")
	}
	if p.LoadShedder.MaxConcurrency < 0 {
		return errors.New("rpc policy load shedder max concurrency must be non-negative")
	}
	if p.LoadShedder.MaxInflight < 0 {
		return errors.New("rpc policy load shedder max inflight must be non-negative")
	}
	if p.LoadShedder.MinWindow < 0 {
		return errors.New("rpc policy load shedder min window must be non-negative")
	}
	if err := validateRPCBalancerPolicy(p.Balancer); err != nil {
		return err
	}
	for method, policy := range p.Methods {
		if strings.TrimSpace(method) == "" {
			return errors.New("rpc policy method key is empty")
		}
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("rpc policy method %q: %w", method, err)
		}
	}
	return nil
}

func validateRPCBalancerPolicy(policy RPCBalancerPolicy) error {
	name := strings.TrimSpace(policy.Name)
	if name == "" {
		return nil
	}
	switch name {
	case RPCBalancerRoundRobin, RPCBalancerWeightedRoundRobin, RPCBalancerP2C, RPCBalancerConsistentHash, RPCBalancerHealth:
	default:
		return fmt.Errorf("rpc policy balancer %q is unsupported", policy.Name)
	}
	for endpoint, weight := range policy.Weights {
		if strings.TrimSpace(endpoint) == "" {
			return errors.New("rpc policy balancer weight endpoint is empty")
		}
		if weight < 0 {
			return fmt.Errorf("rpc policy balancer weight for %q must be non-negative", endpoint)
		}
	}
	return nil
}

func cloneRPCPolicyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRPCPolicyIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneRPCPolicyMethods(in map[string]RPCPolicy) map[string]RPCPolicy {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]RPCPolicy, len(in))
	for key, value := range in {
		cloned := cloneRPCPolicy(value)
		cloned.Methods = nil
		out[key] = cloned
	}
	return out
}

func mergeRPCPolicyMethods(base map[string]RPCPolicy, override map[string]RPCPolicy) map[string]RPCPolicy {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := cloneRPCPolicyMethods(base)
	if out == nil {
		out = make(map[string]RPCPolicy, len(override))
	}
	for key, value := range override {
		if existing, ok := out[key]; ok {
			out[key] = mergeRPCPolicy(existing, value)
			continue
		}
		out[key] = cloneRPCPolicy(value)
	}
	return out
}

func rpcMethodPolicyKeys(methods map[string]RPCPolicy) []string {
	if len(methods) == 0 {
		return nil
	}
	keys := make([]string, 0, len(methods))
	for key := range methods {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeRPCPolicyStringMap(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := cloneRPCPolicyStringMap(base)
	if out == nil {
		out = make(map[string]string, len(override))
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}
