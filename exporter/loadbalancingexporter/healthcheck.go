// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package loadbalancingexporter // import "github.com/open-telemetry/opentelemetry-collector-contrib/exporter/loadbalancingexporter"

import (
	"context"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/loadbalancingexporter/internal/metadata"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	healthCheckResolverAttr           = attribute.String("resolver", "healthcheck")
	healthCheckResolverAttrSet        = attribute.NewSet(healthCheckResolverAttr)
	healthCheckResolverSuccessAttrSet = attribute.NewSet(healthCheckResolverAttr, attribute.Bool("success", true))
	healthCheckResolverFailureAttrSet = attribute.NewSet(healthCheckResolverAttr, attribute.Bool("success", false))
)

var _ resolver = (*healthCheckingResolver)(nil)

type healthCheckingResolver struct {
	logger *zap.Logger

	res resolver

	healthCheck     HealthCheckSettings
	watcherFactory  watcherFactory
	watcherRegistry *watcherRegistry
	resInterval     time.Duration
	resTimeout      time.Duration

	healthyEndpoints  []string
	onChangeCallbacks []func([]string)

	stopCh             chan (struct{})
	updateLock         sync.Mutex
	shutdownWg         sync.WaitGroup
	changeCallbackLock sync.RWMutex
	telemetry          *metadata.TelemetryBuilder
}

func newHealthCheckingResolver(logger *zap.Logger, res resolver, healthCheck HealthCheckSettings, factory watcherFactory, tb *metadata.TelemetryBuilder) (*healthCheckingResolver, error) {
	interval := defaultResInterval
	if healthCheck.Interval > 0 {
		interval = healthCheck.Interval
	}
	timeout := defaultResTimeout
	if healthCheck.Timeout > 0 {
		timeout = healthCheck.Timeout
	}

	return &healthCheckingResolver{
		logger:         logger,
		healthCheck:    healthCheck,
		watcherFactory: factory,
		res:            res,
		resInterval:    interval,
		resTimeout:     timeout,
		stopCh:         make(chan struct{}),
		telemetry:      tb,
	}, nil
}

func (r *healthCheckingResolver) start(ctx context.Context, host component.Host) error {
	r.watcherRegistry = newWatcherRegistry(r.logger, host, r.watcherFactory)

	if _, err := r.resolve(ctx); err != nil {
		r.logger.Warn("failed initial resolve", zap.Error(err))
	}

	r.shutdownWg.Add(1)
	go r.periodicallyResolve()

	r.logger.Debug("Health checking resolver started",
		zap.String("healthPort", r.healthCheck.HealthPort),
		zap.Duration("interval", r.resInterval),
		zap.Duration("timeout", r.resTimeout))
	return nil
}

func (r *healthCheckingResolver) shutdown(_ context.Context) error {
	// Not shutting down underlying resolver because it's never started
	r.changeCallbackLock.Lock()
	r.onChangeCallbacks = nil
	r.changeCallbackLock.Unlock()

	close(r.stopCh)
	r.shutdownWg.Wait()

	if r.watcherRegistry != nil {
		r.watcherRegistry.Close()
	}
	return nil
}

