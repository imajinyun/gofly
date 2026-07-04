package generator

import (
	"strconv"
	"strings"
)

const (
	generatedServiceTimeoutNS          = "3000000000"
	generatedGatewayTimeoutNS          = "5000000000"
	generatedRetryBackoffNS            = "100000000"
	generatedRestBreakerWindowNS       = "10000000000"
	generatedGovernanceBreakerWindowNS = "30000000000"
)

func withGeneratedResilienceTemplateData(data map[string]string, serviceName string) map[string]string {
	if data == nil {
		data = map[string]string{}
	}
	serviceName = strings.TrimSpace(serviceName)
	data["ServiceGovernanceFullJSON"] = generatedServiceGovernanceJSON(true)
	data["ServiceGovernanceMinimalJSON"] = generatedServiceGovernanceJSON(false)
	data["RestTimeoutConfigJSON"] = `{"duration": ` + generatedServiceTimeoutNS + `, "readHeaderTimeout": ` + generatedServiceTimeoutNS + `, "healthTimeout": 1000000000}`
	data["RestBreakerConfigJSON"] = `{"openTimeout": 5000000000, "window": ` + generatedRestBreakerWindowNS + `, "buckets": 10, "minRequests": 20, "failureRatio": 0.5}`
	data["RestRateLimitConfigJSON"] = `{"rate": 100, "burst": 100}`
	data["RestAdaptiveLimitConfigJSON"] = `{"minLimit": 16, "maxLimit": 256, "initialLimit": 64, "cpuThreshold": 80, "window": ` + generatedRestBreakerWindowNS + `, "targetLatency": ` + generatedRetryBackoffNS + `, "targetErrorRatio": 0.05, "minSamples": 20}`
	data["RestMaxConcurrencyConfigJSON"] = `{"limit": 64}`
	data["DefaultPolicyJSON"] = generatedPolicyJSON(generatedServiceTimeoutNS, generatedDefaultRetryJSON(), generatedFullBreakerPolicyJSON())
	data["RPCSayHelloPolicyJSON"] = generatedPolicyJSON("2000000000", generatedDefaultRetryJSON(), generatedMinimalBreakerPolicyJSON())
	data["GatewayPolicyJSON"] = generatedPolicyJSON(generatedGatewayTimeoutNS, generatedGatewayRetryJSON(), generatedFullBreakerPolicyJSON())
	data["GatewayRetryJSON"] = generatedGatewayRetryJSON()
	data["GatewayBreakerJSON"] = generatedFullBreakerPolicyJSON()
	data["GatewayMQPolicyJSON"] = `{"timeout": ` + generatedServiceTimeoutNS + `, "retry": ` + generatedDefaultRetryJSON() + `, "breaker": ` + generatedMinimalBreakerPolicyJSON() + `}`
	data["GovernanceRulesJSON"] = generatedGovernanceRulesJSON(serviceName)
	data["GatewayGovernanceRulesJSON"] = generatedGatewayGovernanceRulesJSON(serviceName)
	return data
}

func generatedServiceGovernanceJSON(includeRPC bool) string {
	base := `{"timeout": ` + generatedServiceTimeoutNS + `, "readHeaderTimeout": ` + generatedServiceTimeoutNS + `, "breaker": true, "retry": ` + generatedDefaultRetryJSON() + `, "rateLimit": ` + generatedRateLimitJSON() + `, "maxConcurrency": 64, "adaptiveLimit": true, "restBreaker": ` + generatedRestBreakerConfigJSON()
	if !includeRPC {
		return base + `}`
	}
	return base + `, "rpcTimeout": {"server": ` + generatedServiceTimeoutNS + `, "client": ` + generatedServiceTimeoutNS + `}, "rpcTransport": {"timeout": 30000000000, "maxIdleConns": 200, "maxIdleConnsPerHost": 100, "dialTimeout": 30000000000, "keepAlive": 30000000000, "idleConnTimeout": 90000000000, "tlsHandshakeTimeout": 10000000000, "expectContinueTimeout": 1000000000}}`
}

