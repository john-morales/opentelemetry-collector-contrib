// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"time"
)

type rateLimiting struct {
	limiter        *rate.Limiter
	spansPerSecond float64
	fn             valueFunction
	logger         *zap.Logger
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
func NewRateLimiting(settings component.TelemetrySettings, spansPerSecond, tracesPerSecond float64, burst int64) PolicyEvaluator {
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

	settings.Logger.Info("Creating rate limiter", zap.Float64("perSecondLimit", perSecondLimit), zap.Int("burstLimit", burstLimit))
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
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (r *rateLimiting) Evaluate(_ context.Context, _ pcommon.TraceID, trace *TraceData) (Decision, error) {
	n := r.fn(trace)
	preTokens := r.limiter.Tokens()
	if r.limiter.AllowN(time.Now(), n) {
		r.logger.Debug("Rate Limiter allowed", zap.Int("n", n), zap.Float64("preTokens", preTokens), zap.Float64("postTokens", r.limiter.Tokens()), zap.Float64("limit", float64(r.limiter.Limit())))
		return Sampled, nil
	}

	r.logger.Debug("Rate Limiter denied", zap.Int("n", n), zap.Float64("preTokens", preTokens), zap.Float64("postTokens", r.limiter.Tokens()), zap.Float64("limit", float64(r.limiter.Limit())))
	return NotSampled, nil
}
