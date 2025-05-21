// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package loadbalancingexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/loadbalancingexporter"

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/loadbalancingexporter/internal/metadata"

	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	dnsHealthCheckingResolverAttr           = attribute.String("resolver", "dns_healthchecking")
	dnsHealthCheckingResolverAttrSet        = attribute.NewSet(dnsHealthCheckingResolverAttr)
	dnsHealthCheckingResolverSuccessAttrSet = attribute.NewSet(dnsHealthCheckingResolverAttr, attribute.Bool("success", true))
	dnsHealthCheckingResolverFailureAttrSet = attribute.NewSet(dnsHealthCheckingResolverAttr, attribute.Bool("success", false))
)

var _ resolver = (*dnsHealthCheckingResolver)(nil)

type dnsHealthCheckingResolver struct {
	logger *zap.Logger

	healthPort  string
	dnsResolver *dnsResolver
	resInterval time.Duration
	resTimeout  time.Duration

	healthyEndpoints  []string
	watchedEndpoints  map[string]*watchedEndpoint
	onChangeCallbacks []func([]string)

	stopCh             chan (struct{})
	updateLock         sync.Mutex
	shutdownWg         sync.WaitGroup
	changeCallbackLock sync.RWMutex
	telemetry          *metadata.TelemetryBuilder
}

func newDNSHealthCheckingResolver(
	logger *zap.Logger,
	cfg *configgrpc.ClientConfig,
	hostname string,
	port string,
	healthPort string,
	interval time.Duration,
	timeout time.Duration,
	tb *metadata.TelemetryBuilder,
) (*dnsHealthCheckingResolver, error) {
	if interval == 0 {
		interval = defaultResInterval
	}
	if timeout == 0 {
		timeout = defaultResTimeout
	}

	dnsRes, err := newDNSResolver(logger, hostname, port, interval, timeout, tb)
	if err != nil {
		return nil, fmt.Errorf("failed to create underling DNS Resolver: %w", err)
	}

	return &dnsHealthCheckingResolver{
		logger:           logger,
		healthPort:       healthPort,
		dnsResolver:      dnsRes,
		resInterval:      interval,
		resTimeout:       timeout,
		watchedEndpoints: make(map[string]*watchedEndpoint, 8),
		stopCh:           make(chan struct{}),
		telemetry:        tb,
	}, nil
}

func (r *dnsHealthCheckingResolver) start(ctx context.Context) error {
	if _, err := r.resolve(ctx); err != nil {
		r.logger.Warn("failed to resolve", zap.Error(err))
	}

	r.shutdownWg.Add(1)
	go r.periodicallyResolve()

	r.logger.Debug("DNS health checking resolver started",
		zap.String("hostname", r.dnsResolver.hostname),
		zap.String("port", r.dnsResolver.port),
		zap.String("healthPort", r.healthPort),
		zap.Duration("interval", r.resInterval),
		zap.Duration("timeout", r.resTimeout))
	return nil
}

func (r *dnsHealthCheckingResolver) shutdown(ctx context.Context) error {
	// Not shutting down dnsResolver because it's never started
	r.changeCallbackLock.Lock()
	r.onChangeCallbacks = nil
	r.changeCallbackLock.Unlock()

	close(r.stopCh)
	r.shutdownWg.Wait()
	return nil
}

func (r *dnsHealthCheckingResolver) periodicallyResolve() {
	ticker := time.NewTicker(r.resInterval)
	defer r.shutdownWg.Done()

	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), r.resTimeout)
			if _, err := r.resolve(ctx); err != nil {
				r.logger.Warn("failed to resolve", zap.Error(err))
			} else {
				r.logger.Debug("resolved successfully")
			}
			cancel()
		case <-r.stopCh:
			return
		}
	}
}

func withHealthcheckPort(endpoint, healthPort string) string {
	host := strings.Split(endpoint, ":")[0]
	return fmt.Sprintf("%s:%s", host, healthPort)
}

