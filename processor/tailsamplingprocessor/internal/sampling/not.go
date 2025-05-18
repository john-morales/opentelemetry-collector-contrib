// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.uber.org/zap"
)

type Not struct {
	// the subpolicy evaluators
	subpolicy PolicyEvaluator
	logger    *zap.Logger
}

func NewNot(
	logger *zap.Logger,
	subpolicy PolicyEvaluator,
) PolicyEvaluator {
	return &Not{
		subpolicy: subpolicy,
		logger:    logger,
	}
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (c *Not) Evaluate(ctx context.Context, traceID pcommon.TraceID, trace *TraceData) (Decision, error) {
	// The policy iterates over all sub-policies and returns Sampled if all sub-policies returned a Sampled Decision.
	// If any subpolicy returns NotSampled or InvertNotSampled, it returns NotSampled Decision.
	decision, err := c.subpolicy.Evaluate(ctx, traceID, trace)
	if err != nil {
		return Unspecified, err
	}
	if decision == Sampled || decision == InvertSampled {
		return NotSampled, nil
	} else if decision == NotSampled || decision == InvertNotSampled {
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
