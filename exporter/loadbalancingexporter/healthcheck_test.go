package loadbalancingexporter

import (
	"context"
	"fmt"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestInitialHealthCheckResolution(t *testing.T) {
	// prepare
	_, tb := getTelemetryAssets(t)
	dnsRes, err := newDNSResolver(zap.NewNop(), "service-1", "55690", 5*time.Second, 1*time.Second, tb)
	require.NoError(t, err)

	dnsRes.resolver = &mockDNSResolver{
		onLookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{
				{IP: net.IPv4(127, 0, 0, 1)},
				{IP: net.IPv4(127, 0, 0, 2)},
				{IP: net.IPv6loopback},
			}, nil
		},
	}

	hc := HealthCheckSettings{HealthPort: "55691", ClientConfig: &configgrpc.ClientConfig{}}
	factory := &mockWatcherFactory{
		w: map[string]*mockWatcher{
			"127.0.0.1:55690": {endpoint: "127.0.0.1:55690", response: true, closed: make(chan bool, 1)},
			"127.0.0.2:55690": {endpoint: "127.0.0.2:55690", response: false, closed: make(chan bool, 1)}, // check fails
			"[::1]:55690":     {endpoint: "[::1]:55690", response: true, closed: make(chan bool, 1)},
		},
		err: nil,
	}

	res, err := newHealthCheckingResolver(zap.NewNop(), dnsRes, hc, factory, tb)
	require.NoError(t, err)

	// test
	var resolved []string
	res.onChange(func(endpoints []string) {
		resolved = endpoints
	})

	require.NoError(t, res.start(context.Background(), componenttest.NewNopHost()))
	defer func() {
		require.NoError(t, res.shutdown(context.Background()))

		// Verify shutdown closes all watchers
		for _, mockW := range factory.w {
			assert.Equal(t, 1, mockW.closedCount)
		}
		assert.Equal(t, 0, len(res.watcherRegistry.watchedEndpoints), "Registry should have been cleared by shutdown")
	}()

	// verify
	assert.Len(t, resolved, 2)
	for i, value := range []string{"127.0.0.1:55690", "[::1]:55690"} {
		assert.Equal(t, value, resolved[i])
	}

}

func TestGrpcWatcher_OKSuccess(t *testing.T) {
	_, tb := getTelemetryAssets(t)
	mockConn := new(mockCloser)
	mockClient := &mockHealthClient{
		checkResponse: &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING},
		err:           nil,
	}
	testWatcher := &grpcWatcher{
		logger:         zap.NewNop(),
		telemetry:      tb,
		endpoint:       "endpoint:2000",
		healthEndpoint: "endpoint:2001",
		conn:           mockConn,
		client:         mockClient,
	}

	assert.Equal(t, "endpoint:2000", testWatcher.target())
	assert.True(t, testWatcher.ok(context.Background()), "Should be ok=true given SERVING status and no error")

	testWatcher.close()
	assert.Equal(t, 1, mockConn.closeCount, "Expected conn to be closed exactly once")
}

func TestGrpcWatcher_OKFailure_WrongStatus(t *testing.T) {
	_, tb := getTelemetryAssets(t)
	mockConn := new(mockCloser)
	mockClient := &mockHealthClient{checkResponse: new(healthpb.HealthCheckResponse)}
	testWatcher := &grpcWatcher{
		logger:         zap.NewNop(),
		telemetry:      tb,
		endpoint:       "endpoint:2000",
		healthEndpoint: "endpoint:2001",
		conn:           mockConn,
		client:         mockClient,
	}

	mockClient.checkResponse.Status = healthpb.HealthCheckResponse_NOT_SERVING
	assert.False(t, testWatcher.ok(context.Background()), "Should be ok=false given NOT_SERVING status and no error")

	mockClient.checkResponse.Status = healthpb.HealthCheckResponse_UNKNOWN
	assert.False(t, testWatcher.ok(context.Background()), "Should be ok=false given UNKNOWN status and no error")

	testWatcher.close()
	assert.Equal(t, 1, mockConn.closeCount, "Expected conn to be closed exactly once")
}