func (r *healthCheckingResolver) periodicallyResolve() {
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

func (r *healthCheckingResolver) resolve(ctx context.Context) ([]string, error) {
	// Maintain separate health check clients to each endpoint?
	// Separate goroutine that's also async checking the endpoints and further removing bad entries?

	endpoints, err := r.res.resolve(ctx)
	if err != nil {
		return endpoints, err
	}

	if r.watcherRegistry == nil {
		return endpoints, fmt.Errorf("illegal: health checking resolver not started before resolve()")
	}

	healthyEndpoints := r.watcherRegistry.check(ctx, endpoints)

	sort.Strings(healthyEndpoints)

	if equalStringSlice(r.healthyEndpoints, healthyEndpoints) {
		return r.healthyEndpoints, nil
	}

	// the list has changed!
	r.updateLock.Lock()
	r.healthyEndpoints = healthyEndpoints
	r.updateLock.Unlock()

	r.telemetry.LoadbalancerNumBackends.Record(ctx, int64(len(healthyEndpoints)), metric.WithAttributeSet(healthCheckResolverAttrSet))
	r.telemetry.LoadbalancerNumBackendUpdates.Add(ctx, 1, metric.WithAttributeSet(healthCheckResolverAttrSet))

	// propagate the change
	r.changeCallbackLock.RLock()
	for _, callback := range r.onChangeCallbacks {
		callback(r.healthyEndpoints)
	}
	r.changeCallbackLock.RUnlock()

	return r.healthyEndpoints, nil
}

func (r *healthCheckingResolver) onChange(f func([]string)) {
	r.changeCallbackLock.Lock()
	defer r.changeCallbackLock.Unlock()
	r.onChangeCallbacks = append(r.onChangeCallbacks, f)
}

type watcher interface {
	target() string
	ok(ctx context.Context) bool
	close()
}

type watcherFactory interface {
	create(ctx context.Context, host component.Host, endpoint string) (watcher, error)
}

type grpcWatcherFactory struct {
	logger      *zap.Logger
	healthCheck HealthCheckSettings
	telemetry   *metadata.TelemetryBuilder
	settings    component.TelemetrySettings
}

func newGrpcWatcherFactory(logger *zap.Logger, healthCheck HealthCheckSettings, telemetry *metadata.TelemetryBuilder, settings component.TelemetrySettings) *grpcWatcherFactory {
	return &grpcWatcherFactory{
		logger:      logger,
		healthCheck: healthCheck,
		telemetry:   telemetry,
		settings:    settings,
	}
}

func (g *grpcWatcherFactory) create(ctx context.Context, host component.Host, endpoint string) (watcher, error) {
	healthEndpoint := withHealthcheckPort(endpoint, g.healthCheck.HealthPort)

	clientConfigCopy := *g.healthCheck.ClientConfig
	clientConfigCopy.Endpoint = healthEndpoint

	grpcBackoff := g.healthCheck.ToGrpcConfig()
	connOption := configgrpc.WithGrpcDialOption(grpc.WithConnectParams(grpc.ConnectParams{Backoff: grpcBackoff}))

	logger := g.logger.With(zap.String("healthEndpoint", healthEndpoint))

	// Set up a connection to the server.
	logger.Debug("Connecting to gRPC health endpoint",
		zap.String("host", fmt.Sprintf("%+v", host)),
		zap.String("backoffConfig", fmt.Sprintf("%+v", grpcBackoff)))

	conn, err := clientConfigCopy.ToClientConn(ctx, host, g.settings, connOption)
	if err != nil {
		return nil, fmt.Errorf("error creating grpc client connection to health endpoint: %w", err)
	}
	client := healthpb.NewHealthClient(conn)

	return &grpcWatcher{
		logger:         logger,
		telemetry:      g.telemetry,
		endpoint:       endpoint,
		healthEndpoint: healthEndpoint,
		conn:           conn,
		client:         client,
	}, nil
}

type grpcWatcher struct {
	logger    *zap.Logger
	telemetry *metadata.TelemetryBuilder

	endpoint       string
	healthEndpoint string
	conn           io.Closer
	client         healthpb.HealthClient
}

func (w *grpcWatcher) target() string {
	return w.endpoint
}

func (w *grpcWatcher) ok(ctx context.Context) bool {
	recv, err := w.client.Check(ctx, new(healthpb.HealthCheckRequest))
	ok := err == nil && recv.Status == healthpb.HealthCheckResponse_SERVING
	if ok {
		w.telemetry.LoadbalancerNumResolutions.Add(ctx, 1, metric.WithAttributeSet(healthCheckResolverSuccessAttrSet))
		w.logger.Debug("Health check response", zap.String("recv", recv.String()))
	} else {
		w.telemetry.LoadbalancerNumResolutions.Add(ctx, 1, metric.WithAttributeSet(healthCheckResolverFailureAttrSet))
		w.logger.Info("Health check failed", zap.String("recv", recv.String()), zap.Error(err))
	}
	return ok
}

func (w *grpcWatcher) close() {
	w.logger.Info("Closing grpcWatcher")
	if err := w.conn.Close(); err != nil {
		w.logger.Warn("Failure closing grpcWatcher before removal", zap.Error(err))
	}
}

func (w *grpcWatcher) String() string {
	return fmt.Sprintf("{%s/%s}", w.endpoint, w.healthEndpoint)
}

type watcherRegistry struct {
	logger *zap.Logger
	host   component.Host

	factory          watcherFactory
	updateLock       sync.Mutex
	watchedEndpoints map[string]watcher
}

func newWatcherRegistry(logger *zap.Logger, host component.Host, factory watcherFactory) *watcherRegistry {
	return &watcherRegistry{
		logger:           logger,
		factory:          factory,
		host:             host,
		watchedEndpoints: make(map[string]watcher, 8),
	}
}

func (w *watcherRegistry) check(ctx context.Context, endpoints []string) (healthyEndpoints []string) {
	w.updateLock.Lock()
	for _, endpoint := range endpoints {
		if _, ok := w.watchedEndpoints[endpoint]; !ok {

			watched, err := w.factory.create(ctx, w.host, endpoint)
			if err != nil {
				// Paranoia - shouldn't be possible according to API doc
				w.logger.Error("Failed to create grpcWatcher", zap.Error(err))
				continue
			}
			w.watchedEndpoints[endpoint] = watched
		}
	}

	// Delete any now missing from underlying resolver
	var toCheck, toClose []watcher
	for knownEndpoint, watched := range w.watchedEndpoints {
		if slices.Contains(endpoints, knownEndpoint) {
			toCheck = append(toCheck, watched)
		} else {
			toClose = append(toClose, watched)
			delete(w.watchedEndpoints, knownEndpoint)
		}
	}
	w.updateLock.Unlock()

	// Perform Close() I/O without holding lock.
	if len(toClose) > 0 {
		// Avoid chance of holding up the resolve result
		go func() {
			for _, watched := range toClose {
				watched.close()
			}
		}()
	}

	// Perform Check() I/O without holding lock
	for _, watched := range toCheck {
		if watched.ok(ctx) {
			healthyEndpoints = append(healthyEndpoints, watched.target())
		}
	}
	return
}

func (w *watcherRegistry) Close() {
	w.updateLock.Lock()
	for _, watched := range w.watchedEndpoints {
		watched.close()
	}
	clear(w.watchedEndpoints)
	w.updateLock.Unlock()
}
