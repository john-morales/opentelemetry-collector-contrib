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
	"go.uber.org/zap"
)

type spanCount struct {
	logger    *zap.Logger
	minSpans  int32
	maxSpans  int32
	attribute metric.MeasurementOption
}

var _ PolicyEvaluator = (*spanCount)(nil)

// NewSpanCount creates a policy evaluator sampling traces with more than one span per trace
func NewSpanCount(settings component.TelemetrySettings, policyName string, minSpans, maxSpans int32) PolicyEvaluator {
	return &spanCount{
		logger:    settings.Logger,
		minSpans:  minSpans,
		maxSpans:  maxSpans,
		attribute: metric.WithAttributes(attribute.String("policy", policyName)),
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (c *spanCount) Evaluate(ctx context.Context, _ pcommon.TraceID, traceData *TraceData) (Decision, error) {
	start := time.Now()
	defer func() {
		GlobalTelemetryBuilder.ProcessorTailSamplingSamplingDecisionLatency.Record(ctx, int64(time.Since(start)/time.Microsecond), c.attribute)
	}()
	c.logger.Debug("Evaluating spans counts in filter")

	spanCount := int(traceData.SpanCount.Load())
	switch {
	case c.maxSpans == 0 && spanCount >= int(c.minSpans):
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, c.attribute, decisionToAttribute[Sampled])
		return Sampled, nil
	case spanCount >= int(c.minSpans) && spanCount <= int(c.maxSpans):
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, c.attribute, decisionToAttribute[Sampled])
		return Sampled, nil
	default:
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, c.attribute, decisionToAttribute[NotSampled])
		return NotSampled, nil
	}
}