func TestGrpcWatcher_OKFailure_Error(t *testing.T) {
	_, tb := getTelemetryAssets(t)
	mockConn := new(mockCloser)
	mockClient := &mockHealthClient{
		checkResponse: new(healthpb.HealthCheckResponse),
		err:           fmt.Errorf("expected error"),
	}
	testWatcher := &grpcWatcher{
		logger:         zap.NewNop(),
		telemetry:      tb,
		endpoint:       "endpoint:2000",
		healthEndpoint: "endpoint:2001",
		conn:           mockConn,
		client:         mockClient,
	}

	mockClient.checkResponse.Status = healthpb.HealthCheckResponse_UNKNOWN
	assert.False(t, testWatcher.ok(context.Background()), "Should be ok=false given UNKNOWN status and error")

	mockClient.checkResponse.Status = healthpb.HealthCheckResponse_NOT_SERVING
	assert.False(t, testWatcher.ok(context.Background()), "Should be ok=false given NOT_SERVING status and error")

	mockClient.checkResponse.Status = healthpb.HealthCheckResponse_SERVING
	assert.False(t, testWatcher.ok(context.Background()), "Should be ok=false given SERVING status and error")

	mockClient.err = nil
	assert.True(t, testWatcher.ok(context.Background()), "Should be ok=true given SERVING status and no error")

	testWatcher.close()
	assert.Equal(t, 1, mockConn.closeCount, "Expected conn to be closed exactly once")
}

func TestWatcherRegistry_AddsWatchers(t *testing.T) {
	watcher1 := &mockWatcher{endpoint: "127.0.0.1:55690", response: true, closed: make(chan bool, 1)}
	watcher2 := &mockWatcher{endpoint: "127.0.0.2:55690", response: false, closed: make(chan bool, 1)} // check fails
	watcher3 := &mockWatcher{endpoint: "[::1]:55690", response: true, closed: make(chan bool, 1)}
	factory := &mockWatcherFactory{
		w: map[string]*mockWatcher{
			watcher1.endpoint: watcher1,
			watcher2.endpoint: watcher2, // check fails
			watcher3.endpoint: watcher3,
		},
		err: nil,
	}

	registry := newWatcherRegistry(zap.NewNop(), componenttest.NewNopHost(), factory)

	resolvedEndpoints := []string{"127.0.0.1:55690", "127.0.0.2:55690", "[::1]:55690"}
	healthyEndpoints := registry.check(context.Background(), resolvedEndpoints)
	sort.Strings(healthyEndpoints)

	expectedHealthyEndpoints := []string{"127.0.0.1:55690", "[::1]:55690"}
	assert.EqualValues(t, expectedHealthyEndpoints, healthyEndpoints)
	assert.Equal(t, 3, factory.numCreated, "Expected the factory to have created 3 watchers, one for each endpoint")
	assert.Equal(t, 3, len(registry.watchedEndpoints), "Expected registry to have 3 watchers, one for each endpoint")

	// Verify Close() closes each watcher and clears registry
	registry.Close()
	assert.Equal(t, 0, len(registry.watchedEndpoints), "Expected registry to have been cleared by Close()")
	assert.Equal(t, 1, watcher1.closedCount, "Watcher should have been closed")
	assert.Equal(t, 1, watcher2.closedCount, "Watcher should have been closed")
	assert.Equal(t, 1, watcher3.closedCount, "Watcher should have been closed")
}

