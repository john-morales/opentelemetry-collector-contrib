// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"time"
)

var (
	attrSampledTrue  = metric.WithAttributes(attribute.String("sampled", "true"))
	attrSampledFalse = metric.WithAttributes(attribute.String("sampled", "false"))
)

type rateLimiting struct {
	limiter        *rate.Limiter
	spansPerSecond float64
	fn             valueFunction
	logger         *zap.Logger

	name      string
	counter   metric.Int64Counter
	attribute metric.MeasurementOption
}

var _ PolicyEvaluator = (*rateLimiting)(nil)

type valueFunction func(trace *TraceData) int

func _spanFn(trace *TraceData) int {
	return int(trace.SpanCount.Load())
}

func _traceFn(trace *TraceData) int {
	return 1
}

// NewRateLimiting creates a policy evaluator the samples all traces.
func NewRateLimiting(settings component.TelemetrySettings, name string, spansPerSecond, tracesPerSecond float64, burst int64) PolicyEvaluator {
	meter := metadata.Meter(settings)
	burstLimit := int(burst)

	// Only one or the other can be set, else 0.0
	var fn valueFunction = _traceFn
	var perSecondLimit float64 = 0.0
	if spansPerSecond > 0.0 && tracesPerSecond <= 0.0 {
		perSecondLimit = spansPerSecond
		fn = _spanFn
	} else if spansPerSecond <= 0.0 && tracesPerSecond > 0.0 {
		perSecondLimit = tracesPerSecond
		fn = _traceFn
	}

	settings.Logger.Info("Creating rate limiter", zap.String("name", name), zap.Float64("perSecondLimit", perSecondLimit), zap.Int("burstLimit", burstLimit))
	counter, err := meter.Int64Counter(
		"otelcol_processor_tail_sampling_rate_limiting_traces_observed",
		metric.WithDescription("Count of traces that this rate limiter has observed"),
		metric.WithUnit("{traces}"))
	if err != nil {
		settings.Logger.Fatal("Failed to create rate limiter", zap.String("name", name), zap.Error(err))
	}

	return &rateLimiting{
		// Must take care when setting burst.
		//
		// When using spans_per_second, the burst value is effectively a limit on the maximum
		// number of spans a trace can have for it to be Sampled; the number of spans in a
		// single trace exceeding the burst value will always evaluate to NotSampled.
		//
		// On the other hand, setting an excessively high burst can lead to an extended initial
		// period where it appears no limiting is happening at all. This is because Limiter is
		// initialized with its number of initial tokens set to the burst value, and not until
		// that large initial pool is drawn down to zero will a NotSample then be returned.
		limiter:        rate.NewLimiter(rate.Limit(perSecondLimit), burstLimit),
		spansPerSecond: spansPerSecond,
		fn:             fn,
		logger:         settings.Logger,

		name:      name,
		counter:   counter,
		attribute: metric.WithAttributes(attribute.String("policy", name)),
	}
}

func (r *rateLimiting) limiterLoggingFields(preTokens float64, n int) []zap.Field {
	return []zap.Field{
		zap.String("name", r.name),
		zap.Int("n", n),
		zap.Float64("preTokens", preTokens),
		zap.Float64("postTokens", r.limiter.Tokens()),
		zap.Float64("limit", float64(r.limiter.Limit())),
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (r *rateLimiting) Evaluate(ctx context.Context, _ pcommon.TraceID, trace *TraceData) (Decision, error) {
	start := time.Now()
	defer func() {
		GlobalTelemetryBuilder.ProcessorTailSamplingSamplingDecisionLatency.Record(ctx, int64(time.Since(start)/time.Microsecond), r.attribute)
	}()

	n := r.fn(trace)
	preTokens := r.limiter.Tokens()
	if r.limiter.AllowN(time.Now(), n) {
		r.counter.Add(ctx, 1, attrSampledTrue, r.attribute)
		if r.logger.Level() <= zap.DebugLevel {
			r.logger.Debug("Rate Limiter allowed", r.limiterLoggingFields(preTokens, n)...)
		}
		return Sampled, nil
	}

	if r.logger.Level() <= zap.DebugLevel {
		r.logger.Debug("Rate Limiter denied", r.limiterLoggingFields(preTokens, n)...)
	}
	r.counter.Add(ctx, 1, attrSampledFalse, r.attribute)
	return NotSampled, nil
}
