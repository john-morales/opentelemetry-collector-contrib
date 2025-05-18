// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type latency struct {
	logger           *zap.Logger
	thresholdMs      int64
	upperThresholdMs int64
	attribute        metric.MeasurementOption
}

var _ PolicyEvaluator = (*latency)(nil)

// NewLatency creates a policy evaluator sampling traces with a duration greater than a configured threshold
func NewLatency(settings component.TelemetrySettings, policyName string, thresholdMs int64, upperThresholdMs int64) PolicyEvaluator {
	return &latency{
		logger:           settings.Logger,
		thresholdMs:      thresholdMs,
		upperThresholdMs: upperThresholdMs,
		attribute:        metric.WithAttributes(attribute.String("policy", policyName)),
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (l *latency) Evaluate(ctx context.Context, _ pcommon.TraceID, traceData *TraceData) (Decision, error) {
	start := time.Now()
	defer func() {
		GlobalTelemetryBuilder.ProcessorTailSamplingSamplingDecisionLatency.Record(ctx, int64(time.Since(start)/time.Microsecond), l.attribute)
	}()

	l.logger.Debug("Evaluating spans in latency filter")

	traceData.Lock()
	defer traceData.Unlock()
	batches := traceData.ReceivedBatches

	var minTime pcommon.Timestamp
	var maxTime pcommon.Timestamp

	return hasSpanWithCondition(batches, func(span ptrace.Span) bool {
		if minTime == 0 || span.StartTimestamp() < minTime {
			minTime = span.StartTimestamp()
		}
		if maxTime == 0 || span.EndTimestamp() > maxTime {
			maxTime = span.EndTimestamp()
		}

		duration := maxTime.AsTime().Sub(minTime.AsTime())
		if l.upperThresholdMs == 0 {
			return duration.Milliseconds() >= l.thresholdMs
		}
		return (l.thresholdMs < duration.Milliseconds() && duration.Milliseconds() <= l.upperThresholdMs)
	}), nil
}