func generatedGovernanceRulesJSON(serviceName string) string {
	serviceNameJSON := strconv.Quote(serviceName)
	return `[
      {"name": "rest-default", "transport": "rest", "service": ` + serviceNameJSON + `, "policy": ` + generatedPolicyJSON(generatedServiceTimeoutNS, generatedDefaultRetryJSON(), generatedFullBreakerPolicyJSON()) + `},
      {"name": "rpc-default", "transport": "rpc", "service": "greeter", "policy": ` + generatedPolicyJSON(generatedServiceTimeoutNS, generatedDefaultRetryJSON(), generatedFullBreakerPolicyJSON()) + `},
      {"name": "rpc-sayhello", "transport": "rpc", "service": "greeter", "method": "SAYHELLO", "policy": ` + generatedCanaryPolicyJSON() + `},
      {"name": "mq-default", "transport": "mq", "service": ` + serviceNameJSON + `, "policy": ` + generatedPolicyJSON(generatedServiceTimeoutNS, generatedDefaultRetryJSON(), generatedFullBreakerPolicyJSON()) + `},
      {"name": "gateway-default", "transport": "gateway", "service": ` + serviceNameJSON + `, "policy": ` + generatedPolicyJSON(generatedGatewayTimeoutNS, generatedGatewayRetryJSON(), generatedFullBreakerPolicyJSON()) + `}
    ]`
}

func generatedGatewayGovernanceRulesJSON(serviceName string) string {
	serviceNameJSON := strconv.Quote(serviceName)
	return `[
      {"name": "gateway-default", "transport": "gateway", "path": "/api/*", "policy": ` + generatedPolicyJSON(generatedGatewayTimeoutNS, generatedGatewayRetryJSON(), generatedFullBreakerPolicyJSON()) + `},
      {"name": "mq-default", "transport": "mq", "service": ` + serviceNameJSON + `, "policy": ` + `{"timeout": ` + generatedServiceTimeoutNS + `, "retry": ` + generatedDefaultRetryJSON() + `, "breaker": ` + generatedMinimalBreakerPolicyJSON() + `}` + `}
    ]`
}

func generatedPolicyJSON(timeoutNS, retryJSON, breakerJSON string) string {
	return `{"timeout": ` + timeoutNS + `, "retry": ` + retryJSON + `, "breaker": ` + breakerJSON + `, "rateLimit": ` + generatedRateLimitJSON() + `, "concurrency": {"limit": 64}}`
}

func generatedCanaryPolicyJSON() string {
	return `{"timeout": 2000000000, "retry": ` + generatedDefaultRetryJSON() + `, "breaker": ` + generatedMinimalBreakerPolicyJSON() + `, "rateLimit": ` + generatedRateLimitJSON() + `, "concurrency": {"limit": 64}, "canary": {"ratio": 0.05, "headers": {"x-gofly-canary": "true"}}}`
}

func generatedDefaultRetryJSON() string {
	return `{"attempts": 2, "backoff": ` + generatedRetryBackoffNS + `}`
}

func generatedGatewayRetryJSON() string {
	return `{"attempts": 2, "backoff": ` + generatedRetryBackoffNS + `, "statuses": [502, 503, 504], "methods": ["GET", "HEAD"]}`
}

func generatedRateLimitJSON() string {
	return `{"rate": 100, "burst": 100}`
}

func generatedRestBreakerConfigJSON() string {
	return `{"openTimeout": 5000000000, "window": ` + generatedRestBreakerWindowNS + `, "buckets": 10, "minRequests": 20, "failureRatio": 0.5}`
}

func generatedFullBreakerPolicyJSON() string {
	return `{"enabled": true, "openTimeout": 5000000000, "window": ` + generatedGovernanceBreakerWindowNS + `, "buckets": 10, "minRequests": 20, "failureRatio": 0.5}`
}

func generatedMinimalBreakerPolicyJSON() string {
	return `{"enabled": true, "failureRatio": 0.5, "minRequests": 20}`
}
