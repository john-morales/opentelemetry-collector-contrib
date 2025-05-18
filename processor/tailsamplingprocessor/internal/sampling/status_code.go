// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package sampling // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/tailsamplingprocessor/internal/sampling"

import (
	"context"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type statusCodeFilter struct {
	logger      *zap.Logger
	statusCodes []ptrace.StatusCode
	attribute   metric.MeasurementOption
}

var _ PolicyEvaluator = (*statusCodeFilter)(nil)

// NewStatusCodeFilter creates a policy evaluator that samples all traces with
// a given status code.
func NewStatusCodeFilter(settings component.TelemetrySettings, policyName string, statusCodeString []string) (PolicyEvaluator, error) {
	if len(statusCodeString) == 0 {
		return nil, errors.New("expected at least one status code to filter on")
	}

	statusCodes := make([]ptrace.StatusCode, len(statusCodeString))

	for i := range statusCodeString {
		switch statusCodeString[i] {
		case "OK":
			statusCodes[i] = ptrace.StatusCodeOk
		case "ERROR":
			statusCodes[i] = ptrace.StatusCodeError
		case "UNSET":
			statusCodes[i] = ptrace.StatusCodeUnset
		default:
			return nil, fmt.Errorf("unknown status code %q, supported: OK, ERROR, UNSET", statusCodeString[i])
		}
	}

	return &statusCodeFilter{
		logger:      settings.Logger,
		statusCodes: statusCodes,
		attribute:   metric.WithAttributes(attribute.String("policy", policyName)),
	}, nil
}

// Evaluate looks at the trace data and returns a corresponding SamplingDecision.
func (r *statusCodeFilter) Evaluate(ctx context.Context, _ pcommon.TraceID, trace *TraceData) (Decision, error) {
	start := time.Now()
	defer func() {
		GlobalTelemetryBuilder.ProcessorTailSamplingSamplingDecisionLatency.Record(ctx, int64(time.Since(start)/time.Microsecond), r.attribute)
	}()

	r.logger.Debug("Evaluating spans in status code filter")

	trace.Lock()
	defer trace.Unlock()
	batches := trace.ReceivedBatches

	decision := hasSpanWithCondition(batches, func(span ptrace.Span) bool {
		for _, statusCode := range r.statusCodes {
			if span.Status().Code() == statusCode {
				return true
			}
		}
		return false
	})

	if attr, ok := decisionToAttribute[decision]; ok {
		GlobalTelemetryBuilder.ProcessorTailSamplingCountTracesSampled.Add(ctx, 1, r.attribute, attr)
	}
	return decision, nil
}