func (r *dnsHealthCheckingResolver) resolve(ctx context.Context) ([]string, error) {
	// Maintain separate health check clients to each endpoint?
	// Separate goroutine that's also async checking the endpoints and further removing bad entries?

	endpoints, err := r.dnsResolver.resolve(ctx)
	if err != nil {
		r.telemetry.LoadbalancerNumResolutions.Add(ctx, 1, metric.WithAttributeSet(dnsHealthCheckingResolverFailureAttrSet))
		return endpoints, err
	}

	r.telemetry.LoadbalancerNumResolutions.Add(ctx, 1, metric.WithAttributeSet(dnsHealthCheckingResolverSuccessAttrSet))

	// Create any new watchers from DNS
	r.updateLock.Lock()
	for _, endpoint := range endpoints {
		if _, ok := r.watchedEndpoints[endpoint]; !ok {
			healthcheckEndpoint := withHealthcheckPort(endpoint, r.healthPort)
			r.logger.Info("Creating new watchedEndpoint", zap.String("healthcheckEndpoint", healthcheckEndpoint))
			watched, watchErr := newWatchedEndpoint(r.logger, endpoint, healthcheckEndpoint)
			if watchErr != nil {
				// Paranoia - shouldn't be possible according to API doc
				r.logger.Error("Failed to create watchedEndpoint", zap.String("healthcheckEndpoint", healthcheckEndpoint), zap.Error(watchErr))
				continue
			}
			r.watchedEndpoints[endpoint] = watched
		}
	}

	// Delete any now missing from DNS
	toCheck := []*watchedEndpoint{}
	toClose := []*watchedEndpoint{}
	for knownEndpoint, watched := range r.watchedEndpoints {
		if slices.Contains(endpoints, knownEndpoint) {
			toCheck = append(toCheck, watched)
		} else {
			toClose = append(toClose, watched)
			delete(r.watchedEndpoints, knownEndpoint)
		}
	}
	r.updateLock.Unlock()

	// Perform Close() I/O without holding lock.
	// No one else could have reference to these because they were removed from
	// r.watchedEndpoints while holding the lock.
	if len(toClose) > 0 {
		// Avoid chance of holding up the resolve result
		go func() {
			for _, watched := range toClose {
				r.logger.Info("Closing removed watchedEndpoint", zap.String("healthcheckEndpoint", watched.healthEndpoint))
				if err = watched.Close(); err != nil {
					r.logger.Warn("Failure closing watchedEndpoint", zap.String("healthcheckEndpoint", watched.healthEndpoint), zap.Error(err))
				}
			}
		}()
	}

	// Perform Check() I/O without holding lock
	healthyEndpoints := make([]string, 0, len(toCheck))
	for _, watched := range toCheck {
		if watched.Ok(ctx) {
			healthyEndpoints = append(healthyEndpoints, watched.endpoint)
		} else {
			r.logger.Info("Endpoint failed health check", zap.String("healthcheckEndpoint", watched.healthEndpoint))
		}
	}

	sort.Strings(healthyEndpoints)

	if equalStringSlice(r.healthyEndpoints, healthyEndpoints) {
		return r.healthyEndpoints, nil
	}

	// the list has changed!
	r.updateLock.Lock()
	r.healthyEndpoints = healthyEndpoints
	r.updateLock.Unlock()
	r.telemetry.LoadbalancerNumBackends.Record(ctx, int64(len(healthyEndpoints)), metric.WithAttributeSet(dnsHealthCheckingResolverAttrSet))
	r.telemetry.LoadbalancerNumBackendUpdates.Add(ctx, 1, metric.WithAttributeSet(dnsHealthCheckingResolverAttrSet))

	// propagate the change
	r.changeCallbackLock.RLock()
	for _, callback := range r.onChangeCallbacks {
		callback(r.healthyEndpoints)
	}
	r.changeCallbackLock.RUnlock()

	return r.healthyEndpoints, nil
}

func (r *dnsHealthCheckingResolver) onChange(f func([]string)) {
	r.changeCallbackLock.Lock()
	defer r.changeCallbackLock.Unlock()
	r.onChangeCallbacks = append(r.onChangeCallbacks, f)
}

type watchedEndpoint struct {
	logger *zap.Logger

	endpoint       string
	healthEndpoint string
	conn           *grpc.ClientConn
	client         healthpb.HealthClient
}

func newWatchedEndpoint(logger *zap.Logger, endpoint, healthEndpoint string) (*watchedEndpoint, error) {
	// TODO: use any other specified connection settings from the config like TLS
	logger.Info("Connecting to gRPC health endpoint", zap.String("healthcheckEndpoint", healthEndpoint))
	dialoptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	// Set up a connection to the server.
	conn, err := grpc.NewClient(healthEndpoint, dialoptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc client to health endpoint: %w", err)
	}
	client := healthpb.NewHealthClient(conn)

	return &watchedEndpoint{
		logger:         logger,
		endpoint:       endpoint,
		healthEndpoint: healthEndpoint,
		conn:           conn,
		client:         client,
	}, nil
}

func (w *watchedEndpoint) Ok(ctx context.Context) bool {
	recv, err := w.client.Check(ctx, new(healthpb.HealthCheckRequest))
	w.logger.Debug("Check received message", zap.String("healthcheckEndpoint", w.healthEndpoint), zap.String("recv", recv.String()), zap.Error(err))
	return err == nil && recv.Status == healthpb.HealthCheckResponse_SERVING
}

func (w *watchedEndpoint) Close() error {
	return w.conn.Close()
}

func (w *watchedEndpoint) String() string {
	return fmt.Sprintf("{%s/%s}", w.endpoint, w.healthEndpoint)
}
