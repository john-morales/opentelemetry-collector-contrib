// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/metadata"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.uber.org/zap"
)

type Not struct {
	// the subpolicy evaluators
	subpolicy PolicyEvaluator
	logger    *zap.Logger
	tel       *metadata.TelemetryBuilder
	attribute metric.MeasurementOption
}

func NewNot(
	logger *zap.Logger,
	tel *metadata.TelemetryBuilder,
	policyName string,
	subpolicy PolicyEvaluator,
) PolicyEvaluator {
	return &Not{
		subpolicy: subpolicy,
		logger:    logger,
		tel:       tel,
		attribute: metric.WithAttributes(attribute.String("policy", policyName)),
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (c *Not) Evaluate(ctx context.Context, traceID pcommon.TraceID, trace *TraceData) (Decision, error) {
	// The policy iterates over all sub-policies and returns Sampled if all sub-policies returned a Sampled Decision.
	// If any subpolicy returns NotSampled or InvertNotSampled, it returns NotSampled Decision.
	start := time.Now()
	defer func() {
		GlobalTelemetryBuilder.ProcessorTailSamplingSamplingDecisionLatency.Record(ctx, int64(time.Since(start)/time.Microsecond), c.attribute)
	}()

	decision, err := c.subpolicy.Evaluate(ctx, traceID, trace)
	if err != nil {
		return Unspecified, err
	}
	if decision == Sampled || decision == InvertSampled {
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, c.attribute, decisionToAttribute[NotSampled])
		return NotSampled, nil
	} else if decision == NotSampled || decision == InvertNotSampled {
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, c.attribute, decisionToAttribute[Sampled])
		return Sampled, nil
	} else {
		return decision, nil
	}
}

// OnDroppedSpans is called when the trace needs to be dropped, due to memory
// pressure, before the decision_wait time has been reached.
func (c *Not) OnDroppedSpans(pcommon.TraceID, *TraceData) (Decision, error) {
	return Sampled, nil
}
