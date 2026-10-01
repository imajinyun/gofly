package svc

import (
	"sync"

	"github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/examples/gosky/internal/app"
	"github.com/imajinyun/gofly/examples/gosky/internal/authz"
	"github.com/imajinyun/gofly/examples/gosky/internal/config"
	"github.com/imajinyun/gofly/rpc"
)

type RPCMuxDiagnosisClient interface {
	UpdateMuxDiagnosisEventExporter(rpc.RPCMuxDiagnosisEventExporter, rpc.RPCMuxDiagnosisFilter)
}

type ServiceContext struct {
	mu         sync.RWMutex
	Config     config.Config
	MQ         mq.Broker
	Authorizer *authz.Authorizer
	Project    *app.ProjectService
	rpcClients []RPCMuxDiagnosisClient
}

func NewServiceContext(c config.Config, brokers ...mq.Broker) *ServiceContext {
	var broker mq.Broker
	if len(brokers) > 0 {
		broker = brokers[0]
	}
	return &ServiceContext{Config: c, MQ: broker}
}

func (s *ServiceContext) SetAuthorizer(authorizer *authz.Authorizer) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Authorizer = authorizer
}

func (s *ServiceContext) CurrentAuthorizer() *authz.Authorizer {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Authorizer
}

func (s *ServiceContext) SetProjectService(projectService *app.ProjectService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Project = projectService
}

func (s *ServiceContext) CurrentProjectService() *app.ProjectService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Project
}

func (s *ServiceContext) UpdateConfig(c config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Config = c
}

func (s *ServiceContext) RegisterRPCClient(client RPCMuxDiagnosisClient) func() {
	if s == nil || client == nil {
		return func() {}
	}
	s.mu.Lock()
	s.rpcClients = append(s.rpcClients, client)
	s.mu.Unlock()
	return func() {
		s.UnregisterRPCClient(client)
	}
}

func (s *ServiceContext) UnregisterRPCClient(client RPCMuxDiagnosisClient) {
	if s == nil || client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, registered := range s.rpcClients {
		if registered == client {
			s.rpcClients = append(s.rpcClients[:index], s.rpcClients[index+1:]...)
			return
		}
	}
}

func (s *ServiceContext) UpdateRPCMuxDiagnosisExporters(exporter rpc.RPCMuxDiagnosisEventExporter, filter rpc.RPCMuxDiagnosisFilter) {
	if s == nil {
		return
	}
	s.mu.RLock()
	clients := append([]RPCMuxDiagnosisClient(nil), s.rpcClients...)
	s.mu.RUnlock()
	for _, client := range clients {
		client.UpdateMuxDiagnosisEventExporter(exporter, filter)
	}
}

func (s *ServiceContext) CurrentConfig() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}
