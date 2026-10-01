package mq

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/imajinyun/gofly/core/governance"
	"github.com/imajinyun/gofly/core/kv/redis"
	coremq "github.com/imajinyun/gofly/core/mq"
	"github.com/imajinyun/gofly/core/mq/kafka"
	"github.com/imajinyun/gofly/core/mq/rabbitmq"
	"github.com/imajinyun/gofly/core/mq/redisstream"

	"github.com/imajinyun/gofly/examples/gosky/internal/config"
)

func NewBroker(cfg config.MQConfig, manager *governance.Manager) (coremq.Broker, error) {
	broker, err := newDriverBroker(cfg)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return broker, nil
	}
	broker, err = coremq.NewGovernanceBroker(
		broker,
		coremq.WithGovernanceService(cfg.Service),
		coremq.WithGovernanceManager(manager),
		coremq.WithGovernanceMetrics(nil),
		coremq.WithGovernanceTrace(cfg.Trace),
		coremq.WithGovernanceLog(cfg.Log),
		coremq.WithGovernanceTimeout(cfg.Timeout),
		coremq.WithGovernanceTags(cfg.Tags),
	)
	if err != nil {
		return nil, fmt.Errorf("setup mq governance: %w", err)
	}
	return broker, nil
}

func newDriverBroker(cfg config.MQConfig) (coremq.Broker, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "", "memory":
		return coremq.AsBroker(coremq.NewMemoryBroker()), nil
	case "kafka":
		return kafka.New(kafka.Options{
			Brokers:      cfg.Kafka.Brokers,
			WriteTimeout: cfg.Kafka.WriteTimeout,
			ReadTimeout:  cfg.Kafka.ReadTimeout,
			MinBytes:     cfg.Kafka.MinBytes,
			MaxBytes:     cfg.Kafka.MaxBytes,
		})
	case "rabbitmq":
		return rabbitmq.New(rabbitmq.Options{
			URL:            cfg.RabbitMQ.URL,
			ExchangePrefix: cfg.RabbitMQ.ExchangePrefix,
			Prefetch:       cfg.RabbitMQ.Prefetch,
		})
	case "redisstream":
		client, err := redis.NewChecked(context.Background(), redis.Config{
			Addr:                    cfg.RedisStream.Redis.Addr,
			Addrs:                   cfg.RedisStream.Redis.Addrs,
			Cluster:                 cfg.RedisStream.Redis.Cluster,
			MasterName:              cfg.RedisStream.Redis.MasterName,
			SentinelUsername:        cfg.RedisStream.Redis.SentinelUsername,
			SentinelPassword:        cfg.RedisStream.Redis.SentinelPassword,
			ReadOnly:                cfg.RedisStream.Redis.ReadOnly,
			RouteByLatency:          cfg.RedisStream.Redis.RouteByLatency,
			RouteRandomly:           cfg.RedisStream.Redis.RouteRandomly,
			Username:                cfg.RedisStream.Redis.Username,
			Password:                cfg.RedisStream.Redis.Password,
			TLS:                     cfg.RedisStream.Redis.TLS,
			Protocol:                cfg.RedisStream.Redis.Protocol,
			EnableIdentity:          cfg.RedisStream.Redis.EnableIdentity,
			MaintNotifications:      cfg.RedisStream.Redis.MaintNotifications,
			EagerConnect:            cfg.RedisStream.Redis.EagerConnect,
			PingTimeout:             cfg.RedisStream.Redis.PingTimeout,
			DB:                      cfg.RedisStream.Redis.DB,
			DialTimeout:             cfg.RedisStream.Redis.DialTimeout,
			Timeout:                 cfg.RedisStream.Redis.Timeout,
			MaxConns:                cfg.RedisStream.Redis.MaxConns,
			MaxIdleConns:            cfg.RedisStream.Redis.MaxIdleConns,
			ConnMaxIdleTime:         cfg.RedisStream.Redis.ConnMaxIdleTime,
			ConnMaxLifetime:         cfg.RedisStream.Redis.ConnMaxLifetime,
			MinIdleConns:            cfg.RedisStream.Redis.MinIdleConns,
			PoolTimeout:             cfg.RedisStream.Redis.PoolTimeout,
			MaxRetries:              cfg.RedisStream.Redis.MaxRetries,
			SlowThreshold:           cfg.RedisStream.Redis.SlowThreshold,
			DisableBreaker:          cfg.RedisStream.Redis.DisableBreaker,
			BreakerAdaptive:         cfg.RedisStream.Redis.BreakerAdaptive,
			BreakerFailureThreshold: cfg.RedisStream.Redis.BreakerFailureThreshold,
			BreakerOpenTimeout:      cfg.RedisStream.Redis.BreakerOpenTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("create Redis client: %w", err)
		}
		broker, err := redisstream.New(client, redisstream.Options{
			MaxLen:        cfg.RedisStream.MaxLen,
			Consumer:      cfg.RedisStream.Consumer,
			BlockInterval: cfg.RedisStream.BlockInterval,
			ReadCount:     cfg.RedisStream.ReadCount,
		})
		if err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("create redis stream broker: %w", err)
		}
		return brokerWithCleanup{Broker: broker, cleanup: client.Close}, nil
	default:
		return nil, fmt.Errorf("unsupported mq driver %q", cfg.Driver)
	}
}

type brokerWithCleanup struct {
	coremq.Broker
	cleanup func() error
}

func (b brokerWithCleanup) Close(ctx context.Context) error {
	return errors.Join(b.Broker.Close(ctx), b.cleanup())
}