func TestWatcherRegistry_RemovesWatchers(t *testing.T) {
	watcher1 := &mockWatcher{endpoint: "127.0.0.1:55690", response: true, closed: make(chan bool, 1)}
	watcher2 := &mockWatcher{endpoint: "127.0.0.2:55690", response: true, closed: make(chan bool, 1)}
	factory := &mockWatcherFactory{
		w: map[string]*mockWatcher{
			watcher1.endpoint: watcher1,
			watcher2.endpoint: watcher2,
		},
		err: nil,
	}

	registry := newWatcherRegistry(zap.NewNop(), componenttest.NewNopHost(), factory)

	// Initial creation of two watchers
	resolvedEndpoints := []string{"127.0.0.1:55690", "127.0.0.2:55690"}
	healthyEndpoints := registry.check(context.Background(), resolvedEndpoints)
	sort.Strings(healthyEndpoints)

	expectedHealthyEndpoints := []string{"127.0.0.1:55690", "127.0.0.2:55690"}
	assert.EqualValues(t, expectedHealthyEndpoints, healthyEndpoints)
	assert.Equal(t, 2, factory.numCreated, "Expected the factory to have created 2 watchers, one for each endpoint")
	assert.Equal(t, 2, len(registry.watchedEndpoints), "Expected registry to have 2 watchers, one for each endpoint")

	// Now DNS returned one fewer address than before
	resolvedEndpoints = []string{"127.0.0.1:55690"}
	healthyEndpoints = registry.check(context.Background(), resolvedEndpoints)
	sort.Strings(healthyEndpoints)

	// Verify registry removed and closed the omitted DNS entry
	expectedHealthyEndpoints = []string{"127.0.0.1:55690"}
	assert.EqualValues(t, expectedHealthyEndpoints, healthyEndpoints)
	assert.Equal(t, 2, factory.numCreated, "Expected the factory to not have created any additional watchers")
	assert.Equal(t, 1, len(registry.watchedEndpoints), "Expected registry to have removed the one watcher")

	// Wait on async close
	select {
	case <-watcher2.closed:
		// success
	case <-time.After(2 * time.Second):
		require.FailNow(t, "watcher2 should have been closed async but after 2 seconds was still not closed")
	}

	assert.Equal(t, 0, watcher1.closedCount, "Active Watcher should NOT have been closed")
	assert.Equal(t, 1, watcher2.closedCount, "Watcher should have been closed once")

	// Verify Close() closes each watcher and clears registry
	registry.Close()
	assert.Equal(t, 0, len(registry.watchedEndpoints), "Expected registry to have been cleared by Close()")
	assert.Equal(t, 1, watcher1.closedCount, "Watcher should have been closed once")
	assert.Equal(t, 1, watcher2.closedCount, "Watcher should have been closed once")
}

var _ watcherFactory = (*mockWatcherFactory)(nil)

type mockWatcherFactory struct {
	w          map[string]*mockWatcher
	err        error
	numCreated int
}

func (m *mockWatcherFactory) create(ctx context.Context, host component.Host, endpoint string) (watcher, error) {
	m.numCreated++
	watch := m.w[endpoint]
	return watch, m.err
}

var _ watcher = (*mockWatcher)(nil)

type mockWatcher struct {
	endpoint    string
	response    bool
	closedCount int
	closed      chan bool
}

func (m *mockWatcher) target() string {
	return m.endpoint
}

func (m *mockWatcher) ok(ctx context.Context) bool {
	return m.response
}

func (m *mockWatcher) close() {
	m.closedCount++
	close(m.closed)
}

var _ healthpb.HealthClient = (*mockHealthClient)(nil)

type mockHealthClient struct {
	checkResponse *healthpb.HealthCheckResponse
	err           error
}

func (m *mockHealthClient) Check(ctx context.Context, in *healthpb.HealthCheckRequest, opts ...grpc.CallOption) (*healthpb.HealthCheckResponse, error) {
	return m.checkResponse, m.err
}

func (m *mockHealthClient) List(ctx context.Context, in *healthpb.HealthListRequest, opts ...grpc.CallOption) (*healthpb.HealthListResponse, error) {
	panic("implement me")
}

func (m *mockHealthClient) Watch(ctx context.Context, in *healthpb.HealthCheckRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[healthpb.HealthCheckResponse], error) {
	panic("implement me")
}

type mockCloser struct {
	closeCount int
}

func (m *mockCloser) Close() error {
	m.closeCount++
	return nil
}
